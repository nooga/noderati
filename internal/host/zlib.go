package host

import (
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// zlib.go implements node:zlib's real decompression Transform streams -
// createGunzip/createInflate/createInflateRaw - the exact reachable
// surface, confirmed by grepping every real node:zlib call site across
// the vendored undici package before writing this, not guessed from
// Node's full zlib API: lib/interceptor/decompress.js (an opt-in
// interceptor, required unconditionally at index.js's own top level) and
// - the actually load-bearing one - lib/web/fetch/index.js, which builds
// exactly this decoder chain for every real fetch() response carrying a
// gzip/deflate/br/zstd Content-Encoding.
//
// createBrotliDecompress/createZstdDecompress deliberately throw a
// clear, real error instead of existing as a lying no-op: Go's stdlib
// has no brotli or zstd decoder, and silently passing the still-
// compressed bytes through (or returning empty output) would corrupt a
// real response body without any visible signal something was wrong -
// exactly the failure mode this project's whole "measure, don't assume"
// discipline exists to avoid. A real fetch() against a server that
// happens to choose gzip/deflate still decompresses correctly; one that
// chooses br/zstd now fails loudly and traceably instead of silently.
//
// The class-based API (Gzip/Gunzip/Deflate/Inflate/DeflateRaw/InflateRaw/
// Unzip, createGzip & co., and the *Sync/callback convenience functions)
// is built on zlib_sync.go's synchronous handle - first reached via real
// tar's minizlib, which constructs `new zlib.Gzip(opts)` and drives it
// through the synchronous `_processChunk(chunk, flushFlag)`.
func declareZlib() {
	registerJSShim("zlib", zlibShim)
}

func installZlibNatives(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	obj := gt.AsPlainObject()
	if obj == nil {
		return
	}

	obj.SetOwn("__noderatiCreateGunzip", vm.NewNativeFunction(0, true, "__noderatiCreateGunzip", func(_ []vm.Value) (vm.Value, error) {
		return vm.NewValueFromPlainObject(buildZlibDecompressor(vmInst, func(r io.Reader) (io.Reader, error) {
			return gzip.NewReader(r)
		})), nil
	}))
	obj.SetOwn("__noderatiCreateInflate", vm.NewNativeFunction(0, true, "__noderatiCreateInflate", func(_ []vm.Value) (vm.Value, error) {
		return vm.NewValueFromPlainObject(buildZlibDecompressor(vmInst, func(r io.Reader) (io.Reader, error) {
			return zlib.NewReader(r)
		})), nil
	}))
	obj.SetOwn("__noderatiCreateInflateRaw", vm.NewNativeFunction(0, true, "__noderatiCreateInflateRaw", func(_ []vm.Value) (vm.Value, error) {
		return vm.NewValueFromPlainObject(buildZlibDecompressor(vmInst, func(r io.Reader) (io.Reader, error) {
			return flate.NewReader(r), nil
		})), nil
	}))
	obj.SetOwn("__noderatiZlibHandle", vm.NewNativeFunction(2, false, "__noderatiZlibHandle", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 1 {
			return vm.Undefined, nil
		}
		level := flate.DefaultCompression
		if len(args) > 1 && args[1].IsNumber() {
			level = int(args[1].ToFloat())
		}
		return buildZlibSyncHandle(vmInst, args[0].ToString(), level)
	}))
}

