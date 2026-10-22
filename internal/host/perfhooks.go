package host

import (
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// perfhooks.go is node:perf_hooks, written after lib/perf_hooks.js and
// lib/internal/perf/*: the Performance timeline (marks, measures,
// resource timings), PerformanceObserver delivery on setImmediate,
// timerify, eventLoopUtilization, and HdrHistogram-compatible
// RecordableHistogram / monitorEventLoopDelay.
//
// The Performance* classes are installed as lazy globals: the shim only
// loads the first time one of them is touched. The `performance` global
// is an engine global slot, not a globalThis property, so it holds one
// object from startup: its now()/timeOrigin answer natively, and every
// other method first loads the shim, which adopts that same object as
// the real Performance instance (so require('perf_hooks').performance ===
// globalThis.performance, as in Node).

func declarePerfHooks() {
	registerJSShim("perf_hooks", perfHooksShim)
}

var perfHooksGlobals = []string{
	"Performance", "PerformanceEntry", "PerformanceMark", "PerformanceMeasure",
	"PerformanceObserver", "PerformanceObserverEntryList", "PerformanceResourceTiming",
}

// perfForwardedMethods are Performance.prototype methods (plus the
// EventTarget ones) that load the shim on first use.
var perfEnumerableMembers = []string{
	"clearMarks", "clearMeasures", "clearResourceTimings", "getEntries", "getEntriesByName",
	"getEntriesByType", "mark", "measure", "now", "setResourceTimingBufferSize", "timeOrigin",
	"toJSON", "onresourcetimingbufferfull",
}

var perfForwardedMethods = []string{
	"clearMarks", "clearMeasures", "clearResourceTimings", "getEntries", "getEntriesByName",
	"getEntriesByType", "mark", "measure", "setResourceTimingBufferSize", "toJSON",
	"eventLoopUtilization", "markResourceTiming", "timerify",
	"addEventListener", "removeEventListener", "dispatchEvent",
}

func installPerfHooksGlobals(p *driver.Paserati) {
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
	lazy := &lazyJSModule{p: p, spec: "perf_hooks"}
	load := func() error {
		_, err := lazy.get("default")
		return err
	}

	nowFn := vm.NewNativeFunction(0, false, "now", func(_ []vm.Value) (vm.Value, error) {
		return vm.NumberValue(float64(time.Since(processStartTime).Nanoseconds()) / 1e6), nil
	})
	timeOrigin := vm.NumberValue(float64(processStartTime.UnixNano()) / 1e6)

	// The EventTarget layer, as in Node: Performance.prototype's parent.
	etProto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	proto := vm.NewObject(vm.NewValueFromPlainObject(etProto)).AsPlainObject()
	instance := vm.NewObject(vm.NewValueFromPlainObject(proto)).AsPlainObject()
	instVal := vm.NewValueFromPlainObject(instance)
	yes, no := true, false
	timeOriginGetter := vm.NewNativeFunction(0, false, "get timeOrigin", func(_ []vm.Value) (vm.Value, error) {
		return timeOrigin, nil
	})
	forward := func(name string) vm.Value {
		return vm.NewNativeFunction(0, false, name, func(args []vm.Value) (vm.Value, error) {
			if err := load(); err != nil {
				return vm.Undefined, err
			}
			fn, err := vmInst.GetProperty(instVal, name)
			if err != nil {
				return vm.Undefined, err
			}
			return vmInst.Call(fn, instVal, args)
		})
	}
	lazyAccessor := func(name string, enumerable bool) {
		getter := vm.NewNativeFunction(0, false, "get "+name, func(_ []vm.Value) (vm.Value, error) {
			if err := load(); err != nil {
				return vm.Undefined, err
			}
			return vmInst.GetProperty(instVal, name)
		})
		setter := vm.NewNativeFunction(1, false, "set "+name, func(args []vm.Value) (vm.Value, error) {
			if err := load(); err != nil {
				return vm.Undefined, err
			}
			return vm.Undefined, vmInst.SetProperty(instVal, name, argAt(args, 0))
		})
		proto.DefineAccessorProperty(name, getter, true, setter, true, &enumerable, &yes)
	}
	// Same enumerable members, in the same order, as Node's
	// Performance.prototype (for-in over performance lists them).
	enumerable := map[string]bool{}
	for _, name := range perfEnumerableMembers {
		enumerable[name] = true
		switch name {
		case "now":
			proto.SetOwn("now", nowFn)
		case "timeOrigin":
			proto.DefineAccessorProperty("timeOrigin", timeOriginGetter, true, vm.Undefined, false, &yes, &yes)
		case "onresourcetimingbufferfull":
			lazyAccessor(name, true)
		default:
			proto.SetOwn(name, forward(name))
		}
	}
	for _, name := range []string{"addEventListener", "removeEventListener", "dispatchEvent"} {
		enumerable[name] = true
		etProto.SetOwn(name, forward(name))
	}
	for _, name := range perfForwardedMethods {
		if !enumerable[name] {
			proto.SetOwnNonEnumerable(name, forward(name))
		}
	}
	lazyAccessor("nodeTiming", false)
	_ = no

	native := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	native.SetOwn("now", nowFn)
	native.SetOwn("hrtime", vm.NewNativeFunction(0, false, "hrtime", func(_ []vm.Value) (vm.Value, error) {
		return vm.NumberValue(float64(time.Since(processStartTime).Nanoseconds())), nil
	}))
	native.SetOwn("timeOrigin", timeOrigin)
	native.SetOwn("instance", instVal)
	gobj.SetOwnNonEnumerable("__noderatiPerf", vm.NewValueFromPlainObject(native))
	vmInst.CurrentRealm().SetGlobal("performance", instVal)

	for _, name := range perfHooksGlobals {
		name := name
		getter := vm.NewNativeFunction(0, false, "get "+name, func(_ []vm.Value) (vm.Value, error) {
			def, err := lazy.get("default")
			if err != nil {
				return vm.Undefined, err
			}
			v, err := vmInst.GetProperty(def, name)
			if err != nil {
				return vm.Undefined, err
			}
			// Settle into the plain data property Node has.
			gobj.DeleteOwn(name)
			gobj.DefineOwnProperty(name, v, &yes, &no, &yes)
			return v, nil
		})
		setter := vm.NewNativeFunction(1, false, "set "+name, func(args []vm.Value) (vm.Value, error) {
			gobj.DeleteOwn(name)
			gobj.DefineOwnProperty(name, argAt(args, 0), &yes, &no, &yes)
			return vm.Undefined, nil
		})
		gobj.DefineAccessorProperty(name, getter, true, setter, true, &no, &yes)
	}
}

const perfHooksShim = `
// One timeline per process: every load of this module (node:perf_hooks,
// perf_hooks, the lazy globals) shares the same instance.
function createApi() {
const __native = globalThis.__noderatiPerf;
const kInternal = Symbol("kInternal");
const toStringTag = Symbol.toStringTag;

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
function illegalConstructor() {
  return codedError(TypeError, "ERR_ILLEGAL_CONSTRUCTOR", "Illegal constructor");
}
function missingArgs(...names) {
  const list = names.map((n) => '"' + n + '"');
  let msg;
  if (list.length === 1) msg = "The " + list[0] + " argument must be specified";
  else msg = "The " + list.slice(0, -1).join(", ") + " and " + list[list.length - 1] + " arguments must be specified";
  return codedError(TypeError, "ERR_MISSING_ARGS", msg);
}
function validateObject(v, name) {
  if (v === null || typeof v !== "object" || Array.isArray(v)) throw invalidArgType(name, "of type object", v);
}
function validateFunction(v, name) {
  if (typeof v !== "function") throw invalidArgType(name, "of type function", v);
}
function validateNumber(v, name) {
  if (typeof v !== "number") throw invalidArgType(name, "of type number", v);
}
function outOfRange(name, range, v) {
  return codedError(RangeError, "ERR_OUT_OF_RANGE", 'The value of "' + name + '" is out of range. It must be ' + range + ". Received " + String(v));
}
function inspectList(arr) {
  if (!Array.isArray(arr)) return String(arr);
  return "[ " + arr.map((x) => (typeof x === "string" ? "'" + x + "'" : String(x))).join(", ") + " ]";
}

const now = __native.now;
const timeOrigin = __native.timeOrigin;

// --- entries ---
class PerformanceEntry {
  #name; #type; #start; #duration;
  constructor(token, name, type, start, duration) {
    if (token !== kInternal) throw illegalConstructor();
    this.#name = name;
    this.#type = type;
    this.#start = start;
    this.#duration = duration;
  }
  get name() { return this.#name; }
  get entryType() { return this.#type; }
  get startTime() { return this.#start; }
  get duration() { return this.#duration; }
  toJSON() {
    return { name: this.name, entryType: this.entryType, startTime: this.startTime, duration: this.duration };
  }
}
Object.defineProperty(PerformanceEntry.prototype, toStringTag, { value: "PerformanceEntry", configurable: true });

function cloneDetail(detail) {
  if (detail == null) return null;
  return typeof structuredClone === "function" ? structuredClone(detail) : detail;
}

class PerformanceMark extends PerformanceEntry {
  #detail;
  constructor(name, options) {
    if (arguments.length === 0) throw missingArgs("name");
    name = String(name);
    if (options !== undefined) validateObject(options, "options");
    const startTime = options && options.startTime !== undefined ? options.startTime : now();
    validateNumber(startTime, "options.startTime");
    if (startTime < 0) throw codedError(TypeError, "ERR_PERFORMANCE_INVALID_TIMESTAMP", startTime + " is not a valid timestamp");
    super(kInternal, name, "mark", startTime, 0);
    this.#detail = cloneDetail(options && options.detail);
  }
  get detail() { return this.#detail; }
  toJSON() {
    return { name: this.name, entryType: this.entryType, startTime: this.startTime, duration: this.duration, detail: this.#detail };
  }
}
Object.defineProperty(PerformanceMark.prototype, toStringTag, { value: "PerformanceMark", configurable: true });

class PerformanceMeasure extends PerformanceEntry {
  #detail;
  constructor(token, name, start, duration, detail) {
    if (token !== kInternal) throw illegalConstructor();
    super(kInternal, name, "measure", start, duration);
    this.#detail = detail;
  }
  get detail() { return this.#detail; }
  toJSON() {
    return { name: this.name, entryType: this.entryType, startTime: this.startTime, duration: this.duration, detail: this.#detail };
  }
}
Object.defineProperty(PerformanceMeasure.prototype, toStringTag, { value: "PerformanceMeasure", configurable: true });

class PerformanceResourceTiming extends PerformanceEntry {
  #t;
  constructor(token, name, t) {
    if (token !== kInternal) throw illegalConstructor();
    super(kInternal, name, "resource", t.startTime, t.duration);
    this.#t = t;
  }
  get initiatorType() { return this.#t.initiatorType; }
  get workerStart() { return 0; }
  get redirectStart() { return this.#t.redirectStart || 0; }
  get redirectEnd() { return this.#t.redirectEnd || 0; }
  get fetchStart() { return this.#t.startTime; }
  get domainLookupStart() { return this.#t.startTime; }
  get domainLookupEnd() { return this.#t.startTime; }
  get connectStart() { return this.#t.startTime; }
  get connectEnd() { return this.#t.startTime; }
  get secureConnectionStart() { return 0; }
  get nextHopProtocol() { return this.#t.nextHopProtocol || ""; }
  get requestStart() { return this.#t.startTime; }
  get responseStart() { return this.#t.startTime + this.#t.duration; }
  get responseEnd() { return this.#t.startTime + this.#t.duration; }
  get encodedBodySize() { return this.#t.encodedBodySize || 0; }
  get decodedBodySize() { return this.#t.decodedBodySize || 0; }
  get transferSize() { return this.#t.encodedBodySize ? this.#t.encodedBodySize + 300 : 0; }
  get deliveryType() { return this.#t.deliveryType || ""; }
  get responseStatus() { return this.#t.responseStatus || 0; }
  toJSON() {
    const out = {};
    for (const k of ["name", "entryType", "startTime", "duration", "initiatorType", "nextHopProtocol", "workerStart",
      "redirectStart", "redirectEnd", "fetchStart", "domainLookupStart", "domainLookupEnd", "connectStart",
      "connectEnd", "secureConnectionStart", "requestStart", "responseStart", "responseEnd", "transferSize",
      "encodedBodySize", "decodedBodySize", "deliveryType", "responseStatus"]) out[k] = this[k];
    return out;
  }
}
Object.defineProperty(PerformanceResourceTiming.prototype, toStringTag, { value: "PerformanceResourceTiming", configurable: true });

class PerformanceNodeTiming {
  constructor(token) {
    if (token !== kInternal) throw illegalConstructor();
  }
  get name() { return "node"; }
  get entryType() { return "node"; }
  get startTime() { return 0; }
  get duration() { return now(); }
  get nodeStart() { return 0.5; }
  get v8Start() { return 1; }
  get bootstrapComplete() { return 2; }
  get environment() { return 1.5; }
  get loopStart() { return loopStarted; }
  get loopExit() { return -1; }
  get idleTime() { return 0; }
  toJSON() {
    return {
      name: "node", entryType: "node", startTime: 0, duration: this.duration,
      nodeStart: this.nodeStart, v8Start: this.v8Start, bootstrapComplete: this.bootstrapComplete,
      environment: this.environment, loopStart: this.loopStart, loopExit: this.loopExit, idleTime: this.idleTime,
    };
  }
}
Object.defineProperty(PerformanceNodeTiming.prototype, toStringTag, { value: "PerformanceNodeTiming", configurable: true });
let loopStarted = -1;
setImmediate(() => { loopStarted = now(); });
const nodeTiming = new PerformanceNodeTiming(kInternal);

// --- the timeline buffers ---
let marks = [];
let measures = [];
let resources = [];
let resourceBufferSize = 250;

function sortByStart(list) {
  return list.slice().sort((a, b) => a.startTime - b.startTime);
}
function filterEntries(list, name, type) {
  return list.filter((e) => (name === undefined || e.name === name) && (type === undefined || e.entryType === type));
}

// --- observers ---
const supportedEntryTypes = Object.freeze(["dns", "function", "gc", "http", "http2", "mark", "measure", "net", "quic", "resource"]);
const observers = new Set();
const pending = new Set();
let dispatchQueued = false;

function queuePending(observer) {
  pending.add(observer);
  if (dispatchQueued) return;
  dispatchQueued = true;
  setImmediate(() => {
    dispatchQueued = false;
    const list = Array.from(pending);
    pending.clear();
    for (const o of list) o[kDispatch]();
  });
}
function enqueue(entry) {
  for (const o of observers) o[kMaybeBuffer](entry);
}
function hasObserver(type) {
  for (const o of observers) if (o[kTypes].has(type)) return true;
  return false;
}

const kDispatch = Symbol("kDispatch");
const kMaybeBuffer = Symbol("kMaybeBuffer");
const kTypes = Symbol("kTypes");

class PerformanceObserverEntryList {
  #entries;
  constructor(token, entries) {
    if (token !== kInternal) throw illegalConstructor();
    this.#entries = sortByStart(entries);
  }
  getEntries() { return this.#entries.slice(); }
  getEntriesByType(type) {
    if (arguments.length === 0) throw missingArgs("type");
    return filterEntries(this.#entries, undefined, String(type));
  }
  getEntriesByName(name, type) {
    if (arguments.length === 0) throw missingArgs("name");
    return filterEntries(this.#entries, String(name), type === undefined ? undefined : String(type));
  }
}
Object.defineProperty(PerformanceObserverEntryList.prototype, toStringTag, { value: "PerformanceObserverEntryList", configurable: true });

class PerformanceObserver {
  #callback;
  #buffer = [];
  #mode = undefined;
  constructor(callback) {
    validateFunction(callback, "callback");
    this.#callback = callback;
    this[kTypes] = new Set();
  }
  observe(options = {}) {
    validateObject(options, "options");
    const { entryTypes, type, buffered } = options;
    if (entryTypes === undefined && type === undefined) throw missingArgs("options.entryTypes", "options.type");
    if (entryTypes != null && type != null) {
      throw codedError(TypeError, "ERR_INVALID_ARG_VALUE",
        "The property 'options.entryTypes' options.entryTypes can not set with options.type together. Received " + inspectList(entryTypes));
    }
    switch (this.#mode) {
      case undefined:
        if (entryTypes !== undefined) this.#mode = "multiple";
        if (type !== undefined) this.#mode = "single";
        break;
      case "single":
        if (entryTypes !== undefined) throw codedError(Error, "ERR_INVALID_STATE", "Invalid state: PerformanceObserver can not change to multiple observations");
        break;
      case "multiple":
        if (type !== undefined) throw codedError(Error, "ERR_INVALID_STATE", "Invalid state: PerformanceObserver can not change to single observation");
        break;
    }
    if (this.#mode === "multiple") {
      if (!Array.isArray(entryTypes)) throw invalidArgType("options.entryTypes", "an instance of Array", entryTypes);
      this[kTypes].clear();
      for (const t of entryTypes) if (supportedEntryTypes.includes(t)) this[kTypes].add(t);
    } else {
      if (!supportedEntryTypes.includes(type)) return;
      this[kTypes].add(type);
      if (buffered) {
        const existing = filterEntries(type === "mark" ? marks : type === "measure" ? measures : type === "resource" ? resources : [], undefined, type);
        if (existing.length) {
          this.#buffer.push(...existing);
          queuePending(this);
        }
      }
    }
    if (this[kTypes].size) observers.add(this);
    else this.disconnect();
  }
  disconnect() {
    observers.delete(this);
    pending.delete(this);
    this.#buffer = [];
    this[kTypes].clear();
    this.#mode = undefined;
  }
  takeRecords() {
    const list = this.#buffer;
    this.#buffer = [];
    return list;
  }
  [kMaybeBuffer](entry) {
    if (!this[kTypes].has(entry.entryType)) return;
    this.#buffer.push(entry);
    queuePending(this);
  }
  [kDispatch]() {
    if (!this.#buffer.length) return;
    const list = new PerformanceObserverEntryList(kInternal, this.takeRecords());
    this.#callback.call(this, list, this);
  }
  static get supportedEntryTypes() { return supportedEntryTypes; }
}
Object.defineProperty(PerformanceObserver.prototype, toStringTag, { value: "PerformanceObserver", configurable: true });

// --- Performance ---
function markTime(name) {
  if (name === undefined) return undefined;
  if (typeof name === "number") {
    if (name < 0) throw codedError(TypeError, "ERR_PERFORMANCE_INVALID_TIMESTAMP", name + " is not a valid timestamp");
    return name;
  }
  name = String(name);
  for (let i = marks.length - 1; i >= 0; i--) if (marks[i].name === name) return marks[i].startTime;
  if (name in nodeTiming && typeof nodeTiming[name] === "number" && name !== "duration" && name !== "startTime") {
    return nodeTiming[name];
  }
  throw new DOMException('The "' + name + '" performance mark has not been set', "SyntaxError");
}

function mark(name, options) {
  const m = new PerformanceMark(...arguments);
  marks.push(m);
  enqueue(m);
  return m;
}

function measure(name, startOrMeasureOptions, endMark) {
  if (arguments.length === 0) throw missingArgs("name");
  name = String(name);
  let start, end, duration, detail = null;
  if (startOrMeasureOptions !== null && typeof startOrMeasureOptions === "object") {
    const o = startOrMeasureOptions;
    if (endMark !== undefined) {
      throw codedError(TypeError, "ERR_PERFORMANCE_MEASURE_INVALID_OPTIONS", "Invalid measure options: endMark must not be specified");
    }
    if (o.start === undefined && o.end === undefined) {
      throw codedError(TypeError, "ERR_PERFORMANCE_MEASURE_INVALID_OPTIONS", "Invalid measure options: One of options.start or options.end is required");
    }
    if (o.start !== undefined && o.end !== undefined && o.duration !== undefined) {
      throw codedError(TypeError, "ERR_PERFORMANCE_MEASURE_INVALID_OPTIONS", "Invalid measure options: Must not have options.start, options.end, and options.duration specified");
    }
    if (o.duration !== undefined) validateNumber(o.duration, "options.duration");
    start = markTime(o.start);
    end = markTime(o.end);
    duration = o.duration;
    if (o.detail !== undefined) detail = cloneDetail(o.detail);
    if (end === undefined) end = start !== undefined && duration !== undefined ? start + duration : now();
    if (start === undefined) start = duration !== undefined ? end - duration : 0;
  } else {
    end = endMark !== undefined ? markTime(endMark) : now();
    start = startOrMeasureOptions !== undefined ? markTime(startOrMeasureOptions) : 0;
  }
  const m = new PerformanceMeasure(kInternal, name, start, end - start, detail);
  measures.push(m);
  enqueue(m);
  return m;
}

function eventLoopUtilization(util1, util2) {
  // No idle-time accounting is available from the engine's loop: the
  // whole elapsed time since the loop started counts as active.
  if (loopStarted < 0) return { idle: 0, active: 0, utilization: 0 };
  const idle = 0;
  const active = now() - loopStarted;
  if (!util1) return { idle, active, utilization: active > 0 ? 1 : 0 };
  const a = util2 ? util1.active - util2.active : active - util1.active;
  const i = util2 ? util1.idle - util2.idle : idle - util1.idle;
  return { idle: i, active: a, utilization: a / (i + a) || 0 };
}

const timerified = new WeakMap();
function timerify(fn, options = {}) {
  validateFunction(fn, "fn");
  validateObject(options, "options");
  const { histogram } = options;
  if (histogram !== undefined && !(histogram instanceof RecordableHistogram)) {
    throw invalidArgType("options.histogram", "an instance of RecordableHistogram", histogram);
  }
  if (timerified.has(fn) && !histogram) return timerified.get(fn);
  function wrapped(...args) {
    const start = now();
    const result = new.target ? Reflect.construct(fn, args, fn) : Reflect.apply(fn, this, args);
    const finish = () => {
      const duration = now() - start;
      if (histogram) histogram.record(Math.max(1, Math.round(duration * 1e6)));
      if (hasObserver("function")) {
        const entry = new PerformanceEntry(kInternal, fn.name, "function", start, duration);
        Object.defineProperty(entry, "detail", { value: args, enumerable: true });
        enqueue(entry);
      }
    };
    if (!new.target && result && typeof result.finally === "function") return result.finally(finish);
    finish();
    return result;
  }
  Object.defineProperties(wrapped, {
    length: { value: fn.length, configurable: true },
    name: { value: "timerified " + fn.name, configurable: true },
  });
  if (!histogram) timerified.set(fn, wrapped);
  return wrapped;
}

let onresourcetimingbufferfull = null;
class Performance extends EventTarget {
  constructor(token) {
    if (token !== kInternal) throw illegalConstructor();
    super();
  }
  clearMarks(name) {
    if (name === undefined) marks = [];
    else { name = String(name); marks = marks.filter((m) => m.name !== name); }
  }
  clearMeasures(name) {
    if (name === undefined) measures = [];
    else { name = String(name); measures = measures.filter((m) => m.name !== name); }
  }
  clearResourceTimings(name) {
    if (name === undefined) resources = [];
    else { name = String(name); resources = resources.filter((m) => m.name !== name); }
  }
  getEntries() { return sortByStart([...marks, ...measures, ...resources]); }
  getEntriesByName(name, type) {
    if (arguments.length === 0) throw missingArgs("name");
    return sortByStart(filterEntries([...marks, ...measures, ...resources], String(name), type === undefined ? undefined : String(type)));
  }
  getEntriesByType(type) {
    if (arguments.length === 0) throw missingArgs("type");
    type = String(type);
    const src = type === "mark" ? marks : type === "measure" ? measures : type === "resource" ? resources : [];
    return sortByStart(src);
  }
  mark(name, options) { return mark(...arguments); }
  measure(name, startOrMeasureOptions, endMark) { return measure(...arguments); }
  now() { return now(); }
  setResourceTimingBufferSize(maxSize) {
    if (arguments.length === 0) throw missingArgs("maxSize");
    validateNumber(maxSize, "maxSize");
    if (maxSize < 0) throw outOfRange("maxSize", ">= 0", maxSize);
    resourceBufferSize = maxSize;
  }
  get timeOrigin() { return timeOrigin; }
  toJSON() {
    return { nodeTiming: this.nodeTiming, timeOrigin: this.timeOrigin, eventLoopUtilization: this.eventLoopUtilization() };
  }
  eventLoopUtilization(util1, util2) { return eventLoopUtilization(util1, util2); }
  get nodeTiming() { return nodeTiming; }
  markResourceTiming(timingInfo, requestedUrl, initiatorType, global, cacheMode, bodyInfo, responseStatus, deliveryType = "") {
    const t = timingInfo || {};
    const startTime = t.startTime || 0;
    const end = t.endTime || startTime;
    const entry = new PerformanceResourceTiming(kInternal, String(requestedUrl), {
      startTime, duration: end - startTime, initiatorType, deliveryType, responseStatus,
      encodedBodySize: bodyInfo && bodyInfo.encodedBodySize, decodedBodySize: bodyInfo && bodyInfo.decodedBodySize,
    });
    if (resources.length < resourceBufferSize) {
      resources.push(entry);
      if (resources.length === resourceBufferSize) {
        const ev = new Event("resourcetimingbufferfull");
        if (typeof onresourcetimingbufferfull === "function") onresourcetimingbufferfull.call(this, ev);
        this.dispatchEvent(ev);
      }
    }
    enqueue(entry);
    return entry;
  }
  timerify(fn, options) { return timerify(fn, options); }
  get onresourcetimingbufferfull() { return onresourcetimingbufferfull; }
  set onresourcetimingbufferfull(v) { onresourcetimingbufferfull = v; }
}
Object.defineProperty(Performance.prototype, toStringTag, { value: "Performance", configurable: true });
// Node's Performance.prototype members are enumerable (for-in lists them).
for (const k of ["clearMarks", "clearMeasures", "clearResourceTimings", "getEntries", "getEntriesByName", "getEntriesByType",
  "mark", "measure", "now", "setResourceTimingBufferSize", "timeOrigin", "toJSON", "onresourcetimingbufferfull"]) {
  const d = Object.getOwnPropertyDescriptor(Performance.prototype, k);
  d.enumerable = true;
  Object.defineProperty(Performance.prototype, k, d);
}

// Adopt the object the engine's "performance" global already holds.
let performance = __native.instance;
if (performance) {
  const fresh = new Performance(kInternal);
  // EventTarget's internal state, kept out of for-in and Object.keys.
  for (const key of Reflect.ownKeys(fresh)) {
    const d = Object.getOwnPropertyDescriptor(fresh, key);
    d.enumerable = false;
    Object.defineProperty(performance, key, d);
  }
  Object.setPrototypeOf(performance, Performance.prototype);
} else {
  performance = new Performance(kInternal);
}

// --- histograms (HdrHistogram, 3 significant figures) ---
const kInt64Max = 9223372036854775807n;
const SUB_BUCKET_MASK = 2047;

// Bucket b holds values in [1024*2^b, 2048*2^b) (b = 0 also holds 0..1023),
// each one 2^b wide - HdrHistogram's layout for 3 significant figures.
function bucketShift(v) {
  let b = 0;
  while (v >= 2048 * 2 ** b) b++;
  return b;
}
function lowestEquivalent(v) {
  const scale = 2 ** bucketShift(v);
  return Math.floor(v / scale) * scale;
}
function equivalentRange(v) {
  return 2 ** bucketShift(v);
}
function highestEquivalent(v) {
  return lowestEquivalent(v) + equivalentRange(v) - 1;
}
function medianEquivalent(v) {
  return lowestEquivalent(v) + Math.floor(equivalentRange(v) / 2);
}

const kState = Symbol("kState");
const kRecord = Symbol("kRecord");
function validateRecordValue(val, name) {
  if (typeof val === "bigint") {
    if (val < 1n || val > BigInt(Number.MAX_SAFE_INTEGER)) throw outOfRange(name, ">= 1 && <= 9007199254740991", val + "n");
    return Number(val);
  }
  if (typeof val !== "number") throw invalidArgType(name, "of type number or bigint", val);
  if (val < 1 || val > Number.MAX_SAFE_INTEGER) throw outOfRange(name, ">= 1 && <= 9007199254740991", val);
  return val;
}

class Histogram {
  constructor(token) {
    if (token !== kInternal) throw illegalConstructor();
    this[kState] = { counts: new Map(), count: 0, min: Infinity, max: 0, exceeds: 0 };
  }
  #sorted() {
    return Array.from(this[kState].counts.entries()).sort((a, b) => a[0] - b[0]);
  }
  [kRecord](v) {
    const s = this[kState];
    v = Math.floor(v);
    const key = lowestEquivalent(v);
    s.counts.set(key, (s.counts.get(key) || 0) + 1);
    s.count++;
    if (v < s.min) s.min = v;
    if (v > s.max) s.max = v;
  }
  get count() { return this[kState].count; }
  get countBigInt() { return BigInt(this[kState].count); }
  get min() { const s = this[kState]; return s.count ? lowestEquivalent(s.min) : 9223372036854775807; }
  get minBigInt() { const s = this[kState]; return s.count ? BigInt(lowestEquivalent(s.min)) : kInt64Max; }
  get max() { const s = this[kState]; return s.count ? highestEquivalent(s.max) : 0; }
  get maxBigInt() { return BigInt(this.max); }
  get mean() {
    const s = this[kState];
    if (!s.count) return NaN;
    let total = 0;
    for (const [k, c] of s.counts) total += c * medianEquivalent(k);
    return total / s.count;
  }
  get exceeds() { return this[kState].exceeds; }
  get exceedsBigInt() { return BigInt(this[kState].exceeds); }
  get stddev() {
    const s = this[kState];
    if (!s.count) return NaN;
    const mean = this.mean;
    let geo = 0;
    for (const [k, c] of s.counts) {
      const d = medianEquivalent(k) - mean;
      geo += d * d * c;
    }
    return Math.sqrt(geo / s.count);
  }
  percentile(percentile) {
    validateNumber(percentile, "percentile");
    if (!(percentile > 0 && percentile <= 100)) throw outOfRange("percentile", "> 0 && <= 100", percentile);
    const s = this[kState];
    if (!s.count) return 0;
    const target = Math.max(Math.floor((percentile / 100) * s.count + 0.5), 1);
    let total = 0;
    for (const [k, c] of this.#sorted()) {
      total += c;
      if (total >= target) return highestEquivalent(k);
    }
    return 0;
  }
  percentileBigInt(percentile) { return BigInt(this.percentile(percentile)); }
  // HdrHistogram's percentile iterator with one tick per half distance.
  #percentileList() {
    const s = this[kState];
    if (!s.count) return [[100, 0]];
    const out = [];
    let toIterate = 0;
    let cumulative = 0;
    let last = 0;
    for (const [k, c] of this.#sorted()) {
      cumulative += c;
      last = k; // the iterator reports each bucket's own (lowest) value
      const current = (100 * cumulative) / s.count;
      while (toIterate <= current && toIterate < 100) {
        out.push([toIterate, last]);
        const temp = Math.floor(Math.log(100 / (100 - toIterate)) / Math.log(2)) + 1;
        toIterate += 100 / 2 ** temp;
        if (cumulative === s.count) break;
      }
      if (cumulative === s.count) break;
    }
    out.push([100, last]);
    return out;
  }
  get percentiles() { return new Map(this.#percentileList()); }
  get percentilesBigInt() { return new Map(this.#percentileList().map(([p, v]) => [p, BigInt(v)])); }
  reset() {
    this[kState] = { counts: new Map(), count: 0, min: Infinity, max: 0, exceeds: 0 };
  }
  toJSON() {
    const percentiles = {};
    for (const [p, v] of this.#percentileList()) percentiles[p] = v;
    return { count: this.count, min: this.min, max: this.max, mean: this.mean, exceeds: this.exceeds, stddev: this.stddev, percentiles };
  }
}
Object.defineProperty(Histogram.prototype, toStringTag, { value: "Histogram", configurable: true });

class RecordableHistogram extends Histogram {
  #prev = 0;
  constructor(token) {
    if (token !== kInternal) throw illegalConstructor();
    super(kInternal);
  }
  record(val) {
    this[kRecord](validateRecordValue(val, "val"));
  }
  recordDelta() {
    const t = __native.hrtime();
    if (this.#prev) this[kRecord](Math.max(1, t - this.#prev));
    this.#prev = t;
  }
  add(other) {
    if (!(other instanceof RecordableHistogram)) throw invalidArgType("other", "an instance of RecordableHistogram", other);
    const s = this[kState], o = other[kState];
    for (const [k, c] of o.counts) s.counts.set(k, (s.counts.get(k) || 0) + c);
    s.count += o.count;
    s.min = Math.min(s.min, o.min);
    s.max = Math.max(s.max, o.max);
    s.exceeds += o.exceeds;
  }
}

class ELDHistogram extends Histogram {
  #timer = null;
  #resolution;
  constructor(token, resolution) {
    if (token !== kInternal) throw illegalConstructor();
    super(kInternal);
    this.#resolution = resolution;
  }
  enable() {
    if (this.#timer) return false;
    let expected = __native.hrtime() + this.#resolution * 1e6;
    this.#timer = setInterval(() => {
      const t = __native.hrtime();
      this[kRecord](Math.max(1, t - expected + this.#resolution * 1e6));
      expected = t + this.#resolution * 1e6;
    }, this.#resolution);
    if (this.#timer && typeof this.#timer.unref === "function") this.#timer.unref();
    return true;
  }
  disable() {
    if (!this.#timer) return false;
    clearInterval(this.#timer);
    this.#timer = null;
    return true;
  }
  [Symbol.dispose]() { this.disable(); }
}

function createHistogram(options = {}) {
  validateObject(options, "options");
  const { lowest = 1, highest = Number.MAX_SAFE_INTEGER, figures = 3 } = options;
  if (typeof lowest !== "bigint") validateNumber(lowest, "options.lowest");
  if (typeof highest !== "bigint") validateNumber(highest, "options.highest");
  validateNumber(figures, "options.figures");
  if (figures < 1 || figures > 5) throw outOfRange("options.figures", ">= 1 && <= 5", figures);
  return new RecordableHistogram(kInternal);
}

function monitorEventLoopDelay(options = {}) {
  validateObject(options, "options");
  const { resolution = 10 } = options;
  validateNumber(resolution, "options.resolution");
  if (resolution <= 0 || !Number.isSafeInteger(resolution)) throw outOfRange("options.resolution", "> 0 && <= 9007199254740991", resolution);
  return new ELDHistogram(kInternal, resolution);
}

const constants = {
  NODE_PERFORMANCE_GC_MAJOR: 4, NODE_PERFORMANCE_GC_MINOR: 1, NODE_PERFORMANCE_GC_INCREMENTAL: 8,
  NODE_PERFORMANCE_GC_WEAKCB: 16, NODE_PERFORMANCE_GC_FLAGS_NO: 0, NODE_PERFORMANCE_GC_FLAGS_CONSTRUCT_RETAINED: 2,
  NODE_PERFORMANCE_GC_FLAGS_FORCED: 4, NODE_PERFORMANCE_GC_FLAGS_SYNCHRONOUS_PHANTOM_PROCESSING: 8,
  NODE_PERFORMANCE_GC_FLAGS_ALL_AVAILABLE_GARBAGE: 16, NODE_PERFORMANCE_GC_FLAGS_ALL_EXTERNAL_MEMORY: 32,
  NODE_PERFORMANCE_GC_FLAGS_SCHEDULE_IDLE: 64,
  NODE_PERFORMANCE_ENTRY_TYPE_GC: 0, NODE_PERFORMANCE_ENTRY_TYPE_HTTP: 1, NODE_PERFORMANCE_ENTRY_TYPE_HTTP2: 2,
  NODE_PERFORMANCE_ENTRY_TYPE_NET: 3, NODE_PERFORMANCE_ENTRY_TYPE_DNS: 4, NODE_PERFORMANCE_ENTRY_TYPE_QUIC: 5,
  NODE_PERFORMANCE_MILESTONE_TIME_ORIGIN_TIMESTAMP: 0, NODE_PERFORMANCE_MILESTONE_TIME_ORIGIN: 1,
  NODE_PERFORMANCE_MILESTONE_ENVIRONMENT: 2, NODE_PERFORMANCE_MILESTONE_NODE_START: 3,
  NODE_PERFORMANCE_MILESTONE_V8_START: 4, NODE_PERFORMANCE_MILESTONE_LOOP_START: 5,
  NODE_PERFORMANCE_MILESTONE_LOOP_EXIT: 6, NODE_PERFORMANCE_MILESTONE_BOOTSTRAP_COMPLETE: 7,
};

return {
  Performance, PerformanceEntry, PerformanceMark, PerformanceMeasure, PerformanceObserver,
  PerformanceObserverEntryList, PerformanceResourceTiming, monitorEventLoopDelay, eventLoopUtilization,
  timerify, createHistogram, performance, constants,
};
}

if (!globalThis.__noderatiPerfHooksApi) {
  Object.defineProperty(globalThis, "__noderatiPerfHooksApi", { value: createApi(), configurable: true });
}
const api = globalThis.__noderatiPerfHooksApi;
const {
  Performance, PerformanceEntry, PerformanceMark, PerformanceMeasure, PerformanceObserver,
  PerformanceObserverEntryList, PerformanceResourceTiming, monitorEventLoopDelay, eventLoopUtilization,
  timerify, createHistogram, performance, constants,
} = api;

export {
  Performance, PerformanceEntry, PerformanceMark, PerformanceMeasure, PerformanceObserver,
  PerformanceObserverEntryList, PerformanceResourceTiming, monitorEventLoopDelay, eventLoopUtilization,
  timerify, createHistogram, performance, constants,
};
export default api;
`
