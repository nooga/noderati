package host

// netShim is node:net's JS half - a close port of Node's own lib/net.js
// (Socket as a real Duplex over a native handle, Server as an
// EventEmitter over a native listening handle), with the libuv handle
// replaced by net.go's binding. See net.go for the handle contract.
const netShim = `import EventEmitter from "events";
import * as __stream from "stream";
import dns from "dns";
import { inspect as __inspect } from "node:util";

const binding = globalThis.__noderatiNetBinding;
const { Readable, Writable } = __stream;

// ---- Duplex base ----------------------------------------------------
// net.Socket must be a real Duplex. Use the stream module's own Duplex
// when it is a real Readable+Writable (Node's shape: Duplex extends
// Readable and mixes Writable in); until then, build exactly that shape
// here from the stream module's real Readable and Writable.
function makeDuplex() {
  const WritableState = new Writable({ write(c, e, cb) { cb(); } })._writableState.constructor;
  class NetDuplex extends Readable {
    constructor(opts) {
      super(opts);
      this._writableState = new WritableState(opts, this);
      if (opts) {
        if (typeof opts.write === "function") this._write = opts.write;
        if (typeof opts.writev === "function") this._writev = opts.writev;
        if (typeof opts.final === "function") this._final = opts.final;
        if (opts.readable === false) { this._readableState.readable = false; this._readableState.ended = true; this._readableState.endEmitted = true; }
        if (opts.writable === false) { this._writableState.writable = false; this._writableState.ending = true; this._writableState.ended = true; this._writableState.finished = true; }
      }
      this.allowHalfOpen = !(opts && opts.allowHalfOpen === false);
      if (!this.allowHalfOpen) {
        this.once("end", () => {
          if (this._writableState.ended) return;
          process.nextTick(() => this.end());
        });
      }
    }
    destroy(err, cb) {
      const r = this._readableState;
      if (r && !r.destroyed) {
        r.destroyed = true;
        if (err && !r.errored) r.errored = err;
      }
      Writable.prototype.destroy.call(this, err, (e) => {
        if (r) {
          r.closed = true;
          if (e) r.errorEmitted = true;
        }
        if (typeof cb === "function") cb(e);
      });
      return this;
    }
    get destroyed() { return this._readableState.destroyed || this._writableState.destroyed; }
    set destroyed(v) { this._readableState.destroyed = v; this._writableState.destroyed = v; }
    get closed() { return this._readableState.closed && this._writableState.closed; }
    get errored() { return this._readableState.errored || this._writableState.errored; }
  }
  const skip = new Set(["constructor", "destroy", "_destroy", "destroyed", "closed", "errored"]);
  for (const key of Reflect.ownKeys(Writable.prototype)) {
    if (skip.has(key) || Object.prototype.hasOwnProperty.call(NetDuplex.prototype, key)) continue;
    Object.defineProperty(NetDuplex.prototype, key, Object.getOwnPropertyDescriptor(Writable.prototype, key));
  }
  return NetDuplex;
}
const DuplexBase = (__stream.Duplex && __stream.Duplex.prototype instanceof Readable) ? __stream.Duplex : makeDuplex();
const duplexProto = DuplexBase.prototype;

// ---- errors ---------------------------------------------------------
function makeErr(Base, code, message) {
  const e = new Base(message);
  Object.defineProperty(e, "code", { value: code, enumerable: true, writable: true, configurable: true });
  try {
    Object.defineProperty(e, "name", { value: Base.name, enumerable: false, writable: true, configurable: true });
  } catch {}
  return e;
}
function describe(v) {
  if (v == null) return " Received " + v;
  if (typeof v === "function") return " Received function " + (v.name || "<anonymous>");
  if (typeof v === "object") {
    if (v.constructor && v.constructor.name && v.constructor !== Object) return " Received an instance of " + v.constructor.name;
    let s;
    try { s = __inspect(v, { depth: -1 }); } catch { s = "[object Object]"; }
    return " Received " + s;
  }
  let s = String(v);
  if (typeof v === "string") { if (s.length > 28) s = s.slice(0, 25) + "..."; s = "'" + s + "'"; }
  if (typeof v === "bigint") s += "n";
  return " Received type " + typeof v + " (" + s + ")";
}
function ERR_INVALID_ARG_TYPE(name, types, actual) {
  const kind = name.includes(".") ? "property" : "argument";
  const t = Array.isArray(types) ? types : [types];
  const list = t.length > 1 ? "one of type " + t.join(" or ") : "of type " + t[0];
  return makeErr(TypeError, "ERR_INVALID_ARG_TYPE", 'The "' + name + '" ' + kind + " must be " + list + "." + describe(actual));
}
function ERR_INVALID_ARG_VALUE(name, value, reason = "is invalid") {
  const kind = name.includes(".") ? "property" : "argument";
  let shown;
  try { shown = __inspect(value); } catch { shown = String(value); }
  return makeErr(TypeError, "ERR_INVALID_ARG_VALUE", "The " + kind + " '" + name + "' " + reason + ". Received " + shown);
}
function ERR_SOCKET_BAD_PORT(name, port, allowZero = true) {
  const op = allowZero ? ">=" : ">";
  return makeErr(RangeError, "ERR_SOCKET_BAD_PORT", name + " should be " + op + " 0 and < 65536." + describe(port) + ".");
}
function validatePort(port, name = "Port", allowZero = true) {
  if ((typeof port !== "number" && typeof port !== "string") ||
      (typeof port === "string" && port.trim().length === 0) ||
      +port !== (+port >>> 0) || port > 0xFFFF || (port === 0 && !allowZero)) {
    throw ERR_SOCKET_BAD_PORT(name, port, allowZero);
  }
  return port | 0;
}
function validateFunction(v, name) {
  if (typeof v !== "function") throw ERR_INVALID_ARG_TYPE(name, "function", v);
}
function validateNumber(v, name, min, max) {
  if (typeof v !== "number") throw ERR_INVALID_ARG_TYPE(name, "number", v);
  if ((min != null && v < min) || (max != null && v > max) || ((min != null || max != null) && Number.isNaN(v))) {
    throw makeErr(RangeError, "ERR_OUT_OF_RANGE", 'The value of "' + name + '" is out of range. It must be ' +
      (min != null && max != null ? ">= " + min + " && <= " + max : min != null ? ">= " + min : "<= " + max) + ". Received " + v);
  }
}
function validateInt32(v, name, min = -2147483648, max = 2147483647) {
  if (typeof v !== "number") throw ERR_INVALID_ARG_TYPE(name, "number", v);
  if (!Number.isInteger(v)) throw makeErr(RangeError, "ERR_OUT_OF_RANGE", 'The value of "' + name + '" is out of range. It must be an integer. Received ' + v);
  if (v < min || v > max) throw makeErr(RangeError, "ERR_OUT_OF_RANGE", 'The value of "' + name + '" is out of range. It must be >= ' + min + " && <= " + max + ". Received " + v);
}
function validateBoolean(v, name) {
  if (typeof v !== "boolean") throw ERR_INVALID_ARG_TYPE(name, "boolean", v);
}
function validateString(v, name) {
  if (typeof v !== "string") throw ERR_INVALID_ARG_TYPE(name, "string", v);
}
function validateAbortSignal(signal, name) {
  if (signal !== undefined && (signal === null || typeof signal !== "object" || !("aborted" in signal))) {
    throw ERR_INVALID_ARG_TYPE(name, "AbortSignal", signal);
  }
}
function abortError(signal) {
  const e = new Error("The operation was aborted");
  e.name = "AbortError";
  e.code = "ABORT_ERR";
  if (signal && signal.reason !== undefined) e.cause = signal.reason;
  return e;
}
function uvMessage(code) {
  return binding.errname(code) || code.toLowerCase();
}
// errnoException: "<syscall> <code>" (+ original Error shape).
function errnoException(info, syscall, original) {
  const code = info.code;
  const msg = info.message && info.code === "UNKNOWN" ? info.message : (syscall ? syscall + " " + code : code);
  const ex = new Error(original ? msg + " " + original : msg);
  ex.errno = info.errno;
  ex.code = code;
  ex.syscall = syscall;
  return ex;
}
// exceptionWithHostPort: "connect ECONNREFUSED 127.0.0.1:80"
function exceptionWithHostPort(info, syscall, address, port, additional) {
  let details = "";
  if (port && port > 0) details = " " + address + ":" + port;
  else if (address) details = " " + address;
  if (additional) details += " - Local (" + additional + ")";
  const ex = new Error(syscall + " " + info.code + details);
  ex.errno = info.errno;
  ex.code = info.code;
  ex.syscall = syscall;
  ex.address = address;
  if (port) ex.port = port;
  return ex;
}
// UVExceptionWithHostPort: "listen EADDRINUSE: address already in use :::80"
function uvExceptionWithHostPort(info, syscall, address, port) {
  let details = "";
  if (port && port > 0) details = " " + address + ":" + port;
  else if (address) details = " " + address;
  const ex = new Error(syscall + " " + info.code + ": " + uvMessage(info.code) + details);
  ex.code = info.code;
  ex.errno = info.errno;
  ex.syscall = syscall;
  ex.address = address;
  if (port) ex.port = port;
  return ex;
}

// ---- helpers --------------------------------------------------------
function isIP(input) { return binding.isIP(String(input)); }
function isIPv4(input) { return isIP(input) === 4; }
function isIPv6(input) { return isIP(input) === 6; }

const normalizedArgsSymbol = Symbol("normalizedArgs");
function toNumber(x) { return (x = Number(x)) >= 0 ? x : false; }
function isPipeName(s) { return typeof s === "string" && toNumber(s) === false; }
function normalizeArgs(args) {
  let arr;
  if (args.length === 0) {
    arr = [{}, null];
    arr[normalizedArgsSymbol] = true;
    return arr;
  }
  const arg0 = args[0];
  let options = {};
  if (typeof arg0 === "object" && arg0 !== null) {
    options = arg0;
  } else if (isPipeName(arg0)) {
    options.path = arg0;
  } else {
    options.port = arg0;
    if (args.length > 1 && typeof args[1] === "string") options.host = args[1];
  }
  const cb = args[args.length - 1];
  arr = typeof cb !== "function" ? [options, null] : [options, cb];
  arr[normalizedArgsSymbol] = true;
  return arr;
}

let autoSelectFamilyDefault = true;
let autoSelectFamilyAttemptTimeoutDefault = 500;
function getDefaultAutoSelectFamily() { return autoSelectFamilyDefault; }
function setDefaultAutoSelectFamily(value) {
  validateBoolean(value, "value");
  autoSelectFamilyDefault = value;
}
function getDefaultAutoSelectFamilyAttemptTimeout() { return autoSelectFamilyAttemptTimeoutDefault; }
function setDefaultAutoSelectFamilyAttemptTimeout(value) {
  validateInt32(value, "value", 1);
  if (value < 10) value = 10;
  autoSelectFamilyAttemptTimeoutDefault = value;
}

const kTimeout = Symbol("timeout");
const kTimeoutMs = Symbol("timeoutMs");
const kSetNoDelay = Symbol("kSetNoDelay");
const kSetKeepAlive = Symbol("kSetKeepAlive");
const kSetKeepAliveInitialDelay = Symbol("kSetKeepAliveInitialDelay");
const kBytesRead = Symbol("kBytesRead");
const kBytesWritten = Symbol("kBytesWritten");
const kOwner = Symbol("owner");
const kAttempt = Symbol("attempt");

function armTimer(s) {
  if (s[kTimeout]) clearTimeout(s[kTimeout]);
  const t = setTimeout(() => { s[kTimeout] = null; s._onTimeout(); }, s[kTimeoutMs]);
  if (t && typeof t.unref === "function") t.unref();
  s[kTimeout] = t;
}

// ---- handle callbacks -----------------------------------------------
function onStreamRead(buf, errInfo) {
  const stream = this[kOwner];
  if (!stream || stream.destroyed) return;
  const handle = this;
  if (buf) {
    stream._unrefTimer();
    const ret = stream.push(buf);
    if (handle.reading && !ret) {
      handle.reading = false;
      handle.readStop();
    }
    return;
  }
  if (buf === undefined && errInfo) {
    stream.destroy(errnoException(errInfo, "read"));
    return;
  }
  // EOF
  handle.reading = false;
  handle.readStop();
  if (!stream._readableState.endEmitted) {
    stream.push(null);
    stream.read(0);
  }
}

function initSocketHandle(self) {
  self._undestroy && self._undestroy();
  self._sockname = null;
  if (self._handle) {
    self._handle[kOwner] = self;
    self._handle.onread = onStreamRead;
    self._handle.reading = self._handle.reading || false;
  }
}

function undestroy(s) {
  const r = s._readableState, w = s._writableState;
  for (const st of [r, w]) {
    if (!st) continue;
    st.destroyed = false; st.closed = false; st.closeEmitted = false;
    st.errored = null; st.errorEmitted = false;
  }
  if (r) { r.ended = r.readable === false; r.endEmitted = r.readable === false; r.reading = false; }
  if (w) { w.ended = w.writable === false; w.ending = w.writable === false; w.finished = w.writable === false; w.finalCalled = false; w.prefinished = false; }
}

// ---- Socket ---------------------------------------------------------
class Socket extends DuplexBase {
  constructor(options) {
    if (typeof options === "number") options = { fd: options };
    else options = { ...options };
    const userAllowHalfOpen = !!options.allowHalfOpen;
    options.allowHalfOpen = userAllowHalfOpen;
    options.emitClose = false;
    options.autoDestroy = true;
    options.decodeStrings = false;
    super(options);
    this.connecting = false;
    this._hadError = false;
    this._parent = null;
    this._host = null;
    this._closeAfterHandlingError = false;
    this._handle = null;
    this._sockname = null;
    this._pendingData = null;
    this._pendingEncoding = "";
    this._bytesDispatched = 0;
    this.server = null;
    this._server = null;
    this.allowHalfOpen = userAllowHalfOpen;
    this[kTimeout] = null;
    this[kSetNoDelay] = Boolean(options.noDelay);
    this[kSetKeepAlive] = Boolean(options.keepAlive);
    this[kSetKeepAliveInitialDelay] = ~~((options.keepAliveInitialDelay || 0) / 1000);
    this[kBytesRead] = 0;
    this[kBytesWritten] = 0;
    if (options.handle) {
      this._handle = options.handle;
      initSocketHandle(this);
    }
    this.on("end", onReadableStreamEnd);
    if (this._handle && options.readable !== false) {
      if (options.pauseOnCreate) {
        this._handle.reading = false;
        this._handle.readStop();
        this.readableFlowing = false;
      } else if (!options.manualStart) {
        this.read(0);
      }
    }
    if (options.signal) {
      validateAbortSignal(options.signal, "options.signal");
      const signal = options.signal;
      const onAbort = () => this.destroy(abortError(signal));
      if (signal.aborted) process.nextTick(onAbort);
      else signal.addEventListener("abort", onAbort, { once: true });
    }
  }

  _undestroy() { undestroy(this); }

  _unrefTimer() {
    for (let s = this; s !== null; s = s._parent) {
      if (s[kTimeout]) armTimer(s);
    }
  }

  _final(cb) {
    if (this.pending) {
      return this.once("connect", () => this._final(cb));
    }
    if (!this._handle) return cb();
    this._handle.shutdown((err) => {
      if (err && !this.destroyed) return cb(errnoException(err, "shutdown"));
      cb();
    });
  }

  setTimeout(msecs, callback) {
    if (this.destroyed) return this;
    this.timeout = msecs;
    if (typeof msecs !== "number") throw ERR_INVALID_ARG_TYPE("msecs", "number", msecs);
    if (msecs < 0 || !Number.isFinite(msecs)) throw makeErr(RangeError, "ERR_OUT_OF_RANGE", 'The value of "msecs" is out of range. It must be a non-negative finite number. Received ' + msecs);
    if (msecs === 0) {
      if (this[kTimeout]) clearTimeout(this[kTimeout]);
      this[kTimeout] = null;
      if (callback !== undefined) {
        validateFunction(callback, "callback");
        this.removeListener("timeout", callback);
      }
    } else {
      this[kTimeoutMs] = msecs;
      armTimer(this);
      if (callback !== undefined) {
        validateFunction(callback, "callback");
        this.once("timeout", callback);
      }
    }
    return this;
  }

  _onTimeout() {
    this.emit("timeout");
  }

  setNoDelay(enable) {
    const v = enable === undefined ? true : !!enable;
    if (!this._handle) {
      this[kSetNoDelay] = v;
      return this;
    }
    if (this._handle.setNoDelay && v !== this[kSetNoDelay]) {
      this[kSetNoDelay] = v;
      this._handle.setNoDelay(v);
    }
    return this;
  }

  setKeepAlive(enable, initialDelayMsecs) {
    enable = Boolean(enable);
    const initialDelay = ~~((initialDelayMsecs || 0) / 1000);
    if (!this._handle) {
      this[kSetKeepAlive] = enable;
      this[kSetKeepAliveInitialDelay] = initialDelay;
      return this;
    }
    if (this._handle.setKeepAlive && enable !== this[kSetKeepAlive]) {
      this[kSetKeepAlive] = enable;
      this[kSetKeepAliveInitialDelay] = initialDelay;
      this._handle.setKeepAlive(enable, initialDelay);
    }
    return this;
  }

  setTypeOfService(tos) {
    validateInt32(tos, "tos", 0, 255);
    this._tos = tos;
    return this;
  }
  getTypeOfService() { return this._tos || 0; }

  address() { return this._getsockname(); }

  get _connecting() { return this.connecting; }
  get pending() { return !this._handle || this.connecting; }
  get readyState() {
    if (this.connecting) return "opening";
    if (this.readable && this.writable) return "open";
    if (this.readable && !this.writable) return "readOnly";
    if (!this.readable && this.writable) return "writeOnly";
    return "closed";
  }
  get bufferSize() {
    if (this._handle) return this.writableLength;
    return undefined;
  }

  _read(n) {
    if (this.connecting || !this._handle) {
      this.once("connect", () => this._read(n));
    } else if (!this._handle.reading) {
      this._handle.reading = true;
      this._handle.readStart();
    }
  }

  end(data, encoding, callback) {
    duplexProto.end.call(this, data, encoding, callback);
    return this;
  }

  resetAndDestroy() {
    if (this._handle) {
      if (this.connecting) this.once("connect", () => this._reset());
      else this._reset();
    } else {
      this.destroy(makeErr(Error, "ERR_SOCKET_CLOSED", "Socket is closed"));
    }
    return this;
  }
  _reset() {
    this.resetAndClosing = true;
    return this.destroy();
  }

  pause() {
    return duplexProto.pause.call(this);
  }
  resume() {
    return duplexProto.resume.call(this);
  }
  read(n) {
    return duplexProto.read.call(this, n);
  }

  destroySoon() {
    if (this.writable) this.end();
    if (this.writableFinished) this.destroy();
    else this.once("finish", this.destroy);
  }

  _destroy(exception, cb) {
    this.connecting = false;
    for (let s = this; s !== null; s = s._parent) {
      if (s[kTimeout]) { clearTimeout(s[kTimeout]); s[kTimeout] = null; }
    }
    if (this[kAttempt]) { this[kAttempt].abort(); this[kAttempt] = null; }
    if (this._handle) {
      const isException = exception ? true : false;
      this[kBytesRead] = this._handle.bytesRead();
      this[kBytesWritten] = this._handle.bytesWritten();
      const handle = this._handle;
      const done = () => this.emit("close", isException);
      if (this.resetAndClosing) {
        this.resetAndClosing = false;
        handle.reset(done);
      } else {
        handle.close(done);
      }
      handle.onread = () => {};
      this._handle = null;
      this._sockname = null;
      cb(exception);
    } else {
      cb(exception);
      process.nextTick(() => this.emit("close"));
    }
    if (this._server) {
      this._server._connections--;
      if (this._server._emitCloseIfDrained) this._server._emitCloseIfDrained();
    }
  }

  _getpeername() {
    if (!this._handle || !this._handle.getpeername || this.connecting) return this._peername || {};
    if (!this._peername) {
      const out = this._handle.getpeername();
      if (!out) return {};
      this._peername = out;
    }
    return this._peername;
  }
  _getsockname() {
    if (!this._handle || !this._handle.getsockname) return {};
    if (!this._sockname) {
      const out = this._handle.getsockname();
      if (!out) return {};
      this._sockname = out;
    }
    return this._sockname;
  }
  get bytesRead() { return this._handle ? this._handle.bytesRead() : this[kBytesRead]; }
  get remoteAddress() { return this._getpeername().address; }
  get remoteFamily() { return this._getpeername().family; }
  get remotePort() { return this._getpeername().port; }
  get localAddress() { return this._getsockname().address; }
  get localPort() { return this._getsockname().port; }
  get localFamily() { return this._getsockname().family; }
  get _bytesDispatchedTotal() { return this._bytesDispatched; }
  get bytesWritten() {
    let bytes = this._bytesDispatched;
    const data = this._pendingData;
    const encoding = this._pendingEncoding;
    const writableBuffer = this.writableBuffer;
    if (!writableBuffer) return undefined;
    for (const el of writableBuffer) {
      bytes += el instanceof Uint8Array ? el.length : Buffer.byteLength(el, encoding);
    }
    if (Array.isArray(data)) {
      for (const el of data) bytes += el.chunk instanceof Uint8Array ? el.chunk.length : Buffer.byteLength(el.chunk, el.encoding);
    } else if (data) {
      bytes += typeof data === "string" ? Buffer.byteLength(data, encoding) : data.length;
    }
    return bytes;
  }

  _writeGeneric(writev, data, encoding, cb) {
    if (this.connecting) {
      this._pendingData = data;
      this._pendingEncoding = encoding;
      const onClose = () => cb(makeErr(Error, "ERR_SOCKET_CLOSED_BEFORE_CONNECTION", "Socket closed before the connection was established"));
      this.once("connect", function connect() {
        this.off("close", onClose);
        this._writeGeneric(writev, data, encoding, cb);
      });
      this.once("close", onClose);
      return;
    }
    this._pendingData = null;
    this._pendingEncoding = "";
    if (!this._handle) {
      cb(makeErr(Error, "ERR_SOCKET_CLOSED", "Socket is closed"));
      return false;
    }
    this._unrefTimer();
    let buf;
    if (writev) {
      const parts = data.map(({ chunk, encoding }) => (typeof chunk === "string" ? Buffer.from(chunk, encoding) : chunk));
      buf = parts.length === 1 ? parts[0] : Buffer.concat(parts);
    } else {
      buf = typeof data === "string" ? Buffer.from(data, encoding) : data;
    }
    this._bytesDispatched += buf.length;
    this._handle.write(buf, (err) => {
      if (this.destroyed) return cb();
      if (err) return cb(errnoException(err, "write"));
      this._unrefTimer();
      cb();
    });
  }
  _writev(chunks, cb) { this._writeGeneric(true, chunks, "", cb); }
  _write(data, encoding, cb) { this._writeGeneric(false, data, encoding, cb); }

  connect(...args) {
    let normalized;
    if (Array.isArray(args[0]) && args[0][normalizedArgsSymbol]) normalized = args[0];
    else normalized = normalizeArgs(args);
    const options = normalized[0];
    const cb = normalized[1];
    if (this.write !== Socket.prototype.write) this.write = Socket.prototype.write;
    if (this.destroyed) {
      this._handle = null;
      this._peername = null;
      this._sockname = null;
      this._undestroy();
    }
    const { path } = options;
    const pipe = !!path;
    if (!this._handle) {
      this._handle = binding.newHandle();
      initSocketHandle(this);
    }
    if (cb !== null) this.once("connect", cb);
    this._unrefTimer();
    this.connecting = true;
    if (pipe) {
      validateString(path, "options.path");
      internalConnect(this, path);
    } else {
      lookupAndConnect(this, options);
    }
    return this;
  }

  ref() {
    if (!this._handle) {
      this.once("connect", this.ref);
      return this;
    }
    this._handle.ref();
    return this;
  }
  unref() {
    if (!this._handle) {
      this.once("connect", this.unref);
      return this;
    }
    this._handle.unref();
    return this;
  }

  [Symbol.asyncDispose]() {
    return new Promise((resolve) => {
      if (this.destroyed) return resolve();
      this.once("close", () => resolve());
      this.destroy();
    });
  }
}

function writeAfterFIN(chunk, encoding, cb) {
  if (!this.writableEnded) return duplexProto.write.call(this, chunk, encoding, cb);
  if (typeof encoding === "function") { cb = encoding; encoding = null; }
  const er = makeErr(Error, "EPIPE", "This socket has been ended by the other party");
  if (typeof cb === "function") process.nextTick(cb, er);
  this.destroy(er);
  return false;
}

function onReadableStreamEnd() {
  if (!this.allowHalfOpen) {
    this.write = writeAfterFIN;
  }
}

function socketToDnsFamily(family) {
  switch (family) {
    case "IPv4": return 4;
    case "IPv6": return 6;
  }
  return family;
}

function lookupAndConnect(self, options) {
  const { localAddress, localPort } = options;
  const host = options.host || "localhost";
  let { port, autoSelectFamilyAttemptTimeout, autoSelectFamily } = options;
  if (localAddress && !isIP(localAddress)) {
    throw makeErr(TypeError, "ERR_INVALID_IP_ADDRESS", "Invalid IP address: " + localAddress);
  }
  if (localPort) validateNumber(localPort, "options.localPort");
  if (typeof port !== "undefined") {
    if (typeof port !== "number" && typeof port !== "string") throw ERR_INVALID_ARG_TYPE("options.port", ["number", "string"], port);
    validatePort(port);
  }
  port |= 0;
  if (autoSelectFamily != null) validateBoolean(autoSelectFamily, "options.autoSelectFamily");
  else autoSelectFamily = autoSelectFamilyDefault;
  if (autoSelectFamilyAttemptTimeout != null) {
    validateInt32(autoSelectFamilyAttemptTimeout, "options.autoSelectFamilyAttemptTimeout", 1);
    if (autoSelectFamilyAttemptTimeout < 10) autoSelectFamilyAttemptTimeout = 10;
  } else {
    autoSelectFamilyAttemptTimeout = autoSelectFamilyAttemptTimeoutDefault;
  }
  const addressType = isIP(host);
  if (addressType) {
    process.nextTick(() => {
      if (self.connecting) internalConnect(self, host, port, addressType, localAddress, localPort);
    });
    return;
  }
  if (options.lookup != null) validateFunction(options.lookup, "options.lookup");
  const dnsopts = { family: socketToDnsFamily(options.family), hints: options.hints || 0 };
  self._host = host;
  self._port = port;
  const lookup = options.lookup || dns.lookup;
  if (dnsopts.family !== 4 && dnsopts.family !== 6 && !localAddress && autoSelectFamily) {
    dnsopts.all = true;
    lookup(host, dnsopts, (err, addresses) => {
      if (err) {
        self.emit("lookup", err, undefined, undefined, host);
        if (self.connecting) process.nextTick(() => self.destroy(err));
        return;
      }
      for (const a of addresses) self.emit("lookup", null, a.address, a.family, host);
      if (!self.connecting) return;
      if (addresses.length === 0) {
        process.nextTick(() => self.destroy(makeErr(Error, "ENOTFOUND", "getaddrinfo ENOTFOUND " + host)));
        return;
      }
      if (addresses.length === 1) {
        self._unrefTimer();
        internalConnect(self, addresses[0].address, port, addresses[0].family, localAddress, localPort);
        return;
      }
      self.autoSelectFamilyAttemptedAddresses = [];
      internalConnectMultiple({ socket: self, addresses, current: 0, port, localPort, timeout: autoSelectFamilyAttemptTimeout, errors: [] });
    });
    return;
  }
  lookup(host, dnsopts, function emitLookup(err, ip, family) {
    self.emit("lookup", err, ip, family, host);
    if (!self.connecting) return;
    if (err) {
      process.nextTick(() => self.destroy(err));
    } else if (!isIP(ip)) {
      process.nextTick(() => self.destroy(makeErr(TypeError, "ERR_INVALID_IP_ADDRESS", "Invalid IP address: " + ip)));
    } else {
      self._unrefTimer();
      internalConnect(self, ip, port, family || isIP(ip), localAddress, localPort);
    }
  });
}

function internalConnect(self, address, port, addressType, localAddress, localPort) {
  if (!self._handle) return;
  const opts = typeof port === "number"
    ? { address, port, localAddress, localPort: localPort || 0 }
    : { path: address };
  self._handle.connect(opts, (err) => {
    if (self.destroyed) return;
    if (err) {
      let details;
      if (localAddress) details = localAddress + ":" + (localPort || 0);
      const ex = exceptionWithHostPort(err, "connect", address, port, details);
      self.destroy(ex);
      return;
    }
    afterConnect(self);
  });
}

function internalConnectMultiple(context, canceled) {
  const self = context.socket;
  if (self.destroyed) return;
  if (context.current === context.addresses.length) {
    const errs = context.errors;
    const ex = new AggregateError(errs, "");
    ex.code = errs[0] && errs[0].code;
    self.destroy(ex);
    return;
  }
  const current = context.current++;
  const { address, family } = context.addresses[current];
  const { port, localPort } = context;
  self.autoSelectFamilyAttemptedAddresses.push(address + ":" + port);
  self.emit("connectionAttempt", address, port, family);
  if (!self._handle) {
    self._handle = binding.newHandle();
    initSocketHandle(self);
  }
  const handle = self._handle;
  let done = false;
  let timer = null;
  const attempt = {
    abort() {
      done = true;
      if (timer) clearTimeout(timer);
    },
  };
  self[kAttempt] = attempt;
  if (current < context.addresses.length - 1) {
    timer = setTimeout(() => {
      if (done) return;
      done = true;
      self.emit("connectionAttemptTimeout", address, port, family);
      handle.close();
      self._handle = null;
      internalConnectMultiple(context, true);
    }, context.timeout);
    if (timer && timer.unref) timer.unref();
  }
  handle.connect({ address, port, localPort: localPort || 0 }, (err) => {
    if (done) return;
    done = true;
    if (timer) clearTimeout(timer);
    self[kAttempt] = null;
    if (self.destroyed) return;
    if (err) {
      const ex = exceptionWithHostPort(err, "connect", address, port);
      context.errors.push(ex);
      self.emit("connectionAttemptFailed", address, port, family, ex);
      handle.close();
      self._handle = null;
      internalConnectMultiple(context);
      return;
    }
    afterConnect(self);
  });
}

function afterConnect(self) {
  if (self.destroyed) return;
  self.connecting = false;
  self._sockname = null;
  self._unrefTimer();
  if (self[kSetNoDelay] && self._handle.setNoDelay) self._handle.setNoDelay(true);
  if (self[kSetKeepAlive] && self._handle.setKeepAlive) self._handle.setKeepAlive(true, self[kSetKeepAliveInitialDelay]);
  self.emit("connect");
  self.emit("ready");
  if (!self.isPaused() || self._readableState.readableListening) self.read(0);
}

function connect(...args) {
  const normalized = normalizeArgs(args);
  const options = normalized[0];
  const socket = new Socket(options);
  if (options.timeout) socket.setTimeout(options.timeout);
  return socket.connect(normalized);
}

// ---- Server ---------------------------------------------------------
class Server extends EventEmitter {
  constructor(options, connectionListener) {
    super();
    if (typeof options === "function") {
      connectionListener = options;
      options = {};
      this.on("connection", connectionListener);
    } else if (options == null || typeof options === "object") {
      options = { ...options };
      if (typeof connectionListener === "function") this.on("connection", connectionListener);
    } else {
      throw ERR_INVALID_ARG_TYPE("options", "Object", options);
    }
    if (options.keepAliveInitialDelay !== undefined) validateNumber(options.keepAliveInitialDelay, "options.keepAliveInitialDelay");
    if (options.highWaterMark !== undefined) validateNumber(options.highWaterMark, "options.highWaterMark");
    this._connections = 0;
    this._handle = null;
    this._usingWorkers = false;
    this._workers = [];
    this._unref = false;
    this._listeningId = 1;
    this.allowHalfOpen = options.allowHalfOpen || false;
    this.pauseOnConnect = !!options.pauseOnConnect;
    this.noDelay = Boolean(options.noDelay);
    this.keepAlive = Boolean(options.keepAlive);
    this.keepAliveInitialDelay = ~~((options.keepAliveInitialDelay || 0) / 1000);
    this.highWaterMark = options.highWaterMark ?? 65536;
  }

  listen(...args) {
    const normalized = normalizeArgs(args);
    let options = normalized[0];
    const cb = normalized[1];
    if (this._handle) {
      throw makeErr(Error, "ERR_SERVER_ALREADY_LISTEN", "Listen method has been called more than once without closing.");
    }
    if (cb !== null) this.once("listening", cb);
    const backlogFromArgs = toNumber(args.length > 1 && args[1]) || toNumber(args.length > 2 && args[2]);
    options = options._handle || options.handle || options;
    if (options.signal) {
      validateAbortSignal(options.signal, "options.signal");
      const signal = options.signal;
      const onAborted = () => this.close();
      if (signal.aborted) process.nextTick(onAborted);
      else {
        signal.addEventListener("abort", onAborted, { once: true });
        this.once("close", () => signal.removeEventListener("abort", onAborted));
      }
    }
    if (args.length === 0 || typeof args[0] === "function" ||
        (typeof options.port === "undefined" && "port" in options) || options.port === null) {
      options.port = 0;
    }
    if (typeof options.port === "number" || typeof options.port === "string") {
      validatePort(options.port, "options.port");
      const backlog = options.backlog || backlogFromArgs;
      if (options.host) {
        lookupAndListen(this, options.port | 0, options.host, backlog, options);
      } else {
        this._listen2(null, options.port | 0, 4, backlog, options);
      }
      return this;
    }
    if (options.path && isPipeName(options.path)) {
      this._pipeName = options.path;
      this._listen2(options.path, -1, -1, options.backlog || backlogFromArgs, options);
      return this;
    }
    if (!("port" in options) && !("path" in options)) {
      throw ERR_INVALID_ARG_VALUE("options", options, 'must have the property "port" or "path"');
    }
    throw ERR_INVALID_ARG_VALUE("options", options);
  }

  _listen2(address, port, addressType, backlog, options) {
    const listenOpts = { ipv6Only: !!(options && options.ipv6Only), reusePort: !!(options && options.reusePort) };
    let rval;
    if (addressType === -1) {
      rval = binding.listen({ ...listenOpts, path: address });
    } else if (!address) {
      rval = binding.listen({ ...listenOpts, address: "::", port });
      if (rval && rval.code) {
        rval = binding.listen({ ...listenOpts, address: "0.0.0.0", port });
        address = "0.0.0.0";
      } else {
        address = "::";
      }
    } else {
      rval = binding.listen({ ...listenOpts, address, port });
    }
    if (rval && rval.code) {
      const error = uvExceptionWithHostPort(rval, "listen", address, port);
      process.nextTick(() => this.emit("error", error));
      return;
    }
    this._handle = rval;
    this._handle[kOwner] = this;
    this._handle.onconnection = onconnection;
    this._handle.onerror = (info) => this.emit("error", errnoException(info, "accept"));
    this._connectionKey = addressType + ":" + address + ":" + port;
    if (this._unref) this._handle.unref();
    this._handle.start();
    process.nextTick(() => {
      if (!this._handle) return;
      this.emit("listening");
    });
  }

  get listening() { return !!(this._handle && this._connectionKey); }

  address() {
    if (this._handle && this._handle.getsockname) {
      const out = this._handle.getsockname();
      if (out && out.path !== undefined) return out.path;
      return out;
    } else if (this._pipeName) {
      return this._pipeName;
    }
    return null;
  }

  getConnections(cb) {
    if (typeof cb === "function") process.nextTick(cb, null, this._connections);
    return this;
  }

  close(cb) {
    if (typeof cb === "function") {
      if (!this._handle) {
        this.once("close", function close() {
          cb(makeErr(Error, "ERR_SERVER_NOT_RUNNING", "Server is not running."));
        });
      } else {
        this.once("close", cb);
      }
    }
    if (this._handle) {
      this._handle.close();
      this._handle = null;
    }
    this._emitCloseIfDrained();
    return this;
  }

  _emitCloseIfDrained() {
    if (this._handle || this._connections) return;
    process.nextTick(() => this.emit("close"));
  }

  ref() {
    this._unref = false;
    if (this._handle) this._handle.ref();
    return this;
  }
  unref() {
    this._unref = true;
    if (this._handle) this._handle.unref();
    return this;
  }

  [Symbol.asyncDispose]() {
    if (!this._handle) return Promise.resolve();
    return new Promise((resolve, reject) => this.close((err) => (err ? reject(err) : resolve())));
  }
}

function lookupAndListen(self, port, address, backlog, options) {
  dns.lookup(address, (err, ip, addressType) => {
    if (err) {
      self.emit("error", err);
    } else {
      self._listen2(ip, port, addressType, backlog, options);
    }
  });
}

function onconnection(clientHandle) {
  const self = this[kOwner];
  if (self.maxConnections != null && self._connections >= self.maxConnections) {
    const data = Object.create(null);
    const local = clientHandle.getsockname() || {};
    const remote = clientHandle.getpeername() || {};
    data.localAddress = local.address;
    data.localPort = local.port;
    data.localFamily = local.family;
    data.remoteAddress = remote.address;
    data.remotePort = remote.port;
    data.remoteFamily = remote.family;
    self.emit("drop", data);
    clientHandle.close();
    return;
  }
  const socket = new self._socketCtor({
    handle: clientHandle,
    allowHalfOpen: self.allowHalfOpen,
    pauseOnCreate: self.pauseOnConnect,
    manualStart: !!self._manualStart,
    readable: true,
    writable: true,
    readableHighWaterMark: self.highWaterMark,
    writableHighWaterMark: self.highWaterMark,
  });
  if (self.noDelay && clientHandle.setNoDelay) {
    socket[kSetNoDelay] = true;
    clientHandle.setNoDelay(true);
  }
  if (self.keepAlive && clientHandle.setKeepAlive) {
    socket[kSetKeepAlive] = true;
    socket[kSetKeepAliveInitialDelay] = self.keepAliveInitialDelay;
    clientHandle.setKeepAlive(true, self.keepAliveInitialDelay);
  }
  self._connections++;
  socket.server = self;
  socket._server = self;
  self.emit("connection", socket);
}
Server.prototype._socketCtor = Socket;

function createServer(options, connectionListener) {
  return new Server(options, connectionListener);
}

function _createServerHandle(address, port, addressType, fd, flags) {
  const rval = addressType === -1 ? binding.listen({ path: address }) : binding.listen({ address: address || "::", port: port | 0 });
  if (rval && rval.code) return rval.errno;
  return rval;
}

// ---- SocketAddress / BlockList -------------------------------------
function ipv4ToNum(s) {
  const p = s.split(".").map(Number);
  return ((p[0] << 24) >>> 0) + (p[1] << 16) + (p[2] << 8) + p[3];
}
function ipv6ToBig(s) {
  let str = s;
  const pct = str.indexOf("%");
  if (pct >= 0) str = str.slice(0, pct);
  let tail = [];
  const lastColon = str.lastIndexOf(":");
  const lastPart = str.slice(lastColon + 1);
  if (lastPart.includes(".")) {
    const v = ipv4ToNum(lastPart);
    tail = [(v >>> 16).toString(16), (v & 0xffff).toString(16)];
    str = str.slice(0, lastColon + 1) + "0:0";
  }
  let parts;
  if (str.includes("::")) {
    const [a, b] = str.split("::");
    const ha = a ? a.split(":") : [];
    const hb = b ? b.split(":") : [];
    parts = [...ha, ...new Array(8 - ha.length - hb.length).fill("0"), ...hb];
  } else {
    parts = str.split(":");
  }
  if (tail.length) { parts[6] = tail[0]; parts[7] = tail[1]; }
  let v = 0n;
  for (const p of parts) v = (v << 16n) + BigInt(parseInt(p || "0", 16));
  return v;
}
function toBig(address, family) {
  return family === "ipv4" ? BigInt(ipv4ToNum(address)) : ipv6ToBig(address);
}
const kSocketAddress = Symbol("kSocketAddress");
class SocketAddress {
  static isSocketAddress(value) { return value != null && typeof value === "object" && value[kSocketAddress] === true; }
  static parse(input) {
    validateString(input, "input");
    let host, port;
    const m = /^\[([^\]]+)\](?::(\d+))?$/.exec(input);
    if (m) { host = m[1]; port = m[2]; }
    else {
      const idx = input.lastIndexOf(":");
      if (idx > 0 && input.indexOf(":") === idx) { host = input.slice(0, idx); port = input.slice(idx + 1); }
      else host = input;
    }
    const t = isIP(host);
    if (!t) return undefined;
    try {
      return new SocketAddress({ address: host, port: port === undefined ? 0 : Number(port), family: t === 6 ? "ipv6" : "ipv4" });
    } catch {
      return undefined;
    }
  }
  constructor(options = {}) {
    if (options === null || typeof options !== "object") throw ERR_INVALID_ARG_TYPE("options", "Object", options);
    let { family = "ipv4" } = options;
    if (typeof family === "string") family = family.toLowerCase();
    if (family !== "ipv4" && family !== "ipv6") throw ERR_INVALID_ARG_VALUE("options.family", options.family);
    const { address = family === "ipv4" ? "127.0.0.1" : "::", port = 0, flowlabel = 0 } = options;
    validateString(address, "options.address");
    if (flowlabel !== undefined) validateInt32(flowlabel, "options.flowlabel", 0);
    validatePort(port, "options.port");
    const t = isIP(address);
    if ((family === "ipv4" && t !== 4) || (family === "ipv6" && t !== 6)) {
      throw makeErr(Error, "ERR_INVALID_ADDRESS", "Invalid socket address");
    }
    Object.defineProperty(this, kSocketAddress, { value: true });
    this._address = address;
    this._port = port | 0;
    this._family = family;
    this._flowlabel = flowlabel | 0;
    Object.defineProperty(this, "_address", { enumerable: false });
    Object.defineProperty(this, "_port", { enumerable: false });
    Object.defineProperty(this, "_family", { enumerable: false });
    Object.defineProperty(this, "_flowlabel", { enumerable: false });
  }
  get address() { return this._address; }
  get port() { return this._port; }
  get family() { return this._family; }
  get flowlabel() { return this._flowlabel; }
  toJSON() { return { address: this.address, port: this.port, family: this.family, flowlabel: this.flowlabel }; }
  [Symbol.for("nodejs.util.inspect.custom")](depth, options, inspect) {
    if (depth < 0) return this;
    return "SocketAddress " + (inspect || __inspect)(this.toJSON(), options);
  }
}

const kBlockList = Symbol("kBlockList");
function checkFamily(family) {
  validateString(family, "family");
  const f = family.toLowerCase();
  if (f !== "ipv4" && f !== "ipv6") throw ERR_INVALID_ARG_VALUE("family", family);
  return f;
}
function addrArg(address, family, name) {
  if (SocketAddress.isSocketAddress(address)) return [address.address, address.family];
  validateString(address, name);
  const f = checkFamily(family);
  if (isIP(address) !== (f === "ipv4" ? 4 : 6)) throw makeErr(Error, "ERR_INVALID_ADDRESS", "Invalid socket address");
  return [address, f];
}
class BlockList {
  static isBlockList(value) { return value != null && typeof value === "object" && value[kBlockList] === true; }
  constructor() {
    Object.defineProperty(this, kBlockList, { value: true });
    Object.defineProperty(this, "_rules", { value: [], enumerable: false });
  }
  addAddress(address, family = "ipv4") {
    const [a, f] = addrArg(address, family, "address");
    const v = toBig(a, f);
    this._rules.unshift({ type: "address", family: f, start: v, end: v, text: "Address: " + (f === "ipv4" ? "IPv4 " : "IPv6 ") + a });
  }
  addRange(start, end, family = "ipv4") {
    const [s, f] = addrArg(start, family, "start");
    const [e, f2] = addrArg(end, family, "end");
    const sv = toBig(s, f), ev = toBig(e, f2);
    if (f !== f2 || sv > ev) throw ERR_INVALID_ARG_VALUE("start", start, "must come before end");
    this._rules.unshift({ type: "range", family: f, start: sv, end: ev, text: "Range: " + (f === "ipv4" ? "IPv4 " : "IPv6 ") + s + "-" + e });
  }
  addSubnet(network, prefix, family = "ipv4") {
    const [n, f] = addrArg(network, family, "network");
    const bits = f === "ipv4" ? 32 : 128;
    validateInt32(prefix, "prefix", 0, bits);
    const v = toBig(n, f);
    const host = BigInt(bits - prefix);
    const mask = ((1n << BigInt(bits)) - 1n) ^ ((1n << host) - 1n);
    const start = v & mask;
    const end = start | ((1n << host) - 1n);
    this._rules.unshift({ type: "subnet", family: f, start, end, text: "Subnet: " + (f === "ipv4" ? "IPv4 " : "IPv6 ") + n + "/" + prefix });
  }
  check(address, family = "ipv4") {
    let a, f;
    if (SocketAddress.isSocketAddress(address)) { a = address.address; f = address.family; }
    else {
      validateString(address, "address");
      f = checkFamily(family);
      if (isIP(address) !== (f === "ipv4" ? 4 : 6)) return false;
      a = address;
    }
    const v = toBig(a, f);
    let mapped = null;
    if (f === "ipv6" && (v >> 32n) === 0xffffn) mapped = v & 0xffffffffn;
    for (const r of this._rules) {
      if (r.family === f && v >= r.start && v <= r.end) return true;
      if (mapped !== null && r.family === "ipv4" && mapped >= r.start && mapped <= r.end) return true;
    }
    return false;
  }
  get rules() { return this._rules.map((r) => r.text); }
  toJSON() { return this.rules; }
  fromJSON(data) {
    const list = typeof data === "string" ? JSON.parse(data) : data;
    if (!Array.isArray(list)) throw ERR_INVALID_ARG_TYPE("data", "Array", data);
    for (const rule of list.slice().reverse()) {
      const m = /^(Address|Range|Subnet): (IPv4|IPv6) (.+)$/.exec(rule);
      if (!m) continue;
      const fam = m[2].toLowerCase();
      if (m[1] === "Address") this.addAddress(m[3], fam);
      else if (m[1] === "Range") { const [s, e] = m[3].split("-"); this.addRange(s, e, fam); }
      else { const i = m[3].lastIndexOf("/"); this.addSubnet(m[3].slice(0, i), Number(m[3].slice(i + 1)), fam); }
    }
  }
  [Symbol.for("nodejs.util.inspect.custom")](depth, options, inspect) {
    return "BlockList " + (inspect || __inspect)({ rules: this.rules }, options);
  }
}

const Stream = Socket;
const createConnection = connect;
const _normalizeArgs = normalizeArgs;


// paserati: a subclass inherits its parent's name when the parent's name
// was set with defineProperty (EventEmitter's is) - pin our own names.
Object.defineProperty(Socket, "name", { value: "Socket", configurable: true });
Object.defineProperty(Server, "name", { value: "Server", configurable: true });
Object.defineProperty(BlockList, "name", { value: "BlockList", configurable: true });
Object.defineProperty(SocketAddress, "name", { value: "SocketAddress", configurable: true });

globalThis.__noderatiNetInternals = {
  errnoException, exceptionWithHostPort, uvExceptionWithHostPort, makeErr, validatePort, ERR_INVALID_ARG_TYPE,
  ERR_INVALID_ARG_VALUE, validateString, validateFunction, validateBoolean, validateNumber, validateInt32,
  validateAbortSignal, abortError, kOwner, normalizedArgsSymbol, normalizeArgs, onconnection, describe,
};

export {
  connect, createConnection, createServer, Socket, Stream, Server, BlockList, SocketAddress,
  isIP, isIPv4, isIPv6, getDefaultAutoSelectFamily, setDefaultAutoSelectFamily,
  getDefaultAutoSelectFamilyAttemptTimeout, setDefaultAutoSelectFamilyAttemptTimeout,
  _createServerHandle, _normalizeArgs,
};
export default {
  connect, createConnection, createServer, Socket, Stream, Server, BlockList, SocketAddress,
  isIP, isIPv4, isIPv6, getDefaultAutoSelectFamily, setDefaultAutoSelectFamily,
  getDefaultAutoSelectFamilyAttemptTimeout, setDefaultAutoSelectFamilyAttemptTimeout,
  _createServerHandle, _normalizeArgs,
};
`