// buildZlibDecompressor wires a real, incremental decompression pipe:
// write()/end() enqueue raw compressed bytes onto a buffered channel
// (never blocking the VM thread on the underlying io.Pipe - the same
// bodyCh discipline http.go's doHTTPRequest already uses for request
// bodies), a feeder goroutine drains that channel into the pipe's write
// side, and a second goroutine reads decompressed bytes back out through
// newReader, emitting real 'data'/'end'/'error' events incrementally
// (never buffering a whole response before emitting anything - the same
// class of bug this codebase has already had to fix for http and net).
// Built on newReadableStream (emitter.go) for the readable half (pipe/
// setEncoding/destroy come for free) with write()/end() layered on top,
// exactly mirroring net.go's own Socket - real Node's zlib streams are
// genuinely a readable+writable Transform, not just a Readable.
func buildZlibDecompressor(vmInst *vm.VM, newReader func(io.Reader) (io.Reader, error)) *vm.PlainObject {
	obj := newReadableStream(vmInst)
	obj.SetOwn("writable", vm.True)
	self := vm.NewValueFromPlainObject(obj)
	rt := vmInst.GetAsyncRuntime()

	pr, pw := io.Pipe()
	bodyCh := make(chan []byte, 64)
	go func() {
		for chunk := range bodyCh {
			if _, err := pw.Write(chunk); err != nil {
				break
			}
		}
		_ = pw.Close()
	}()

	obj.SetOwn("write", vm.NewNativeFunction(3, true, "write", func(args []vm.Value) (vm.Value, error) {
		encoding, cb := parseWriteEncodingAndCallback(args, 1)
		if len(args) > 0 && !args[0].IsUndefined() {
			bodyCh <- valueToBytesWithEncoding(vmInst, args[0], encoding)
		}
		if cb.IsCallable() {
			rt.ScheduleNextTick(func() { _, _ = vmInst.Call(cb, vm.Undefined, nil) })
		}
		return vm.True, nil
	}))
	obj.SetOwn("end", vm.NewNativeFunction(2, true, "end", func(args []vm.Value) (vm.Value, error) {
		if len(args) > 0 && args[0].IsCallable() {
			close(bodyCh)
			cb := args[0]
			rt.ScheduleNextTick(func() { _, _ = vmInst.Call(cb, vm.Undefined, nil) })
			return self, nil
		}
		encoding, cb := parseWriteEncodingAndCallback(args, 1)
		if len(args) > 0 && !args[0].IsUndefined() {
			bodyCh <- valueToBytesWithEncoding(vmInst, args[0], encoding)
		}
		close(bodyCh)
		if cb.IsCallable() {
			rt.ScheduleNextTick(func() { _, _ = vmInst.Call(cb, vm.Undefined, nil) })
		}
		return self, nil
	}))

	rt.BeginExternalOp()
	go func() {
		defer rt.EndExternalOp()
		reader, err := newReader(pr)
		if err != nil {
			scheduleErrorEmit(vmInst, rt, obj, err)
			return
		}
		buf := make([]byte, 32*1024)
		for {
			n, rerr := reader.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				scheduleEmit(vmInst, obj, "data", wrapBuffer(vmInst, chunk))
			}
			if rerr != nil {
				if rerr == io.EOF {
					scheduleEmit(vmInst, obj, "end")
				} else {
					scheduleErrorEmit(vmInst, rt, obj, rerr)
				}
				return
			}
		}
	}()

	return obj
}

