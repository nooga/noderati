// node:crypto for noderati. The files in crypto_js/ are concatenated in
// name order into one ES module (see crypto.go); they share this scope.
// Byte-level work happens in the Go binding (crypto_binding.go and
// friends), reached through the non-enumerable global __noderatiCrypto.
import { Transform, Writable } from "node:stream";

const binding = globalThis.__noderatiCrypto;
const kHandle = Symbol("kHandle");
const kKeyType = Symbol("kKeyType");
const kKeyData = Symbol("kKeyData");
const kState = Symbol("kState");
const kAlgorithm = Symbol("kAlgorithm");
const kExtractable = Symbol("kExtractable");
const kKeyUsages = Symbol("kKeyUsages");
const kKeyObject = Symbol("kKeyObject");

const kMaxLength = 2 ** 31 - 1;
const kMaxUint32 = 4294967295;

// ---- errors ----------------------------------------------------------------

function addNumericalSeparator(val) {
  let res = "";
  let i = val.length;
  const start = val[0] === "-" ? 1 : 0;
  for (; i >= start + 4; i -= 3) res = "_" + val.slice(i - 3, i) + res;
  return val.slice(0, i) + res;
}

function inspectShort(v) {
  if (typeof v === "string") {
    if (v.length > 28) v = v.slice(0, 25) + "...";
    return v.indexOf("'") === -1 ? "'" + v + "'" : JSON.stringify(v);
  }
  if (typeof v === "bigint") return String(v) + "n";
  if (typeof v === "symbol") return v.toString();
  if (v === null || v === undefined) return String(v);
  if (typeof v === "object") {
    return Array.isArray(v) ? "[ " + v.map(inspectShort).join(", ") + " ]" : "{}";
  }
  return String(v);
}

function describeReceived(v) {
  if (v == null) return " Received " + v;
  if (typeof v === "function") return " Received function " + (v.name || "<anonymous>");
  if (typeof v === "object") {
    if (v.constructor && v.constructor.name) return " Received an instance of " + v.constructor.name;
    return " Received " + inspectShort(v);
  }
  return " Received type " + typeof v + " (" + inspectShort(v) + ")";
}

function makeError(Base, code, message) {
  const err = new Base(message);
  if (code) {
    Object.defineProperty(err, "code", { value: code, enumerable: true, writable: true, configurable: true });
  }
  return err;
}

function errInvalidArgType(name, expected, actual) {
  const what = name.includes(".") ? "property" : "argument";
  return makeError(TypeError, "ERR_INVALID_ARG_TYPE",
    'The "' + name + '" ' + what + " must be " + expected + "." + describeReceived(actual));
}

function errInvalidArgValue(name, value, reason) {
  const what = name.includes(".") ? "property" : "argument";
  return makeError(TypeError, "ERR_INVALID_ARG_VALUE",
    "The " + what + " '" + name + "' " + (reason || "is invalid") + ". Received " + inspectShort(value));
}

function formatRangeReceived(v) {
  if (typeof v === "number" && Number.isInteger(v) && Math.abs(v) > 2 ** 32) return addNumericalSeparator(String(v));
  if (typeof v === "bigint") {
    let s = String(v);
    if (v > 2n ** 32n || v < -(2n ** 32n)) s = addNumericalSeparator(s);
    return s + "n";
  }
  return inspectShort(v);
}

function errOutOfRange(name, range, actual) {
  return makeError(RangeError, "ERR_OUT_OF_RANGE",
    'The value of "' + name + '" is out of range. It must be ' + range + ". Received " + formatRangeReceived(actual));
}

function errMissingArgs(name) {
  return makeError(TypeError, "ERR_MISSING_ARGS", 'The "' + name + '" argument must be specified');
}

function domException(message, name) {
  return new DOMException(message, name);
}

// ---- validators ------------------------------------------------------------

function validateString(v, name) {
  if (typeof v !== "string") throw errInvalidArgType(name, "of type string", v);
}

function validateFunction(v, name) {
  if (typeof v !== "function") throw errInvalidArgType(name, "of type function", v);
}

function validateBoolean(v, name) {
  if (typeof v !== "boolean") throw errInvalidArgType(name, "of type boolean", v);
}

function validateObject(v, name, nullable) {
  if ((v === null && !nullable) || typeof v !== "object" || Array.isArray(v)) {
    throw errInvalidArgType(name, "of type object", v);
  }
}

