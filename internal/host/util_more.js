// The rest of node:util, loaded on first use: MIMEType/MIMEParams (after
// Node's lib/internal/mime.js), diff (lib/internal/util/diff.js and
// lib/internal/assert/myers_diff.js), parseEnv (src/node_dotenv.cc),
// getCallSites, the transferable AbortController helpers, setTraceSigInt
// and convertProcessSignalToExitCode. Node.js sources are MIT licensed.
import { inspect } from "noderati-internal:inspect";
import os from "node:os";

function codeError(Ctor, code, message) {
  const err = new Ctor(message);
  err.code = code;
  return err;
}

function received(v) {
  if (v == null) return ` Received ${v}`;
  if (typeof v === "function") return ` Received function ${v.name || "<anonymous>"}`;
  if (typeof v === "object") {
    if (v.constructor?.name) return ` Received an instance of ${v.constructor.name}`;
    return ` Received ${inspect(v, { depth: -1 })}`;
  }
  let inspected = inspect(v, { colors: false });
  if (inspected.length > 28) inspected = `${inspected.slice(0, 25)}...`;
  return ` Received type ${typeof v} (${inspected})`;
}

function invalidArgType(name, expected, v) {
  const kind = name.includes(".") ? "property" : "argument";
  return codeError(TypeError, "ERR_INVALID_ARG_TYPE", `The "${name}" ${kind} must be ${expected}.${received(v)}`);
}

function validateString(v, name) {
  if (typeof v !== "string") throw invalidArgType(name, "of type string", v);
}

function validateObject(v, name) {
  if (v === null || typeof v !== "object" || Array.isArray(v)) throw invalidArgType(name, "of type object", v);
}

function validateBoolean(v, name) {
  if (typeof v !== "boolean") throw invalidArgType(name, "of type boolean", v);
}

