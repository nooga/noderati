
// ---- random ------------------------------------------------------------------

function assertOffset(offset, elementSize, length) {
  validateNumber(offset, "offset");
  offset *= elementSize;
  const maxLength = Math.min(length, kMaxLength);
  if (Number.isNaN(offset) || offset > maxLength || offset < 0) {
    throw errOutOfRange("offset", ">= 0 && <= " + maxLength, offset);
  }
  return offset >>> 0;
}

function assertSize(size, elementSize, offset, length) {
  validateNumber(size, "size");
  size *= elementSize;
  if (Number.isNaN(size) || size > kMaxLength || size < 0) {
    throw errOutOfRange("size", ">= 0 && <= " + kMaxLength, size);
  }
  if (size + offset > length) {
    throw errOutOfRange("size + offset", "<= " + length, size + offset);
  }
  return size >>> 0;
}

function randomBytes(size, callback) {
  size = assertSize(size, 1, 0, Infinity);
  if (callback !== undefined) validateFunction(callback, "callback");
  const buf = binding.randomBytes(size);
  if (callback === undefined) return buf;
  defer(() => callback(null, buf));
}

function validateRandomBuf(buf) {
  if (!isAnyArrayBuffer(buf) && !isArrayBufferView(buf)) {
    throw errInvalidArgType("buf", "an instance of ArrayBuffer or ArrayBufferView", buf);
  }
}

function randomFillSync(buf, offset = 0, size) {
  validateRandomBuf(buf);
  const elementSize = buf.BYTES_PER_ELEMENT || 1;
  offset = assertOffset(offset, elementSize, buf.byteLength);
  if (size === undefined) size = buf.byteLength - offset;
  else size = assertSize(size, elementSize, offset, buf.byteLength);
  if (size > 0) binding.randomFill(buf, offset, size);
  return buf;
}

function randomFill(buf, offset, size, callback) {
  validateRandomBuf(buf);
  const elementSize = buf.BYTES_PER_ELEMENT || 1;
  if (typeof offset === "function") {
    callback = offset;
    offset = 0;
    size = buf.byteLength;
  } else if (typeof size === "function") {
    callback = size;
    offset = assertOffset(offset, elementSize, buf.byteLength);
    size = buf.byteLength - offset;
  } else {
    validateFunction(callback, "callback");
    offset = assertOffset(offset, elementSize, buf.byteLength);
    if (size === undefined) size = buf.byteLength - offset;
    else size = assertSize(size, elementSize, offset, buf.byteLength);
  }
  validateFunction(callback, "callback");
  if (size > 0) binding.randomFill(buf, offset, size);
  defer(() => callback(null, buf));
}

const RAND_MAX = 0xFFFFFFFFFFFF; // 2**48 - 1

function randomInt(min, max, callback) {
  const minNotSpecified = typeof max === "undefined" || typeof max === "function";
  if (minNotSpecified) {
    callback = max;
    max = min;
    min = 0;
  }
  const isSync = typeof callback === "undefined";
  if (!isSync) validateFunction(callback, "callback");
  if (!Number.isSafeInteger(min)) throw errInvalidArgType("min", "a safe integer", min);
  if (!Number.isSafeInteger(max)) throw errInvalidArgType("max", "a safe integer", max);
  if (max <= min) {
    throw errOutOfRange("max", 'greater than the value of "min" (' + min + ")", max);
  }
  const range = max - min;
  if (!(range <= RAND_MAX)) {
    throw errOutOfRange("max" + (minNotSpecified ? "" : " - min"), "<= " + RAND_MAX, range);
  }
  // Rejection sampling over 48-bit values, as node does.
  const randLimit = RAND_MAX - (RAND_MAX % range);
  let n;
  for (;;) {
    const b = binding.randomBytes(6);
    const x = b.readUIntBE ? b.readUIntBE(0, 6) : (b[0] * 2 ** 40 + b[1] * 2 ** 32 + b[2] * 2 ** 24 + b[3] * 2 ** 16 + b[4] * 2 ** 8 + b[5]);
    if (x < randLimit) {
      n = (x % range) + min;
      break;
    }
  }
  if (isSync) return n;
  defer(() => callback(undefined, n));
}

