package host

import (
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/adler32"
	"hash/crc32"
	"io"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/nooga/paserati/pkg/vm"
)

// Real zlib flush modes (zlib.h), as passed to _processChunk.
const (
	zNoFlush      = 0
	zPartialFlush = 1
	zSyncFlush    = 2
	zFullFlush    = 3
	zFinish       = 4
	zBlock        = 5
)

// Brotli (BROTLI_OPERATION_*) and zstd (ZSTD_e_*) share one numbering for
// their flush kinds: 0 = process/continue, 1 = flush, 2 = finish/end.
const (
	opProcess = 0
	opFlush   = 1
	opFinish  = 2
)

// zlibSyncHandle is the Go half of node:zlib's class-based API (Gzip,
// Gunzip, Deflate, BrotliCompress, ZstdDecompress, ...): one synchronous
// process(chunk, flushFlag) step returning whatever output that step
// produced. Real minizlib (tar's zlib layer) drives Node's zlib entirely
// through the synchronous `_processChunk(chunk, flushFlag)` rather than as
// a stream, so this has to answer in the same call instead of via a later
// 'data' event.
type zlibSyncHandle interface {
	process(chunk []byte, flush int) ([]byte, error)
	reset()
	close()
}

// zlibHandleOptions is what the JS side already validated and normalized
// out of the user's options object before creating the handle.
type zlibHandleOptions struct {
	level      int
	strategy   int
	dictionary []byte
	// brotli encoder
	quality int
	lgwin   int
	// zstd
	checksum       bool
	windowLog      int
	pledgedSrcSize int64
	windowLogMax   int
}

// --- deflate family (Gzip / Deflate / DeflateRaw) ---------------------

// deflateCompressor writes the gzip/zlib framing itself around a raw
// flate.Writer, rather than using compress/gzip or compress/zlib, so
// params() can swap the compression level mid-stream (a fresh raw writer
// seeded with the last 32K of input as its dictionary continues the same
// stream) and the header bytes match real zlib's (level flags, XFL, OS).
type deflateCompressor struct {
	mode     string
	level    int
	strategy int
	dict     []byte

	out      bytes.Buffer
	fw       *flate.Writer
	sum      uint32 // crc32 (gzip) or adler32 (zlib) of all input so far
	size     uint32
	history  []byte // trailing 32K of input, for params() continuity
	dirty    bool   // input written since the last flush
	finished bool
}

const zHuffmanOnly = 2

func newDeflateCompressor(mode string, o zlibHandleOptions) (*deflateCompressor, error) {
	c := &deflateCompressor{mode: mode, level: o.level, strategy: o.strategy, dict: o.dictionary}
	if mode == "Gzip" {
		c.dict = nil
	}
	if err := c.open(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *deflateCompressor) goLevel() int {
	if c.strategy == zHuffmanOnly {
		return flate.HuffmanOnly
	}
	return c.level
}

func (c *deflateCompressor) effectiveLevel() int {
	if c.level == -1 {
		return 6
	}
	return c.level
}

func gzipOSCode() byte {
	// Real zlib stamps the header's OS byte from its own OS_CODE table
	// (zutil.h); Go's default is 255 (unknown).
	switch runtime.GOOS {
	case "darwin", "ios":
		return 19
	case "windows":
		return 10
	default:
		return 3
	}
}

func (c *deflateCompressor) open() error {
	c.out.Reset()
	c.finished = false
	c.size = 0
	c.history = c.history[:0]
	level := c.effectiveLevel()
	switch c.mode {
	case "Gzip":
		c.sum = 0
		xfl := byte(0)
		if level == 9 {
			xfl = 2
		} else if c.strategy >= zHuffmanOnly || level < 2 {
			xfl = 4
		}
		c.out.Write([]byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, xfl, gzipOSCode()})
	case "Deflate":
		c.sum = 1
		var lflags byte
		switch {
		case c.strategy >= zHuffmanOnly || level < 2:
			lflags = 0
		case level < 6:
			lflags = 1
		case level == 6:
			lflags = 2
		default:
			lflags = 3
		}
		cmf := byte(0x78)
		flg := lflags << 6
		if c.dict != nil {
			flg |= 0x20
		}
		flg += byte(31 - (int(cmf)*256+int(flg))%31)
		c.out.Write([]byte{cmf, flg})
		if c.dict != nil {
			var id [4]byte
			binary.BigEndian.PutUint32(id[:], adler32.Checksum(c.dict))
			c.out.Write(id[:])
		}
	case "DeflateRaw":
	default:
		return errors.New("unknown compression mode " + c.mode)
	}
	fw, err := flate.NewWriterDict(&c.out, c.goLevel(), c.dict)
	if err != nil {
		return err
	}
	c.fw = fw
	return nil
}

