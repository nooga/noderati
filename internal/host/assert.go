package host

// node:assert and node:assert/strict, following Node's lib/assert.js and
// lib/internal/assert/assertion_error.js: the same comparisons
// (comparisons_shim.go), AssertionError shape, and message formats -
// including the "+ actual - expected" line diff - with values rendered by
// assert-inspect's subset of util.inspect.
//
// This replaced a Go module whose comparisons went through ToString(), so
// assert.strictEqual(1, "1") passed and most of the API (deepEqual,
// throws, rejects, match, AssertionError...) didn't exist.
const assertShim = `import { isDeepEqual, isDeepStrictEqual } from "noderati-internal:comparisons";
import { inspectValue } from "noderati-internal:assert-inspect";
import { types } from "node:util";
import fs from "node:fs";

const kReadableOperator = {
  deepStrictEqual: "Expected values to be strictly deep-equal:",
  strictEqual: "Expected values to be strictly equal:",
  strictEqualObject: 'Expected "actual" to be reference-equal to "expected":',
  deepEqual: "Expected values to be loosely deep-equal:",
  notDeepStrictEqual: 'Expected "actual" not to be strictly deep-equal to:',
  notStrictEqual: 'Expected "actual" to be strictly unequal to:',
  notStrictEqualObject: 'Expected "actual" not to be reference-equal to "expected":',
  notDeepEqual: 'Expected "actual" not to be loosely deep-equal to:',
  notIdentical: "Values have same structure but are not reference-equal:",
  notDeepEqualUnequal: "Expected values not to be loosely deep-equal:",
};
const kMaxShortStringLength = 12;
const kNopLinesToCollapse = 5;

// Line diff (longest common subsequence), with Node's comma tolerance:
// "x" and "x," are the same line when comparing objects.
function lineDiff(actual, expected, checkCommaDisparity) {
  const same = (a, b) => a === b || (checkCommaDisparity && (a + "," === b || a === b + ","));
  const n = actual.length;
  const m = expected.length;
  const lcs = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = same(actual[i], expected[j]) ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }
  const ops = [];
  let i = 0;
  let j = 0;
  let ins = [];
  let del = [];
  const flush = () => {
    for (const v of ins) ops.push(["insert", v]);
    for (const v of del) ops.push(["delete", v]);
    ins = [];
    del = [];
  };
  while (i < n || j < m) {
    if (i < n && j < m && same(actual[i], expected[j])) {
      flush();
      ops.push(["nop", expected[j]]);
      i++;
      j++;
    } else if (j >= m || (i < n && lcs[i + 1][j] >= lcs[i][j + 1])) {
      ins.push(actual[i++]);
    } else {
      del.push(expected[j++]);
    }
  }
  flush();
  return ops;
}

function printDiff(ops) {
  let message = "";
  let skipped = false;
  let nopCount = 0;
  for (let idx = 0; idx < ops.length; idx++) {
    const [operation, value] = ops[idx];
    const previous = idx > 0 ? ops[idx - 1][0] : null;
    if (previous === "nop" && operation !== previous) {
      if (nopCount === kNopLinesToCollapse + 1) {
        message += "  " + ops[idx - 1][1] + "\n";
      } else if (nopCount === kNopLinesToCollapse + 2) {
        message += "  " + ops[idx - 2][1] + "\n";
        message += "  " + ops[idx - 1][1] + "\n";
      } else if (nopCount >= kNopLinesToCollapse + 3) {
        message += "...\n";
        message += "  " + ops[idx - 1][1] + "\n";
        skipped = true;
      }
      nopCount = 0;
    }
    if (operation === "insert") message += "+ " + value + "\n";
    else if (operation === "delete") message += "- " + value + "\n";
    else if (nopCount < kNopLinesToCollapse) {
      message += "  " + value + "\n";
      nopCount++;
    } else {
      nopCount++;
    }
  }
  return { message: "\n" + message.trimEnd(), skipped };
}

function getStackedDiff(actual, expected) {
  let message = "\n+ " + actual + "\n- " + expected;
  const isStringComparison = actual[0] === "'" && expected[0] === "'";
  if (isStringComparison && actual.length + expected.length <= 80) {
    let indicatorIdx = -1;
    for (let i = 0; i < actual.length; i++) {
      if (actual[i] !== expected[i]) {
        if (i >= 3) indicatorIdx = i;
        break;
      }
    }
    if (indicatorIdx !== -1) message += "\n" + " ".repeat(indicatorIdx + 2) + "^";
  }
  return { message };
}

function getSimpleDiff(originalActual, actual, originalExpected, expected) {
  let stringsLen = actual.length + expected.length;
  if (typeof originalActual === "string") stringsLen -= 2;
  if (typeof originalExpected === "string") stringsLen -= 2;
  if (stringsLen <= kMaxShortStringLength && (originalActual !== 0 || originalExpected !== 0)) {
    return { message: actual + " !== " + expected, header: "" };
  }
  return getStackedDiff(actual, expected);
}

function createErrDiff(actual, expected, operator, customMessage) {
  if (operator === "strictEqual" && ((typeof actual === "object" && actual !== null && typeof expected === "object" && expected !== null) ||
    (typeof actual === "function" && typeof expected === "function"))) {
    operator = "strictEqualObject";
  }
  const inspectedActual = inspectValue(actual);
  const inspectedExpected = inspectValue(expected);
  if (inspectedActual === inspectedExpected && (operator === "strictEqualObject" || operator === "deepStrictEqual")) {
    return (customMessage || kReadableOperator.notIdentical) + "\n\n" + inspectedActual + "\n";
  }
  const splitActual = inspectedActual.split("\n");
  const splitExpected = inspectedExpected.split("\n");
  let header = "+ actual - expected";
  let message;
  let skipped = false;
  const simple = splitActual.length === 1 && splitExpected.length === 1 &&
    (typeof actual !== "object" || actual === null || typeof expected !== "object" || expected === null);
  if (simple) {
    const d = getSimpleDiff(actual, inspectedActual, expected, inspectedExpected);
    message = d.message;
    if (d.header !== undefined) header = d.header;
  } else {
    const d = printDiff(lineDiff(splitActual, splitExpected, actual != null && typeof actual === "object"));
    message = d.message;
    skipped = d.skipped;
  }
  const headerMessage = (customMessage || kReadableOperator[operator]) + "\n" + header;
  return headerMessage + (skipped ? "\n... Skipped lines" : "") + "\n" + message + "\n";
}

class AssertionError extends Error {
  constructor(options) {
    if (options === null || typeof options !== "object") {
      const err = new TypeError('The "options" argument must be of type object.');
      err.code = "ERR_INVALID_ARG_TYPE";
      throw err;
    }
    const { message, operator, stackStartFn, diff = "simple" } = options;
    const { actual, expected } = options;
    let msg;
    if (message != null) {
      if (operator === "deepStrictEqual" || operator === "strictEqual") msg = createErrDiff(actual, expected, operator, String(message));
      else msg = String(message);
    } else if (operator === "deepStrictEqual" || operator === "strictEqual") {
      msg = createErrDiff(actual, expected, operator);
    } else if (operator === "notDeepStrictEqual" || operator === "notStrictEqual") {
      let base = kReadableOperator[operator];
      const res = inspectValue(actual).split("\n");
      if (operator === "notStrictEqual" && ((typeof actual === "object" && actual !== null) || typeof actual === "function")) {
        base = kReadableOperator.notStrictEqualObject;
      }
      if (res.length > 50) {
        res[46] = "...";
        while (res.length > 47) res.pop();
      }
      if (res.length === 1) msg = base + (res[0].length > 5 ? "\n\n" : " ") + res[0];
      else msg = base + "\n\n" + res.join("\n") + "\n";
    } else {
      let res = inspectValue(actual);
      let other = inspectValue(expected);
      const knownOperator = kReadableOperator[operator];
      if (operator === "notDeepEqual" && res === other) {
        res = knownOperator + "\n\n" + res;
        if (res.length > 1024) res = res.slice(0, 1021) + "...";
        msg = res;
      } else {
        if (res.length > 512) res = res.slice(0, 509) + "...";
        if (other.length > 512) other = other.slice(0, 509) + "...";
        if (operator === "deepEqual") {
          res = knownOperator + "\n\n" + res + "\n\nshould loosely deep-equal\n\n";
        } else {
          const newOp = kReadableOperator[operator + "Unequal"];
          if (newOp) res = newOp + "\n\n" + res + "\n\nshould not loosely deep-equal\n\n";
          else other = " " + operator + " " + other;
        }
        msg = res + other;
      }
    }
    super(msg);
    this.generatedMessage = !message;
    Object.defineProperty(this, "name", { value: "AssertionError [ERR_ASSERTION]", enumerable: false, writable: true, configurable: true });
    this.code = "ERR_ASSERTION";
    this.actual = actual;
    this.expected = expected;
    this.operator = operator;
    this.diff = diff;
    if (typeof Error.captureStackTrace === "function") Error.captureStackTrace(this, stackStartFn || AssertionError);
    if (typeof this.stack === "string" && this.stack.startsWith("Error")) {
      this.stack = "AssertionError [ERR_ASSERTION]" + this.stack.slice("Error".length);
    }
    this.name = "AssertionError";
  }
  toString() {
    return this.name + " [" + this.code + "]: " + this.message;
  }
}

function innerFail(obj) {
  if (obj.message instanceof Error) throw obj.message;
  throw new AssertionError(obj);
}

function missingArgs(...names) {
  const list = names.map((n) => '"' + n + '"');
  const err = new TypeError("The " + list.join(" and ") + " arguments must be specified");
  err.code = "ERR_MISSING_ARGS";
  return err;
}

function invalidArgType(name, expected, actual) {
  let received;
  if (actual == null) received = " Received " + actual;
  else if (typeof actual === "function") received = " Received function " + (actual.name || "<anonymous>");
  else if (typeof actual === "object") received = " Received an instance of " + ((actual.constructor && actual.constructor.name) || "Object");
  else received = " Received type " + typeof actual + " (" + inspectValue(actual) + ")";
  const err = new TypeError('The "' + name + '" argument must be ' + expected + "." + received);
  err.code = "ERR_INVALID_ARG_TYPE";
  return err;
}

// The source text of the failing call, for assert.ok's generated message,
// as Node finds it: the caller's line from its stack frame.
function callSiteSource() {
  const stack = new Error().stack || "";
  const frames = stack.split("\n").slice(1);
  for (const frame of frames) {
    const m = /\(?([^()\s]+):(\d+):(\d+)\)?\s*$/.exec(frame);
    if (!m || m[1].startsWith("noderati-shim:")) continue;
    let file = m[1];
    if (file === "<eval>") file = process.argv[1];
    if (file && file.startsWith("file://")) file = decodeURIComponent(new URL(file).pathname);
    let text;
    try {
      text = fs.readFileSync(file, "utf8");
    } catch {
      return undefined;
    }
    const line = text.split("\n")[Number(m[2]) - 1];
    if (line === undefined) return undefined;
    const col = Number(m[3]) - 1;
    const re = /[A-Za-z_$][\w$]*(?:\s*\.\s*[A-Za-z_$][\w$]*)*\s*\(/g;
    let best;
    for (let hit; (hit = re.exec(line)) !== null; ) {
      if (!/(^|\.)\s*(assert|ok)\s*\($/.test(hit[0].replace(/\s+/g, " ").trim()) && !/\bassert\b/.test(hit[0])) continue;
      if (best === undefined || Math.abs(hit.index - col) < Math.abs(best - col)) best = hit.index;
    }
    if (best === undefined) return undefined;
    let depth = 0;
    let end = line.length;
    let quote = null;
    for (let i = line.indexOf("(", best); i < line.length; i++) {
      const ch = line[i];
      if (quote) {
        if (ch === "\\") i++;
        else if (ch === quote) quote = null;
      } else if (ch === "'" || ch === '"' || ch === "\u0060") {
        quote = ch;
      } else if (ch === "(") {
        depth++;
      } else if (ch === ")" && --depth === 0) {
        end = i + 1;
        break;
      }
    }
    return line.slice(best, end) + (end === line.length ? "\n" : "");
  }
  return undefined;
}

function innerOk(fn, argLen, value, message) {
  if (!value) {
    let generatedMessage = false;
    if (argLen === 0) {
      generatedMessage = true;
      message = "No value argument passed to \u0060assert.ok()\u0060";
    } else if (message == null) {
      generatedMessage = true;
      const src = callSiteSource();
      message = src === undefined ? "The expression evaluated to a falsy value" :
        "The expression evaluated to a falsy value:\n\n  " + (src.endsWith("\n") ? src : src + "\n");
    } else if (message instanceof Error) {
      throw message;
    }
    const err = new AssertionError({ actual: value, expected: true, message, operator: "==", stackStartFn: fn });
    err.generatedMessage = generatedMessage;
    throw err;
  }
}

function assert(...args) {
  innerOk(assert, args.length, ...args);
}

function ok(...args) {
  innerOk(ok, args.length, ...args);
}

function fail(actual, expected, message, operator, stackStartFn) {
  const argsLen = arguments.length;
  let internalMessage = false;
  if (actual == null && argsLen <= 1) {
    internalMessage = true;
    message = "Failed";
  } else if (argsLen === 1) {
    message = actual;
    actual = undefined;
  } else {
    if (argsLen === 2) operator = "!=";
  }
  if (message instanceof Error) throw message;
  const errArgs = { actual, expected, operator: operator === undefined ? "fail" : operator, stackStartFn: stackStartFn || fail, message };
  const err = new AssertionError(errArgs);
  if (internalMessage) err.generatedMessage = true;
  throw err;
}

function equal(actual, expected, message) {
  if (arguments.length < 2) throw missingArgs("actual", "expected");
  if (actual != expected && (!Number.isNaN(actual) || !Number.isNaN(expected))) {
    innerFail({ actual, expected, message, operator: "==", stackStartFn: equal });
  }
}

function notEqual(actual, expected, message) {
  if (arguments.length < 2) throw missingArgs("actual", "expected");
  if (actual == expected || (Number.isNaN(actual) && Number.isNaN(expected))) {
    innerFail({ actual, expected, message, operator: "!=", stackStartFn: notEqual });
  }
}

function deepEqual(actual, expected, message) {
  if (arguments.length < 2) throw missingArgs("actual", "expected");
  if (!isDeepEqual(actual, expected)) innerFail({ actual, expected, message, operator: "deepEqual", stackStartFn: deepEqual });
}

function notDeepEqual(actual, expected, message) {
  if (arguments.length < 2) throw missingArgs("actual", "expected");
  if (isDeepEqual(actual, expected)) innerFail({ actual, expected, message, operator: "notDeepEqual", stackStartFn: notDeepEqual });
}

function deepStrictEqual(actual, expected, message) {
  if (arguments.length < 2) throw missingArgs("actual", "expected");
  if (!isDeepStrictEqual(actual, expected)) {
    innerFail({ actual, expected, message, operator: "deepStrictEqual", stackStartFn: deepStrictEqual });
  }
}

function notDeepStrictEqual(actual, expected, message) {
  if (arguments.length < 2) throw missingArgs("actual", "expected");
  if (isDeepStrictEqual(actual, expected)) {
    innerFail({ actual, expected, message, operator: "notDeepStrictEqual", stackStartFn: notDeepStrictEqual });
  }
}

function strictEqual(actual, expected, message) {
  if (arguments.length < 2) throw missingArgs("actual", "expected");
  if (!Object.is(actual, expected)) innerFail({ actual, expected, message, operator: "strictEqual", stackStartFn: strictEqual });
}

function notStrictEqual(actual, expected, message) {
  if (arguments.length < 2) throw missingArgs("actual", "expected");
  if (Object.is(actual, expected)) innerFail({ actual, expected, message, operator: "notStrictEqual", stackStartFn: notStrictEqual });
}

class Comparison {
  constructor(obj, keys, actual) {
    for (const key of keys) {
      if (key in obj) {
        if (actual !== undefined && typeof actual[key] === "string" && types.isRegExp(obj[key]) && obj[key].exec(actual[key]) !== null) {
          this[key] = actual[key];
        } else {
          this[key] = obj[key];
        }
      }
    }
  }
}

function compareExceptionKey(actual, expected, key, message, keys, fn) {
  if (!(key in actual) || !isDeepStrictEqual(actual[key], expected[key])) {
    if (!message) {
      const a = new Comparison(actual, keys);
      const b = new Comparison(expected, keys, actual);
      const err = new AssertionError({ actual: a, expected: b, operator: "deepStrictEqual", stackStartFn: fn });
      err.actual = actual;
      err.expected = expected;
      err.operator = fn.name;
      throw err;
    }
    innerFail({ actual, expected, message, operator: fn.name, stackStartFn: fn });
  }
}

// Error.isPrototypeOf(fn), walked by hand: isPrototypeOf with a function
// receiver is broken in paserati (#565).
function isErrorClass(fn) {
  for (let p = Object.getPrototypeOf(fn); p !== null; p = Object.getPrototypeOf(p)) if (p === Error) return true;
  return false;
}

const isError = (e) => types.isNativeError(e) || e instanceof Error;

function expectedException(actual, expected, message, fn) {
  let generatedMessage = false;
  let throwError = false;
  if (typeof expected !== "function") {
    if (types.isRegExp(expected)) {
      const str = String(actual);
      if (expected.exec(str) !== null) return;
      if (!message) {
        generatedMessage = true;
        message = "The input did not match the regular expression " + inspectValue(expected) + ". Input:\n\n" + inspectValue(str) + "\n";
      }
      throwError = true;
    } else if (typeof actual !== "object" || actual === null) {
      const err = new AssertionError({ actual, expected, message, operator: "deepStrictEqual", stackStartFn: fn });
      err.operator = fn.name;
      throw err;
    } else {
      const keys = Object.keys(expected);
      if (expected instanceof Error) {
        keys.push("name", "message");
      } else if (keys.length === 0) {
        const err = new TypeError("The argument 'error' may not be an empty object. Received {}");
        err.code = "ERR_INVALID_ARG_VALUE";
        throw err;
      }
      for (const key of keys) {
        if (typeof actual[key] === "string" && types.isRegExp(expected[key]) && expected[key].exec(actual[key]) !== null) continue;
        compareExceptionKey(actual, expected, key, message, keys, fn);
      }
      return;
    }
  } else if (expected.prototype !== undefined && actual instanceof expected) {
    return;
  } else if (isErrorClass(expected)) {
    if (!message) {
      generatedMessage = true;
      message = 'The error is expected to be an instance of "' + expected.name + '". Received ';
      if (isError(actual)) {
        const name = (actual.constructor && actual.constructor.name) || actual.name;
        if (expected.name === name) message += "an error with identical name but a different prototype.";
        else message += '"' + name + '"';
        if (actual.message) message += "\n\nError message:\n\n" + actual.message;
      } else {
        message += '"' + inspectValue(actual) + '"';
      }
    }
    throwError = true;
  } else {
    const res = Reflect.apply(expected, {}, [actual]);
    if (res !== true) {
      if (!message) {
        generatedMessage = true;
        const name = expected.name ? '"' + expected.name + '" ' : "";
        message = "The " + name + 'validation function is expected to return "true". Received ' + inspectValue(res);
        if (isError(actual)) message += "\n\nCaught error:\n\n" + actual;
      }
      throwError = true;
    }
  }
  if (throwError) {
    const err = new AssertionError({ actual, expected, message, operator: fn.name, stackStartFn: fn });
    err.generatedMessage = generatedMessage;
    throw err;
  }
}

const NO_EXCEPTION_SENTINEL = {};

function getActual(fn) {
  if (typeof fn !== "function") throw invalidArgType("fn", "of type function", fn);
  try {
    fn();
  } catch (e) {
    return e;
  }
  return NO_EXCEPTION_SENTINEL;
}

function checkIsPromise(obj) {
  return types.isPromise(obj) || (obj !== null && typeof obj === "object" && typeof obj.then === "function" && typeof obj.catch === "function");
}

async function waitForActual(promiseFn) {
  let resultPromise;
  if (typeof promiseFn === "function") {
    resultPromise = promiseFn();
    if (!checkIsPromise(resultPromise)) {
      const err = new TypeError('Expected instance of Promise to be returned from the "promiseFn" function but got ' +
        (resultPromise === undefined ? "undefined" : "type " + typeof resultPromise) + ".");
      err.code = "ERR_INVALID_RETURN_VALUE";
      throw err;
    }
  } else if (checkIsPromise(promiseFn)) {
    resultPromise = promiseFn;
  } else {
    throw invalidArgType("promiseFn", "of type function or an instance of Promise", promiseFn);
  }
  try {
    await resultPromise;
  } catch (e) {
    return e;
  }
  return NO_EXCEPTION_SENTINEL;
}

function expectsError(stackStartFn, actual, error, message) {
  if (typeof error === "string") {
    if (arguments.length === 4) throw invalidArgType("error", "of type function or an instance of Error, RegExp, or Object", error);
    if (typeof actual === "object" && actual !== null) {
      if (actual.message === error) {
        const err = new TypeError('The "error/message" argument is ambiguous. The error message "' + actual.message + '" is identical to the message.');
        err.code = "ERR_AMBIGUOUS_ARGUMENT";
        throw err;
      }
    } else if (actual === error) {
      const err = new TypeError('The "error/message" argument is ambiguous. The error "' + actual + '" is identical to the message.');
      err.code = "ERR_AMBIGUOUS_ARGUMENT";
      throw err;
    }
    message = error;
    error = undefined;
  } else if (error != null && typeof error !== "object" && typeof error !== "function") {
    throw invalidArgType("error", "of type function or an instance of Error, RegExp, or Object", error);
  }
  if (actual === NO_EXCEPTION_SENTINEL) {
    let details = "";
    if (error && error.name) details += " (" + error.name + ")";
    details += message ? ": " + message : ".";
    const fnType = stackStartFn === rejects ? "rejection" : "exception";
    innerFail({ actual: undefined, expected: error, operator: stackStartFn.name, message: "Missing expected " + fnType + details, stackStartFn });
  }
  if (!error) return;
  expectedException(actual, error, message, stackStartFn);
}

function hasMatchingError(actual, expected) {
  if (typeof expected !== "function") {
    if (types.isRegExp(expected)) return expected.exec(String(actual)) !== null;
    throw invalidArgType("expected", "of type function or an instance of RegExp", expected);
  }
  if (expected.prototype !== undefined && actual instanceof expected) return true;
  if (isErrorClass(expected)) return false;
  return Reflect.apply(expected, {}, [actual]) === true;
}

function expectsNoError(stackStartFn, actual, error, message) {
  if (actual === NO_EXCEPTION_SENTINEL) return;
  if (typeof error === "string") {
    message = error;
    error = undefined;
  }
  if (!error || hasMatchingError(actual, error)) {
    const details = message ? ": " + message : ".";
    const fnType = stackStartFn === doesNotReject ? "rejection" : "exception";
    innerFail({ actual, expected: error, operator: stackStartFn.name,
      message: "Got unwanted " + fnType + details + "\nActual message: \"" + (actual && actual.message) + '"', stackStartFn });
  }
  throw actual;
}

function throws(promiseFn, ...args) {
  expectsError(throws, getActual(promiseFn), ...args);
}

async function rejects(promiseFn, ...args) {
  expectsError(rejects, await waitForActual(promiseFn), ...args);
}

function doesNotThrow(fn, ...args) {
  expectsNoError(doesNotThrow, getActual(fn), ...args);
}

async function doesNotReject(fn, ...args) {
  expectsNoError(doesNotReject, await waitForActual(fn), ...args);
}

function ifError(err) {
  if (err !== null && err !== undefined) {
    let message = "ifError got unwanted exception: ";
    if (typeof err === "object" && typeof err.message === "string") {
      if (err.message.length === 0 && err.constructor) message += err.constructor.name;
      else message += err.message;
    } else {
      message += inspectValue(err);
    }
    throw new AssertionError({ actual: err, expected: null, operator: "ifError", message, stackStartFn: ifError });
  }
}

function internalMatch(string, regexp, message, fn) {
  if (!types.isRegExp(regexp)) throw invalidArgType("regexp", "an instance of RegExp", regexp);
  const isMatch = fn === match;
  if (typeof string !== "string" || (regexp.exec(string) !== null) !== isMatch) {
    if (message instanceof Error) throw message;
    const generatedMessage = !message;
    message = message || (typeof string !== "string"
      ? 'The "string" argument must be of type string. Received type ' + typeof string + " (" + inspectValue(string) + ")"
      : (isMatch ? "The input did not match the regular expression " : "The input was expected to not match the regular expression ") +
        inspectValue(regexp) + ". Input:\n\n" + inspectValue(string) + "\n");
    const err = new AssertionError({ actual: string, expected: regexp, message, operator: fn.name, stackStartFn: fn });
    err.generatedMessage = generatedMessage;
    throw err;
  }
}

function match(string, regexp, message) {
  internalMatch(string, regexp, message, match);
}

function doesNotMatch(string, regexp, message) {
  internalMatch(string, regexp, message, doesNotMatch);
}

function strict(...args) {
  innerOk(strict, args.length, ...args);
}

Object.assign(assert, {
  ok, fail, equal, notEqual, deepEqual, notDeepEqual, deepStrictEqual, notDeepStrictEqual, strictEqual,
  notStrictEqual, throws, rejects, doesNotThrow, doesNotReject, ifError, match, doesNotMatch, AssertionError,
});
Object.assign(strict, assert, {
  equal: strictEqual, deepEqual: deepStrictEqual, notEqual: notStrictEqual, notDeepEqual: notDeepStrictEqual,
});
assert.strict = strict;
strict.strict = strict;

export { AssertionError, ok, fail, equal, notEqual, deepEqual, notDeepEqual, deepStrictEqual, notDeepStrictEqual,
  strictEqual, notStrictEqual, throws, rejects, doesNotThrow, doesNotReject, ifError, match, doesNotMatch, strict };
export default assert;
`

const assertStrictShim = `import assert from "node:assert";
const strict = assert.strict;
export default strict;
`

func declareAssert() {
	registerJSShim("assert", assertShim)
	registerJSShim("assert/strict", assertStrictShim)
}