function hex2(b) {
  return (b < 16 ? "0" : "") + b.toString(16);
}

function formatUUID(b) {
  let s = "";
  for (let i = 0; i < 16; i++) {
    if (i === 4 || i === 6 || i === 8 || i === 10) s += "-";
    s += hex2(b[i]);
  }
  return s;
}

function randomUUID(options) {
  if (options !== undefined) validateObject(options, "options");
  if (options && options.disableEntropyCache !== undefined) {
    validateBoolean(options.disableEntropyCache, "options.disableEntropyCache");
  }
  const b = binding.randomBytes(16);
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  return formatUUID(b);
}

let lastV7Ms = 0;
let lastV7Seq = 0;

function randomUUIDv7(options) {
  if (options !== undefined) validateObject(options, "options");
  if (options && options.disableEntropyCache !== undefined) {
    validateBoolean(options.disableEntropyCache, "options.disableEntropyCache");
  }
  const b = binding.randomBytes(16);
  let ms = Date.now();
  // Keep ids monotonic within a millisecond via the 12-bit rand_a field.
  if (ms <= lastV7Ms) {
    ms = lastV7Ms;
    lastV7Seq = (lastV7Seq + 1) & 0xfff;
    if (lastV7Seq === 0) ms = ++lastV7Ms;
  } else {
    lastV7Seq = ((b[6] & 0x0f) << 8) | b[7];
    lastV7Seq &= 0x7ff;
    lastV7Ms = ms;
  }
  b[0] = Math.floor(ms / 2 ** 40) & 0xff;
  b[1] = Math.floor(ms / 2 ** 32) & 0xff;
  b[2] = (ms >>> 24) & 0xff;
  b[3] = (ms >>> 16) & 0xff;
  b[4] = (ms >>> 8) & 0xff;
  b[5] = ms & 0xff;
  b[6] = 0x70 | ((lastV7Seq >> 8) & 0x0f);
  b[7] = lastV7Seq & 0xff;
  b[8] = (b[8] & 0x3f) | 0x80;
  return formatUUID(b);
}

function isIntegerTypedArray(v) {
  return v instanceof Int8Array || v instanceof Uint8Array || v instanceof Uint8ClampedArray ||
    v instanceof Int16Array || v instanceof Uint16Array || v instanceof Int32Array ||
    v instanceof Uint32Array || v instanceof BigInt64Array || v instanceof BigUint64Array;
}

function getRandomValues(data) {
  if (!isIntegerTypedArray(data)) {
    throw domException("The data argument must be an integer-type TypedArray", "TypeMismatchError");
  }
  if (data.byteLength > 65536) {
    const msg = "The requested length exceeds 65,536 bytes";
    if (typeof QuotaExceededError === "function") throw new QuotaExceededError(msg);
    const e = domException(msg, "QuotaExceededError");
    e.quota = null;
    e.requested = null;
    throw e;
  }
  if (data.byteLength > 0) binding.randomFill(data, 0, data.byteLength);
  return data;
}

// ---- KDFs --------------------------------------------------------------------

function pbkdf2Check(password, salt, iterations, keylen, digest) {
  validateString(digest, "digest");
  password = getArrayBufferOrView(password, "password");
  salt = getArrayBufferOrView(salt, "salt");
  validateInt32(iterations, "iterations", 1);
  validateInt32(keylen, "keylen", 0);
  return { password, salt, iterations, keylen, digest };
}

function pbkdf2(password, salt, iterations, keylen, digest, callback) {
  if (typeof digest === "function") {
    callback = digest;
    digest = undefined;
  }
  const a = pbkdf2Check(password, salt, iterations, keylen, digest);
  validateFunction(callback, "callback");
  if (a.keylen === 0) {
    defer(() => callback(new Error("PBKDF2 derivation failed")));
    return;
  }
  binding.pbkdf2(a.password, a.salt, a.iterations, a.keylen, a.digest, (err, key) => {
    if (err) return callback(err);
    callback(null, key);
  });
}

function pbkdf2Sync(password, salt, iterations, keylen, digest) {
  const a = pbkdf2Check(password, salt, iterations, keylen, digest);
  if (a.keylen === 0) throw new Error("PBKDF2 derivation failed");
  return binding.pbkdf2(a.password, a.salt, a.iterations, a.keylen, a.digest);
}