func (c *deflateCompressor) consume(chunk []byte) error {
	if _, err := c.fw.Write(chunk); err != nil {
		return err
	}
	switch c.mode {
	case "Gzip":
		c.sum = crc32.Update(c.sum, crc32.IEEETable, chunk)
	case "Deflate":
		c.sum = adler32Update(c.sum, chunk)
	}
	c.size += uint32(len(chunk))
	c.dirty = true
	c.history = append(c.history, chunk...)
	if len(c.history) > 64*1024 {
		c.history = append(c.history[:0], c.history[len(c.history)-32*1024:]...)
	}
	return nil
}

func adler32Update(adler uint32, p []byte) uint32 {
	const mod = 65521
	s1, s2 := adler&0xffff, adler>>16
	for len(p) > 0 {
		n := len(p)
		if n > 5552 {
			n = 5552
		}
		for _, b := range p[:n] {
			s1 += uint32(b)
			s2 += s1
		}
		s1 %= mod
		s2 %= mod
		p = p[n:]
	}
	return s2<<16 | s1
}

func (c *deflateCompressor) process(chunk []byte, flush int) ([]byte, error) {
	if c.finished {
		return nil, nil
	}
	if len(chunk) > 0 {
		if err := c.consume(chunk); err != nil {
			return nil, err
		}
	}
	switch flush {
	case zFinish:
		if err := c.fw.Close(); err != nil {
			return nil, err
		}
		var tr [8]byte
		switch c.mode {
		case "Gzip":
			binary.LittleEndian.PutUint32(tr[:4], c.sum)
			binary.LittleEndian.PutUint32(tr[4:], c.size)
			c.out.Write(tr[:8])
		case "Deflate":
			binary.BigEndian.PutUint32(tr[:4], c.sum)
			c.out.Write(tr[:4])
		}
		c.finished = true
	case zPartialFlush, zSyncFlush, zFullFlush, zBlock:
		if err := c.fw.Flush(); err != nil {
			return nil, err
		}
		c.dirty = false
	}
	return c.take(), nil
}

func (c *deflateCompressor) take() []byte {
	out := append([]byte(nil), c.out.Bytes()...)
	c.out.Reset()
	return out
}

// params switches level/strategy mid-stream: the JS side has already
// sync-flushed, so a new raw writer seeded with the recent input history
// simply continues the same deflate stream.
func (c *deflateCompressor) params(level, strategy int) error {
	if c.finished {
		return nil
	}
	if c.dirty {
		if err := c.fw.Flush(); err != nil {
			return err
		}
		c.dirty = false
	}
	c.level, c.strategy = level, strategy
	hist := c.history
	if len(hist) > 32*1024 {
		hist = hist[len(hist)-32*1024:]
	}
	if c.size == 0 && c.dict != nil {
		hist = c.dict
	}
	fw, err := flate.NewWriterDict(&c.out, c.goLevel(), hist)
	if err != nil {
		return err
	}
	c.fw = fw
	return nil
}

func (c *deflateCompressor) reset() { _ = c.open() }
func (c *deflateCompressor) close() {}

// --- brotli / zstd compressors -----------------------------------------

type brotliCompressor struct {
	opts     zlibHandleOptions
	out      bytes.Buffer
	w        *brotli.Writer
	finished bool
}

func newBrotliCompressor(o zlibHandleOptions) *brotliCompressor {
	c := &brotliCompressor{opts: o}
	c.reset()
	return c
}

func (c *brotliCompressor) reset() {
	c.out.Reset()
	c.finished = false
	c.w = brotli.NewWriterOptions(&c.out, brotli.WriterOptions{Quality: c.opts.quality, LGWin: c.opts.lgwin})
}