function validateNumber(v, name, min, max) {
  if (typeof v !== "number") throw errInvalidArgType(name, "of type number", v);
  if ((min != null && v < min) || (max != null && v > max) || ((min != null || max != null) && Number.isNaN(v))) {
    throw errOutOfRange(name,
      (min != null ? ">= " + min : "") + (min != null && max != null ? " && " : "") + (max != null ? "<= " + max : ""), v);
  }
}

function validateInteger(v, name, min = Number.MIN_SAFE_INTEGER, max = Number.MAX_SAFE_INTEGER) {
  if (typeof v !== "number") throw errInvalidArgType(name, "of type number", v);
  if (!Number.isInteger(v)) throw errOutOfRange(name, "an integer", v);
  if (v < min || v > max) throw errOutOfRange(name, ">= " + min + " && <= " + max, v);
}

function validateInt32(v, name, min = -2147483648, max = 2147483647) {
  if (typeof v !== "number") throw errInvalidArgType(name, "of type number", v);
  if (!Number.isInteger(v)) throw errOutOfRange(name, "an integer", v);
  if (v < min || v > max) throw errOutOfRange(name, ">= " + min + " && <= " + max, v);
}

function validateUint32(v, name, positive) {
  if (typeof v !== "number") throw errInvalidArgType(name, "of type number", v);
  if (!Number.isInteger(v)) throw errOutOfRange(name, "an integer", v);
  const min = positive ? 1 : 0;
  if (v < min || v > kMaxUint32) throw errOutOfRange(name, ">= " + min + " && <= " + kMaxUint32, v);
}

function validateOneOf(v, name, list) {
  if (!list.includes(v)) {
    const allowed = list.map((x) => (typeof x === "string" ? "'" + x + "'" : String(x))).join(", ");
    throw errInvalidArgValue(name, v, "must be one of: " + allowed);
  }
}

function isArrayBufferView(v) {
  return ArrayBuffer.isView(v) || v instanceof DataView;
}

function isAnyArrayBuffer(v) {
  return v instanceof ArrayBuffer || (typeof SharedArrayBuffer !== "undefined" && v instanceof SharedArrayBuffer);
}

// toU8 returns a Uint8Array view (no copy) of an ArrayBuffer or view.
function toU8(v) {
  if (v instanceof Uint8Array) return v;
  if (isAnyArrayBuffer(v)) return new Uint8Array(v);
  return new Uint8Array(v.buffer, v.byteOffset, v.byteLength);
}

function toBuf(v) {
  if (Buffer.isBuffer(v)) return v;
  const u = toU8(v);
  return Buffer.from(u.buffer, u.byteOffset, u.byteLength);
}

const kBinaryTypes = "an instance of ArrayBuffer, Buffer, TypedArray, or DataView";

// getArrayBufferOrView mirrors node's internal helper of the same name:
// strings are decoded with encoding, binary inputs are viewed as bytes.
function getArrayBufferOrView(v, name, encoding) {
  if (isAnyArrayBuffer(v)) return new Uint8Array(v);
  if (typeof v === "string") {
    if (encoding === "buffer") encoding = "utf8";
    return Buffer.from(v, encoding);
  }
  if (!isArrayBufferView(v)) {
    throw errInvalidArgType(name, "of type string or " + kBinaryTypes, v);
  }
  return toU8(v);
}

function normalizeEncoding(enc) {
  if (enc == null || enc === "buffer") return undefined;
  const e = String(enc).toLowerCase();
  if (e === "utf-8") return "utf8";
  if (e === "ucs-2" || e === "ucs2" || e === "utf-16le") return "utf16le";
  if (e === "binary") return "latin1";
  return Buffer.isEncoding(e) ? e : undefined;
}

function encodeOutput(buf, enc) {
  const e = normalizeEncoding(enc);
  if (!e) return buf;
  // noderati's Buffer pads base64url output; node's never does.
  if (e === "base64url") return buf.toString("base64").replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_");
  // ...and decodes utf16le one code unit per byte.
  if (e === "utf16le") {
    let s = "";
    for (let i = 0; i + 1 < buf.length; i += 2) s += String.fromCharCode(buf[i] | (buf[i + 1] << 8));
    return s;
  }
  return buf.toString(e);
}

// inheritStream wires a function constructor under a stream class. The
// explicit length restore works around paserati reporting the base
// class's length for F after Object.setPrototypeOf(F, Transform).
function inheritStream(F, Base) {
  const len = F.length;
  Object.setPrototypeOf(F.prototype, Base.prototype);
  Object.setPrototypeOf(F, Base);
  if (F.length !== len) Object.defineProperty(F, "length", { value: len, configurable: true });
}

function defer(fn) {
  if (typeof setImmediate === "function") setImmediate(fn);
  else setTimeout(fn, 0);
}
