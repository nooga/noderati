package host

import (
	"github.com/nooga/paserati/pkg/vm"
)

// v8Classify reports the engine's own view of what kind of value v is,
// for the serializer below: V8's ValueSerializer dispatches on internal
// object types (a Date, an Error, a boxed primitive...), which a
// Symbol.toStringTag or a subclass can disguise from JS-level checks.
func v8Classify(v vm.Value) string {
	switch v.Type() {
	case vm.TypeUndefined:
		return "undefined"
	case vm.TypeNull:
		return "null"
	case vm.TypeBoolean:
		return "boolean"
	case vm.TypeFloatNumber, vm.TypeIntegerNumber:
		return "number"
	case vm.TypeBigInt:
		return "bigint"
	case vm.TypeString:
		return "string"
	case vm.TypeSymbol:
		return "symbol"
	case vm.TypeFunction, vm.TypeNativeFunction, vm.TypeNativeFunctionWithProps,
		vm.TypeAsyncNativeFunction, vm.TypeClosure, vm.TypeBoundFunction:
		return "function"
	case vm.TypeArray:
		return "array"
	case vm.TypeRegExp:
		return "regexp"
	case vm.TypeMap:
		return "map"
	case vm.TypeSet:
		return "set"
	case vm.TypeArrayBuffer:
		return "arraybuffer"
	case vm.TypeSharedArrayBuffer:
		return "uncloneable:#<SharedArrayBuffer>"
	case vm.TypeTypedArray:
		return "typedarray"
	case vm.TypeDataView:
		return "dataview"
	case vm.TypePromise:
		return "uncloneable:#<Promise>"
	case vm.TypeWeakMap:
		return "uncloneable:#<WeakMap>"
	case vm.TypeWeakSet:
		return "uncloneable:#<WeakSet>"
	case vm.TypeWeakRef:
		return "uncloneable:#<WeakRef>"
	case vm.TypeFinalizationRegistry:
		return "uncloneable:#<FinalizationRegistry>"
	case vm.TypeGenerator:
		return "uncloneable:[object Generator]"
	case vm.TypeAsyncGenerator:
		return "uncloneable:[object AsyncGenerator]"
	case vm.TypeProxy, vm.TypeArguments:
		return "uncloneable:#<Object>"
	case vm.TypeObject:
		obj := v.AsPlainObject()
		if _, ok := obj.GetOwn("__timestamp__"); ok {
			return "date"
		}
		if _, ok := obj.GetOwn("[[ErrorData]]"); ok {
			return "error"
		}
		if pv, ok := obj.GetOwn("[[PrimitiveValue]]"); ok {
			return "boxed:" + v8Classify(pv)
		}
		return "object"
	case vm.TypeDictObject:
		return "object"
	}
	return "uncloneable:#<Object>"
}

