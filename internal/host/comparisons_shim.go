package host

// comparisonsShim is Node's lib/internal/util/comparisons.js
// (isDeepEqual / isDeepStrictEqual), shared by node:assert and
// util.isDeepStrictEqual.
const comparisonsShim = `import { types } from "node:util";

const kStrict = 1;
const kLoose = 0;
const kNoIterator = 0;
const kIsArray = 1;
const kIsSet = 2;
const kIsMap = 3;

const { isDate, isRegExp, isNativeError, isBoxedPrimitive, isNumberObject, isStringObject, isBooleanObject,
  isBigIntObject, isSymbolObject, isArrayBufferView, isAnyArrayBuffer, isMap, isSet, isFloat32Array,
  isFloat64Array, isFloat16Array, isWeakMap, isWeakSet } = types;
const hasOwn = (o, k) => Object.prototype.hasOwnProperty.call(o, k);
const isEnumerable = (o, k) => Object.prototype.propertyIsEnumerable.call(o, k);
const toTag = (v) => Object.prototype.toString.call(v);

function isIndexKey(key) {
  if (typeof key !== "string") return false;
  const n = Number(key);
  return Number.isInteger(n) && n >= 0 && n < 4294967295 && String(n) === key;
}

// Own enumerable non-index keys (plus enumerable symbols unless skipSymbols).
function getOwnNonIndexProperties(obj, skipSymbols) {
  const out = [];
  for (const key of Reflect.ownKeys(obj)) {
    if (typeof key === "symbol" ? skipSymbols : isIndexKey(key)) continue;
    if (isEnumerable(obj, key)) out.push(key);
  }
  return out;
}

function areSimilarRegExps(a, b) {
  return a.source === b.source && a.flags === b.flags && a.lastIndex === b.lastIndex;
}

function areSimilarFloatArrays(a, b) {
  if (a.byteLength !== b.byteLength) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

function areEqualBytes(a, b) {
  if (a.byteLength !== b.byteLength) return false;
  const x = new Uint8Array(a.buffer, a.byteOffset, a.byteLength);
  const y = new Uint8Array(b.buffer, b.byteOffset, b.byteLength);
  for (let i = 0; i < x.length; i++) if (x[i] !== y[i]) return false;
  return true;
}

function areEqualArrayBuffers(a, b) {
  return a.byteLength === b.byteLength && areEqualBytes(new Uint8Array(a), new Uint8Array(b));
}

function isEqualBoxedPrimitive(a, b) {
  if (isNumberObject(a)) return isNumberObject(b) && Object.is(Number.prototype.valueOf.call(a), Number.prototype.valueOf.call(b));
  if (isStringObject(a)) return isStringObject(b) && String.prototype.valueOf.call(a) === String.prototype.valueOf.call(b);
  if (isBooleanObject(a)) return isBooleanObject(b) && Boolean.prototype.valueOf.call(a) === Boolean.prototype.valueOf.call(b);
  if (isBigIntObject(a)) return isBigIntObject(b) && BigInt.prototype.valueOf.call(a) === BigInt.prototype.valueOf.call(b);
  if (isSymbolObject(a)) return isSymbolObject(b) && Symbol.prototype.valueOf.call(a) === Symbol.prototype.valueOf.call(b);
  return false;
}

function innerDeepEqual(val1, val2, mode, memos) {
  if (val1 === val2) return val1 !== 0 || Object.is(val1, val2) || mode === kLoose;
  if (mode === kStrict) {
    if (typeof val1 !== "object") return typeof val1 === "number" && Number.isNaN(val1) && Number.isNaN(val2);
    if (typeof val2 !== "object" || val1 === null || val2 === null) return false;
    if (Object.getPrototypeOf(val1) !== Object.getPrototypeOf(val2)) return false;
  } else {
    if (val1 === null || typeof val1 !== "object") {
      if (val2 === null || typeof val2 !== "object") return val1 == val2 || (Number.isNaN(val1) && Number.isNaN(val2));
      return false;
    }
    if (val2 === null || typeof val2 !== "object") return false;
  }
  return objectComparisonStart(val1, val2, mode, memos);
}

function objectComparisonStart(val1, val2, mode, memos) {
  if (toTag(val1) !== toTag(val2)) return false;
  if (Array.isArray(val1)) {
    if (!Array.isArray(val2) || val1.length !== val2.length) return false;
    const keys1 = getOwnNonIndexProperties(val1, mode === kLoose);
    const keys2 = getOwnNonIndexProperties(val2, mode === kLoose);
    if (keys1.length !== keys2.length) return false;
    return keyCheck(val1, val2, mode, memos, kIsArray, keys1);
  } else if (toTag(val1) === "[object Object]" && !isBoxedPrimitive(val1) && !isNativeError(val1) && !(val1 instanceof Error)) {
    return keyCheck(val1, val2, mode, memos, kNoIterator);
  } else if (isDate(val1)) {
    if (!isDate(val2) || Date.prototype.getTime.call(val1) !== Date.prototype.getTime.call(val2)) return false;
  } else if (isRegExp(val1)) {
    if (!isRegExp(val2) || !areSimilarRegExps(val1, val2)) return false;
  } else if (isNativeError(val1) || val1 instanceof Error) {
    if ((!isNativeError(val2) && !(val2 instanceof Error)) || val1.message !== val2.message || val1.name !== val2.name) return false;
    for (const key of ["cause", "errors"]) {
      if (hasOwn(val1, key) !== hasOwn(val2, key)) return false;
      if (hasOwn(val1, key) && !isEnumerable(val1, key) && !innerDeepEqual(val1[key], val2[key], mode, memos)) return false;
    }
  } else if (isArrayBufferView(val1)) {
    if (mode === kLoose && (isFloat32Array(val1) || isFloat64Array(val1) || (isFloat16Array && isFloat16Array(val1)))) {
      if (!areSimilarFloatArrays(val1, val2)) return false;
    } else if (!areEqualBytes(val1, val2)) {
      return false;
    }
    const keys1 = getOwnNonIndexProperties(val1, mode === kLoose);
    const keys2 = getOwnNonIndexProperties(val2, mode === kLoose);
    if (keys1.length !== keys2.length) return false;
    return keyCheck(val1, val2, mode, memos, kNoIterator, keys1);
  } else if (isSet(val1)) {
    if (!isSet(val2) || val1.size !== val2.size) return false;
    return keyCheck(val1, val2, mode, memos, kIsSet);
  } else if (isMap(val1)) {
    if (!isMap(val2) || val1.size !== val2.size) return false;
    return keyCheck(val1, val2, mode, memos, kIsMap);
  } else if (isAnyArrayBuffer(val1)) {
    if (!isAnyArrayBuffer(val2) || !areEqualArrayBuffers(val1, val2)) return false;
  } else if (isBoxedPrimitive(val1)) {
    if (!isEqualBoxedPrimitive(val1, val2)) return false;
  } else if (Array.isArray(val2) || isArrayBufferView(val2) || isSet(val2) || isMap(val2) || isDate(val2) ||
    isRegExp(val2) || isAnyArrayBuffer(val2) || isBoxedPrimitive(val2) || isNativeError(val2) || val2 instanceof Error) {
    return false;
  } else if (isWeakMap(val1) || isWeakSet(val1)) {
    return false;
  }
  return keyCheck(val1, val2, mode, memos, kNoIterator);
}

function getEnumerables(val, keys) {
  return keys.filter((k) => isEnumerable(val, k));
}

function keyCheck(val1, val2, mode, memos, iterationType, keys2) {
  const isArrayLikeObject = keys2 !== undefined;
  if (keys2 === undefined) keys2 = Object.keys(val2);
  for (const key of keys2) {
    if (!isEnumerable(val1, key)) return false;
  }
  if (!isArrayLikeObject) {
    if (keys2.length !== Object.keys(val1).length) return false;
    if (mode === kStrict) {
      const symbolKeysA = Object.getOwnPropertySymbols(val1);
      if (symbolKeysA.length !== 0) {
        let count = 0;
        for (const key of symbolKeysA) {
          if (isEnumerable(val1, key)) {
            if (!isEnumerable(val2, key)) return false;
            keys2.push(key);
            count++;
          } else if (isEnumerable(val2, key)) {
            return false;
          }
        }
        const symbolKeysB = Object.getOwnPropertySymbols(val2);
        if (symbolKeysA.length !== symbolKeysB.length && getEnumerables(val2, symbolKeysB).length !== count) return false;
      } else {
        const symbolKeysB = Object.getOwnPropertySymbols(val2);
        if (symbolKeysB.length !== 0 && getEnumerables(val2, symbolKeysB).length !== 0) return false;
      }
    }
  }
  if (keys2.length === 0 && (iterationType === kNoIterator || (iterationType === kIsArray && val2.length === 0) || val2.size === 0)) {
    return true;
  }
  // Cycles: a pair already being compared further up counts as equal.
  if (memos === undefined) memos = { a: new Map(), b: new Map() };
  const seenA = memos.a.get(val1);
  if (seenA !== undefined) {
    const seenB = memos.b.get(val2);
    if (seenB !== undefined) return seenA === seenB;
  }
  const depth = memos.a.size;
  memos.a.set(val1, depth);
  memos.b.set(val2, depth);
  const result = objEquiv(val1, val2, mode, keys2, memos, iterationType);
  memos.a.delete(val1);
  memos.b.delete(val2);
  return result;
}

function setHasEqualElement(set, val1, mode, memo) {
  for (const val2 of set) {
    if (innerDeepEqual(val1, val2, mode, memo)) {
      set.delete(val2);
      return true;
    }
  }
  return false;
}

function findLooseMatchingPrimitives(prim) {
  switch (typeof prim) {
    case "undefined":
      return null;
    case "object":
      return undefined;
    case "symbol":
      return false;
    case "string":
      prim = +prim;
    // falls through
    case "number":
      if (Number.isNaN(prim)) return false;
  }
  return true;
}

function setMightHaveLoosePrim(a, b, prim) {
  const altValue = findLooseMatchingPrimitives(prim);
  if (altValue != null) return altValue;
  return b.has(altValue) && !a.has(altValue);
}

function mapMightHaveLoosePrim(a, b, prim, item, memo) {
  const altValue = findLooseMatchingPrimitives(prim);
  if (altValue != null) return altValue;
  const curB = b.get(altValue);
  if ((curB === undefined && !b.has(altValue)) || !innerDeepEqual(item, curB, kLoose, memo)) return false;
  return !a.has(altValue) && innerDeepEqual(item, curB, kLoose, memo);
}

function setEquiv(a, b, mode, memo) {
  let set = null;
  for (const val of a) {
    if (typeof val === "object" && val !== null) {
      (set ??= new Set()).add(val);
    } else if (!b.has(val)) {
      if (mode !== kLoose) return false;
      if (!setMightHaveLoosePrim(a, b, val)) return false;
      (set ??= new Set()).add(val);
    }
  }
  if (set !== null) {
    for (const val of b) {
      if (typeof val === "object" && val !== null) {
        if (!setHasEqualElement(set, val, mode, memo)) return false;
      } else if (mode === kLoose && !a.has(val) && !setHasEqualElement(set, val, mode, memo)) {
        return false;
      }
    }
    return set.size === 0;
  }
  return true;
}

function mapHasEqualEntry(set, map, key1, item1, mode, memo) {
  for (const key2 of set) {
    if (innerDeepEqual(key1, key2, mode, memo) && innerDeepEqual(item1, map.get(key2), mode, memo)) {
      set.delete(key2);
      return true;
    }
  }
  return false;
}

function mapEquiv(a, b, mode, memo) {
  let set = null;
  for (const [key, item1] of a) {
    if (typeof key === "object" && key !== null) {
      (set ??= new Set()).add(key);
    } else {
      const item2 = b.get(key);
      if ((item2 === undefined && !b.has(key)) || !innerDeepEqual(item1, item2, mode, memo)) {
        if (mode !== kLoose) return false;
        if (!mapMightHaveLoosePrim(a, b, key, item1, memo)) return false;
        (set ??= new Set()).add(key);
      }
    }
  }
  if (set !== null) {
    for (const [key, item] of b) {
      if (typeof key === "object" && key !== null) {
        if (!mapHasEqualEntry(set, a, key, item, mode, memo)) return false;
      } else if (mode === kLoose && (!a.has(key) || !innerDeepEqual(a.get(key), item, mode, memo)) &&
        !mapHasEqualEntry(set, a, key, item, mode, memo)) {
        return false;
      }
    }
    return set.size === 0;
  }
  return true;
}

function objEquiv(a, b, mode, keys2, memos, iterationType) {
  let i = 0;
  if (iterationType === kIsSet) {
    if (!setEquiv(a, b, mode, memos)) return false;
  } else if (iterationType === kIsMap) {
    if (!mapEquiv(a, b, mode, memos)) return false;
  } else if (iterationType === kIsArray) {
    for (; i < a.length; i++) {
      if (hasOwn(a, i)) {
        if (!hasOwn(b, i) || !innerDeepEqual(a[i], b[i], mode, memos)) return false;
      } else if (hasOwn(b, i)) {
        return false;
      } else {
        const keysA = Object.keys(a);
        for (; i < keysA.length; i++) {
          const key = keysA[i];
          if (!hasOwn(b, key) || !innerDeepEqual(a[key], b[key], mode, memos)) return false;
        }
        return keysA.length === Object.keys(b).length;
      }
    }
  }
  for (i = 0; i < keys2.length; i++) {
    const key = keys2[i];
    if (!innerDeepEqual(a[key], b[key], mode, memos)) return false;
  }
  return true;
}

export function isDeepEqual(val1, val2) {
  return innerDeepEqual(val1, val2, kLoose);
}

export function isDeepStrictEqual(val1, val2) {
  return innerDeepEqual(val1, val2, kStrict);
}
`

