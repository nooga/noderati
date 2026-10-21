package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// stream_web.go: node:stream/web. ReadableStream/WritableStream/
// TransformStream are the engine's own globals; the reader/writer
// classes are thin constructors over getReader()/getWriter(), the
// controller classes are non-constructible as in Node, and the queuing
// strategies, TextEncoderStream/TextDecoderStream and
// CompressionStream/DecompressionStream (over node:zlib, the way Node
// builds them) are implemented here. The ones missing from the global
// scope are also installed as lazy globals, as Node has them.
const streamWebShim = `import Stream from "stream";
import zlib from "zlib";

const { newReadableWritablePairFromDuplex } = Stream[Symbol.for("noderati.stream.internals")];

function illegalConstructor() {
  const err = new TypeError("Illegal constructor");
  err.code = "ERR_ILLEGAL_CONSTRUCTOR";
  return err;
}
function invalidThis(type) {
  const err = new TypeError('Value of "this" must be of type ' + type);
  err.code = "ERR_INVALID_THIS";
  return err;
}

const ReadableStream = globalThis.ReadableStream;
const WritableStream = globalThis.WritableStream;
const TransformStream = globalThis.TransformStream;

function adopt(obj, proto) {
  try { Object.setPrototypeOf(obj, proto); } catch {}
  return obj;
}

function ReadableStreamDefaultReader(stream) {
  if (!new.target) throw new TypeError("Class constructor ReadableStreamDefaultReader cannot be invoked without 'new'");
  if (!(stream instanceof ReadableStream)) {
    const err = new TypeError('The "stream" argument must be an instance of ReadableStream.');
    err.code = "ERR_INVALID_ARG_TYPE";
    throw err;
  }
  return adopt(stream.getReader(), new.target.prototype);
}
function ReadableStreamBYOBReader(stream) {
  if (!new.target) throw new TypeError("Class constructor ReadableStreamBYOBReader cannot be invoked without 'new'");
  if (!(stream instanceof ReadableStream)) {
    const err = new TypeError('The "stream" argument must be an instance of ReadableStream.');
    err.code = "ERR_INVALID_ARG_TYPE";
    throw err;
  }
  return adopt(stream.getReader({ mode: "byob" }), new.target.prototype);
}
function WritableStreamDefaultWriter(stream) {
  if (!new.target) throw new TypeError("Class constructor WritableStreamDefaultWriter cannot be invoked without 'new'");
  if (!(stream instanceof WritableStream)) {
    const err = new TypeError('The "stream" argument must be an instance of WritableStream.');
    err.code = "ERR_INVALID_ARG_TYPE";
    throw err;
  }
  return adopt(stream.getWriter(), new.target.prototype);
}
function ReadableStreamBYOBRequest() { throw illegalConstructor(); }
function ReadableByteStreamController() { throw illegalConstructor(); }
function ReadableStreamDefaultController() { throw illegalConstructor(); }
function TransformStreamDefaultController() { throw illegalConstructor(); }
function WritableStreamDefaultController() { throw illegalConstructor(); }

for (const [ctor, name] of [
  [ReadableStreamDefaultReader, "ReadableStreamDefaultReader"],
  [ReadableStreamBYOBReader, "ReadableStreamBYOBReader"],
  [WritableStreamDefaultWriter, "WritableStreamDefaultWriter"],
  [ReadableStreamBYOBRequest, "ReadableStreamBYOBRequest"],
  [ReadableByteStreamController, "ReadableByteStreamController"],
  [ReadableStreamDefaultController, "ReadableStreamDefaultController"],
  [TransformStreamDefaultController, "TransformStreamDefaultController"],
  [WritableStreamDefaultController, "WritableStreamDefaultController"],
]) {
  Object.defineProperty(ctor.prototype, Symbol.toStringTag, { value: name, configurable: true });
}

function strategyInit(init, name) {
  if (init === null || typeof init !== "object") {
    const err = new TypeError('The "init" argument must be of type object.' +
      (init === undefined ? " Received undefined" : ""));
    err.code = "ERR_INVALID_ARG_TYPE";
    throw err;
  }
  if (init.highWaterMark === undefined) {
    const err = new TypeError("init.highWaterMark is required");
    err.code = "ERR_MISSING_OPTION";
    throw err;
  }
  return +init.highWaterMark;
}

const byteSize = function size(chunk) { return chunk.byteLength; };
const countSize = function size() { return 1; };

class ByteLengthQueuingStrategy {
  #hwm;
  constructor(init) {
    this.#hwm = strategyInit(init, "ByteLengthQueuingStrategy");
  }
  get highWaterMark() {
    if (!(#hwm in this)) throw invalidThis("ByteLengthQueuingStrategy");
    return this.#hwm;
  }
  get size() {
    if (!(#hwm in this)) throw invalidThis("ByteLengthQueuingStrategy");
    return byteSize;
  }
}
class CountQueuingStrategy {
  #hwm;
  constructor(init) {
    this.#hwm = strategyInit(init, "CountQueuingStrategy");
  }
  get highWaterMark() {
    if (!(#hwm in this)) throw invalidThis("CountQueuingStrategy");
    return this.#hwm;
  }
  get size() {
    if (!(#hwm in this)) throw invalidThis("CountQueuingStrategy");
    return countSize;
  }
}
for (const C of [ByteLengthQueuingStrategy, CountQueuingStrategy]) {
  Object.defineProperty(C.prototype, Symbol.toStringTag, { value: C.name, configurable: true });
  for (const k of ["highWaterMark", "size"]) {
    const d = Object.getOwnPropertyDescriptor(C.prototype, k);
    d.enumerable = true;
    Object.defineProperty(C.prototype, k, d);
  }
}

class TextEncoderStream {
  #transform;
  constructor() {
    const encoder = new TextEncoder();
    let pendingHighSurrogate = null;
    this.#transform = new TransformStream({
      transform(chunk, controller) {
        chunk = String(chunk);
        if (pendingHighSurrogate !== null) {
          chunk = pendingHighSurrogate + chunk;
          pendingHighSurrogate = null;
        }
        if (chunk.length > 0) {
          const last = chunk.charCodeAt(chunk.length - 1);
          if (last >= 0xd800 && last <= 0xdbff) {
            pendingHighSurrogate = chunk[chunk.length - 1];
            chunk = chunk.slice(0, -1);
          }
        }
        if (chunk.length > 0) controller.enqueue(encoder.encode(chunk));
      },
      flush(controller) {
        if (pendingHighSurrogate !== null) controller.enqueue(new Uint8Array([0xef, 0xbf, 0xbd]));
      },
    });
  }
  get encoding() { return "utf-8"; }
  get readable() { return this.#transform.readable; }
  get writable() { return this.#transform.writable; }
}

class TextDecoderStream {
  #transform;
  #decoder;
  constructor(encoding, options) {
    if (encoding === undefined) encoding = "utf-8";
    this.#decoder = new TextDecoder(encoding, options);
    const decoder = this.#decoder;
    this.#transform = new TransformStream({
      transform(chunk, controller) {
        if (chunk instanceof ArrayBuffer) chunk = new Uint8Array(chunk);
        const value = decoder.decode(chunk, { stream: true });
        if (value) controller.enqueue(value);
      },
      flush(controller) {
        const value = decoder.decode();
        if (value) controller.enqueue(value);
        controller.terminate();
      },
    });
  }
  get encoding() { return this.#decoder.encoding; }
  get fatal() { return this.#decoder.fatal; }
  get ignoreBOM() { return this.#decoder.ignoreBOM; }
  get readable() { return this.#transform.readable; }
  get writable() { return this.#transform.writable; }
}

function compressionFormat(format, ctor) {
  format = String(format);
  if (format !== "deflate" && format !== "deflate-raw" && format !== "gzip" && format !== "brotli") {
    const err = new TypeError("Failed to construct '" + ctor + "': 1st argument value '" + format +
      "' is not a valid enum value of type CompressionFormat.");
    err.code = "ERR_INVALID_ARG_VALUE";
    throw err;
  }
  return format;
}

class CompressionStream {
  #transform;
  constructor(format) {
    format = compressionFormat(format, "CompressionStream");
    let handle;
    switch (format) {
      case "deflate": handle = new zlib.Deflate(); break;
      case "deflate-raw": handle = new zlib.DeflateRaw(); break;
      case "gzip": handle = new zlib.Gzip(); break;
      case "brotli": handle = zlib.createBrotliCompress(); break;
    }
    this.#transform = newReadableWritablePairFromDuplex(handle);
  }
  get readable() { return this.#transform.readable; }
  get writable() { return this.#transform.writable; }
}

class DecompressionStream {
  #transform;
  constructor(format) {
    format = compressionFormat(format, "DecompressionStream");
    let handle;
    switch (format) {
      case "deflate": handle = new zlib.Inflate(); break;
      case "deflate-raw": handle = new zlib.InflateRaw(); break;
      case "gzip": handle = new zlib.Gunzip(); break;
      case "brotli": handle = zlib.createBrotliDecompress(); break;
    }
    this.#transform = newReadableWritablePairFromDuplex(handle);
  }
  get readable() { return this.#transform.readable; }
  get writable() { return this.#transform.writable; }
}

for (const C of [TextEncoderStream, TextDecoderStream, CompressionStream, DecompressionStream]) {
  Object.defineProperty(C.prototype, Symbol.toStringTag, { value: C.name, configurable: true });
}

const web = {
  ReadableStream,
  ReadableStreamDefaultReader,
  ReadableStreamBYOBReader,
  ReadableStreamBYOBRequest,
  ReadableByteStreamController,
  ReadableStreamDefaultController,
  TransformStream,
  TransformStreamDefaultController,
  WritableStream,
  WritableStreamDefaultWriter,
  WritableStreamDefaultController,
  ByteLengthQueuingStrategy,
  CountQueuingStrategy,
  TextEncoderStream,
  TextDecoderStream,
  CompressionStream,
  DecompressionStream,
};
export {
  ReadableStream,
  ReadableStreamDefaultReader,
  ReadableStreamBYOBReader,
  ReadableStreamBYOBRequest,
  ReadableByteStreamController,
  ReadableStreamDefaultController,
  TransformStream,
  TransformStreamDefaultController,
  WritableStream,
  WritableStreamDefaultWriter,
  WritableStreamDefaultController,
  ByteLengthQueuingStrategy,
  CountQueuingStrategy,
  TextEncoderStream,
  TextDecoderStream,
  CompressionStream,
  DecompressionStream,
};
export default web;
`

