package host

const eventsShim = `import { AsyncResource } from "node:async_hooks";

class EventEmitter {
  constructor() {
    this._events = Object.create(null);
  }
  on(event, listener) {
    if (!this._events[event]) this._events[event] = [];
    this._events[event].push(listener);
    return this;
  }
  once(event, listener) {
    const wrapper = (...args) => {
      this.off(event, wrapper);
      listener(...args);
    };
    return this.on(event, wrapper);
  }
  off(event, listener) {
    return this.removeListener(event, listener);
  }
  removeListener(event, listener) {
    const list = this._events[event];
    if (!list) return this;
    const i = list.indexOf(listener);
    if (i >= 0) list.splice(i, 1);
    return this;
  }
  // "error" is special-cased per real Node's own EventEmitter contract:
  // emitting it with no listener registered throws (the error itself,
  // if that's what was passed) instead of silently doing nothing like
  // every other event name - found missing chasing real tinypool's own
  // worker-pool setup under noderati: worker/fork creation failures
  // (both worker_threads.Worker and child_process.fork are still
  // unimplemented here) get reported via this.emit('error', err)
  // internally, and with no listener yet attached at that exact
  // moment, this used to just return false and vanish - not a thrown,
  // visible crash the way real Node's own "Unhandled 'error' event"
  // behavior would surface it, but a promise nothing will ever settle,
  // silently hanging forever instead. The same root shape as the
  // emitOnObject fix (round 135) for a different EventEmitter
  // implementation (the Go-native one backing streams/child_process/
  // http, not this JS-shim one instantiated by real user code that
  // extends EventEmitter directly) - worth having in both.
  emit(event, ...args) {
    const list = this._events[event];
    if (!list || list.length === 0) {
      if (event === "error") {
        const er = args[0];
        if (er instanceof Error) throw er;
        const err = new Error("Unhandled error." + (er !== undefined ? " (" + er + ")" : ""));
        err.context = er;
        throw err;
      }
      return false;
    }
    for (const fn of list.slice()) fn.call(this, ...args);
    return true;
  }
  // setMaxListeners/getMaxListeners INSTANCE methods were missing
  // entirely - a different API from the *static* events.setMaxListeners/
  // getMaxListeners this file already implements below (this file's own
  // pre-existing doc comment on those covers only the newer, Node 15+
  // static module-level form: 'events.setMaxListeners(n, ...emitters)').
  // Real Node's EventEmitter has always had these as plain instance
  // methods too (emitter.setMaxListeners(n)/emitter.getMaxListeners()),
  // predating the static form by years and far more commonly used in
  // practice. Found this round (docs/real-node-plan.md): real,
  // unmodified 'merge-stream' (a real, direct dependency of
  // 'jest-worker', itself used by 'terser-webpack-plugin' for its
  // worker-pool minification) does 'output.setMaxListeners(0)'
  // unconditionally on a real 'stream.PassThrough' instance -
  // PassThrough extends Transform extends this same EventEmitter
  // (stream.go), so the missing instance method broke real webpack's
  // own default production-mode minification step. Mirrors the static
  // form's own semantics (an explicit, non-negative limit; 0 means
  // unlimited) but scoped to 'this' alone, and returns 'this' to match
  // real Node's chainable API ('output.setMaxListeners(0)' is used
  // standalone here, but real code elsewhere chains off the return
  // value too).
  setMaxListeners(n) {
    this._maxListeners = n;
    return this;
  }
  getMaxListeners() {
    return typeof this._maxListeners === "number" ? this._maxListeners : defaultMaxListeners;
  }
}
// Real Node's require("node:events")/require("events") returns the
// EventEmitter class itself, not a namespace object wrapping it (also
// true of the default import - EventEmitter.EventEmitter === EventEmitter
// in real Node, a self-reference kept here for the same reason). Found
// the hard way while probing real undici (round 69, docs/real-node-plan.md):
// undici's dispatcher.js does 'const EventEmitter = require("node:events")'
// then 'class Dispatcher extends EventEmitter' - with the previous
// { EventEmitter } wrapper object as the default export, cjs.go's
// requireNative handed that whole plain object back as EventEmitter, and
// 'extends' a non-constructor object throws "Class extends value object
// is not a constructor or null" - a real, encountered failure, not a
// hypothetical one.
EventEmitter.EventEmitter = EventEmitter;

// getMaxListeners/setMaxListeners/defaultMaxListeners were missing
// entirely - found while probing real undici (round 74,
// docs/real-node-plan.md): its own lib/web/fetch/request.js calls
// getMaxListeners(new AbortController().signal) at module load time
// (guarded by its own try/catch, so this wasn't the thing blocking
// fetch(), but a real, separate gap encountered along the way). Real
// Node's getMaxListeners/setMaxListeners work on *any* object with an
// EventEmitter-like _maxListeners slot, not just instances of this
// class - an AbortSignal is a real example, confirmed directly by
// this exact real call site - so these check for the slot generically
// rather than requiring instanceof EventEmitter.
let defaultMaxListeners = 10;
function getMaxListeners(emitter) {
  return (emitter && typeof emitter._maxListeners === "number") ? emitter._maxListeners : defaultMaxListeners;
}
function setMaxListeners(n, ...emitters) {
  if (emitters.length === 0) {
    defaultMaxListeners = n;
    return;
  }
  for (const e of emitters) e._maxListeners = n;
}
// Attached directly onto the class/function itself (not a separate
// wrapper default export) - real Node's own events module default
// export genuinely is the EventEmitter function with these as its own
// static properties, and cjs.go's requireNative hands the "default"
// export straight back as require()'s result, so a bare
// 'const EventEmitter = require("node:events")' (real undici's own
// dispatcher.js, round 69) must keep working unchanged alongside
// 'const { getMaxListeners } = require("node:events")' (real undici's
// request.js, round 74) - both real call shapes, confirmed directly.
EventEmitter.getMaxListeners = getMaxListeners;
EventEmitter.setMaxListeners = setMaxListeners;
EventEmitter.defaultMaxListeners = defaultMaxListeners;

// addAbortListener was missing entirely - found while re-probing real
// undici after paserati#302 was fixed (round 75, docs/real-node-plan.md):
// with the accessor-destroying bug gone, a real fetch() call now gets
// all the way into undici's own lib/core/util.js, which does
// 'const { addAbortListener: addAbortListenerNative } =
// require("node:events")' at module load time and calls it
// unconditionally (no try/catch) the moment any request carries a
// signal - a real, unavoidable call site, not a hypothetical one. Real
// Node's own implementation (lib/events.js): if the signal is already
// aborted, queues the listener as a microtask instead of calling it
// synchronously (spec requires abort listeners never run
// synchronously with the call that set .aborted); otherwise it's a
// real signal.addEventListener('abort', ..., {once:true}) - both
// AbortController/AbortSignal are real paserati builtins (a real
// EventTarget), not anything faked here. The return value matters:
// undici does 'addAbortListenerNative(signal, listener)[Symbol.dispose]'
// (grabbing the disposer function, not calling it yet), so the returned
// object must carry a real, callable Symbol.dispose property - Symbol.dispose
// is itself a real well-known symbol in paserati (confirmed directly),
// so this needs no faking either.
function addAbortListener(signal, listener) {
  let removeEventListener;
  if (signal && signal.aborted) {
    queueMicrotask(() => listener());
  } else {
    signal.addEventListener("abort", listener, { once: true });
    removeEventListener = () => { signal.removeEventListener("abort", listener); };
  }
  return { [Symbol.dispose]() { if (removeEventListener) removeEventListener(); } };
}
EventEmitter.addAbortListener = addAbortListener;

// EventEmitterAsyncResource was entirely missing - found chasing real
// tinypool (vitest's own real worker-pool dependency, its actual
// process/worker orchestration - exactly what made this a worthwhile
// probe target) under noderati: real, unmodified
// tinypool/dist/index.js does 'class ... extends EventEmitterAsyncResource'
// (via 'import { EventEmitterAsyncResource } from "node:events"') at
// module scope. Real Node's own version binds an AsyncResource to an
// EventEmitter so emitted events run within its own async execution
// context (for async_hooks tracing) - implemented here by delegating
// to the real AsyncResource this module already imports, rather than
// reimplementing async-context tracking from scratch.
class EventEmitterAsyncResource extends EventEmitter {
  constructor(options) {
    let name = "EventEmitterAsyncResource";
    let asyncResourceOptions;
    let emitterOptions;
    if (typeof options === "string") {
      name = options;
    } else if (options && typeof options === "object") {
      const { name: optName, ...rest } = options;
      if (optName) name = optName;
      asyncResourceOptions = rest;
      emitterOptions = rest;
    }
    super(emitterOptions);
    this.asyncResource = new AsyncResource(name, asyncResourceOptions);
  }
  emit(...args) {
    return this.asyncResource.runInAsyncScope(() => super.emit(...args), this);
  }
  emitDestroy() {
    this.asyncResource.emitDestroy();
  }
  get asyncId() {
    return this.asyncResource.asyncId();
  }
  get triggerAsyncId() {
    return this.asyncResource.triggerAsyncId();
  }
}

export { EventEmitter, EventEmitterAsyncResource, getMaxListeners, setMaxListeners, defaultMaxListeners, addAbortListener };
export default EventEmitter;
`

func declareEvents() {
	registerJSShim("events", eventsShim)
}
