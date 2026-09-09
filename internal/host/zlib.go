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
// Compression-direction functions (createGzip/createDeflate/
// createDeflateRaw) are not built at all - grepped for real call sites
// across undici and pi-coding-agent's own dist and found none anywhere
// in this dependency tree. Nothing reachable needs them, so nothing was
// built against a guess; add them the same way (Go's compress/gzip.
// NewWriter/compress/zlib.NewWriter/compress/flate.NewWriter, pumped the
// mirror-image way this file's decompressors are) if and when something
// reachable actually calls one.
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

	obj.SetOwn("write", vm.NewNativeFunction(2, true, "write", func(args []vm.Value) (vm.Value, error) {
		if len(args) > 0 && !args[0].IsUndefined() {
			bodyCh <- valueToBytes(vmInst, args[0])
		}
		if len(args) > 1 && args[1].IsCallable() {
			cb := args[1]
			rt.ScheduleNextTick(func() { _, _ = vmInst.Call(cb, vm.Undefined, nil) })
		}
		return vm.True, nil
	}))
	obj.SetOwn("end", vm.NewNativeFunction(1, true, "end", func(args []vm.Value) (vm.Value, error) {
		if len(args) > 0 && args[0].IsCallable() {
			close(bodyCh)
			cb := args[0]
			rt.ScheduleNextTick(func() { _, _ = vmInst.Call(cb, vm.Undefined, nil) })
			return self, nil
		}
		if len(args) > 0 && !args[0].IsUndefined() {
			bodyCh <- valueToBytes(vmInst, args[0])
		}
		close(bodyCh)
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

const zlibShim = `const __createGunzip = globalThis.__noderatiCreateGunzip;
const __createInflate = globalThis.__noderatiCreateInflate;
const __createInflateRaw = globalThis.__noderatiCreateInflateRaw;

function createGunzip(_options) { return __createGunzip(); }
function createInflate(_options) { return __createInflate(); }
function createInflateRaw(_options) { return __createInflateRaw(); }

function notImplemented(name) {
  return function () {
    throw new Error(
      "zlib." + name + "() is not implemented in noderati - Go's stdlib has no " +
      "brotli/zstd decoder, and faking decompression would silently corrupt a " +
      "real response body. See docs/real-node-plan.md's round 73 entry."
    );
  };
}
const createBrotliDecompress = notImplemented("createBrotliDecompress");
const createZstdDecompress = notImplemented("createZstdDecompress");

// Real, standard zlib flush-mode constants (stable, public values from
// the zlib C library itself, not Node-specific) - present so option
// objects built as { flush: zlib.constants.Z_SYNC_FLUSH, ... } don't
// reference undefined, even though this implementation's decompressors
// don't interpret flush mode at all (Go's compress/gzip and compress/
// zlib readers have no equivalent streaming-flush concept to honor).
const constants = {
  Z_NO_FLUSH: 0,
  Z_PARTIAL_FLUSH: 1,
  Z_SYNC_FLUSH: 2,
  Z_FULL_FLUSH: 3,
  Z_FINISH: 4,
  Z_BLOCK: 5,
  Z_TREES: 6,
};

export { createGunzip, createInflate, createInflateRaw, createBrotliDecompress, createZstdDecompress, constants };
export default { createGunzip, createInflate, createInflateRaw, createBrotliDecompress, createZstdDecompress, constants };
`
