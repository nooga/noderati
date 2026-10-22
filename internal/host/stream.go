package host

import "strings"

// node:stream is a JS shim assembled from the stream_*.go pieces:
//
//	stream_core.go     errors, validators, stream predicates, destroy(),
//	                   legacy Stream, highWaterMark defaults
//	stream_readable.go Readable, Readable.from
//	stream_writable.go Writable
//	stream_duplex.go   Duplex, Transform, PassThrough, duplexPair
//	stream_pipeline.go finished/eos, addAbortSignal, pipeline, compose,
//	                   Duplex.from
//	stream_ops.go      Readable iterator helpers, web stream adapters
//
// The pieces share one module scope (they reference each other's
// functions freely), so they are concatenated rather than imported.
//
// Real Node's require('stream') is the legacy Stream constructor itself,
// with every other export hung off it as a static property; real code
// relies on that (send does util.inherits(SendStream, Stream) on the
// whole module value), so the default export here is Stream too.
var streamShim = strings.Join([]string{
	streamCoreJS,
	streamReadableJS,
	streamWritableJS,
	streamDuplexJS,
	streamPipelineJS,
	streamOpsJS,
	streamExportsJS,
}, "\n")

const streamExportsJS = `
// Re-pin each constructor's own name: the engine resolves .name through
// the [[Prototype]] chain once a function's prototype is reassigned to a
// function whose own prototype is a class (Readable.name read "EventEmitter").
for (const [fn, name] of [[Stream, "Stream"], [Readable, "Readable"], [Writable, "Writable"],
                          [Duplex, "Duplex"], [Transform, "Transform"], [PassThrough, "PassThrough"],
                          [Duplexify, "Duplexify"]]) {
  Object.defineProperty(fn, "name", { value: name, configurable: true });
}

const promises = { finished, pipeline: pipelinePromise };

Stream.isDestroyed = isDestroyed;
Stream.isDisturbed = isDisturbed;
Stream.isErrored = isErrored;
Stream.isReadable = isReadable;
Stream.isWritable = isWritable;
Stream.Readable = Readable;
Stream.Writable = Writable;
Stream.Duplex = Duplex;
Stream.Transform = Transform;
Stream.PassThrough = PassThrough;
Stream.duplexPair = duplexPair;
Stream.pipeline = pipeline;
Stream.addAbortSignal = addAbortSignal;
Stream.finished = eos;
Stream.destroy = destroyer;
Stream.compose = composeStreams;
Stream.setDefaultHighWaterMark = setDefaultHighWaterMark;
Stream.getDefaultHighWaterMark = getDefaultHighWaterMark;
Object.defineProperty(Stream, "promises", {
  configurable: true,
  enumerable: true,
  get() { return promises; },
});
const kCustomPromisify = Symbol.for("nodejs.util.promisify.custom");
Object.defineProperty(pipeline, kCustomPromisify, {
  enumerable: true,
  get() { return promises.pipeline; },
});
Object.defineProperty(eos, kCustomPromisify, {
  enumerable: true,
  get() { return promises.finished; },
});
Stream.Stream = Stream;
Stream._isArrayBufferView = isArrayBufferView;
Stream._isUint8Array = isUint8Array;
Stream._uint8ArrayToBuffer = uint8ArrayToBuffer;

// Hidden hook for the sibling shims (stream/web needs the Duplex<->web
// pair adapter to build CompressionStream and friends).
Object.defineProperty(Stream, Symbol.for("noderati.stream.internals"), {
  value: { newReadableWritablePairFromDuplex, kWebClosed },
});

const finishedExport = eos;
export {
  Stream, Readable, Writable, Duplex, Transform, PassThrough, duplexPair, pipeline, addAbortSignal,
  finishedExport as finished, destroyer as destroy, composeStreams as compose, setDefaultHighWaterMark,
  getDefaultHighWaterMark, promises, isDestroyed, isDisturbed, isErrored, isReadable, isWritable,
  isArrayBufferView as _isArrayBufferView, isUint8Array as _isUint8Array, uint8ArrayToBuffer as _uint8ArrayToBuffer,
};
export default Stream;
`

// require('stream/promises') === require('stream').promises in Node.
const streamPromisesShim = `import Stream from "stream";
const promises = Stream.promises;
export const finished = promises.finished;
export const pipeline = promises.pipeline;
export default promises;
`

const streamConsumersShim = `import Stream from "stream";

// Host-native emitter streams (http.IncomingMessage) may lack
// [Symbol.asyncIterator]; read those through Readable's iterator, which
// wraps a stream without read().
function iterable(stream) {
  if (stream && typeof stream[Symbol.asyncIterator] !== "function" && typeof stream.on === "function") {
    return Stream.Readable.prototype.iterator.call(stream);
  }
  return stream;
}
async function blob(stream) {
  const chunks = [];
  for await (const chunk of iterable(stream)) chunks.push(chunk);
  return new Blob(chunks);
}
async function arrayBuffer(stream) {
  const ret = await blob(stream);
  return ret.arrayBuffer();
}
async function buffer(stream) {
  return Buffer.from(await arrayBuffer(stream));
}
async function bytes(stream) {
  return new Uint8Array(await arrayBuffer(stream));
}
async function text(stream) {
  const dec = new TextDecoder();
  let str = "";
  for await (const chunk of iterable(stream)) {
    if (typeof chunk === "string") str += chunk;
    else str += dec.decode(chunk, { stream: true });
  }
  str += dec.decode(undefined, { stream: false });
  return str;
}
async function json(stream) {
  const str = await text(stream);
  return JSON.parse(str);
}
const consumers = { arrayBuffer, blob, buffer, bytes, text, json };
export { arrayBuffer, blob, buffer, bytes, text, json };
export default consumers;
`

func declareStream() {
	registerJSShim("stream", streamShim)
	registerJSShim("stream/promises", streamPromisesShim)
	registerJSShim("stream/consumers", streamConsumersShim)
	registerJSShim("stream/web", streamWebShim)
}