func (c *brotliCompressor) process(chunk []byte, flush int) ([]byte, error) {
	if c.finished {
		return nil, nil
	}
	if len(chunk) > 0 {
		if _, err := c.w.Write(chunk); err != nil {
			return nil, err
		}
	}
	switch flush {
	case opFinish:
		if err := c.w.Close(); err != nil {
			return nil, err
		}
		c.finished = true
	case opFlush, 3:
		if err := c.w.Flush(); err != nil {
			return nil, err
		}
	}
	out := append([]byte(nil), c.out.Bytes()...)
	c.out.Reset()
	return out, nil
}

func (c *brotliCompressor) close() {}

type zstdCompressor struct {
	opts     zlibHandleOptions
	out      bytes.Buffer
	enc      *zstd.Encoder
	started  bool
	finished bool
}

func newZstdCompressor(o zlibHandleOptions) (*zstdCompressor, error) {
	c := &zstdCompressor{opts: o}
	eopts := []zstd.EOption{
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(o.level)),
		zstd.WithEncoderCRC(o.checksum),
		zstd.WithZeroFrames(true),
	}
	if o.windowLog > 0 {
		wl := o.windowLog
		if wl < 10 {
			wl = 10
		}
		if wl > 27 {
			wl = 27
		}
		eopts = append(eopts, zstd.WithWindowSize(1<<wl))
	}
	enc, err := zstd.NewWriter(nil, eopts...)
	if err != nil {
		return nil, err
	}
	c.enc = enc
	c.reset()
	return c, nil
}

func (c *zstdCompressor) reset() {
	c.out.Reset()
	c.started = false
	c.finished = false
	if c.opts.pledgedSrcSize >= 0 {
		c.enc.ResetContentSize(&c.out, c.opts.pledgedSrcSize)
	} else {
		c.enc.Reset(&c.out)
	}
}

func (c *zstdCompressor) process(chunk []byte, flush int) ([]byte, error) {
	if c.finished {
		return nil, nil
	}
	// A whole input handed over in one finishing step (every *Sync and
	// callback convenience function) is encoded as a single-shot frame,
	// which - like libzstd's ZSTD_e_end on a fresh stream - records the
	// content size in the frame header.
	if !c.started && flush == opFinish {
		c.finished = true
		return c.enc.EncodeAll(chunk, nil), nil
	}
	c.started = true
	if len(chunk) > 0 {
		if _, err := c.enc.Write(chunk); err != nil {
			return nil, err
		}
	}
	switch flush {
	case opFinish:
		if err := c.enc.Close(); err != nil {
			return nil, err
		}
		c.finished = true
	case opFlush:
		if err := c.enc.Flush(); err != nil {
			return nil, err
		}
	}
	out := append([]byte(nil), c.out.Bytes()...)
	c.out.Reset()
	return out, nil
}

func (c *zstdCompressor) close() {}

// --- decompressors -------------------------------------------------------

// zlibFeed is the input side of a zlibDecompressor: an io.Reader the
// decoder goroutine pulls from, which reports (via starved) the moment it
// has handed over every byte fed so far and is waiting for more.
type zlibFeed struct {
	mu      sync.Mutex
	cond    *sync.Cond
	buf     []byte
	eof     bool
	aborted bool
	starved bool

	out  []byte
	done bool
	err  error
}

func (f *zlibFeed) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.buf) == 0 && !f.eof && !f.aborted {
		f.starved = true
		f.cond.Broadcast()
		f.cond.Wait()
	}
	f.starved = false
	if f.aborted {
		return 0, io.ErrClosedPipe
	}
	if len(f.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, f.buf)
	f.buf = f.buf[n:]
	return n, nil
}

// zlibDecompressor runs Go's pull-based decoder on its own goroutine and
// turns it into the push-based, synchronous shape _processChunk needs:
// process() hands the chunk over, then blocks only until the decoder has
// either consumed every byte so far (starved) or finished - never on
// anything the VM thread itself would have to do.
type zlibDecompressor struct {
	mode string
	opts zlibHandleOptions
	feed *zlibFeed
}

