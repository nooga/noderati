package host

// async_hooks.go is node:async_hooks on top of async_context.go's frames:
// every callback the async runtime schedules (promise jobs, nextTick,
// setImmediate, timers) runs in the frame that was current when it was
// scheduled, so AsyncLocalStorage stores propagate across setTimeout,
// promises/await, queueMicrotask and friends; AsyncResource captures a
// frame at construction and re-enters it in runInAsyncScope/bind.
//
// A frame's store is an immutable Map (AsyncLocalStorage instance ->
// value); run()/enterWith() install a copy, never mutate one a scheduled
// callback may already have captured.
//
// createHook's init/before/after/destroy fire for the runtime's own
// scheduled callbacks (types PROMISE, TickObject, Immediate, Timeout) and
// for AsyncResource; promiseResolve never fires. See async_context.go for
// the one known propagation gap (promises settled from host goroutines).
const asyncHooksShim = `const ctx = globalThis.__noderatiAsyncContext;
ctx.enable();

function codedError(Ctor, code, message) {
  const err = new Ctor(message);
  err.code = code;
  return err;
}
function received(v) {
  if (v == null) return " Received " + v;
  if (typeof v === "function") return " Received function " + (v.name || "<anonymous>");
  if (typeof v === "object") return " Received an instance of " + ((v.constructor && v.constructor.name) || "Object");
  let s = typeof v === "string" ? "'" + v + "'" : String(v);
  if (typeof v === "string" && v.length > 28) s = "'" + v.slice(0, 25) + "'...";
  return " Received type " + typeof v + " (" + s + ")";
}
function invalidArgType(name, expected, v) {
  const what = '"' + name + '" ' + (name.includes(".") ? "property" : "argument");
  return codedError(TypeError, "ERR_INVALID_ARG_TYPE", "The " + what + " must be " + expected + "." + received(v));
}
function validateFunction(v, name) {
  if (typeof v !== "function") throw invalidArgType(name, "of type function", v);
}
function validateString(v, name) {
  if (typeof v !== "string") throw invalidArgType(name, "of type string", v);
}
function validateObject(v, name) {
  if (v === null || typeof v !== "object") throw invalidArgType(name, "of type object", v);
}

const executionAsyncId = () => ctx.executionAsyncId();
const triggerAsyncId = () => ctx.triggerAsyncId();
const executionAsyncResource = () => ctx.executionAsyncResource();

// --- createHook ---
const activeHooks = new Set();
function dispatch(event, asyncId, type, trigger, resource) {
  for (const hook of Array.from(activeHooks)) {
    const fn = hook[kCallbacks][event];
    if (typeof fn !== "function") continue;
    if (event === "init") fn.call(hook[kCallbacks], asyncId, type, trigger, resource);
    else fn.call(hook[kCallbacks], asyncId);
  }
}
const kCallbacks = Symbol("callbacks");
class AsyncHook {
  constructor({ init, before, after, destroy, promiseResolve }) {
    for (const [name, fn] of [["init", init], ["before", before], ["after", after], ["destroy", destroy], ["promiseResolve", promiseResolve]]) {
      if (fn !== undefined && typeof fn !== "function") throw codedError(TypeError, "ERR_ASYNC_CALLBACK", "hook." + name + " must be a function");
    }
    this[kCallbacks] = { init, before, after, destroy, promiseResolve };
  }
  enable() {
    if (!activeHooks.has(this)) {
      activeHooks.add(this);
      ctx.setHooks(dispatch);
    }
    return this;
  }
  disable() {
    activeHooks.delete(this);
    if (activeHooks.size === 0) ctx.setHooks(undefined);
    return this;
  }
}
function createHook(fns) {
  return new AsyncHook(fns || {});
}

// --- AsyncResource ---
const kAsyncId = Symbol("asyncId");
const kTriggerId = Symbol("triggerAsyncId");
const kStore = Symbol("store");
const kDestroyed = Symbol("destroyed");

class AsyncResource {
  constructor(type, opts = {}) {
    validateString(type, "type");
    let triggerAsyncId = opts;
    if (typeof opts !== "number") {
      triggerAsyncId = opts.triggerAsyncId === undefined ? executionAsyncId() : opts.triggerAsyncId;
    }
    if (!Number.isSafeInteger(triggerAsyncId) || triggerAsyncId < -1) {
      throw codedError(RangeError, "ERR_INVALID_ASYNC_ID", "Invalid triggerAsyncId value: " + triggerAsyncId);
    }
    this[kAsyncId] = ctx.newAsyncId();
    this[kTriggerId] = triggerAsyncId;
    this[kStore] = ctx.getStore();
    if (activeHooks.size) dispatch("init", this[kAsyncId], type, triggerAsyncId, this);
  }
  runInAsyncScope(fn, thisArg, ...args) {
    if (activeHooks.size) dispatch("before", this[kAsyncId]);
    try {
      return ctx.runInFrame(this[kAsyncId], this[kTriggerId], this[kStore], this, fn, thisArg, args);
    } finally {
      if (activeHooks.size) dispatch("after", this[kAsyncId]);
    }
  }
  emitDestroy() {
    if (!this[kDestroyed]) {
      this[kDestroyed] = true;
      if (activeHooks.size) dispatch("destroy", this[kAsyncId]);
    }
    return this;
  }
  asyncId() { return this[kAsyncId]; }
  triggerAsyncId() { return this[kTriggerId]; }
  bind(fn, thisArg) {
    validateFunction(fn, "fn");
    let bound;
    if (thisArg === undefined) {
      const resource = this;
      bound = function (...args) {
        return resource.runInAsyncScope(fn, this, ...args);
      };
    } else {
      bound = this.runInAsyncScope.bind(this, fn, thisArg);
    }
    Object.defineProperties(bound, {
      length: { configurable: true, enumerable: false, value: fn.length, writable: false },
      asyncResource: { configurable: true, enumerable: true, value: this, writable: true },
    });
    return bound;
  }
  static bind(fn, type, thisArg) {
    type = type || fn.name || "bound-anonymous-fn";
    return (new AsyncResource(type)).bind(fn, thisArg);
  }
}

// --- AsyncLocalStorage ---
function withStore(als, value) {
  const prev = ctx.getStore();
  const next = new Map(prev);
  next.set(als, value);
  return next;
}

class AsyncLocalStorage {
  #name;
  #defaultValue;
  #disabled = false;
  constructor(options = {}) {
    validateObject(options, "options");
    this.#defaultValue = options.defaultValue;
    this.#name = options.name === undefined ? "" : String(options.name);
  }
  get name() { return this.#name; }
  static bind(fn) {
    validateFunction(fn, "fn");
    return AsyncResource.bind(fn);
  }
  static snapshot() {
    const store = ctx.getStore();
    return function runInAsyncScope(fn, ...args) {
      const prev = ctx.getStore();
      ctx.setStore(store);
      try {
        return fn.apply(this, args);
      } finally {
        ctx.setStore(prev);
      }
    };
  }
  disable() {
    this.#disabled = true;
    const cur = ctx.getStore();
    if (cur && cur.has(this)) {
      const next = new Map(cur);
      next.delete(this);
      ctx.setStore(next);
    }
  }
  getStore() {
    if (this.#disabled) return undefined;
    const m = ctx.getStore();
    if (!m || !m.has(this)) return this.#defaultValue;
    return m.get(this);
  }
  enterWith(store) {
    this.#disabled = false;
    ctx.setStore(withStore(this, store));
  }
  run(store, callback, ...args) {
    this.#disabled = false;
    const prev = ctx.getStore();
    ctx.setStore(withStore(this, store));
    try {
      return Reflect.apply(callback, null, args);
    } finally {
      ctx.setStore(prev);
    }
  }
  exit(callback, ...args) {
    return this.run(undefined, callback, ...args);
  }
  withScope(store) {
    return new RunScope(this, store);
  }
}

class RunScope {
  #storage;
  #previousStore;
  #disposed = false;
  constructor(storage, store) {
    this.#storage = storage;
    this.#previousStore = ctx.getStore();
    storage.enterWith(store);
  }
  [Symbol.dispose]() {
    if (this.#disposed) return;
    this.#disposed = true;
    ctx.setStore(this.#previousStore);
  }
}

const asyncWrapProviders = Object.freeze(Object.assign(Object.create(null), {
  NONE: 0, DIRHANDLE: 1, DNSCHANNEL: 2, ELDHISTOGRAM: 3, FILEHANDLE: 4, FILEHANDLECLOSEREQ: 5, BLOBREADER: 6,
  FSEVENTWRAP: 7, FSREQCALLBACK: 8, FSREQPROMISE: 9, GETADDRINFOREQWRAP: 10, GETNAMEINFOREQWRAP: 11,
  HEAPSNAPSHOT: 12, HTTP2SESSION: 13, HTTP2STREAM: 14, HTTP2PING: 15, HTTP2SETTINGS: 16,
  HTTPINCOMINGMESSAGE: 17, HTTPCLIENTREQUEST: 18, LOCKS: 19, JSSTREAM: 20, JSUDPWRAP: 21, MESSAGEPORT: 22,
  PIPECONNECTWRAP: 23, PIPESERVERWRAP: 24, PIPEWRAP: 25, PROCESSWRAP: 26, PROMISE: 27, QUERYWRAP: 28,
  QUIC_ENDPOINT: 29, QUIC_LOGSTREAM: 30, QUIC_SESSION: 31, QUIC_STREAM: 32, QUIC_UDP: 33, SHUTDOWNWRAP: 34,
  SIGNALWRAP: 35, STATWATCHER: 36, STREAMPIPE: 37, TCPCONNECTWRAP: 38, TCPSERVERWRAP: 39, TCPWRAP: 40,
  TTYWRAP: 41, UDPSENDWRAP: 42, UDPWRAP: 43, SIGINTWATCHDOG: 44, WORKER: 45, WORKERCPUPROFILE: 46,
  WORKERCPUUSAGE: 47, WORKERHEAPPROFILE: 48, WORKERHEAPSNAPSHOT: 49, WORKERHEAPSTATISTICS: 50, WRITEWRAP: 51,
  ZLIB: 52, CHECKPRIMEREQUEST: 53, PBKDF2REQUEST: 54, KEYPAIRGENREQUEST: 55, KEYGENREQUEST: 56,
  KEYEXPORTREQUEST: 57, ARGON2REQUEST: 58, CIPHERREQUEST: 59, DERIVEBITSREQUEST: 60, HASHREQUEST: 61,
  RANDOMBYTESREQUEST: 62, RANDOMPRIMEREQUEST: 63, SCRYPTREQUEST: 64, SIGNREQUEST: 65, TLSWRAP: 66,
  VERIFYREQUEST: 67,
}));

export {
  AsyncLocalStorage, createHook, executionAsyncId, triggerAsyncId, executionAsyncResource,
  asyncWrapProviders, AsyncResource,
};
export default {
  AsyncLocalStorage, createHook, executionAsyncId, triggerAsyncId, executionAsyncResource,
  asyncWrapProviders, AsyncResource,
};
`

func declareAsyncHooks() {
	registerJSShim("async_hooks", asyncHooksShim)
}