// v8Shim is node:v8: the Go-backed helpers from noderati-internal-v8
// plus a real V8 ValueSerializer (wire format version 15) in JS, with
// Node's own DefaultSerializer/DefaultDeserializer host-object handling
// for ArrayBufferViews. Byte output matches real Node's v8.serialize for
// every value kind V8 can clone; first needed by vitest, which frames
// every worker RPC message with v8.serialize/v8.deserialize.
const v8Shim = `import native from "noderati-internal-v8";

const classify = native.classify;
const kLatestVersion = 15;

const T = {
  padding: 0x00, verifyObjectCount: 0x3f, theHole: 0x2d, undefined: 0x5f, null: 0x30,
  true: 0x54, false: 0x46, int32: 0x49, uint32: 0x55, double: 0x4e, bigint: 0x5a,
  utf8String: 0x53, oneByteString: 0x22, twoByteString: 0x63, objectReference: 0x5e,
  beginObject: 0x6f, endObject: 0x7b, beginSparseArray: 0x61, endSparseArray: 0x40,
  beginDenseArray: 0x41, endDenseArray: 0x24, date: 0x44, trueObject: 0x79,
  falseObject: 0x78, numberObject: 0x6e, bigintObject: 0x7a, stringObject: 0x73,
  regexp: 0x52, beginMap: 0x3b, endMap: 0x3a, beginSet: 0x27, endSet: 0x2c,
  arrayBuffer: 0x42, arrayBufferTransfer: 0x74, arrayBufferView: 0x56, hostObject: 0x5c,
  error: 0x72, version: 0xff,
};

const ErrorTag = { eval: 0x45, range: 0x52, reference: 0x46, syntax: 0x53, type: 0x54, uri: 0x55, message: 0x6d, cause: 0x63, stack: 0x73, end: 0x2e };
const errorTagByName = { EvalError: ErrorTag.eval, RangeError: ErrorTag.range, ReferenceError: ErrorTag.reference, SyntaxError: ErrorTag.syntax, TypeError: ErrorTag.type, URIError: ErrorTag.uri };
const errorCtorByTag = { [ErrorTag.eval]: EvalError, [ErrorTag.range]: RangeError, [ErrorTag.reference]: ReferenceError, [ErrorTag.syntax]: SyntaxError, [ErrorTag.type]: TypeError, [ErrorTag.uri]: URIError };

// V8's ArrayBufferViewTag bytes, keyed by constructor name.
const viewTag = { Int8Array: 0x62, Uint8Array: 0x42, Uint8ClampedArray: 0x43, Int16Array: 0x77, Uint16Array: 0x57, Int32Array: 0x64, Uint32Array: 0x44, Float16Array: 0x68, Float32Array: 0x66, Float64Array: 0x46, BigInt64Array: 0x71, BigUint64Array: 0x51, DataView: 0x3f };
const viewCtorByTag = {};
for (const [name, tag] of Object.entries(viewTag)) if (globalThis[name]) viewCtorByTag[tag] = globalThis[name];

const regexpFlagBits = { d: 128, g: 1, i: 2, m: 4, s: 32, u: 16, v: 256, y: 8 };

function viewKind(view) {
  if (view instanceof DataView) return "DataView";
  for (const name of Object.keys(viewTag)) {
    const C = globalThis[name];
    if (C && name !== "DataView" && view instanceof C) return name;
  }
  return "Uint8Array";
}

function isArrayIndexKey(k) {
  if (k === "0") return true;
  if (!/^[1-9][0-9]*$/.test(k)) return false;
  const n = Number(k);
  return n <= 4294967294;
}

function cloneError(what) {
  return new Error(what + " could not be cloned.");
}

class Serializer {
  constructor() {
    this._buf = new Uint8Array(64);
    this._len = 0;
    this._ids = new Map();
    this._nextId = 0;
    this._treatViewsAsHost = false;
    this._transfers = new Map();
  }
  _grow(n) {
    if (this._len + n <= this._buf.length) return;
    let cap = this._buf.length * 2;
    while (cap < this._len + n) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this._buf.subarray(0, this._len));
    this._buf = next;
  }
  _byte(b) {
    this._grow(1);
    this._buf[this._len++] = b;
  }
  _bytes(u8) {
    this._grow(u8.length);
    this._buf.set(u8, this._len);
    this._len += u8.length;
  }
  _varint(n) {
    // Unsigned LEB128 over values up to 2^53, without 32-bit truncation.
    do {
      let b = n % 128;
      n = Math.floor(n / 128);
      if (n > 0) b |= 0x80;
      this._byte(b);
    } while (n > 0);
  }
  _varintBig(n) {
    do {
      let b = Number(n & 0x7fn);
      n >>= 7n;
      if (n > 0n) b |= 0x80;
      this._byte(b);
    } while (n > 0n);
  }
  _zigzag(n) {
    this._varint(((n << 1) ^ (n >> 31)) >>> 0);
  }
  _double(d) {
    // V8 writes the canonical quiet NaN; DataView may store another NaN
    // bit pattern here.
    if (Number.isNaN(d)) return this._bytes(new Uint8Array([0, 0, 0, 0, 0, 0, 0xf8, 0x7f]));
    const tmp = new DataView(new ArrayBuffer(8));
    tmp.setFloat64(0, d, true);
    this._bytes(new Uint8Array(tmp.buffer));
  }
  _bigintContents(v) {
    const neg = v < 0n;
    let mag = neg ? -v : v;
    const digits = [];
    while (mag > 0n) {
      digits.push(mag & 0xffffffffffffffffn);
      mag >>= 64n;
    }
    this._varint(digits.length * 8 * 2 + (neg ? 1 : 0));
    const tmp = new DataView(new ArrayBuffer(8));
    for (const d of digits) {
      tmp.setBigUint64(0, d, true);
      this._bytes(new Uint8Array(tmp.buffer));
    }
  }
  _string(s) {
    let oneByte = true;
    for (let i = 0; i < s.length; i++) {
      if (s.charCodeAt(i) > 0xff) { oneByte = false; break; }
    }
    if (oneByte) {
      this._byte(T.oneByteString);
      this._varint(s.length);
      const out = new Uint8Array(s.length);
      for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i);
      this._bytes(out);
      return;
    }
    const byteLength = s.length * 2;
    let varintLen = 1;
    for (let n = byteLength; n >= 128; n = Math.floor(n / 128)) varintLen++;
    // V8 pads so the UTF-16 payload starts at an even offset.
    if ((this._len + 1 + varintLen) & 1) this._byte(T.padding);
    this._byte(T.twoByteString);
    this._varint(byteLength);
    const out = new Uint8Array(byteLength);
    for (let i = 0; i < s.length; i++) {
      const c = s.charCodeAt(i);
      out[2 * i] = c & 0xff;
      out[2 * i + 1] = c >> 8;
    }
    this._bytes(out);
  }

  writeHeader() {
    this._byte(T.version);
    this._varint(kLatestVersion);
  }
  writeValue(value) {
    this._writeObject(value);
    return true;
  }
  releaseBuffer() {
    const out = Buffer.from(this._buf.subarray(0, this._len));
    this._buf = new Uint8Array(64);
    this._len = 0;
    return out;
  }
  transferArrayBuffer(id, arrayBuffer) {
    this._transfers.set(arrayBuffer, id);
  }
  writeUint32(n) {
    this._varint(n >>> 0);
  }
  writeUint64(hi, lo) {
    this._varintBig((BigInt(hi >>> 0) << 32n) | BigInt(lo >>> 0));
  }
  writeDouble(d) {
    this._double(d);
  }
  writeRawBytes(src) {
    if (!ArrayBuffer.isView(src)) {
      const err = new TypeError('The "source" argument must be an instance of Buffer, TypedArray, or DataView.');
      err.code = "ERR_INVALID_ARG_TYPE";
      throw err;
    }
    this._bytes(new Uint8Array(src.buffer, src.byteOffset, src.byteLength));
  }
  _getDataCloneError(message) {
    return new Error(message);
  }
  _setTreatArrayBufferViewsAsHostObjects(flag) {
    this._treatViewsAsHost = !!flag;
  }
  _writeHostObject(object) {
    throw new Error("Unserializable host object: " + Object.prototype.toString.call(object));
  }

  _writeObject(v) {
    const kind = classify(v);
    switch (kind) {
      case "undefined": return this._byte(T.undefined);
      case "null": return this._byte(T.null);
      case "boolean": return this._byte(v ? T.true : T.false);
      case "number":
        if (Number.isInteger(v) && v >= -2147483648 && v <= 2147483647 && !Object.is(v, -0)) {
          this._byte(T.int32);
          return this._zigzag(v);
        }
        this._byte(T.double);
        return this._double(v);
      case "bigint":
        this._byte(T.bigint);
        return this._bigintContents(v);
      case "string":
        return this._string(v);
      case "symbol":
        throw cloneError(String(v));
      case "function":
        throw cloneError(Function.prototype.toString.call(v));
    }
    if (kind.startsWith("uncloneable:")) throw cloneError(kind.slice(12));
    if (this._ids.has(v)) {
      this._byte(T.objectReference);
      return this._varint(this._ids.get(v));
    }
    if (kind === "arraybuffer" && this._transfers.has(v)) {
      this._ids.set(v, this._nextId++);
      this._byte(T.arrayBufferTransfer);
      return this._varint(this._transfers.get(v));
    }
    if ((kind === "typedarray" || kind === "dataview") && this._treatViewsAsHost) {
      this._ids.set(v, this._nextId++);
      this._byte(T.hostObject);
      return this._writeHostObject(v);
    }
    if (kind === "typedarray" || kind === "dataview") {
      // Plain V8 format: the backing buffer, then the view over it.
      this._writeObject(v.buffer);
      this._ids.set(v, this._nextId++);
      this._byte(T.arrayBufferView);
      this._byte(viewTag[viewKind(v)]);
      this._varint(v.byteOffset);
      this._varint(v.byteLength);
      return this._varint(0);
    }
    this._ids.set(v, this._nextId++);
    switch (kind) {
      case "array": return this._writeArray(v);
      case "date":
        this._byte(T.date);
        return this._double(v.getTime());
      case "regexp": {
        this._byte(T.regexp);
        this._string(v.source);
        let bits = 0;
        for (const f of v.flags) bits |= regexpFlagBits[f] || 0;
        return this._varint(bits);
      }
      case "map": {
        const entries = [...Map.prototype.entries.call(v)];
        this._byte(T.beginMap);
        for (const [k, val] of entries) {
          this._writeObject(k);
          this._writeObject(val);
        }
        this._byte(T.endMap);
        return this._varint(entries.length * 2);
      }
      case "set": {
        const values = [...Set.prototype.values.call(v)];
        this._byte(T.beginSet);
        for (const val of values) this._writeObject(val);
        this._byte(T.endSet);
        return this._varint(values.length);
      }
      case "arraybuffer": {
        this._byte(T.arrayBuffer);
        this._varint(v.byteLength);
        return this._bytes(new Uint8Array(v));
      }
      case "error": return this._writeError(v);
      case "boxed:number":
        this._byte(T.numberObject);
        return this._double(Number.prototype.valueOf.call(v));
      case "boxed:string":
        this._byte(T.stringObject);
        return this._string(String.prototype.valueOf.call(v));
      case "boxed:boolean":
        return this._byte(Boolean.prototype.valueOf.call(v) ? T.trueObject : T.falseObject);
      case "boxed:bigint":
        this._byte(T.bigintObject);
        return this._bigintContents(BigInt.prototype.valueOf.call(v));
      case "boxed:symbol":
        throw cloneError("Symbol(" + (Symbol.prototype.valueOf.call(v).description ?? "") + ")");
    }
    this._byte(T.beginObject);
    const n = this._writeProperties(v, Object.keys(v));
    this._byte(T.endObject);
    this._varint(n);
  }

  _writeProperties(obj, keys) {
    let written = 0;
    for (const key of keys) {
      if (!Object.prototype.hasOwnProperty.call(obj, key)) continue;
      const value = obj[key];
      if (isArrayIndexKey(key)) this._writeObject(Number(key));
      else this._string(key);
      this._writeObject(value);
      written++;
    }
    return written;
  }

  _writeArray(arr) {
    const length = arr.length;
    let dense = true;
    for (let i = 0; i < length; i++) {
      if (!(i in arr)) { dense = false; break; }
    }
    if (dense) {
      this._byte(T.beginDenseArray);
      this._varint(length);
      for (let i = 0; i < length; i++) {
        if (i in arr) this._writeObject(arr[i]);
        else this._byte(T.theHole);
      }
      const rest = Object.keys(arr).filter((k) => !isArrayIndexKey(k) || Number(k) >= length);
      const n = this._writeProperties(arr, rest);
      this._byte(T.endDenseArray);
      this._varint(n);
      return this._varint(length);
    }
    this._byte(T.beginSparseArray);
    this._varint(length);
    const n = this._writeProperties(arr, Object.keys(arr));
    this._byte(T.endSparseArray);
    this._varint(n);
    this._varint(length);
  }

  _writeError(err) {
    this._byte(T.error);
    let name;
    try { name = String(err.name); } catch (e) { name = ""; }
    if (errorTagByName[name]) this._varint(errorTagByName[name]);
    const md = Object.getOwnPropertyDescriptor(err, "message");
    if (md && "value" in md) {
      this._varint(ErrorTag.message);
      this._string(String(md.value));
    }
    const stack = err.stack;
    if (typeof stack === "string") {
      this._varint(ErrorTag.stack);
      this._string(stack);
    }
    const cd = Object.getOwnPropertyDescriptor(err, "cause");
    if (cd && "value" in cd) {
      this._varint(ErrorTag.cause);
      this._writeObject(cd.value);
    }
    this._varint(ErrorTag.end);
  }
}

function badData() {
  return new Error("Unable to deserialize cloned data.");
}

class Deserializer {
  constructor(buffer) {
    if (!ArrayBuffer.isView(buffer)) {
      const err = new TypeError('The "buffer" argument must be an instance of Buffer, TypedArray, or DataView.');
      err.code = "ERR_INVALID_ARG_TYPE";
      throw err;
    }
    this.buffer = Buffer.isBuffer(buffer) ? buffer : Buffer.from(buffer.buffer, buffer.byteOffset, buffer.byteLength);
    this._u8 = new Uint8Array(buffer.buffer, buffer.byteOffset, buffer.byteLength);
    this._pos = 0;
    this._version = 0;
    this._objects = [];
    this._transfers = new Map();
  }
  _byte() {
    if (this._pos >= this._u8.length) throw badData();
    return this._u8[this._pos++];
  }
  _peek() {
    return this._pos < this._u8.length ? this._u8[this._pos] : -1;
  }
  _varint() {
    let result = 0, mul = 1, b;
    do {
      b = this._byte();
      result += (b & 0x7f) * mul;
      mul *= 128;
    } while (b & 0x80);
    return result;
  }
  _varintBig() {
    let result = 0n, shift = 0n, b;
    do {
      b = this._byte();
      result |= BigInt(b & 0x7f) << shift;
      shift += 7n;
    } while (b & 0x80);
    return result;
  }
  _zigzag() {
    const n = this._varint();
    return (n % 2 === 0) ? n / 2 : -(n + 1) / 2;
  }
  _double() {
    if (this._pos + 8 > this._u8.length) throw badData();
    const tmp = new DataView(this._u8.buffer, this._u8.byteOffset + this._pos, 8);
    this._pos += 8;
    return tmp.getFloat64(0, true);
  }
  _raw(n) {
    if (this._pos + n > this._u8.length) throw badData();
    const out = this._u8.subarray(this._pos, this._pos + n);
    this._pos += n;
    return out;
  }
  _bigintContents() {
    const bitfield = this._varint();
    const neg = bitfield % 2 === 1;
    const byteLength = Math.floor(bitfield / 2);
    const bytes = this._raw(byteLength);
    let v = 0n;
    for (let i = byteLength - 1; i >= 0; i--) v = (v << 8n) | BigInt(bytes[i]);
    return neg ? -v : v;
  }
  _oneByteString() {
    const n = this._varint();
    const b = this._raw(n);
    let s = "";
    for (let i = 0; i < b.length; i += 4096) s += String.fromCharCode.apply(null, b.subarray(i, i + 4096));
    return s;
  }
  _twoByteString() {
    const n = this._varint();
    if (n % 2) throw badData();
    const b = this._raw(n);
    const codes = new Array(n / 2);
    for (let i = 0; i < codes.length; i++) codes[i] = b[2 * i] | (b[2 * i + 1] << 8);
    let s = "";
    for (let i = 0; i < codes.length; i += 4096) s += String.fromCharCode.apply(null, codes.slice(i, i + 4096));
    return s;
  }
  _utf8String() {
    const n = this._varint();
    return Buffer.from(this._raw(n)).toString("utf8");
  }
  _readString() {
    let tag = this._byte();
    while (tag === T.padding) tag = this._byte();
    if (tag === T.oneByteString) return this._oneByteString();
    if (tag === T.twoByteString) return this._twoByteString();
    if (tag === T.utf8String) return this._utf8String();
    throw badData();
  }

  readHeader() {
    if (this._peek() === T.version) {
      this._pos++;
      this._version = this._varint();
      if (this._version > kLatestVersion) throw new Error("Unable to deserialize cloned data due to invalid or unsupported version.");
    } else {
      throw new Error("Unable to deserialize cloned data due to invalid or unsupported version.");
    }
    return true;
  }
  getWireFormatVersion() {
    return this._version;
  }
  readValue() {
    return this._readObject();
  }
  transferArrayBuffer(id, arrayBuffer) {
    this._transfers.set(id, arrayBuffer);
  }
  readUint32() {
    return this._varint() >>> 0;
  }
  readUint64() {
    const v = this._varintBig();
    return [Number((v >> 32n) & 0xffffffffn), Number(v & 0xffffffffn)];
  }
  readDouble() {
    return this._double();
  }
  _readRawBytes(length) {
    const offset = this._pos;
    this._raw(length);
    return offset;
  }
  readRawBytes(length) {
    const offset = this._readRawBytes(length);
    return this.buffer.subarray(offset, offset + length);
  }
  _readHostObject() {
    throw badData();
  }

  _readObject() {
    const v = this._readObjectInternal();
    // A plain-format ArrayBuffer may be followed by a view over it.
    if (v instanceof ArrayBuffer && this._peek() === T.arrayBufferView) {
      this._pos++;
      return this._readView(v);
    }
    return v;
  }
  _readView(buffer) {
    const tag = this._byte();
    const byteOffset = this._varint();
    const byteLength = this._varint();
    if (this._version >= 14) this._varint();
    const C = viewCtorByTag[tag];
    if (!C) throw badData();
    const view = C === DataView
      ? new DataView(buffer, byteOffset, byteLength)
      : new C(buffer, byteOffset, byteLength / (C.BYTES_PER_ELEMENT || 1));
    this._objects.push(view);
    return view;
  }
  _readObjectInternal() {
    const tag = this._byte();
    switch (tag) {
      case T.padding: return this._readObjectInternal();
      case T.verifyObjectCount: this._varint(); return this._readObjectInternal();
      case T.undefined: return undefined;
      case T.null: return null;
      case T.true: return true;
      case T.false: return false;
      case T.int32: return this._zigzag();
      case T.uint32: return this._varint();
      case T.double: return this._double();
      case T.bigint: return this._bigintContents();
      case T.utf8String: return this._utf8String();
      case T.oneByteString: return this._oneByteString();
      case T.twoByteString: return this._twoByteString();
      case T.objectReference: {
        const id = this._varint();
        if (id >= this._objects.length) throw badData();
        return this._objects[id];
      }
      case T.beginObject: {
        const obj = {};
        this._objects.push(obj);
        this._readProperties(obj, T.endObject);
        return obj;
      }
      case T.beginSparseArray: {
        const length = this._varint();
        const arr = new Array(length);
        this._objects.push(arr);
        this._readProperties(arr, T.endSparseArray);
        this._varint();
        return arr;
      }
      case T.beginDenseArray: {
        const length = this._varint();
        const arr = new Array(length);
        this._objects.push(arr);
        for (let i = 0; i < length; i++) {
          if (this._peek() === T.theHole) { this._pos++; continue; }
          arr[i] = this._readObject();
        }
        this._readProperties(arr, T.endDenseArray);
        this._varint();
        return arr;
      }
      case T.date: {
        const d = new Date(this._double());
        this._objects.push(d);
        return d;
      }
      case T.trueObject: case T.falseObject: {
        const o = Object(tag === T.trueObject);
        this._objects.push(o);
        return o;
      }
      case T.numberObject: {
        const o = Object(this._double());
        this._objects.push(o);
        return o;
      }
      case T.bigintObject: {
        const o = Object(this._bigintContents());
        this._objects.push(o);
        return o;
      }
      case T.stringObject: {
        const idx = this._objects.length;
        this._objects.push(null);
        const o = Object(this._readString());
        this._objects[idx] = o;
        return o;
      }
      case T.regexp: {
        const idx = this._objects.length;
        this._objects.push(null);
        const source = this._readString();
        const bits = this._varint();
        let flags = "";
        for (const f of "dgimsuvy") if (bits & regexpFlagBits[f]) flags += f;
        const re = new RegExp(source, flags);
        this._objects[idx] = re;
        return re;
      }
      case T.beginMap: {
        const m = new Map();
        this._objects.push(m);
        while (this._peek() !== T.endMap) {
          const k = this._readObject();
          m.set(k, this._readObject());
        }
        this._pos++;
        this._varint();
        return m;
      }
      case T.beginSet: {
        const s = new Set();
        this._objects.push(s);
        while (this._peek() !== T.endSet) s.add(this._readObject());
        this._pos++;
        this._varint();
        return s;
      }
      case T.arrayBuffer: {
        const n = this._varint();
        const ab = new ArrayBuffer(n);
        new Uint8Array(ab).set(this._raw(n));
        this._objects.push(ab);
        return ab;
      }
      case T.arrayBufferTransfer: {
        const id = this._varint();
        if (!this._transfers.has(id)) throw badData();
        const ab = this._transfers.get(id);
        this._objects.push(ab);
        return ab;
      }
      case T.hostObject: {
        const idx = this._objects.length;
        this._objects.push(null);
        const o = this._readHostObject();
        this._objects[idx] = o;
        return o;
      }
      case T.error: return this._readError();
    }
    throw badData();
  }
  _readProperties(obj, endTag) {
    while (this._peek() !== endTag) {
      if (this._peek() === -1) throw badData();
      const key = this._readObject();
      obj[key] = this._readObject();
    }
    this._pos++;
    this._varint();
  }
  _readError() {
    const idx = this._objects.length;
    this._objects.push(null);
    let Ctor = Error, message, stack, cause, hasMessage = false, hasCause = false;
    for (;;) {
      const t = this._varint();
      if (t === ErrorTag.end) break;
      if (errorCtorByTag[t]) Ctor = errorCtorByTag[t];
      else if (t === ErrorTag.message) { message = this._readString(); hasMessage = true; }
      else if (t === ErrorTag.stack) stack = this._readString();
      else if (t === ErrorTag.cause) { cause = this._readObject(); hasCause = true; }
      else throw badData();
    }
    const err = hasMessage ? new Ctor(message, hasCause ? { cause } : undefined) : new Ctor(undefined, hasCause ? { cause } : undefined);
    if (!hasMessage) delete err.message;
    Object.defineProperty(err, "stack", { value: stack, writable: true, enumerable: false, configurable: true });
    this._objects[idx] = err;
    return err;
  }
}

// Index of each ArrayBufferView type in Node's own host-object encoding
// (lib/v8.js); a real Buffer is 10.
const arrayBufferViewTypes = [Int8Array, Uint8Array, Uint8ClampedArray, Int16Array, Uint16Array, Int32Array, Uint32Array, Float32Array, Float64Array, DataView, Buffer, BigInt64Array, BigUint64Array];

class DefaultSerializer extends Serializer {
  constructor() {
    super();
    this._setTreatArrayBufferViewsAsHostObjects(true);
  }
  _writeHostObject(abView) {
    let i = 10;
    if (abView.constructor !== Buffer) {
      i = arrayBufferViewTypes.findIndex((C) => C !== Buffer && C.name === viewKind(abView));
      if (i === -1) throw this._getDataCloneError("Unserializable host object: " + Object.prototype.toString.call(abView));
    }
    this.writeUint32(i);
    this.writeUint32(abView.byteLength);
    this.writeRawBytes(new Uint8Array(abView.buffer, abView.byteOffset, abView.byteLength));
  }
}

class DefaultDeserializer extends Deserializer {
  _readHostObject() {
    const typeIndex = this.readUint32();
    const ctor = arrayBufferViewTypes[typeIndex];
    if (!ctor) throw badData();
    const byteLength = this.readUint32();
    const bytes = this.readRawBytes(byteLength);
    const copy = new Uint8Array(byteLength);
    copy.set(bytes);
    if (ctor === Buffer) return Buffer.from(copy.buffer, 0, byteLength);
    if (ctor === DataView) return new DataView(copy.buffer, 0, byteLength);
    return new ctor(copy.buffer, 0, byteLength / (ctor.BYTES_PER_ELEMENT || 1));
  }
}

function serialize(value) {
  const ser = new DefaultSerializer();
  ser.writeHeader();
  ser.writeValue(value);
  return ser.releaseBuffer();
}

function deserialize(buffer) {
  const der = new DefaultDeserializer(buffer);
  der.readHeader();
  return der.readValue();
}

const getHeapStatistics = native.getHeapStatistics;
const setFlagsFromString = native.setFlagsFromString;
const startupSnapshot = native.startupSnapshot;

const api = { serialize, deserialize, Serializer, Deserializer, DefaultSerializer, DefaultDeserializer, getHeapStatistics, setFlagsFromString, startupSnapshot };
export { serialize, deserialize, Serializer, Deserializer, DefaultSerializer, DefaultDeserializer, getHeapStatistics, setFlagsFromString, startupSnapshot };
export default api;
`
