package host

// workerShim is noderati-internal:worker, the JS half of worker_threads:
// the Worker class, the worker side's parentPort, BroadcastChannel,
// environment data, transfer marks, postMessageToThread and an in-process
// Web Locks LockManager. Messages cross threads as bytes: the sending VM
// serializes with its own node:v8 serializer, the receiving VM
// deserializes with its own, so no JS value is ever shared between VMs.
// Loaded lazily (see worker_threads.go): undici requires worker_threads
// at startup, and none of this is needed until it's used.
const workerShim = `import EventEmitter from "node:events";
import v8 from "node:v8";

const native = globalThis.__noderatiWorkerNative;
const self = globalThis.__noderatiWorkerSelf; // undefined on the main thread

function codedError(Ctor, code, message) {
  const err = new Ctor(message);
  err.code = code;
  return err;
}
function received(v) {
  if (v == null) return " Received " + v;
  if (typeof v === "function") return " Received function " + (v.name || "<anonymous>");
  if (typeof v === "object") return " Received an instance of " + ((v.constructor && v.constructor.name) || "Object");
  return " Received type " + typeof v + " (" + (typeof v === "string" ? "'" + v + "'" : String(v)) + ")";
}
function invalidArgType(name, expected, v) {
  const what = '"' + name + '" ' + (name.includes(".") ? "property" : "argument");
  return codedError(TypeError, "ERR_INVALID_ARG_TYPE", "The " + what + " must be " + expected + "." + received(v));
}
function dataCloneError(message) {
  if (typeof DOMException === "function") return new DOMException(message, "DataCloneError");
  const err = new Error(message);
  err.name = "DataCloneError";
  return err;
}

// --- structured clone over bytes ---
const untransferable = new WeakSet();
function describe(v) {
  if (typeof v === "function") {
    try { return Function.prototype.toString.call(v); } catch { return "function"; }
  }
  return String(v);
}
function checkCloneable(v, seen) {
  if (typeof v === "function" || typeof v === "symbol") throw dataCloneError(describe(v) + " could not be cloned.");
  if (v === null || typeof v !== "object" || seen.has(v)) return;
  seen.add(v);
  if (ArrayBuffer.isView(v) || v instanceof ArrayBuffer || v instanceof Date || v instanceof RegExp) return;
  if (v instanceof Map) {
    for (const [k, x] of v) { checkCloneable(k, seen); checkCloneable(x, seen); }
    return;
  }
  if (v instanceof Set) {
    for (const x of v) checkCloneable(x, seen);
    return;
  }
  if (v instanceof Promise || v instanceof WeakMap || v instanceof WeakSet) throw dataCloneError("#<" + v.constructor.name + "> could not be cloned.");
  for (const k of Object.keys(v)) checkCloneable(v[k], seen);
}
function encode(value, transferList) {
  if (transferList !== undefined && transferList !== null) {
    const list = Array.isArray(transferList) ? transferList : (transferList.transfer || []);
    for (const t of list) {
      if (t && typeof t === "object" && untransferable.has(t)) throw dataCloneError("An ArrayBuffer is marked as untransferable");
      if (t && typeof t === "object" && !(t instanceof ArrayBuffer) && typeof t.postMessage === "function") {
        throw dataCloneError("Transferring a MessagePort to another thread is not supported");
      }
    }
  }
  checkCloneable(value, new Set());
  return v8.serialize(value);
}
// Structured clone delivers Buffers as plain Uint8Arrays.
class CloneDeserializer extends v8.DefaultDeserializer {
  _readHostObject() {
    const v = super._readHostObject();
    return Buffer.isBuffer(v) ? new Uint8Array(v.buffer, v.byteOffset, v.length) : v;
  }
}
function decode(buf) {
  const d = new CloneDeserializer(buf);
  d.readHeader();
  return d.readValue();
}

// --- environment data ---
const environmentData = new Map();
if (self && self.environmentData) {
  for (const [k, v] of decode(self.environmentData)) environmentData.set(k, v);
}
function getEnvironmentData(key) { return environmentData.get(key); }
function setEnvironmentData(key, value) {
  if (value === undefined) environmentData.delete(key);
  else environmentData.set(key, value);
}

function markAsUntransferable(obj) {
  if ((typeof obj === "object" && obj !== null) || typeof obj === "function") untransferable.add(obj);
}
function isMarkedAsUntransferable(obj) {
  return obj != null && untransferable.has(obj);
}
function moveMessagePortToContext(port, contextifiedSandbox) {
  if (!port || typeof port.postMessage !== "function") throw invalidArgType("port", "an instance of MessagePort", port);
  return port;
}

// --- Worker (parent side) ---
const SHARE_ENV = native.SHARE_ENV;
const kHandle = Symbol("kHandle");

class Worker extends EventEmitter {
  #handle;
  #exited = false;
  #exitCode = undefined;
  #exitWaiters = [];
  constructor(filename, options = {}) {
    super();
    if (options === null || typeof options !== "object") throw invalidArgType("options", "of type object", options);
    let isEval = !!options.eval;
    let code = "";
    if (isEval) {
      if (typeof filename !== "string") throw invalidArgType("filename", "of type string", filename);
      code = filename;
      filename = "[worker eval]";
    } else {
      if (filename instanceof URL) filename = decodeURIComponent(filename.pathname);
      if (typeof filename !== "string") throw invalidArgType("filename", "of type string or an instance of URL", filename);
      if (filename.startsWith("data:")) {
        const comma = filename.indexOf(",");
        const meta = filename.slice(5, comma);
        code = meta.endsWith(";base64") ? Buffer.from(filename.slice(comma + 1), "base64").toString() : decodeURIComponent(filename.slice(comma + 1));
        isEval = true;
        filename = "[worker eval]";
      } else if (!/^(\/|\.\/|\.\.\/|file:)/.test(filename) && !/^[A-Za-z]:[\\/]/.test(filename)) {
        throw codedError(TypeError, "ERR_WORKER_PATH",
          "The worker script or module filename must be an absolute path or a relative path starting with './' or '../'. Received \"" + filename + "\"");
      } else {
        if (filename.startsWith("file:")) filename = decodeURIComponent(new URL(filename).pathname);
        const path = native.resolvePath(filename);
        filename = path;
      }
    }
    let env = options.env;
    if (env === undefined || env === SHARE_ENV) env = process.env;
    if (env === null || typeof env !== "object") throw invalidArgType("options.env", "of type object", env);
    const envCopy = {};
    for (const k of Object.keys(env)) envCopy[k] = String(env[k]);
    const argv = Array.isArray(options.argv) ? options.argv.map(String) : [];
    const execArgv = Array.isArray(options.execArgv) ? options.execArgv.map(String) : process.execArgv.slice();
    const resourceLimits = Object.assign({ maxYoungGenerationSizeMb: 48, maxOldGenerationSizeMb: 2048, codeRangeSizeMb: 0, stackSizeMb: 4 }, options.resourceLimits || {});
    this.resourceLimits = resourceLimits;
    this.#handle = native.spawn({
      filename, isEval, code,
      workerData: v8.serialize(options.workerData),
      env: v8.serialize(envCopy),
      environmentData: v8.serialize(environmentData),
      resourceLimits: v8.serialize(resourceLimits),
      argv, execArgv,
      name: options.name === undefined ? "" : String(options.name),
      deliver: (kind, buf) => this.#deliver(kind, buf),
    });
    this[kHandle] = this.#handle;
    this.threadId = this.#handle.threadId;
    this.stdin = null;
    this.stdout = null;
    this.stderr = null;
    this.performance = { eventLoopUtilization: () => ({ idle: 0, active: 0, utilization: 0 }) };
  }
  #deliver(kind, buf) {
    switch (kind) {
      case "online":
        this.emit("online");
        break;
      case "message": {
        let value;
        try {
          value = decode(buf);
        } catch (err) {
          this.emit("messageerror", err);
          return;
        }
        this.emit("message", value);
        break;
      }
      case "error":
        this.emit("error", decode(buf));
        break;
      case "exit": {
        const code = Number(buf.toString());
        this.#exited = true;
        this.#exitCode = code;
        this.emit("exit", code);
        for (const w of this.#exitWaiters) w(code);
        this.#exitWaiters = [];
        this.removeAllListeners("message");
        break;
      }
    }
  }
  postMessage(value, transferList) {
    if (arguments.length === 0) throw codedError(TypeError, "ERR_MISSING_ARGS", 'The "value" argument must be specified');
    if (this.#exited) return;
    this.#handle.post(encode(value, transferList));
  }
  terminate(callback) {
    const p = new Promise((resolve) => {
      if (this.#exited) {
        resolve(this.#exitCode);
        return;
      }
      this.#exitWaiters.push(resolve);
      this.#handle.terminate();
    });
    if (typeof callback === "function") p.then((code) => callback(null, code));
    return p;
  }
  ref() { this.#handle.ref(); }
  unref() { this.#handle.unref(); }
  getHeapSnapshot() { return Promise.resolve(v8.getHeapSnapshot()); }
  getHeapStatistics() { return Promise.resolve(v8.getHeapStatistics()); }
  [Symbol.asyncDispose]() { return this.terminate(); }
}

// --- the worker's own end: parentPort ---
class ParentPort extends EventEmitter {
  #closed = false;
  #listening = 0;
  #pending = [];
  constructor() {
    super();
    this.on("newListener", (name) => {
      if (name === "message" && this.#listening++ === 0) {
        self.ref();
        if (this.#pending.length) setImmediate(() => this._flush());
      }
    });
    this.on("removeListener", (name) => {
      if (name === "message" && --this.#listening === 0) self.unref();
    });
  }
  postMessage(value, transferList) {
    if (this.#closed) return;
    self.post("message", encode(value, transferList));
  }
  // Messages that arrive before anyone listens wait, as on a port that
  // hasn't been started yet.
  _receive(buf) {
    if (this.#closed) return;
    if (this.listenerCount("message") === 0) {
      this.#pending.push(buf);
      return;
    }
    let value;
    try {
      value = decode(buf);
    } catch (err) {
      this.emit("messageerror", err);
      return;
    }
    this.emit("message", value);
  }
  _flush() {
    while (this.#pending.length && this.listenerCount("message") > 0) this._receive(this.#pending.shift());
  }
  close() {
    if (this.#closed) return;
    this.#closed = true;
    self.unref();
    process.nextTick(() => this.emit("close"));
  }
  start() {}
  ref() { self.ref(); return this; }
  unref() { self.unref(); return this; }
  hasRef() { return this.#listening > 0; }
  addEventListener(type, fn) {
    this.on(type, (data) => fn.call(this, type === "message" ? new MessageEvent("message", { data }) : data));
  }
  removeEventListener() {}
  set onmessage(fn) {
    this.removeAllListeners("message");
    if (typeof fn === "function") this.on("message", (data) => fn.call(this, new MessageEvent("message", { data })));
  }
}

function initChild() {
  const port = new ParentPort();
  self.setDeliver((kind, buf) => {
    if (kind === "message") port._receive(buf);
  });
  const env = decode(self.env);
  for (const k of Object.keys(process.env)) delete process.env[k];
  Object.assign(process.env, env);
  return {
    parentPort: port,
    workerData: decode(self.workerData),
    resourceLimits: decode(self.resourceLimits),
  };
}

function encodeError(e) {
  try {
    return v8.serialize(e);
  } catch {
    return v8.serialize(new Error(String(e && e.message !== undefined ? e.message : e)));
  }
}

// --- postMessageToThread ---
function postMessageToThread(threadId, value, transferList, timeout) {
  if (typeof transferList === "number" && timeout === undefined) {
    timeout = transferList;
    transferList = undefined;
  }
  if (typeof threadId !== "number") return Promise.reject(invalidArgType("threadId", "of type number", threadId));
  let buf;
  try {
    buf = encode(value, transferList);
  } catch (err) {
    return Promise.reject(err);
  }
  if (!native.postToThread(threadId, buf)) {
    return Promise.reject(codedError(Error, "ERR_WORKER_MESSAGING_FAILED", "Cannot find the destination thread or listener"));
  }
  return Promise.resolve();
}
function deliverThreadMessage(buf, source) {
  const value = decode(buf);
  if (process.listenerCount("workerMessage") > 0) process.emit("workerMessage", value, source);
}

// --- BroadcastChannel ---
class BroadcastChannel extends EventTarget {
  #name;
  #handle;
  #onmessage = null;
  #onmessageerror = null;
  constructor(name) {
    if (arguments.length === 0) throw codedError(TypeError, "ERR_MISSING_ARGS", 'The "name" argument must be specified');
    super();
    this.#name = String(name);
    this.#handle = native.broadcastOpen(this.#name, (buf) => {
      let data;
      try {
        data = decode(buf);
      } catch (err) {
        const ev = new MessageEvent("messageerror", { data: err });
        if (typeof this.#onmessageerror === "function") this.#onmessageerror(ev);
        this.dispatchEvent(ev);
        return;
      }
      const ev = new MessageEvent("message", { data });
      if (typeof this.#onmessage === "function") this.#onmessage(ev);
      this.dispatchEvent(ev);
    });
  }
  get name() { return this.#name; }
  get onmessage() { return this.#onmessage; }
  set onmessage(fn) { this.#onmessage = fn; }
  get onmessageerror() { return this.#onmessageerror; }
  set onmessageerror(fn) { this.#onmessageerror = fn; }
  postMessage(message) {
    if (arguments.length === 0) throw codedError(TypeError, "ERR_MISSING_ARGS", 'The "message" argument must be specified');
    if (!this.#handle) throw new DOMException("BroadcastChannel is closed.", "InvalidStateError");
    this.#handle.post(encode(message));
  }
  close() {
    if (!this.#handle) return;
    this.#handle.close();
    this.#handle = null;
  }
  ref() { if (this.#handle) this.#handle.ref(); return this; }
  unref() { if (this.#handle) this.#handle.unref(); return this; }
}
Object.defineProperty(BroadcastChannel.prototype, Symbol.toStringTag, { value: "BroadcastChannel", configurable: true });

// --- Web Locks (in-process, per thread) ---
class Lock {
  #name; #mode;
  constructor(name, mode) { this.#name = name; this.#mode = mode; }
  get name() { return this.#name; }
  get mode() { return this.#mode; }
}
Object.defineProperty(Lock.prototype, Symbol.toStringTag, { value: "Lock", configurable: true });
const held = [];
const pendingLocks = [];
const clientId = "node-" + process.pid + "-" + (self ? self.threadId : 0);
function grantable(req) {
  const same = held.filter((h) => h.name === req.name);
  if (same.length === 0) return true;
  return req.mode === "shared" && same.every((h) => h.mode === "shared");
}
function pump() {
  for (let i = 0; i < pendingLocks.length; i++) {
    const req = pendingLocks[i];
    const earlier = pendingLocks.slice(0, i).some((r) => r.name === req.name);
    if (!earlier && grantable(req)) {
      pendingLocks.splice(i, 1);
      i--;
      grant(req);
    }
  }
}
function grant(req) {
  const entry = { name: req.name, mode: req.mode, clientId };
  held.push(entry);
  let result;
  try {
    result = Promise.resolve(req.callback(new Lock(req.name, req.mode)));
  } catch (err) {
    result = Promise.reject(err);
  }
  result.then(req.resolve, req.reject).finally(() => {
    const i = held.indexOf(entry);
    if (i >= 0) held.splice(i, 1);
    pump();
  });
}
class LockManager {
  request(name, options, callback) {
    if (typeof options === "function") {
      callback = options;
      options = {};
    }
    if (typeof callback !== "function") return Promise.reject(invalidArgType("callback", "of type function", callback));
    options = options || {};
    name = String(name);
    const mode = options.mode === undefined ? "exclusive" : options.mode;
    if (mode !== "exclusive" && mode !== "shared") {
      return Promise.reject(codedError(TypeError, "ERR_INVALID_ARG_VALUE", "The property 'options.mode' must be one of: 'exclusive', 'shared'. Received '" + mode + "'"));
    }
    if (name.startsWith("-")) return Promise.reject(new DOMException("Lock name may not start with hyphen", "NotSupportedError"));
    return new Promise((resolve, reject) => {
      const req = { name, mode, callback, resolve, reject };
      if (options.signal && options.signal.aborted) {
        reject(options.signal.reason);
        return;
      }
      if (options.steal) {
        for (let i = held.length - 1; i >= 0; i--) if (held[i].name === name) held.splice(i, 1);
        grant(req);
        return;
      }
      if (options.ifAvailable) {
        if (grantable(req) && !pendingLocks.some((r) => r.name === name)) grant(req);
        else {
          try { resolve(callback(null)); } catch (err) { reject(err); }
        }
        return;
      }
      if (options.signal) {
        options.signal.addEventListener("abort", () => {
          const i = pendingLocks.indexOf(req);
          if (i >= 0) {
            pendingLocks.splice(i, 1);
            reject(options.signal.reason);
          }
        }, { once: true });
      }
      pendingLocks.push(req);
      pump();
    });
  }
  query() {
    return Promise.resolve({
      held: held.map((h) => ({ name: h.name, mode: h.mode, clientId: h.clientId })),
      pending: pendingLocks.map((r) => ({ name: r.name, mode: r.mode, clientId })),
    });
  }
}
Object.defineProperty(LockManager.prototype, Symbol.toStringTag, { value: "LockManager", configurable: true });
const locks = new LockManager();

export {
  Worker, BroadcastChannel, getEnvironmentData, setEnvironmentData, markAsUntransferable,
  isMarkedAsUntransferable, moveMessagePortToContext, postMessageToThread, locks, LockManager, Lock,
  initChild, encodeError, deliverThreadMessage,
};
export default {
  Worker, BroadcastChannel, getEnvironmentData, setEnvironmentData, markAsUntransferable,
  isMarkedAsUntransferable, moveMessagePortToContext, postMessageToThread, locks, LockManager, Lock,
  initChild, encodeError, deliverThreadMessage,
};
`