func newZlibDecompressor(mode string, o zlibHandleOptions) *zlibDecompressor {
	// The decoder goroutine starts on first use, so a stream that's
	// created and never written to holds no goroutine.
	return &zlibDecompressor{mode: mode, opts: o}
}

func (d *zlibDecompressor) finishFlag() int {
	switch d.mode {
	case "BrotliDecode", "ZstdDecompress":
		return opFinish
	}
	return zFinish
}

func (d *zlibDecompressor) start() {
	f := &zlibFeed{}
	f.cond = sync.NewCond(&f.mu)
	d.feed = f
	go d.run(f)
}

func (d *zlibDecompressor) run(f *zlibFeed) {
	br := bufio.NewReader(f)
	err := d.decode(br, func(chunk []byte) {
		f.mu.Lock()
		f.out = append(f.out, chunk...)
		f.mu.Unlock()
	})
	f.mu.Lock()
	if err != nil && !f.aborted {
		f.err = err
	}
	f.done = true
	f.cond.Broadcast()
	f.mu.Unlock()
}

// Errors carrying real zlib's own wording, for failures Go's decoders
// either don't detect the same way or don't distinguish.
var (
	errZlibHeader      = errors.New("incorrect header check")
	errZlibDataCheck   = errors.New("incorrect data check")
	errZlibLengthCheck = errors.New("incorrect length check")
	errZlibWindowSize  = errors.New("invalid window size")
	errZlibUnknownComp = errors.New("unknown compression method")
	errZlibMissingDict = errors.New("Missing dictionary")
	errZlibBadDict     = errors.New("Bad dictionary")
)