// MIMEType / MIMEParams.
const NOT_HTTP_TOKEN_CODE_POINT = /[^!#$%&'*+\-.^_`|~A-Za-z0-9]/g;
const NOT_HTTP_QUOTED_STRING_CODE_POINT = /[^\t -~\u0080-ÿ]/g;
const END_BEGINNING_WHITESPACE = /[^\r\n\t ]|$/;
const START_ENDING_WHITESPACE = /[\r\n\t ]*$/;
const EQUALS_SEMICOLON_OR_END = /[;=]|$/;
const QUOTED_VALUE_PATTERN = /^(?:([\\]$)|[\\][\s\S]|[^"])*(?:(")|$)/u;

// String.prototype.search without the regexp's lastIndex/global state.
function search(str, re) {
  const plain = new RegExp(re.source, re.flags.replace("g", ""));
  const m = plain.exec(str);
  return m === null ? -1 : m.index;
}

function mimeSyntaxError(production, str, invalidIndex) {
  return codeError(
    TypeError,
    "ERR_INVALID_MIME_SYNTAX",
    `The MIME syntax for a ${production} in "${str}" is invalid` + (invalidIndex !== -1 ? ` at ${invalidIndex}` : ""),
  );
}

function toASCIILower(str) {
  if (!/[^\x00-\x7f]/.test(str)) return str.toLowerCase();
  let result = "";
  for (let i = 0; i < str.length; i++) {
    const char = str[i];
    result += char >= "A" && char <= "Z" ? char.toLowerCase() : char;
  }
  return result;
}

function parseTypeAndSubtype(str) {
  let position = search(str, END_BEGINNING_WHITESPACE);
  const typeEnd = str.indexOf("/", position);
  const trimmedType = typeEnd === -1 ? str.slice(position) : str.slice(position, typeEnd);
  const invalidTypeIndex = search(trimmedType, NOT_HTTP_TOKEN_CODE_POINT);
  if (trimmedType === "" || invalidTypeIndex !== -1 || typeEnd === -1) {
    throw mimeSyntaxError("type", str, invalidTypeIndex);
  }
  position = typeEnd + 1;
  const type = toASCIILower(trimmedType);
  const subtypeEnd = str.indexOf(";", position);
  const rawSubtype = subtypeEnd === -1 ? str.slice(position) : str.slice(position, subtypeEnd);
  position += rawSubtype.length;
  if (subtypeEnd !== -1) position += 1;
  const trimmedSubtype = rawSubtype.slice(0, search(rawSubtype, START_ENDING_WHITESPACE));
  const invalidSubtypeIndex = search(trimmedSubtype, NOT_HTTP_TOKEN_CODE_POINT);
  if (trimmedSubtype === "" || invalidSubtypeIndex !== -1) {
    throw mimeSyntaxError("subtype", str, invalidSubtypeIndex);
  }
  return [type, toASCIILower(trimmedSubtype), position];
}

function removeBackslashes(str) {
  let ret = "";
  let i;
  for (i = 0; i < str.length - 1; i++) {
    const c = str[i];
    if (c === "\\") {
      i++;
      ret += str[i];
    } else {
      ret += c;
    }
  }
  if (i === str.length - 1) ret += str[i];
  return ret;
}

function escapeQuoteOrSolidus(str) {
  let result = "";
  for (let i = 0; i < str.length; i++) {
    const char = str[i];
    result += char === '"' || char === "\\" ? `\\${char}` : char;
  }
  return result;
}

const encodeParamValue = (value) => {
  if (value.length === 0) return '""';
  if (search(value, NOT_HTTP_TOKEN_CODE_POINT) === -1) return value;
  return `"${escapeQuoteOrSolidus(value)}"`;
};

// Private state lives in WeakMaps keyed by instance.
const paramsState = new WeakMap();

function parseParams(state) {
  if (state.processed) return;
  const paramsMap = state.data;
  let position = 0;
  const str = state.string;
  const endOfSource = search(str.slice(position), START_ENDING_WHITESPACE) + position;
  while (position < endOfSource) {
    position += search(str.slice(position), END_BEGINNING_WHITESPACE);
    const afterParameterName = search(str.slice(position), EQUALS_SEMICOLON_OR_END) + position;
    const parameterString = toASCIILower(str.slice(position, afterParameterName));
    position = afterParameterName;
    if (position < endOfSource) {
      const char = str.charAt(position);
      position += 1;
      if (char === ";") continue;
    }
    if (position >= endOfSource) break;
    const char = str.charAt(position);
    let parameterValue = null;
    if (char === '"') {
      position += 1;
      const insideMatch = QUOTED_VALUE_PATTERN.exec(str.slice(position));
      position += insideMatch[0].length;
      const inside = insideMatch[1] || insideMatch[2] ? insideMatch[0].slice(0, -1) : insideMatch[0];
      parameterValue = removeBackslashes(inside);
      if (insideMatch[1]) parameterValue += "\\";
    } else {
      const valueEnd = str.indexOf(";", position);
      const rawValue = valueEnd === -1 ? str.slice(position) : str.slice(position, valueEnd);
      position += rawValue.length;
      const trimmedValue = rawValue.slice(0, search(rawValue, START_ENDING_WHITESPACE));
      if (trimmedValue === "") continue;
      parameterValue = trimmedValue;
    }
    if (
      parameterString !== "" &&
      search(parameterString, NOT_HTTP_TOKEN_CODE_POINT) === -1 &&
      search(parameterValue, NOT_HTTP_QUOTED_STRING_CODE_POINT) === -1 &&
      paramsMap.has(parameterString) === false
    ) {
      paramsMap.set(parameterString, parameterValue);
    }
    position++;
  }
  state.processed = true;
}

function paramsOf(self) {
  const state = paramsState.get(self);
  if (state === undefined) throw new TypeError("Illegal invocation");
  parseParams(state);
  return state.data;
}

export class MIMEParams {
  constructor() {
    paramsState.set(this, { data: new Map(), processed: true, string: null });
  }

  delete(name) {
    paramsOf(this).delete(name);
  }

  get(name) {
    const data = paramsOf(this);
    return data.has(name) ? data.get(name) : null;
  }

  has(name) {
    return paramsOf(this).has(name);
  }

  set(name, value) {
    const data = paramsOf(this);
    name = `${name}`;
    value = `${value}`;
    const invalidNameIndex = search(name, NOT_HTTP_TOKEN_CODE_POINT);
    if (name.length === 0 || invalidNameIndex !== -1) throw mimeSyntaxError("parameter name", name, invalidNameIndex);
    const invalidValueIndex = search(value, NOT_HTTP_QUOTED_STRING_CODE_POINT);
    if (invalidValueIndex !== -1) throw mimeSyntaxError("parameter value", value, invalidValueIndex);
    data.set(name, value);
  }

  // Node's are generators; plain Map iterators here, since the engine's
  // array spread rejects generator-backed iterables.
  entries() {
    return paramsOf(this).entries();
  }

  keys() {
    return paramsOf(this).keys();
  }

  values() {
    return paramsOf(this).values();
  }

  toString() {
    let ret = "";
    for (const entry of paramsOf(this)) {
      if (ret.length) ret += ";";
      ret += `${entry[0]}=${encodeParamValue(entry[1])}`;
    }
    return ret;
  }
}
Object.defineProperty(MIMEParams.prototype, Symbol.iterator, {
  configurable: true,
  value: MIMEParams.prototype.entries,
  writable: true,
});
Object.defineProperty(MIMEParams.prototype, "toJSON", {
  configurable: true,
  value: MIMEParams.prototype.toString,
  writable: true,
});

function instantiateMimeParams(str) {
  const instance = new MIMEParams();
  const state = paramsState.get(instance);
  state.string = str;
  state.processed = false;
  return instance;
}

const mimeState = new WeakMap();
function mimeOf(self) {
  const state = mimeState.get(self);
  if (state === undefined) throw new TypeError("Illegal invocation");
  return state;
}

export class MIMEType {
  constructor(string) {
    string = `${string}`;
    const data = parseTypeAndSubtype(string);
    mimeState.set(this, { type: data[0], subtype: data[1], parameters: instantiateMimeParams(string.slice(data[2])) });
  }

  get type() {
    return mimeOf(this).type;
  }

  set type(v) {
    v = `${v}`;
    const invalidTypeIndex = search(v, NOT_HTTP_TOKEN_CODE_POINT);
    if (v.length === 0 || invalidTypeIndex !== -1) throw mimeSyntaxError("type", v, invalidTypeIndex);
    mimeOf(this).type = toASCIILower(v);
  }

  get subtype() {
    return mimeOf(this).subtype;
  }

  set subtype(v) {
    v = `${v}`;
    const invalidSubtypeIndex = search(v, NOT_HTTP_TOKEN_CODE_POINT);
    if (v.length === 0 || invalidSubtypeIndex !== -1) throw mimeSyntaxError("subtype", v, invalidSubtypeIndex);
    mimeOf(this).subtype = toASCIILower(v);
  }

  get essence() {
    const s = mimeOf(this);
    return `${s.type}/${s.subtype}`;
  }

  get params() {
    return mimeOf(this).parameters;
  }

  toString() {
    const s = mimeOf(this);
    let ret = `${s.type}/${s.subtype}`;
    const paramStr = MIMEParams.prototype.toString.call(s.parameters);
    if (paramStr.length) ret += `;${paramStr}`;
    return ret;
  }
}
Object.defineProperty(MIMEType.prototype, "toJSON", {
  configurable: true,
  value: MIMEType.prototype.toString,
  writable: true,
});

// diff: Myers' algorithm, reversed into actual-to-expected order.
function myersDiff(actual, expected) {
  const actualLength = actual.length;
  const expectedLength = expected.length;
  const max = actualLength + expectedLength;
  if (max > 2 ** 31 - 1) {
    throw codeError(
      RangeError,
      "ERR_OUT_OF_RANGE",
      `The value of "myersDiff input size" is out of range. It must be < 2^31. Received ${max}`,
    );
  }
  const v = new Int32Array(2 * max + 1);
  const trace = [];
  for (let diffLevel = 0; diffLevel <= max; diffLevel++) {
    trace.push(new Int32Array(v));
    for (let diagonalIndex = -diffLevel; diagonalIndex <= diffLevel; diagonalIndex += 2) {
      const offset = diagonalIndex + max;
      const previousOffset = v[offset - 1];
      const nextOffset = v[offset + 1];
      let x =
        diagonalIndex === -diffLevel || (diagonalIndex !== diffLevel && previousOffset < nextOffset)
          ? nextOffset
          : previousOffset + 1;
      let y = x - diagonalIndex;
      while (x < actualLength && y < expectedLength && actual[x] === expected[y]) {
        x++;
        y++;
      }
      v[offset] = x;
      if (x >= actualLength && y >= expectedLength) return backtrack(trace, actual, expected);
    }
  }
  return [];
}

function backtrack(trace, actual, expected) {
  const max = actual.length + expected.length;
  let x = actual.length;
  let y = expected.length;
  const result = [];
  for (let diffLevel = trace.length - 1; diffLevel >= 0; diffLevel--) {
    const v = trace[diffLevel];
    const diagonalIndex = x - y;
    const offset = diagonalIndex + max;
    let prevDiagonalIndex;
    if (diagonalIndex === -diffLevel || (diagonalIndex !== diffLevel && v[offset - 1] < v[offset + 1])) {
      prevDiagonalIndex = diagonalIndex + 1;
    } else {
      prevDiagonalIndex = diagonalIndex - 1;
    }
    const prevX = v[prevDiagonalIndex + max];
    const prevY = prevX - prevDiagonalIndex;
    while (x > prevX && y > prevY) {
      result.push([0, actual[x - 1]]);
      x--;
      y--;
    }
    if (diffLevel > 0) {
      if (x > prevX) result.push([1, actual[--x]]);
      else result.push([-1, expected[--y]]);
    }
  }
  return result;
}

function validateDiffInput(value, name) {
  if (!Array.isArray(value)) {
    validateString(value, name);
    return;
  }
  for (let i = 0; i < value.length; i++) validateString(value[i], `${name}[${i}]`);
}

export function diff(actual, expected) {
  if (actual === expected) return [];
  validateDiffInput(actual, "actual");
  validateDiffInput(expected, "expected");
  return myersDiff(actual, expected).reverse();
}

// parseEnv: the .env grammar of Node's Dotenv::ParseContent.
const trimSpaces = (s) => s.replace(/^[ \t\n]+/, "").replace(/[ \t\n]+$/, "");

export function parseEnv(content) {
  validateString(content, "content");
  const store = { __proto__: null };
  let rest = trimSpaces(content.replace(/\r/g, ""));
  while (rest.length > 0) {
    if (rest[0] === "\n" || rest[0] === "#") {
      const nl = rest.indexOf("\n");
      rest = nl === -1 ? "" : rest.slice(nl + 1);
      continue;
    }
    const eqOrNl = rest.search(/[=\n]/);
    if (eqOrNl === -1 || rest[eqOrNl] === "\n") {
      const nl = rest.indexOf("\n");
      if (nl === -1) break;
      rest = trimSpaces(rest.slice(nl + 1));
      continue;
    }
    let key = trimSpaces(rest.slice(0, eqOrNl));
    rest = rest.slice(eqOrNl + 1);
    if (key === "") {
      const nl = rest.indexOf("\n");
      if (nl === -1) break;
      rest = trimSpaces(rest.slice(nl + 1));
      continue;
    }
    if (key.startsWith("export ")) key = trimSpaces(key.slice(7));
    if (rest.length === 0 || rest[0] === "\n") {
      store[key] = "";
      continue;
    }
    rest = trimSpaces(rest);
    if (rest.length === 0) {
      store[key] = "";
      break;
    }
    const q = rest[0];
    if (q === '"') {
      const close = rest.indexOf('"', 1);
      if (close !== -1) {
        store[key] = rest.slice(1, close).replaceAll("\\n", "\n");
        const nl = rest.indexOf("\n", close + 1);
        rest = nl === -1 ? "" : rest.slice(nl + 1);
        continue;
      }
    }
    if (q === "'" || q === '"' || q === "`") {
      const close = rest.indexOf(q, 1);
      if (close === -1) {
        const nl = rest.indexOf("\n");
        if (nl === -1) {
          store[key] = rest;
          break;
        }
        store[key] = rest.slice(0, nl);
        rest = rest.slice(nl);
      } else {
        store[key] = rest.slice(1, close);
        const nl = rest.indexOf("\n", close + 1);
        rest = nl === -1 ? "" : rest.slice(nl + 1);
      }
    } else {
      const nl = rest.indexOf("\n");
      let value = nl === -1 ? rest : rest.slice(0, nl);
      const hash = value.indexOf("#");
      if (hash !== -1) value = value.slice(0, hash);
      store[key] = trimSpaces(value);
      rest = nl === -1 ? "" : rest.slice(nl + 1);
    }
    rest = trimSpaces(rest);
  }
  return store;
}

// getCallSites: the caller's frames, read from the engine's stack trace.
const frameRe = /^\s*at (?:(.*?) \((.*):(\d+):(\d+)\)|(.*):(\d+):(\d+))$/;

export function getCallSites(frameCount = 10, options) {
  if (options === undefined) {
    if (typeof frameCount === "object") {
      options = frameCount;
      validateObject(options, "options");
      if (options.sourceMap !== undefined) validateBoolean(options.sourceMap, "options.sourceMap");
      frameCount = 10;
    } else {
      options = {};
    }
  } else {
    validateObject(options, "options");
    if (options.sourceMap !== undefined) validateBoolean(options.sourceMap, "options.sourceMap");
  }
  if (typeof frameCount !== "number") throw invalidArgType("frameCount", "of type number", frameCount);
  if (!Number.isInteger(frameCount)) {
    throw codeError(RangeError, "ERR_OUT_OF_RANGE", `The value of "frameCount" is out of range. It must be an integer. Received ${frameCount}`);
  }
  if (frameCount < 1 || frameCount > 200) {
    throw codeError(RangeError, "ERR_OUT_OF_RANGE", `The value of "frameCount" is out of range. It must be >= 1 && <= 200. Received ${frameCount}`);
  }
  const stack = String(new Error().stack || "");
  const sites = [];
  for (const line of stack.split("\n")) {
    const m = frameRe.exec(line);
    if (m === null) continue;
    const scriptName = m[2] ?? m[5];
    if (scriptName.startsWith("noderati-shim:") || scriptName.startsWith("noderati-internal:")) continue;
    let functionName = m[1] ?? "";
    if (functionName === "<anonymous>" || functionName === "<script>") functionName = "";
    const lineNumber = Number(m[3] ?? m[6]);
    const column = Number(m[4] ?? m[7]);
    sites.push({
      __proto__: null,
      functionName,
      scriptId: "0",
      scriptName,
      lineNumber,
      columnNumber: column,
      column,
    });
    if (sites.length === frameCount) break;
  }
  return sites;
}

// The engine's AbortSignal global isn't a callable constructor, so
// instanceof can't be used; recognise signals by prototype, then by shape.
function isAbortSignal(v) {
  const Ctor = globalThis.AbortSignal;
  if (typeof Ctor === "function") return v instanceof Ctor;
  const proto = Ctor?.prototype ?? Object.getPrototypeOf(new AbortController().signal);
  for (let p = Object.getPrototypeOf(v); p !== null; p = Object.getPrototypeOf(p)) if (p === proto) return true;
  return typeof v.aborted === "boolean" && typeof v.throwIfAborted === "function";
}

export function transferableAbortController() {
  return new AbortController();
}

export function transferableAbortSignal(signal) {
  if (typeof signal !== "object" || signal === null || !isAbortSignal(signal)) {
    throw invalidArgType("signal", "an instance of AbortSignal", signal);
  }
  return signal;
}

// setTraceSigInt: Node prints a stack trace on SIGINT while enabled. There
// is no interruptible watchdog here, so this validates like Node and
// otherwise has no effect.
export function setTraceSigInt(enable) {
  void enable;
}

export function convertProcessSignalToExitCode(signalCode) {
  const signals = os.constants.signals;
  if (typeof signalCode !== "string" || !Object.prototype.hasOwnProperty.call(signals, signalCode)) {
    const allowed = Object.keys(signals).map((s) => `'${s}'`).join(", ");
    throw codeError(
      TypeError,
      "ERR_INVALID_ARG_VALUE",
      `The argument 'signalCode' must be one of: ${allowed}. Received ${inspect(signalCode)}`,
    );
  }
  return 128 + signals[signalCode];
}