const scryptDefaults = { N: 16384, r: 8, p: 1, maxmem: 32 << 20 };

function errIncompatiblePair(a, b) {
  return makeError(TypeError, "ERR_INCOMPATIBLE_OPTION_PAIR",
    'Option "' + a + '" cannot be used in combination with option "' + b + '"');
}

function scryptCheck(password, salt, keylen, options) {
  password = getArrayBufferOrView(password, "password");
  salt = getArrayBufferOrView(salt, "salt");
  validateInt32(keylen, "keylen", 0);
  let { N, r, p, maxmem } = scryptDefaults;
  if (options && options !== scryptDefaults) {
    const hasN = options.N !== undefined;
    if (hasN) {
      N = options.N;
      validateUint32(N, "N");
    }
    if (options.cost !== undefined) {
      if (hasN) throw errIncompatiblePair("N", "cost");
      N = options.cost;
      validateUint32(N, "cost");
    }
    const hasR = options.r !== undefined;
    if (hasR) {
      r = options.r;
      validateUint32(r, "r");
    }
    if (options.blockSize !== undefined) {
      if (hasR) throw errIncompatiblePair("r", "blockSize");
      r = options.blockSize;
      validateUint32(r, "blockSize");
    }
    const hasP = options.p !== undefined;
    if (hasP) {
      p = options.p;
      validateUint32(p, "p");
    }
    if (options.parallelization !== undefined) {
      if (hasP) throw errIncompatiblePair("p", "parallelization");
      p = options.parallelization;
      validateUint32(p, "parallelization");
    }
    if (options.maxmem !== undefined) {
      maxmem = options.maxmem;
      validateInteger(maxmem, "maxmem", 0);
    }
    if (N === 0) N = scryptDefaults.N;
    if (r === 0) r = scryptDefaults.r;
    if (p === 0) p = scryptDefaults.p;
    if (maxmem === 0) maxmem = scryptDefaults.maxmem;
  }
  // OpenSSL's checks: N a power of two > 1, r*p < 2^30, memory in bounds.
  if (N < 2 || (N & (N - 1)) !== 0 || r * p >= 2 ** 30) {
    throw makeError(RangeError, "ERR_CRYPTO_INVALID_SCRYPT_PARAMS", "Invalid scrypt params");
  }
  const mem = 128 * N * r + 128 * r * p + 256 * r;
  if (mem > maxmem) {
    throw makeError(RangeError, "ERR_CRYPTO_INVALID_SCRYPT_PARAMS",
      "Invalid scrypt params: error:030000AC:digital envelope routines::memory limit exceeded");
  }
  return { password, salt, keylen, N, r, p };
}

function scrypt(password, salt, keylen, options, callback = scryptDefaults) {
  if (callback === scryptDefaults) {
    callback = options;
    options = scryptDefaults;
  }
  const a = scryptCheck(password, salt, keylen, options);
  validateFunction(callback, "callback");
  binding.scrypt(a.password, a.salt, a.N, a.r, a.p, a.keylen, (err, key) => {
    if (err) return callback(err);
    callback(null, key);
  });
}

function scryptSync(password, salt, keylen, options = scryptDefaults) {
  const a = scryptCheck(password, salt, keylen, options);
  return binding.scrypt(a.password, a.salt, a.N, a.r, a.p, a.keylen);
}

function prepareHkdfKey(key) {
  if (isKeyObject(key)) {
    if (key.type !== "secret") throw errInvalidKeyObjectType(key.type, "secret");
    return key[kKeyData];
  }
  if (isCryptoKey(key)) return prepareHkdfKey(key[kKeyObject]);
  if (typeof key === "string") return Buffer.from(key, "utf8");
  if (isAnyArrayBuffer(key) || isArrayBufferView(key)) return toU8(key);
  throw errInvalidArgType("ikm", "of type string or an instance of SecretKeyObject, ArrayBuffer, TypedArray, DataView, or Buffer", key);
}