// assertInspectShim is the subset of util.inspect node:assert's messages
// use, with the fixed options Node's assertion_error.js passes
// (compact: false, sorted: true, depth: 1000, customInspect: false,
// getters: true): every entry on its own line, keys sorted.
const assertInspectShim = `import { types } from "node:util";

const identRe = /^[a-zA-Z_][a-zA-Z_0-9]*$/;

export function strEscape(str) {
  let quote = "'";
  if (str.includes("'")) {
    if (!str.includes('"')) quote = '"';
    else if (!str.includes("\u0060") && !str.includes("${")) quote = "\u0060";
  }
  let out = "";
  for (const ch of str) {
    const c = ch.codePointAt(0);
    if (ch === quote || ch === "\\") out += "\\" + ch;
    else if (ch === "\n") out += "\\n";
    else if (ch === "\t") out += "\\t";
    else if (ch === "\r") out += "\\r";
    else if (ch === "\b") out += "\\b";
    else if (ch === "\f") out += "\\f";
    else if (ch === "\v") out += "\\v";
    else if (c < 0x20 || c === 0x7f) out += "\\x" + c.toString(16).toUpperCase().padStart(2, "0");
    else out += ch;
  }
  return quote + out + quote;
}

function formatPrimitive(v) {
  switch (typeof v) {
    case "string":
      return strEscape(v);
    case "number":
      return Object.is(v, -0) ? "-0" : String(v);
    case "bigint":
      return v + "n";
    case "symbol":
      return v.toString();
    default:
      return String(v);
  }
}

function formatKey(key) {
  if (typeof key === "symbol") return "[" + key.toString() + "]";
  return identRe.test(key) ? key : strEscape(key);
}

function functionBase(fn) {
  let src = "";
  try {
    src = Function.prototype.toString.call(fn);
  } catch {}
  const name = fn.name;
  if (src.startsWith("class")) {
    const parent = Object.getPrototypeOf(fn);
    let base = "[class " + (name || "(anonymous)");
    if (parent && parent !== Function.prototype && parent.name) base += " extends " + parent.name;
    return base + "]";
  }
  let type = "Function";
  if (types.isGeneratorFunction(fn)) type = "GeneratorFunction";
  if (types.isAsyncFunction(fn)) type = types.isGeneratorFunction(fn) ? "AsyncGeneratorFunction" : "AsyncFunction";
  return name ? "[" + type + ": " + name + "]" : "[" + type + " (anonymous)]";
}

function constructorName(obj) {
  let proto = Object.getPrototypeOf(obj);
  if (proto === null) return null;
  while (proto) {
    const desc = Object.getOwnPropertyDescriptor(proto, "constructor");
    if (desc && typeof desc.value === "function" && desc.value.name !== "") return desc.value.name;
    proto = Object.getPrototypeOf(proto);
  }
  return "";
}

function ownEntries(ctx, obj, level, skipIndices) {
  const out = [];
  const keys = Reflect.ownKeys(obj).filter((k) => Object.prototype.propertyIsEnumerable.call(obj, k));
  for (const key of keys) {
    if (skipIndices && typeof key === "string" && /^(0|[1-9][0-9]*)$/.test(key)) continue;
    const desc = Object.getOwnPropertyDescriptor(obj, key);
    let value;
    if (desc.get || desc.set) {
      if (desc.get) {
        let v;
        try {
          v = formatValue(ctx, desc.get.call(obj), level + 1);
        } catch (e) {
          v = "<Inspection threw (" + e.message + ")>";
        }
        value = "[" + (desc.set ? "Getter/Setter" : "Getter") + ": " + v + "]";
      } else {
        value = "[Setter]";
      }
    } else {
      value = formatValue(ctx, desc.value, level + 1);
    }
    out.push(formatKey(key) + ": " + value);
  }
  return out;
}

function wrap(prefix, open, close, entries, level) {
  if (entries.length === 0) return (prefix ? prefix + " " : "") + open + close;
  const pad = "  ".repeat(level);
  return (prefix ? prefix + " " : "") + open + "\n" + pad + "  " + entries.join(",\n" + pad + "  ") + "\n" + pad + close;
}

function formatValue(ctx, v, level) {
  if (v === null || (typeof v !== "object" && typeof v !== "function")) return formatPrimitive(v);
  if (ctx.seen.includes(v)) {
    let idx = ctx.circular.get(v);
    if (idx === undefined) {
      idx = ctx.circular.size + 1;
      ctx.circular.set(v, idx);
    }
    return "[Circular *" + idx + "]";
  }
  ctx.seen.push(v);
  let out = formatRaw(ctx, v, level);
  ctx.seen.pop();
  const idx = ctx.circular.get(v);
  if (idx !== undefined) out = "<ref *" + idx + "> " + out;
  return out;
}

function formatRaw(ctx, v, level) {
  if (typeof v === "function") {
    const base = functionBase(v);
    const entries = ownEntries(ctx, v, level);
    if (entries.length === 0) return base;
    entries.sort();
    return wrap(base, "{", "}", entries, level);
  }
  const ctor = constructorName(v);
  const prefixFor = (fallback, size) => {
    const name = ctor === null ? "[" + fallback + ": null prototype]" : ctor;
    return size === undefined ? name : name + "(" + size + ")";
  };
  if (Array.isArray(v)) {
    const entries = [];
    let holes = 0;
    const flush = () => {
      if (holes) entries.push("<" + holes + " empty item" + (holes > 1 ? "s" : "") + ">");
      holes = 0;
    };
    for (let i = 0; i < v.length; i++) {
      if (!Object.prototype.hasOwnProperty.call(v, i)) {
        holes++;
        continue;
      }
      flush();
      entries.push(formatValue(ctx, v[i], level + 1));
    }
    flush();
    const extra = ownEntries(ctx, v, level, true).filter((e) => !e.startsWith("length:"));
    extra.sort();
    entries.push(...extra);
    const prefix = ctor === "Array" ? "" : prefixFor("Array", v.length);
    return wrap(prefix, "[", "]", entries, level);
  }
  if (types.isTypedArray(v)) {
    const entries = Array.from(v, (x) => formatPrimitive(x));
    const extra = ownEntries(ctx, v, level, true);
    extra.sort();
    entries.push(...extra);
    let prefix = prefixFor("TypedArray", v.length);
    if (ctor === "Buffer") prefix += " [Uint8Array]";
    return wrap(prefix, "[", "]", entries, level);
  }
  if (types.isMap(v)) {
    const entries = [];
    for (const [k, val] of v) entries.push(formatValue(ctx, k, level + 1) + " => " + formatValue(ctx, val, level + 1));
    entries.push(...ownEntries(ctx, v, level));
    entries.sort();
    return wrap(prefixFor("Map", v.size), "{", "}", entries, level);
  }
  if (types.isSet(v)) {
    const entries = [];
    for (const val of v) entries.push(formatValue(ctx, val, level + 1));
    entries.push(...ownEntries(ctx, v, level));
    entries.sort();
    return wrap(prefixFor("Set", v.size), "{", "}", entries, level);
  }
  let base = "";
  if (types.isDate(v)) {
    const t = Date.prototype.getTime.call(v);
    base = Number.isNaN(t) ? "Invalid Date" : Date.prototype.toISOString.call(v);
  } else if (types.isRegExp(v)) {
    base = RegExp.prototype.toString.call(v);
  } else if (types.isNativeError(v) || v instanceof Error) {
    base = typeof v.stack === "string" && v.stack ? v.stack : "[" + Error.prototype.toString.call(v) + "]";
  } else if (types.isBoxedPrimitive(v)) {
    const prim = v.valueOf();
    const kind = typeof prim === "bigint" ? "BigInt" : typeof prim === "symbol" ? "Symbol" : typeof prim === "number" ? "Number" : typeof prim === "string" ? "String" : "Boolean";
    base = "[" + kind + ": " + formatPrimitive(prim) + "]";
  } else if (types.isPromise(v)) {
    base = "Promise {";
  } else if (types.isWeakMap(v) || types.isWeakSet(v)) {
    return prefixFor(types.isWeakMap(v) ? "WeakMap" : "WeakSet") + " { <items unknown> }";
  }
  const entries = ownEntries(ctx, v, level);
  entries.sort();
  if (base) {
    if (base === "Promise {") return wrap("Promise", "{", "}", entries, level);
    return entries.length === 0 ? base : wrap(base, "{", "}", entries, level);
  }
  const prefix = ctor === "Object" ? "" : prefixFor("Object");
  return wrap(prefix, "{", "}", entries, level);
}

export function inspectValue(v) {
  return formatValue({ seen: [], circular: new Map() }, v, 0);
}
`

func declareComparisons() {
	registerJSShim("noderati-internal:comparisons", comparisonsShim)
	registerJSShim("noderati-internal:assert-inspect", assertInspectShim)
}
