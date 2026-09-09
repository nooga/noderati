package host

const eventsShim = `class EventEmitter {
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
  emit(event, ...args) {
    const list = this._events[event];
    if (!list || list.length === 0) return false;
    for (const fn of list.slice()) fn(...args);
    return true;
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

export { EventEmitter, getMaxListeners, setMaxListeners, defaultMaxListeners, addAbortListener };
export default EventEmitter;
`

func declareEvents() {
	registerJSShim("events", eventsShim)
}
