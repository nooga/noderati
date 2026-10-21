package host

// streamCoreJS is the first chunk of the node:stream shim (assembled in
// stream.go): Node's internal error codes and validators as the stream
// implementation needs them, the stream-type predicates of
// lib/internal/streams/utils.js, the shared destroy()/construct()
// machinery both halves of a Duplex go through, the legacy Stream base
// class, and the default highWaterMark knobs.
//
// Everything here is a plain function/prototype implementation rather
// than ES classes on purpose: real Node's Stream/Readable/Writable/
// Duplex/Transform are plain functions, and a lot of real code still
// does "Readable.call(this, opts)" + util.inherits() (send, through2,
// readable-stream v2 style libraries) which throws on an ES class.
const streamCoreJS = `import EventEmitter from "events";
import { StringDecoder } from "string_decoder";
import { inspect as __inspect } from "util";

const nop = () => {};
const kEmptyObject = Object.freeze({ __proto__: null });

// ---- errors ---------------------------------------------------------

function formatList(arr, type) {
  type = type || "and";
  if (arr.length === 0) return "";
  if (arr.length === 1) return String(arr[0]);
  if (arr.length === 2) return arr[0] + " " + type + " " + arr[1];
  return arr.slice(0, -1).join(", ") + ", " + type + " " + arr[arr.length - 1];
}

function safeInspect(v) {
  try { return __inspect(v); } catch { return String(v); }
}

function determineSpecificType(value) {
  if (value === null) return "null";
  if (value === undefined) return "undefined";
  const type = typeof value;
  switch (type) {
    case "bigint": return "type bigint (" + value + "n)";
    case "number":
      if (value === 0) return 1 / value === -Infinity ? "type number (-0)" : "type number (0)";
      return "type number (" + value + ")";
    case "boolean": return "type boolean (" + value + ")";
    case "symbol": return "type symbol (" + String(value) + ")";
    case "function": return "function " + value.name;
    case "object":
      if (value.constructor && "name" in value.constructor) return "an instance of " + value.constructor.name;
      return safeInspect(value);
    case "string":
      if (value.length > 28) value = value.slice(0, 25) + "...";
      if (value.indexOf("'") === -1) return "type string ('" + value + "')";
      return "type string (" + JSON.stringify(value) + ")";
  }
  return safeInspect(value);
}

const kTypes = ["string", "function", "number", "object", "Function", "Object", "boolean", "bigint", "symbol"];

function invalidArgTypeMessage(name, expected, actual) {
  if (!Array.isArray(expected)) expected = [expected];
  let msg = "The ";
  if (name.endsWith(" argument")) msg += name + " ";
  else msg += '"' + name + '" ' + (name.includes(".") ? "property" : "argument") + " ";
  msg += "must be ";
  const types = [], instances = [], other = [];
  for (const value of expected) {
    if (kTypes.includes(value)) types.push(value.toLowerCase());
    else if (/^[A-Z][a-zA-Z0-9]*$/.test(value)) instances.push(value);
    else other.push(value);
  }
  if (instances.length > 0) {
    const pos = types.indexOf("object");
    if (pos !== -1) { types.splice(pos, 1); instances.push("Object"); }
  }
  if (types.length > 0) {
    msg += (types.length > 1 ? "one of type " : "of type ") + formatList(types, "or");
    if (instances.length > 0 || other.length > 0) msg += " or ";
  }
  if (instances.length > 0) {
    msg += "an instance of " + formatList(instances, "or");
    if (other.length > 0) msg += " or ";
  }
  if (other.length > 0) {
    if (other.length > 1) msg += "one of " + formatList(other, "or");
    else {
      if (other[0].toLowerCase() !== other[0]) msg += "an ";
      msg += other[0];
    }
  }
  return msg + ". Received " + determineSpecificType(actual);
}

function makeNodeError(Base, code, format) {
  const NodeError = class extends Base {
    constructor(...args) {
      super(typeof format === "function" ? format(...args) : format.replace(/%s/g, () => String(args.shift())));
      Object.defineProperty(this, "code", { value: code, enumerable: true, writable: true, configurable: true });
      try {
        const stack = this.stack;
        const head = this.name + ": ";
        if (typeof stack === "string" && stack.startsWith(head)) {
          Object.defineProperty(this, "stack", { value: this.name + " [" + code + "]: " + stack.slice(head.length), enumerable: false, writable: true, configurable: true });
        }
      } catch {}
    }
    toString() {
      return this.name + " [" + code + "]: " + this.message;
    }
  };
  Object.defineProperty(NodeError.prototype, "name", { value: Base.prototype.name, enumerable: false, writable: true, configurable: true });
  return NodeError;
}

const codes = {
  ERR_ILLEGAL_CONSTRUCTOR: makeNodeError(TypeError, "ERR_ILLEGAL_CONSTRUCTOR", "Illegal constructor"),
  ERR_INVALID_ARG_TYPE: makeNodeError(TypeError, "ERR_INVALID_ARG_TYPE", invalidArgTypeMessage),
  ERR_INVALID_ARG_VALUE: makeNodeError(TypeError, "ERR_INVALID_ARG_VALUE", (name, value, reason) => {
    let inspected = safeInspect(value);
    if (inspected.length > 128) inspected = inspected.slice(0, 128) + "...";
    return "The " + (name.includes(".") ? "property" : "argument") + " '" + name + "' " + (reason || "is invalid") + ". Received " + inspected;
  }),
  ERR_INVALID_RETURN_VALUE: makeNodeError(TypeError, "ERR_INVALID_RETURN_VALUE", (input, name, value) =>
    "Expected " + input + ' to be returned from the "' + name + '" function but got ' + determineSpecificType(value) + "."),
  ERR_INVALID_STATE: makeNodeError(Error, "ERR_INVALID_STATE", "Invalid state: %s"),
  ERR_METHOD_NOT_IMPLEMENTED: makeNodeError(Error, "ERR_METHOD_NOT_IMPLEMENTED", "The %s method is not implemented"),
  ERR_MISSING_ARGS: makeNodeError(TypeError, "ERR_MISSING_ARGS", (...args) => {
    const wrapped = args.map((a) => Array.isArray(a) ? a.map((x) => '"' + x + '"').join(" or ") : '"' + a + '"');
    return "The " + formatList(wrapped) + " argument" + (args.length > 1 ? "s" : "") + " must be specified";
  }),
  ERR_MULTIPLE_CALLBACK: makeNodeError(Error, "ERR_MULTIPLE_CALLBACK", "Callback called multiple times"),
  ERR_OUT_OF_RANGE: makeNodeError(RangeError, "ERR_OUT_OF_RANGE", (str, range, input) =>
    'The value of "' + str + '" is out of range. It must be ' + range + ". Received " + safeInspect(input)),
  ERR_STREAM_ALREADY_FINISHED: makeNodeError(Error, "ERR_STREAM_ALREADY_FINISHED", "Cannot call %s after a stream was finished"),
  ERR_STREAM_CANNOT_PIPE: makeNodeError(Error, "ERR_STREAM_CANNOT_PIPE", "Cannot pipe, not readable"),
  ERR_STREAM_DESTROYED: makeNodeError(Error, "ERR_STREAM_DESTROYED", "Cannot call %s after a stream was destroyed"),
  ERR_STREAM_NULL_VALUES: makeNodeError(TypeError, "ERR_STREAM_NULL_VALUES", "May not write null values to stream"),
  ERR_STREAM_PREMATURE_CLOSE: makeNodeError(Error, "ERR_STREAM_PREMATURE_CLOSE", "Premature close"),
  ERR_STREAM_PUSH_AFTER_EOF: makeNodeError(Error, "ERR_STREAM_PUSH_AFTER_EOF", "stream.push() after EOF"),
  ERR_STREAM_UNABLE_TO_PIPE: makeNodeError(Error, "ERR_STREAM_UNABLE_TO_PIPE", "Cannot pipe to a closed or destroyed stream"),
  ERR_STREAM_UNSHIFT_AFTER_END_EVENT: makeNodeError(Error, "ERR_STREAM_UNSHIFT_AFTER_END_EVENT", "stream.unshift() after end event"),
  ERR_STREAM_WRITE_AFTER_END: makeNodeError(Error, "ERR_STREAM_WRITE_AFTER_END", "write after end"),
  ERR_UNKNOWN_ENCODING: makeNodeError(TypeError, "ERR_UNKNOWN_ENCODING", "Unknown encoding: %s"),
};
const {
  ERR_INVALID_ARG_TYPE, ERR_INVALID_ARG_VALUE, ERR_INVALID_RETURN_VALUE, ERR_METHOD_NOT_IMPLEMENTED,
  ERR_MISSING_ARGS, ERR_MULTIPLE_CALLBACK, ERR_OUT_OF_RANGE, ERR_STREAM_ALREADY_FINISHED,
  ERR_STREAM_CANNOT_PIPE, ERR_STREAM_DESTROYED, ERR_STREAM_NULL_VALUES, ERR_STREAM_PREMATURE_CLOSE,
  ERR_STREAM_PUSH_AFTER_EOF, ERR_STREAM_UNABLE_TO_PIPE, ERR_STREAM_UNSHIFT_AFTER_END_EVENT,
  ERR_STREAM_WRITE_AFTER_END, ERR_UNKNOWN_ENCODING, ERR_ILLEGAL_CONSTRUCTOR,
} = codes;

class AbortError extends Error {
  constructor(message, options) {
    if (message === undefined) message = "The operation was aborted";
    if (options !== undefined && typeof options !== "object") {
      throw new ERR_INVALID_ARG_TYPE("options", "Object", options);
    }
    super(message, options);
    this.code = "ABORT_ERR";
    this.name = "AbortError";
  }
}

function aggregateTwoErrors(innerError, outerError) {
  if (innerError && outerError && innerError !== outerError) {
    if (Array.isArray(outerError.errors)) {
      outerError.errors.push(innerError);
      return outerError;
    }
    const err = new AggregateError([outerError, innerError], outerError.message);
    err.code = outerError.code;
    return err;
  }
  return innerError || outerError;
}

// ---- validators -----------------------------------------------------

function validateFunction(value, name) {
  if (typeof value !== "function") throw new ERR_INVALID_ARG_TYPE(name, "Function", value);
}
function validateObject(value, name) {
  if (value === null || Array.isArray(value) || typeof value !== "object") {
    throw new ERR_INVALID_ARG_TYPE(name, "Object", value);
  }
}
function validateBoolean(value, name) {
  if (typeof value !== "boolean") throw new ERR_INVALID_ARG_TYPE(name, "boolean", value);
}
function validateInteger(value, name, min, max) {
  if (min === undefined) min = Number.MIN_SAFE_INTEGER;
  if (max === undefined) max = Number.MAX_SAFE_INTEGER;
  if (typeof value !== "number") throw new ERR_INVALID_ARG_TYPE(name, "number", value);
  if (!Number.isInteger(value)) throw new ERR_OUT_OF_RANGE(name, "an integer", value);
  if (value < min || value > max) throw new ERR_OUT_OF_RANGE(name, ">= " + min + " && <= " + max, value);
}
function validateAbortSignal(signal, name) {
  if (signal !== undefined && (signal === null || typeof signal !== "object" || !("aborted" in signal))) {
    throw new ERR_INVALID_ARG_TYPE(name, "AbortSignal", signal);
  }
}

function once(callback) {
  let called = false;
  return function (...args) {
    if (called) return;
    called = true;
    return Reflect.apply(callback, this, args);
  };
}

// addAbortListener mirrors events.addAbortListener, returning a disposable.
function addAbortListener(signal, listener) {
  if (signal.aborted) {
    queueMicrotask(() => listener());
    return { [Symbol.dispose]() {} };
  }
  signal.addEventListener("abort", listener, { once: true });
  return {
    [Symbol.dispose]() {
      signal.removeEventListener("abort", listener);
    },
  };
}

function isArrayBufferView(v) { return ArrayBuffer.isView(v); }
function isUint8Array(v) { return v instanceof Uint8Array; }
function uint8ArrayToBuffer(chunk) {
  return Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
}
function isBlob(b) { return typeof Blob === "function" && b instanceof Blob; }

// ---- stream predicates (lib/internal/streams/utils.js) --------------

const kIsDestroyed = Symbol.for("nodejs.stream.destroyed");
const kIsErrored = Symbol.for("nodejs.stream.errored");
const kIsReadable = Symbol.for("nodejs.stream.readable");
const kIsWritable = Symbol.for("nodejs.stream.writable");
const kIsDisturbed = Symbol.for("nodejs.stream.disturbed");
const kOnConstructed = Symbol("kOnConstructed");

function isReadableNodeStream(obj, strict) {
  return !!(obj &&
    typeof obj.pipe === "function" &&
    typeof obj.on === "function" &&
    (!strict || (typeof obj.pause === "function" && typeof obj.resume === "function")) &&
    (!obj._writableState || (obj._readableState && obj._readableState.readable !== false)) &&
    (!obj._writableState || obj._readableState));
}
function isWritableNodeStream(obj) {
  return !!(obj &&
    typeof obj.write === "function" &&
    typeof obj.on === "function" &&
    (!obj._readableState || (obj._writableState && obj._writableState.writable !== false)));
}
function isDuplexNodeStream(obj) {
  return !!(obj &&
    (typeof obj.pipe === "function" && obj._readableState) &&
    typeof obj.on === "function" &&
    typeof obj.write === "function");
}
function isNodeStream(obj) {
  return !!(obj && (
    obj._readableState ||
    obj._writableState ||
    (typeof obj.write === "function" && typeof obj.on === "function") ||
    (typeof obj.pipe === "function" && typeof obj.on === "function")));
}
function isReadableStream(obj) {
  return !!(obj && !isNodeStream(obj) &&
    typeof obj.pipeThrough === "function" &&
    typeof obj.getReader === "function" &&
    typeof obj.cancel === "function");
}
function isWritableStream(obj) {
  return !!(obj && !isNodeStream(obj) &&
    typeof obj.getWriter === "function" &&
    typeof obj.abort === "function");
}
function isTransformStream(obj) {
  return !!(obj && !isNodeStream(obj) &&
    typeof obj.readable === "object" &&
    typeof obj.writable === "object");
}
function isWebStream(obj) {
  return isReadableStream(obj) || isWritableStream(obj) || isTransformStream(obj);
}
function isIterable(obj, isAsync) {
  if (obj == null) return false;
  if (isAsync === true) return typeof obj[Symbol.asyncIterator] === "function";
  if (isAsync === false) return typeof obj[Symbol.iterator] === "function";
  return typeof obj[Symbol.asyncIterator] === "function" || typeof obj[Symbol.iterator] === "function";
}
function isDestroyed(stream) {
  if (!isNodeStream(stream)) return null;
  const state = stream._writableState || stream._readableState;
  return !!(stream.destroyed || stream[kIsDestroyed] || (state && state.destroyed));
}
function isWritableEnded(stream) {
  if (!isWritableNodeStream(stream)) return null;
  if (stream.writableEnded === true) return true;
  const w = stream._writableState;
  if (w && w.errored) return false;
  if (!w || typeof w.ended !== "boolean") return null;
  return w.ended;
}
function isWritableFinished(stream, strict) {
  if (!isWritableNodeStream(stream)) return null;
  if (stream.writableFinished === true) return true;
  const w = stream._writableState;
  if (w && w.errored) return false;
  if (!w || typeof w.finished !== "boolean") return null;
  return !!(w.finished || (strict === false && w.ended === true && w.length === 0));
}
function isReadableFinished(stream, strict) {
  if (!isReadableNodeStream(stream)) return null;
  const r = stream._readableState;
  if (r && r.errored) return false;
  if (!r || typeof r.endEmitted !== "boolean") return null;
  return !!(r.endEmitted || (strict === false && r.ended === true && r.length === 0));
}
function isReadable(stream) {
  if (stream && stream[kIsReadable] != null) return stream[kIsReadable];
  if (!stream || typeof stream.readable !== "boolean") return null;
  if (isDestroyed(stream)) return false;
  return isReadableNodeStream(stream) && stream.readable && !isReadableFinished(stream);
}
function isWritable(stream) {
  if (stream && stream[kIsWritable] != null) return stream[kIsWritable];
  if (!stream || typeof stream.writable !== "boolean") return null;
  if (isDestroyed(stream)) return false;
  return isWritableNodeStream(stream) && stream.writable && !isWritableEnded(stream);
}
function isFinished(stream, opts) {
  if (!isNodeStream(stream)) return null;
  if (isDestroyed(stream)) return true;
  if ((!opts || opts.readable !== false) && isReadable(stream)) return false;
  if ((!opts || opts.writable !== false) && isWritable(stream)) return false;
  return true;
}
function isWritableErrored(stream) {
  if (!isNodeStream(stream)) return null;
  if (stream.writableErrored) return stream.writableErrored;
  const w = stream._writableState;
  return (w && w.errored) ?? null;
}
function isReadableErrored(stream) {
  if (!isNodeStream(stream)) return null;
  if (stream.readableErrored) return stream.readableErrored;
  const r = stream._readableState;
  return (r && r.errored) ?? null;
}
function isOutgoingMessage(stream) {
  return typeof stream._closed === "boolean" &&
    typeof stream._defaultKeepAlive === "boolean" &&
    typeof stream._removedConnection === "boolean" &&
    typeof stream._removedContLen === "boolean";
}
function isServerResponse(stream) {
  return typeof stream._sent100 === "boolean" && isOutgoingMessage(stream);
}
function isServerRequest(stream) {
  return typeof stream._consuming === "boolean" &&
    typeof stream._dumped === "boolean" &&
    (!stream.req || stream.req.upgradeOrConnect === undefined);
}
function isClosed(stream) {
  if (!isNodeStream(stream)) return null;
  if (typeof stream.closed === "boolean") return stream.closed;
  const w = stream._writableState, r = stream._readableState;
  if ((w && typeof w.closed === "boolean") || (r && typeof r.closed === "boolean")) {
    return (w && w.closed) || (r && r.closed);
  }
  if (typeof stream._closed === "boolean" && isOutgoingMessage(stream)) return stream._closed;
  return null;
}
function willEmitClose(stream) {
  if (!isNodeStream(stream)) return null;
  const state = stream._writableState || stream._readableState;
  return (!state && isServerResponse(stream)) ||
    !!(state && state.autoDestroy && state.emitClose && state.closed === false);
}
function isDisturbed(stream) {
  return !!(stream && (stream[kIsDisturbed] ??
    (stream.readableDidRead || stream.readableAborted || stream._disturbed)));
}
function isErrored(stream) {
  if (!stream) return false;
  const r = stream._readableState, w = stream._writableState;
  return !!(stream[kIsErrored] ??
    stream.readableErrored ??
    stream.writableErrored ??
    (r ? r.errorEmitted : undefined) ??
    (w ? w.errorEmitted : undefined) ??
    (r ? r.errored : undefined) ??
    (w ? w.errored : undefined) ??
    stream._error);
}

// ---- destroy / construct (lib/internal/streams/destroy.js) ----------

const kDestroy = Symbol("kDestroy");
const kConstruct = Symbol("kConstruct");

function checkError(err, w, r) {
  if (err) {
    if (w && !w.errored) w.errored = err;
    if (r && !r.errored) r.errored = err;
  }
}

function destroyStream(err, cb) {
  const r = this._readableState;
  const w = this._writableState;
  const s = w || r;
  if ((w && w.destroyed) || (r && r.destroyed)) {
    if (typeof cb === "function") cb();
    return this;
  }
  checkError(err, w, r);
  if (w) w.destroyed = true;
  if (r) r.destroyed = true;
  if (!s.constructed) {
    this.once(kDestroy, function (er) {
      _destroy(this, aggregateTwoErrors(er, err), cb);
    });
  } else {
    _destroy(this, err, cb);
  }
  return this;
}

function _destroy(self, err, cb) {
  let called = false;
  function onDestroy(err) {
    if (called) return;
    called = true;
    const r = self._readableState;
    const w = self._writableState;
    checkError(err, w, r);
    if (w) w.closed = true;
    if (r) r.closed = true;
    if (typeof cb === "function") cb(err);
    if (err) process.nextTick(emitErrorCloseNT, self, err);
    else process.nextTick(emitCloseNT, self);
  }
  try {
    self._destroy(err || null, onDestroy);
  } catch (e) {
    onDestroy(e);
  }
}

function emitErrorCloseNT(self, err) {
  emitErrorNT(self, err);
  emitCloseNT(self);
}

function emitCloseNT(self) {
  const r = self._readableState;
  const w = self._writableState;
  if (w) w.closeEmitted = true;
  if (r) r.closeEmitted = true;
  if ((w && w.emitClose) || (r && r.emitClose)) self.emit("close");
}

function emitErrorNT(self, err) {
  const r = self._readableState;
  const w = self._writableState;
  if ((w && w.errorEmitted) || (r && r.errorEmitted)) return;
  if (w) w.errorEmitted = true;
  if (r) r.errorEmitted = true;
  self.emit("error", err);
}

function undestroy() {
  const r = this._readableState;
  const w = this._writableState;
  if (r) {
    r.constructed = true;
    r.closed = false;
    r.closeEmitted = false;
    r.destroyed = false;
    r.errored = null;
    r.errorEmitted = false;
    r.reading = false;
    r.ended = r.readable === false;
    r.endEmitted = r.readable === false;
  }
  if (w) {
    w.constructed = true;
    w.destroyed = false;
    w.closed = false;
    w.closeEmitted = false;
    w.errored = null;
    w.errorEmitted = false;
    w.finalCalled = false;
    w.prefinished = false;
    w.ended = w.writable === false;
    w.ending = w.writable === false;
    w.finished = w.writable === false;
  }
}

function errorOrDestroy(stream, err, sync) {
  const r = stream._readableState;
  const w = stream._writableState;
  if ((w && w.destroyed) || (r && r.destroyed)) return;
  if ((r && r.autoDestroy) || (w && w.autoDestroy)) {
    stream.destroy(err);
  } else if (err) {
    if (w && !w.errored) w.errored = err;
    if (r && !r.errored) r.errored = err;
    if (sync) process.nextTick(emitErrorNT, stream, err);
    else emitErrorNT(stream, err);
  }
}

function construct(stream, cb) {
  if (typeof stream._construct !== "function") return;
  const r = stream._readableState;
  const w = stream._writableState;
  if (r) r.constructed = false;
  if (w) w.constructed = false;
  stream.once(kConstruct, cb);
  if (stream.listenerCount(kConstruct) > 1) return;
  process.nextTick(constructNT, stream);
}

function constructNT(stream) {
  let called = false;
  function onConstruct(err) {
    if (called) {
      errorOrDestroy(stream, err ?? new ERR_MULTIPLE_CALLBACK());
      return;
    }
    called = true;
    const r = stream._readableState;
    const w = stream._writableState;
    const s = w || r;
    if (r) r.constructed = true;
    if (w) w.constructed = true;
    if (s.destroyed) stream.emit(kDestroy, err);
    else if (err) errorOrDestroy(stream, err, true);
    else stream.emit(kConstruct);
  }
  try {
    stream._construct((err) => {
      process.nextTick(onConstruct, err);
    });
  } catch (err) {
    process.nextTick(onConstruct, err);
  }
}

function isRequest(stream) {
  return !!(stream && stream.setHeader && typeof stream.abort === "function");
}

function emitCloseLegacy(stream) {
  stream.emit("close");
}
function emitErrorCloseLegacy(stream, err) {
  stream.emit("error", err);
  process.nextTick(emitCloseLegacy, stream);
}

// destroyer is the public stream.destroy(stream, err).
function destroyer(stream, err) {
  if (!stream || isDestroyed(stream)) return;
  if (!err && !isFinished(stream)) err = new AbortError();
  if (isServerRequest(stream)) {
    stream.socket = null;
    stream.destroy(err);
  } else if (isRequest(stream)) {
    stream.abort();
  } else if (isRequest(stream.req)) {
    stream.req.abort();
  } else if (typeof stream.destroy === "function") {
    stream.destroy(err);
  } else if (typeof stream.close === "function") {
    stream.close();
  } else if (err) {
    process.nextTick(emitErrorCloseLegacy, stream, err);
  } else {
    process.nextTick(emitCloseLegacy, stream);
  }
  if (!stream.destroyed) stream[kIsDestroyed] = true;
}

// ---- legacy Stream (lib/internal/streams/legacy.js) -----------------

function Stream(opts) {
  EventEmitter.init.call(this, opts);
}
Object.setPrototypeOf(Stream.prototype, EventEmitter.prototype);
Object.setPrototypeOf(Stream, EventEmitter);

function prependListener(emitter, event, fn) {
  if (typeof emitter.prependListener === "function") return emitter.prependListener(event, fn);
  if (!emitter._events || !emitter._events[event]) emitter.on(event, fn);
  else if (Array.isArray(emitter._events[event])) emitter._events[event].unshift(fn);
  else emitter._events[event] = [fn, emitter._events[event]];
}

// The classic pre-streams2 pipe(): what a class that extends Stream
// directly (emitting 'data'/'end' by hand, e.g. send's SendStream) gets.
Stream.prototype.pipe = function (dest, options) {
  const source = this;
  function ondata(chunk) {
    if (dest.writable && dest.write(chunk) === false && source.pause) source.pause();
  }
  source.on("data", ondata);
  function ondrain() {
    if (source.readable && source.resume) source.resume();
  }
  dest.on("drain", ondrain);
  if (!dest._isStdio && (!options || options.end !== false)) {
    source.on("end", onend);
    source.on("close", onclose);
  }
  let didOnEnd = false;
  function onend() {
    if (didOnEnd) return;
    didOnEnd = true;
    dest.end();
  }
  function onclose() {
    if (didOnEnd) return;
    didOnEnd = true;
    if (typeof dest.destroy === "function") dest.destroy();
  }
  function onerror(er) {
    cleanup();
    if (this.listenerCount && this.listenerCount("error") === 0) this.emit("error", er);
  }
  prependListener(source, "error", onerror);
  prependListener(dest, "error", onerror);
  function cleanup() {
    source.removeListener("data", ondata);
    dest.removeListener("drain", ondrain);
    source.removeListener("end", onend);
    source.removeListener("close", onclose);
    source.removeListener("error", onerror);
    dest.removeListener("error", onerror);
    source.removeListener("end", cleanup);
    source.removeListener("close", cleanup);
    dest.removeListener("close", cleanup);
  }
  source.on("end", cleanup);
  source.on("close", cleanup);
  dest.on("close", cleanup);
  dest.emit("pipe", source);
  return dest;
};

// ---- highWaterMark (lib/internal/streams/state.js) ------------------

let defaultHighWaterMarkBytes = process.platform === "win32" ? 16 * 1024 : 64 * 1024;
let defaultHighWaterMarkObjectMode = 16;

function getDefaultHighWaterMark(objectMode) {
  return objectMode ? defaultHighWaterMarkObjectMode : defaultHighWaterMarkBytes;
}
function setDefaultHighWaterMark(objectMode, value) {
  validateInteger(value, "value", 0);
  if (objectMode) defaultHighWaterMarkObjectMode = value;
  else defaultHighWaterMarkBytes = value;
}
function getHighWaterMark(state, options, duplexKey, isDuplex) {
  const hwm = options.highWaterMark != null ? options.highWaterMark : (isDuplex ? options[duplexKey] : null);
  if (hwm != null) {
    if (!Number.isInteger(hwm) || hwm < 0) {
      const name = isDuplex ? "options." + duplexKey : "options.highWaterMark";
      throw new ERR_INVALID_ARG_VALUE(name, hwm);
    }
    return Math.floor(hwm);
  }
  return getDefaultHighWaterMark(state.objectMode);
}
`
