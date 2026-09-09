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

export { EventEmitter, getMaxListeners, setMaxListeners, defaultMaxListeners };
export default EventEmitter;
`

func declareEvents() {
	registerJSShim("events", eventsShim)
}