function hkdfCheck(hash, key, salt, info, length) {
  validateString(hash, "digest");
  key = prepareHkdfKey(key);
  salt = getArrayBufferOrView(salt, "salt");
  info = getArrayBufferOrView(info, "info");
  validateInteger(length, "length", 0, kMaxLength);
  if (info.byteLength > 1024) {
    throw errOutOfRange("info", "must not contain more than 1024 bytes", info.byteLength);
  }
  const d = binding.digestInfo(hash);
  if (!d || JSON.parse(d).xof) throw makeError(TypeError, "ERR_CRYPTO_INVALID_DIGEST", "Invalid digest: " + hash);
  if (length > 255 * JSON.parse(d).size) {
    throw makeError(RangeError, "ERR_CRYPTO_INVALID_KEYLEN", "Invalid key length");
  }
  return { hash, key, salt, info, length };
}

function hkdf(hash, key, salt, info, length, callback) {
  const a = hkdfCheck(hash, key, salt, info, length);
  validateFunction(callback, "callback");
  binding.hkdf(a.hash, a.key, a.salt, a.info, a.length, (err, bits) => {
    if (err) return callback(err);
    callback(null, bits);
  });
}

function hkdfSync(hash, key, salt, info, length) {
  const a = hkdfCheck(hash, key, salt, info, length);
  return binding.hkdf(a.hash, a.key, a.salt, a.info, a.length);
}

function argon2Check(algorithm, parameters) {
  validateString(algorithm, "algorithm");
  validateOneOf(algorithm, "algorithm", ["argon2d", "argon2i", "argon2id"]);
  validateObject(parameters, "parameters");
  const message = getArrayBufferOrView(parameters.message, "parameters.message");
  const nonce = getArrayBufferOrView(parameters.nonce, "parameters.nonce");
  validateInteger(nonce.byteLength, "parameters.nonce.byteLength", 8, kMaxUint32);
  const { parallelism, tagLength, memory, passes } = parameters;
  validateInteger(parallelism, "parameters.parallelism", 1, 2 ** 24 - 1);
  validateInteger(tagLength, "parameters.tagLength", 4, kMaxUint32);
  validateInteger(memory, "parameters.memory", 8 * parallelism, kMaxUint32);
  validateInteger(passes, "parameters.passes", 1, kMaxUint32);
  let secret, associatedData;
  if (parameters.secret !== undefined) {
    secret = getArrayBufferOrView(parameters.secret, "parameters.secret");
    validateInteger(secret.byteLength, "parameters.secret.byteLength", 0, kMaxUint32);
  }
  if (parameters.associatedData !== undefined) {
    associatedData = getArrayBufferOrView(parameters.associatedData, "parameters.associatedData");
    validateInteger(associatedData.byteLength, "parameters.associatedData.byteLength", 0, kMaxUint32);
  }
  return [algorithm, message, nonce, secret, associatedData, parallelism, tagLength, memory, passes];
}

function argon2(algorithm, parameters, callback) {
  const a = argon2Check(algorithm, parameters);
  validateFunction(callback, "callback");
  a.push((err, key) => {
    if (err) return callback(err);
    callback(null, key);
  });
  binding.argon2.apply(binding, a);
}

function argon2Sync(algorithm, parameters) {
  // Not `return binding.argon2(...argon2Check(...))`: paserati returns the
  // spread array itself from `return obj.m(...call())`.
  const a = argon2Check(algorithm, parameters);
  return binding.argon2.apply(binding, a);
}

function timingSafeEqual(buf1, buf2) {
  // node's native check reports these without a "Received ..." suffix.
  if (!isAnyArrayBuffer(buf1) && !isArrayBufferView(buf1)) {
    throw makeError(TypeError, "ERR_INVALID_ARG_TYPE", 'The "buf1" argument must be ' + kBinaryTypes + ".");
  }
  if (!isAnyArrayBuffer(buf2) && !isArrayBufferView(buf2)) {
    throw makeError(TypeError, "ERR_INVALID_ARG_TYPE", 'The "buf2" argument must be ' + kBinaryTypes + ".");
  }
  const a = toU8(buf1);
  const b = toU8(buf2);
  if (a.byteLength !== b.byteLength) {
    throw makeError(RangeError, "ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH", "Input buffers must have the same byte length");
  }
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ b[i];
  return diff === 0;
}