// streamWebGlobals are the stream/web classes Node also exposes as
// globals; the ones the engine doesn't already provide are installed as
// accessors that load stream/web on first use.
var streamWebGlobals = []string{
	"ReadableStreamDefaultReader",
	"ReadableStreamBYOBReader",
	"ReadableStreamBYOBRequest",
	"ReadableByteStreamController",
	"ReadableStreamDefaultController",
	"TransformStreamDefaultController",
	"WritableStreamDefaultWriter",
	"WritableStreamDefaultController",
	"ByteLengthQueuingStrategy",
	"CountQueuingStrategy",
	"TextEncoderStream",
	"TextDecoderStream",
	"CompressionStream",
	"DecompressionStream",
}

func installStreamWebGlobals(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	gobj := gt.AsPlainObject()
	if gobj == nil {
		return
	}
	lazy := &lazyJSModule{p: p, spec: "stream/web"}
	for _, name := range streamWebGlobals {
		name := name
		if _, exists := gobj.GetOwn(name); exists {
			continue
		}
		getter := vm.NewNativeFunction(0, false, "get "+name, func(_ []vm.Value) (vm.Value, error) {
			return lazy.get(name)
		})
		setter := vm.NewNativeFunction(1, false, "set "+name, func(args []vm.Value) (vm.Value, error) {
			gobj.DeleteOwn(name)
			gobj.SetOwn(name, argAt(args, 0))
			return vm.Undefined, nil
		})
		yes, no := true, false
		gobj.DefineAccessorProperty(name, getter, true, setter, true, &no, &yes)
	}
}
