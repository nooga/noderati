package host

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"runtime"
	"sync"

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

// zlibSyncHandle is the Go half of node:zlib's class-based API (Gzip,
// Gunzip, Deflate, ...): one synchronous process(chunk, flushFlag) step
// returning whatever output that step produced. Real minizlib (tar's zlib
// layer) drives Node's zlib entirely through the synchronous
// `_processChunk(chunk, flushFlag)` rather than as a stream, so this has
// to answer in the same call instead of via a later 'data' event.
type zlibSyncHandle interface {
	process(chunk []byte, flush int) ([]byte, error)
	reset()
	close()
}

type zlibCompressor struct {
	mode  string
	level int
	out   bytes.Buffer
	w     interface {
		io.WriteCloser
		Flush() error
	}
	finished bool
}

func newZlibCompressor(mode string, level int) (*zlibCompressor, error) {
	c := &zlibCompressor{mode: mode, level: level}
	if err := c.open(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *zlibCompressor) open() error {
	c.out.Reset()
	c.finished = false
	switch c.mode {
	case "Gzip":
		w, err := gzip.NewWriterLevel(&c.out, c.level)
		if err != nil {
			return err
		}
		// Real zlib stamps the header's OS byte from its own OS_CODE table
		// (zutil.h); Go's default is 255 (unknown).
		switch runtime.GOOS {
		case "darwin", "ios":
			w.Header.OS = 19
		case "windows":
			w.Header.OS = 10
		default:
			w.Header.OS = 3
		}
		c.w = w
	case "Deflate":
		w, err := zlib.NewWriterLevel(&c.out, c.level)
		if err != nil {
			return err
		}
		c.w = w
	case "DeflateRaw":
		w, err := flate.NewWriter(&c.out, c.level)
		if err != nil {
			return err
		}
		c.w = w
	default:
		return errors.New("unknown compression mode " + c.mode)
	}
	return nil
}

func (c *zlibCompressor) process(chunk []byte, flush int) ([]byte, error) {
	if c.finished {
		return nil, nil
	}
	if len(chunk) > 0 {
		if _, err := c.w.Write(chunk); err != nil {
			return nil, err
		}
	}
	switch flush {
	case zFinish:
		if err := c.w.Close(); err != nil {
			return nil, err
		}
		c.finished = true
	case zPartialFlush, zSyncFlush, zFullFlush, zBlock:
		if err := c.w.Flush(); err != nil {
			return nil, err
		}
	}
	out := append([]byte(nil), c.out.Bytes()...)
	c.out.Reset()
	return out, nil
}

func (c *zlibCompressor) reset() { _ = c.open() }
func (c *zlibCompressor) close() {}

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
	feed *zlibFeed
}

func newZlibDecompressor(mode string) *zlibDecompressor {
	d := &zlibDecompressor{mode: mode}
	d.start()
	return d
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
	// Real zlib rejects a bad gzip magic as soon as those bytes arrive,
	// rather than first waiting for a full 10-byte header like Go does.
	checkGzipMagic := func() error {
		head, _ := br.Peek(2)
		if (len(head) > 0 && head[0] != 0x1f) || (len(head) > 1 && head[1] != 0x8b) {
			return gzip.ErrHeader
		}
		return nil
	}
	buf := make([]byte, 32*1024)
	drain := func(r io.Reader) error {
		for {
			n, err := r.Read(buf)
			if n > 0 {
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
		if err := checkGzipMagic(); err != nil {
			return err
		}
		zr, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		// Real Node decodes concatenated gzip members and ignores trailing
		// data only when it starts with a 0x00 byte; anything else is parsed
		// as the next member's header (so trailing garbage is an error).
		// Driven member-by-member since Go's own multistream mode has no
		// equivalent of that zero-byte rule.
		zr.Multistream(false)
		for {
			if err := drain(zr); err != nil {
				return err
			}
			next, _ := br.Peek(1)
			if len(next) == 0 || next[0] == 0x00 {
				_, _ = io.Copy(io.Discard, br)
				return nil
			}
			if err := checkGzipMagic(); err != nil {
				return err
			}
			if err := zr.Reset(br); err != nil {
				return err
			}
			zr.Multistream(false)
		}
	case "Inflate":
		zr, err := zlib.NewReader(br)
		if err != nil {
			return err
		}
		return drain(zr)
	case "InflateRaw":
		return drain(flate.NewReader(br))
	}
	return errors.New("unknown decompression mode " + mode)
}

func (d *zlibDecompressor) process(chunk []byte, flush int) ([]byte, error) {
	f := d.feed
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(chunk) > 0 && !f.done {
		f.buf = append(f.buf, chunk...)
	}
	if flush == zFinish {
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
	f.mu.Lock()
	f.aborted = true
	f.cond.Broadcast()
	f.mu.Unlock()
}

func (d *zlibDecompressor) reset() {
	d.close()
	d.start()
}

// zlibErrorInfo maps a Go decoder error onto the code/errno real Node's
// zlib reports (Z_DATA_ERROR = -3 for corrupt input, Z_BUF_ERROR = -5 for
// truncated input), since callers branch on err.code.
func zlibErrorInfo(err error) (string, string, int) {
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "unexpected end of file", "Z_BUF_ERROR", -5
	case errors.Is(err, gzip.ErrHeader), errors.Is(err, zlib.ErrHeader):
		return "incorrect header check", "Z_DATA_ERROR", -3
	case errors.Is(err, gzip.ErrChecksum), errors.Is(err, zlib.ErrChecksum):
		return "incorrect data check", "Z_DATA_ERROR", -3
	}
	var corrupt flate.CorruptInputError
	if errors.As(err, &corrupt) {
		return "invalid block type", "Z_DATA_ERROR", -3
	}
	return err.Error(), "Z_DATA_ERROR", -3
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
		obj.SetOwn("code", vm.NewString(code))
		obj.SetOwn("errno", vm.NumberValue(float64(errno)))
	}
	return &fsSystemError{exception: exception, message: message}
}

// buildZlibSyncHandle is __noderatiZlibHandle(mode, level): the object
// every JS zlib class instance keeps as its own `_handle`.
func buildZlibSyncHandle(vmInst *vm.VM, mode string, level int) (vm.Value, error) {
	var h zlibSyncHandle
	switch mode {
	case "Gzip", "Deflate", "DeflateRaw":
		c, err := newZlibCompressor(mode, level)
		if err != nil {
			return vm.Undefined, err
		}
		h = c
	case "Gunzip", "Inflate", "InflateRaw", "Unzip":
		h = newZlibDecompressor(mode)
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