const zlibShim = `import { Transform } from "stream";

const __createGunzip = globalThis.__noderatiCreateGunzip;
const __createInflate = globalThis.__noderatiCreateInflate;
const __createInflateRaw = globalThis.__noderatiCreateInflateRaw;
const __zlibHandle = globalThis.__noderatiZlibHandle;

// createGunzip/createInflate/createInflateRaw stay on the goroutine-pumped
// streaming decoders undici's fetch() was built and tested against; the
// classes below are the synchronous-handle API minizlib (tar) drives via
// _processChunk.
function createGunzip(_options) { return __createGunzip(); }
function createInflate(_options) { return __createInflate(); }
function createInflateRaw(_options) { return __createInflateRaw(); }

function notImplemented(name) {
  return function () {
    throw new Error(
      "zlib." + name + "() is not implemented in noderati - Go's stdlib has no " +
      "brotli/zstd codec, and faking one would silently corrupt real data. " +
      "See docs/real-node-plan.md's round 73 entry."
    );
  };
}
const createBrotliDecompress = notImplemented("createBrotliDecompress");
const createBrotliCompress = notImplemented("createBrotliCompress");
const createZstdDecompress = notImplemented("createZstdDecompress");
const createZstdCompress = notImplemented("createZstdCompress");

const constants = {
  Z_NO_FLUSH: 0, Z_PARTIAL_FLUSH: 1, Z_SYNC_FLUSH: 2, Z_FULL_FLUSH: 3, Z_FINISH: 4, Z_BLOCK: 5, Z_TREES: 6,
  Z_OK: 0, Z_STREAM_END: 1, Z_NEED_DICT: 2, Z_ERRNO: -1, Z_STREAM_ERROR: -2, Z_DATA_ERROR: -3,
  Z_MEM_ERROR: -4, Z_BUF_ERROR: -5, Z_VERSION_ERROR: -6,
  Z_NO_COMPRESSION: 0, Z_BEST_SPEED: 1, Z_BEST_COMPRESSION: 9, Z_DEFAULT_COMPRESSION: -1,
  Z_FILTERED: 1, Z_HUFFMAN_ONLY: 2, Z_RLE: 3, Z_FIXED: 4, Z_DEFAULT_STRATEGY: 0,
  DEFLATE: 1, INFLATE: 2, GZIP: 3, GUNZIP: 4, DEFLATERAW: 5, INFLATERAW: 6, UNZIP: 7,
  Z_MIN_WINDOWBITS: 8, Z_MAX_WINDOWBITS: 15, Z_DEFAULT_WINDOWBITS: 15,
  Z_MIN_CHUNK: 64, Z_MAX_CHUNK: Infinity, Z_DEFAULT_CHUNK: 16384,
  Z_MIN_MEMLEVEL: 1, Z_MAX_MEMLEVEL: 9, Z_DEFAULT_MEMLEVEL: 8,
  Z_MIN_LEVEL: -1, Z_MAX_LEVEL: 9, Z_DEFAULT_LEVEL: -1,
  BROTLI_OPERATION_PROCESS: 0, BROTLI_OPERATION_FLUSH: 1, BROTLI_OPERATION_FINISH: 2, BROTLI_OPERATION_EMIT_METADATA: 3,
};

function toBuffer(chunk, encoding) {
  if (typeof chunk === "string") return Buffer.from(chunk, encoding || "utf8");
  if (chunk instanceof Uint8Array) return chunk;
  if (ArrayBuffer.isView(chunk)) return Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
  if (chunk instanceof ArrayBuffer) return Buffer.from(chunk);
  throw new TypeError('The "buffer" argument must be of type string or an instance of Buffer, TypedArray, DataView, or ArrayBuffer.');
}

class ZlibBase extends Transform {
  constructor(opts, mode) {
    opts = opts || {};
    super(opts);
    let level = opts.level ?? constants.Z_DEFAULT_COMPRESSION;
    if (typeof level !== "number" || level < -1 || level > 9 || !Number.isInteger(level)) {
      const err = new RangeError('The value of "options.level" is out of range. It must be >= -1 and <= 9. Received ' + level);
      err.code = "ERR_OUT_OF_RANGE";
      throw err;
    }
    // Go's flate has no strategy knob; Huffman-only is the one strategy it
    // can honor, as its own dedicated level.
    if (opts.strategy === constants.Z_HUFFMAN_ONLY) level = -2;
    this._handle = __zlibHandle(mode, level);
    this._defaultFlushFlag = opts.flush ?? constants.Z_NO_FLUSH;
    this._finishFlushFlag = opts.finishFlush ?? constants.Z_FINISH;
    this._defaultFullFlushFlag = constants.Z_FULL_FLUSH;
    this.bytesWritten = 0;
    this._closed = false;
  }
  _processChunk(chunk, flushFlag, cb) {
    if (typeof cb === "function") {
      let out;
      try {
        out = this._processChunk(chunk, flushFlag);
      } catch (err) {
        cb(err);
        return;
      }
      if (out.length > 0) this.push(out);
      cb();
      return;
    }
    if (!this._handle) throw new Error("zlib binding closed");
    const buf = toBuffer(chunk);
    this.bytesWritten += buf.length;
    return this._handle.process(buf, flushFlag);
  }
  _transform(chunk, encoding, cb) {
    this._processChunk(toBuffer(chunk, encoding), this._defaultFlushFlag, cb);
  }
  _flush(cb) {
    this._processChunk(Buffer.alloc(0), this._finishFlushFlag, cb);
  }
  flush(kind, cb) {
    if (typeof kind === "function" || kind === undefined) {
      cb = kind;
      kind = this._defaultFullFlushFlag;
    }
    this._processChunk(Buffer.alloc(0), kind, (err) => {
      if (err) this.emit("error", err);
      if (typeof cb === "function") process.nextTick(cb);
    });
  }
  reset() {
    if (!this._handle) throw new Error("zlib binding closed");
    this._handle.reset();
  }
  close(cb) {
    if (typeof cb === "function") this.once("close", cb);
    if (this._closed) return;
    this._closed = true;
    if (this._handle) this._handle.close();
    this._handle = null;
    process.nextTick(() => this.emit("close"));
  }
  destroy(err) {
    this.close();
    if (err) process.nextTick(() => this.emit("error", err));
    return this;
  }
}

class Gzip extends ZlibBase { constructor(opts) { super(opts, "Gzip"); } }
class Gunzip extends ZlibBase { constructor(opts) { super(opts, "Gunzip"); } }
class Deflate extends ZlibBase { constructor(opts) { super(opts, "Deflate"); } }
class Inflate extends ZlibBase { constructor(opts) { super(opts, "Inflate"); } }
class DeflateRaw extends ZlibBase { constructor(opts) { super(opts, "DeflateRaw"); } }
class InflateRaw extends ZlibBase { constructor(opts) { super(opts, "InflateRaw"); } }
class Unzip extends ZlibBase { constructor(opts) { super(opts, "Unzip"); } }

function createGzip(opts) { return new Gzip(opts); }
function createDeflate(opts) { return new Deflate(opts); }
function createDeflateRaw(opts) { return new DeflateRaw(opts); }
function createUnzip(opts) { return new Unzip(opts); }

function zlibBufferSync(Ctor, buffer, opts) {
  const z = new Ctor(opts);
  try {
    return z._processChunk(toBuffer(buffer), z._finishFlushFlag);
  } finally {
    z.close();
  }
}
function zlibBuffer(Ctor, buffer, opts, cb) {
  if (typeof opts === "function") {
    cb = opts;
    opts = {};
  }
  let result, error = null;
  try {
    result = zlibBufferSync(Ctor, buffer, opts);
  } catch (err) {
    error = err;
  }
  process.nextTick(() => (error ? cb(error) : cb(null, result)));
}

const gzipSync = (buf, opts) => zlibBufferSync(Gzip, buf, opts);
const gunzipSync = (buf, opts) => zlibBufferSync(Gunzip, buf, opts);
const deflateSync = (buf, opts) => zlibBufferSync(Deflate, buf, opts);
const inflateSync = (buf, opts) => zlibBufferSync(Inflate, buf, opts);
const deflateRawSync = (buf, opts) => zlibBufferSync(DeflateRaw, buf, opts);
const inflateRawSync = (buf, opts) => zlibBufferSync(InflateRaw, buf, opts);
const unzipSync = (buf, opts) => zlibBufferSync(Unzip, buf, opts);
const gzip = (buf, opts, cb) => zlibBuffer(Gzip, buf, opts, cb);
const gunzip = (buf, opts, cb) => zlibBuffer(Gunzip, buf, opts, cb);
const deflate = (buf, opts, cb) => zlibBuffer(Deflate, buf, opts, cb);
const inflate = (buf, opts, cb) => zlibBuffer(Inflate, buf, opts, cb);
const deflateRaw = (buf, opts, cb) => zlibBuffer(DeflateRaw, buf, opts, cb);
const inflateRaw = (buf, opts, cb) => zlibBuffer(InflateRaw, buf, opts, cb);
const unzip = (buf, opts, cb) => zlibBuffer(Unzip, buf, opts, cb);

const api = {
  Gzip, Gunzip, Deflate, Inflate, DeflateRaw, InflateRaw, Unzip,
  createGzip, createGunzip, createDeflate, createInflate, createDeflateRaw, createInflateRaw, createUnzip,
  createBrotliCompress, createBrotliDecompress, createZstdCompress, createZstdDecompress,
  gzip, gunzip, deflate, inflate, deflateRaw, inflateRaw, unzip,
  gzipSync, gunzipSync, deflateSync, inflateSync, deflateRawSync, inflateRawSync, unzipSync,
  constants,
};

export {
  Gzip, Gunzip, Deflate, Inflate, DeflateRaw, InflateRaw, Unzip,
  createGzip, createGunzip, createDeflate, createInflate, createDeflateRaw, createInflateRaw, createUnzip,
  createBrotliCompress, createBrotliDecompress, createZstdCompress, createZstdDecompress,
  gzip, gunzip, deflate, inflate, deflateRaw, inflateRaw, unzip,
  gzipSync, gunzipSync, deflateSync, inflateSync, deflateRawSync, inflateRawSync, unzipSync,
  constants,
};
export default api;
`