func readFullOrEOF(r io.Reader, p []byte) error {
	_, err := io.ReadFull(r, p)
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func (d *zlibDecompressor) decode(br *bufio.Reader, emit func([]byte)) error {
	mode := d.mode
	if mode == "Unzip" {
		head, _ := br.Peek(2)
		if len(head) == 0 {
			return io.ErrUnexpectedEOF
		}
		if head[0] == 0x1f {
			mode = "Gunzip"
		} else {
			mode = "Inflate"
		}
	}
	buf := make([]byte, 32*1024)
	drain := func(r io.Reader, sum func([]byte)) error {
		for {
			n, err := r.Read(buf)
			if n > 0 {
				if sum != nil {
					sum(buf[:n])
				}
				emit(append([]byte(nil), buf[:n]...))
			}
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
	switch mode {
	case "Gunzip":
		// Real Node decodes concatenated gzip members and ignores trailing
		// data only when it starts with a 0x00 byte; anything else is parsed
		// as the next member's header (so trailing garbage is an error).
		for first := true; ; first = false {
			if !first {
				next, _ := br.Peek(1)
				if len(next) == 0 || next[0] == 0x00 {
					_, _ = io.Copy(io.Discard, br)
					return nil
				}
			}
			if err := readGzipHeader(br); err != nil {
				return err
			}
			var crc, size uint32
			if err := drain(flate.NewReader(br), func(p []byte) {
				crc = crc32.Update(crc, crc32.IEEETable, p)
				size += uint32(len(p))
			}); err != nil {
				return err
			}
			var tr [8]byte
			if err := readFullOrEOF(br, tr[:]); err != nil {
				return err
			}
			if binary.LittleEndian.Uint32(tr[:4]) != crc {
				return errZlibDataCheck
			}
			if binary.LittleEndian.Uint32(tr[4:]) != size {
				return errZlibLengthCheck
			}
		}
	case "Inflate":
		var hdr [2]byte
		if err := readFullOrEOF(br, hdr[:1]); err != nil {
			return err
		}
		// Real zlib rejects a bad method byte as soon as it arrives.
		if hdr[0]&0x0f != 8 {
			return errZlibHeader
		}
		if err := readFullOrEOF(br, hdr[1:]); err != nil {
			return err
		}
		if (int(hdr[0])<<8|int(hdr[1]))%31 != 0 {
			return errZlibHeader
		}
		if hdr[0]>>4 > 7 {
			return errZlibWindowSize
		}
		dict := d.opts.dictionary
		if hdr[1]&0x20 != 0 {
			var id [4]byte
			if err := readFullOrEOF(br, id[:]); err != nil {
				return err
			}
			if dict == nil {
				return errZlibMissingDict
			}
			if adler32.Checksum(dict) != binary.BigEndian.Uint32(id[:]) {
				return errZlibBadDict
			}
		} else {
			dict = nil
		}
		sum := uint32(1)
		if err := drain(flate.NewReaderDict(br, dict), func(p []byte) { sum = adler32Update(sum, p) }); err != nil {
			return err
		}
		var tr [4]byte
		if err := readFullOrEOF(br, tr[:]); err != nil {
			return err
		}
		if binary.BigEndian.Uint32(tr[:]) != sum {
			return errZlibDataCheck
		}
		_, _ = io.Copy(io.Discard, br)
		return nil
	case "InflateRaw":
		if err := drain(flate.NewReaderDict(br, d.opts.dictionary), nil); err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, br)
		return nil
	case "BrotliDecode":
		if err := drain(brotli.NewReader(br), nil); err != nil {
			if err.Error() == "brotli: excessive input" {
				return nil
			}
			return err
		}
		return nil
	case "ZstdDecompress":
		dopts := []zstd.DOption{zstd.WithDecoderConcurrency(1)}
		if d.opts.windowLogMax > 0 {
			dopts = append(dopts, zstd.WithDecoderMaxWindow(uint64(1)<<d.opts.windowLogMax))
		}
		zr, err := zstd.NewReader(br, dopts...)
		if err != nil {
			return err
		}
		defer zr.Close()
		err = drain(zr, nil)
		// Real node returns whatever it decoded, without an error, for a
		// zstd stream that's merely cut short.
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		return err
	}
	return errors.New("unknown decompression mode " + mode)
}

// readGzipHeader consumes one gzip member header (RFC 1952).
func readGzipHeader(br *bufio.Reader) error {
	var h [10]byte
	// Real zlib rejects a bad magic as soon as those bytes arrive,
	// rather than first waiting for a full 10-byte header.
	if err := readFullOrEOF(br, h[:1]); err != nil {
		return err
	}
	if h[0] != 0x1f {
		return errZlibHeader
	}
	if err := readFullOrEOF(br, h[1:2]); err != nil {
		return err
	}
	if h[1] != 0x8b {
		return errZlibHeader
	}
	if err := readFullOrEOF(br, h[2:]); err != nil {
		return err
	}
	if h[2] != 8 {
		return errZlibUnknownComp
	}
	flg := h[3]
	if flg&0x04 != 0 {
		var xl [2]byte
		if err := readFullOrEOF(br, xl[:]); err != nil {
			return err
		}
		if _, err := io.CopyN(io.Discard, br, int64(binary.LittleEndian.Uint16(xl[:]))); err != nil {
			return io.ErrUnexpectedEOF
		}
	}
	for _, bit := range []byte{0x08, 0x10} {
		if flg&bit != 0 {
			if _, err := br.ReadBytes(0); err != nil {
				return io.ErrUnexpectedEOF
			}
		}
	}
	if flg&0x02 != 0 {
		var hc [2]byte
		if err := readFullOrEOF(br, hc[:]); err != nil {
			return err
		}
	}
	return nil
}

func (d *zlibDecompressor) process(chunk []byte, flush int) ([]byte, error) {
	if d.feed == nil {
		d.start()
	}
	f := d.feed
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(chunk) > 0 && !f.done {
		f.buf = append(f.buf, chunk...)
	}
	if flush == d.finishFlag() {
		f.eof = true
	}
	f.cond.Broadcast()
	for !f.done && !(f.starved && len(f.buf) == 0 && !f.eof) {
		f.cond.Wait()
	}
	out := f.out
	f.out = nil
	if f.err != nil {
		err := f.err
		f.err = nil
		return out, err
	}
	return out, nil
}

func (d *zlibDecompressor) close() {
	f := d.feed
	if f == nil {
		return
	}
	d.feed = nil
	f.mu.Lock()
	f.aborted = true
	f.cond.Broadcast()
	f.mu.Unlock()
}

func (d *zlibDecompressor) reset() {
	d.close()
}

// --- errors --------------------------------------------------------------

var brotliErrorPrefixes = map[int]string{
	-19: "_ERROR_", -20: "_ERROR_", -31: "_ERROR_",
	-21: "_ERROR_ALLOC_", -22: "_ERROR_ALLOC_", -25: "_ERROR_ALLOC_",
	-26: "_ERROR_ALLOC_", -27: "_ERROR_ALLOC_", -30: "_ERROR_ALLOC_",
}

// zlibErrorInfo maps a Go decoder error onto the message/code/errno real
// Node's zlib reports, since callers branch on err.code.
func zlibErrorInfo(err error) (string, string, int) {
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "unexpected end of file", "Z_BUF_ERROR", -5
	case errors.Is(err, errZlibMissingDict), errors.Is(err, errZlibBadDict):
		return err.Error(), "Z_NEED_DICT", 2
	case errors.Is(err, errZlibHeader), errors.Is(err, errZlibDataCheck), errors.Is(err, errZlibLengthCheck),
		errors.Is(err, errZlibWindowSize), errors.Is(err, errZlibUnknownComp):
		return err.Error(), "Z_DATA_ERROR", -3
	case errors.Is(err, zstd.ErrMagicMismatch):
		return "Unknown frame descriptor", "ZSTD_error_prefix_unknown", 10
	case errors.Is(err, zstd.ErrCRCMismatch):
		return "Restored data doesn't match checksum", "ZSTD_error_checksum_wrong", 22
	case errors.Is(err, zstd.ErrWindowSizeExceeded), errors.Is(err, zstd.ErrDecoderSizeExceeded):
		return "Frame requires too much memory for decoding", "ZSTD_error_frameParameter_windowTooLarge", 16
	}
	// andybalholm/brotli's decodeError is an int holding the exact
	// BROTLI_DECODER_ERROR_* value real Node reports as errno.
	if rv := reflect.ValueOf(err); rv.Kind() == reflect.Int && strings.HasPrefix(err.Error(), "brotli: ") {
		code := int(rv.Int())
		prefix, ok := brotliErrorPrefixes[code]
		if !ok {
			prefix = "_ERROR_FORMAT_"
		}
		return "Decompression failed", "ERR_" + prefix + strings.TrimPrefix(err.Error(), "brotli: "), code
	}
	if strings.HasPrefix(err.Error(), "brotli: ") {
		return "Decompression failed", "ERR_BROTLI_DECOMPRESSION_FAILED", -31
	}
	if isZstdError(err) {
		return "Data corruption detected", "ZSTD_error_corruption_detected", 20
	}
	var corrupt flate.CorruptInputError
	if errors.As(err, &corrupt) {
		return "invalid block type", "Z_DATA_ERROR", -3
	}
	return err.Error(), "Z_DATA_ERROR", -3
}

func isZstdError(err error) bool {
	for _, e := range []error{zstd.ErrReservedBlockType, zstd.ErrCompressedSizeTooBig, zstd.ErrBlockTooSmall,
		zstd.ErrUnexpectedBlockSize, zstd.ErrWindowSizeTooSmall, zstd.ErrUnknownDictionary,
		zstd.ErrFrameSizeExceeded, zstd.ErrFrameSizeMismatch} {
		if errors.Is(err, e) {
			return true
		}
	}
	return strings.Contains(err.Error(), "zstd") || strings.Contains(err.Error(), "invalid input")
}

func newZlibJSError(vmInst *vm.VM, err error) error {
	message, code, errno := zlibErrorInfo(err)
	exception := vm.Undefined
	if errCtor, ok := vmInst.GetGlobal("Error"); ok {
		if v, cerr := vmInst.Construct(errCtor, []vm.Value{vm.NewString(message)}); cerr == nil {
			exception = v
		}
	}
	if obj := exception.AsPlainObject(); obj != nil {
		obj.SetOwn("errno", vm.NumberValue(float64(errno)))
		obj.SetOwn("code", vm.NewString(code))
	}
	return &fsSystemError{exception: exception, message: message}
}

func zlibOptInt(obj *vm.PlainObject, name string, def int) int {
	if obj == nil {
		return def
	}
	if v, ok := obj.Get(name); ok && v.IsNumber() {
		return int(v.ToFloat())
	}
	return def
}

// buildZlibSyncHandle is __noderatiZlibHandle(mode, opts): the object
// every JS zlib class instance keeps as its own `_handle`.
func buildZlibSyncHandle(vmInst *vm.VM, mode string, optsVal vm.Value) (vm.Value, error) {
	var oo *vm.PlainObject
	o := zlibHandleOptions{level: -1, quality: 11, lgwin: 22, pledgedSrcSize: -1}
	if optsVal.IsNumber() {
		o.level = int(optsVal.ToFloat())
	} else {
		oo = optsVal.AsPlainObject()
	}
	if oo != nil {
		o.level = zlibOptInt(oo, "level", o.level)
		o.strategy = zlibOptInt(oo, "strategy", 0)
		o.quality = zlibOptInt(oo, "quality", o.quality)
		o.lgwin = zlibOptInt(oo, "lgwin", o.lgwin)
		o.checksum = zlibOptInt(oo, "checksum", 0) != 0
		o.windowLog = zlibOptInt(oo, "windowLog", 0)
		o.pledgedSrcSize = int64(zlibOptInt(oo, "pledgedSrcSize", -1))
		o.windowLogMax = zlibOptInt(oo, "windowLogMax", 0)
		if v, ok := oo.Get("dictionary"); ok && !v.IsUndefined() && !v.IsNull() {
			o.dictionary = valueToBytes(vmInst, v)
		}
	}
	if o.quality < 0 {
		o.quality = 0
	}
	if o.quality > 11 {
		o.quality = 11
	}
	if o.lgwin < 10 || o.lgwin > 24 {
		o.lgwin = 22
	}
	if mode == "ZstdCompress" && oo != nil {
		if v, ok := oo.Get("level"); !ok || !v.IsNumber() {
			o.level = 3
		}
	}

	var h zlibSyncHandle
	var dc *deflateCompressor
	switch mode {
	case "Gzip", "Deflate", "DeflateRaw":
		c, err := newDeflateCompressor(mode, o)
		if err != nil {
			return vm.Undefined, err
		}
		h, dc = c, c
	case "Gunzip", "Inflate", "InflateRaw", "Unzip", "BrotliDecode", "ZstdDecompress":
		h = newZlibDecompressor(mode, o)
	case "BrotliEncode":
		h = newBrotliCompressor(o)
	case "ZstdCompress":
		c, err := newZstdCompressor(o)
		if err != nil {
			return vm.Undefined, err
		}
		h = c
	default:
		return vm.Undefined, errors.New("unknown zlib mode " + mode)
	}

	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	closed := false
	obj.SetOwn("process", vm.NewNativeFunction(2, false, "process", func(args []vm.Value) (vm.Value, error) {
		if closed {
			return vm.Undefined, errors.New("zlib binding closed")
		}
		var chunk []byte
		if len(args) > 0 && !args[0].IsUndefined() {
			chunk = valueToBytes(vmInst, args[0])
		}
		flush := zNoFlush
		if len(args) > 1 && args[1].IsNumber() {
			flush = int(args[1].ToFloat())
		}
		out, err := h.process(chunk, flush)
		if err != nil {
			return vm.Undefined, newZlibJSError(vmInst, err)
		}
		return wrapBuffer(vmInst, out), nil
	}))
	obj.SetOwn("params", vm.NewNativeFunction(2, false, "params", func(args []vm.Value) (vm.Value, error) {
		if dc == nil || len(args) < 2 {
			return vm.Undefined, nil
		}
		if err := dc.params(int(args[0].ToFloat()), int(args[1].ToFloat())); err != nil {
			return vm.Undefined, err
		}
		return wrapBuffer(vmInst, dc.take()), nil
	}))
	obj.SetOwn("reset", vm.NewNativeFunction(0, false, "reset", func(_ []vm.Value) (vm.Value, error) {
		h.reset()
		return vm.Undefined, nil
	}))
	obj.SetOwn("close", vm.NewNativeFunction(0, false, "close", func(_ []vm.Value) (vm.Value, error) {
		if !closed {
			closed = true
			h.close()
		}
		return vm.Undefined, nil
	}))
	return vm.NewValueFromPlainObject(obj), nil
}

// zlibCRC32 is zlib.crc32(data, value).
func zlibCRC32(data []byte, value uint32) uint32 {
	return crc32.Update(value, crc32.IEEETable, data)
}
