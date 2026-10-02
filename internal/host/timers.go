package host

// timers.go implements node:timers and node:timers/promises. node:timers
// re-exports the globals (timeout_object.go's setTimeout/setInterval,
// immediate_object.go's setImmediate); node:timers/promises follows
// Node's lib/timers/promises.js on top of them.
const timersShim = `import promises from "node:timers/promises";

const setTimeout = globalThis.setTimeout;
const clearTimeout = globalThis.clearTimeout;
const setInterval = globalThis.setInterval;
const clearInterval = globalThis.clearInterval;
const setImmediate = globalThis.setImmediate;
const clearImmediate = globalThis.clearImmediate;

export { setTimeout, clearTimeout, setInterval, clearInterval, setImmediate, clearImmediate, promises };
export default { setTimeout, clearTimeout, setInterval, clearInterval, setImmediate, clearImmediate, promises };
`

const timersPromisesShim = `const timeout = globalThis.setTimeout;
const clear = globalThis.clearTimeout;
const interval = globalThis.setInterval;
const immediate = globalThis.setImmediate;
const clearImm = globalThis.clearImmediate;

function received(v) {
  if (v == null) return " Received " + v;
  if (typeof v === "function") return " Received function " + (v.name || "<anonymous>");
  if (typeof v === "object") return " Received an instance of " + ((v.constructor && v.constructor.name) || "Object");
  return " Received type " + typeof v + " (" + (typeof v === "string" ? "'" + v + "'" : String(v)) + ")";
}
function invalidArgType(what, expected, v) {
  const err = new TypeError("The " + what + " must be of type " + expected + "." + received(v));
  err.code = "ERR_INVALID_ARG_TYPE";
  return err;
}
function abortError(signal) {
  const err = new Error("The operation was aborted", { cause: signal && signal.reason });
  err.name = "AbortError";
  err.code = "ABORT_ERR";
  return err;
}
function validateOptions(options) {
  if (options === null || typeof options !== "object") throw invalidArgType('"options" argument', "object", options);
  const { signal, ref = true } = options;
  if (signal !== undefined && (signal === null || typeof signal !== "object" || !("aborted" in signal))) {
    throw invalidArgType('"options.signal" property', "AbortSignal", signal);
  }
  if (typeof ref !== "boolean") throw invalidArgType('"options.ref" property', "boolean", ref);
  return { signal, ref };
}

function schedule(start, cancel, value, options) {
  let signal, ref;
  try {
    ({ signal, ref } = validateOptions(options));
  } catch (err) {
    return Promise.reject(err);
  }
  if (signal && signal.aborted) return Promise.reject(abortError(signal));
  return new Promise((resolve, reject) => {
    let onAbort;
    const handle = start(() => {
      if (signal) signal.removeEventListener("abort", onAbort);
      resolve(value);
    });
    if (!ref && handle && typeof handle.unref === "function") handle.unref();
    if (signal) {
      onAbort = () => {
        cancel(handle);
        reject(abortError(signal));
      };
      signal.addEventListener("abort", onAbort, { once: true });
    }
  });
}

export function setTimeout(after, value, options = {}) {
  return schedule((fn) => timeout(fn, after), clear, value, options);
}

export function setImmediate(value, options = {}) {
  return schedule((fn) => immediate(fn), clearImm, value, options);
}

export async function* setInterval(after, value, options = {}) {
  const { signal, ref } = validateOptions(options);
  if (signal && signal.aborted) throw abortError(signal);
  let notYielded = 0;
  let wake;
  const handle = interval(() => {
    notYielded++;
    if (wake) {
      wake();
      wake = undefined;
    }
  }, after);
  if (!ref) handle.unref();
  let aborted = false;
  const onAbort = () => {
    aborted = true;
    if (wake) {
      wake();
      wake = undefined;
    }
  };
  if (signal) signal.addEventListener("abort", onAbort, { once: true });
  try {
    while (!aborted) {
      if (notYielded === 0) await new Promise((resolve) => (wake = resolve));
      if (aborted) break;
      for (; notYielded > 0; notYielded--) yield value;
    }
    throw abortError(signal);
  } finally {
    clear(handle);
    if (signal) signal.removeEventListener("abort", onAbort);
  }
}

class Scheduler {
  yield() {
    return setImmediate();
  }
  wait(delay, options) {
    return setTimeout(delay, undefined, options);
  }
}
export const scheduler = new Scheduler();

export default { setTimeout, setImmediate, setInterval, scheduler };
`

func declareTimers() {
	registerJSShim("timers", timersShim)
	registerJSShim("timers/promises", timersPromisesShim)
}
