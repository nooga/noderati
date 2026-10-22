
// ---- sign / verify ---------------------------------------------------------------------

function getIntOption(name, options) {
  const value = options[name];
  if (value !== undefined) {
    if (value === value >> 0) return value;
    throw errInvalidArgValue("options." + name, value);
  }
  return undefined;
}

function signingOptions(key) {
  if (key === null || typeof key !== "object" || isKeyObject(key) || isCryptoKey(key) || isArrayBufferView(key)) {
    return {};
  }
  const padding = getIntOption("padding", key);
  const saltLength = getIntOption("saltLength", key);
  let dsaEncoding = key.dsaEncoding;
  if (dsaEncoding !== undefined && dsaEncoding !== "der" && dsaEncoding !== "ieee-p1363") {
    throw errInvalidArgValue("options.dsaEncoding", dsaEncoding);
  }
  let context;
  if (key.context !== undefined) context = getArrayBufferOrView(key.context, "options.context");
  return { padding, saltLength, dsaEncoding, context };
}

function asPrivateKeyObject(key) {
  if (isCryptoKey(key)) key = key[kKeyObject];
  if (key !== null && typeof key === "object" && !isKeyObject(key) && !isArrayBufferView(key) && (isKeyObject(key.key) || isCryptoKey(key.key))) {
    key = isCryptoKey(key.key) ? key.key[kKeyObject] : key.key;
  }
  if (isKeyObject(key)) {
    if (key.type !== "private") throw errInvalidKeyObjectType(key.type, "private");
    return key;
  }
  return createPrivateKey(key);
}

function asPublicOrPrivateKeyObject(key) {
  if (isCryptoKey(key)) key = key[kKeyObject];
  if (key !== null && typeof key === "object" && !isKeyObject(key) && !isArrayBufferView(key) && (isKeyObject(key.key) || isCryptoKey(key.key))) {
    key = isCryptoKey(key.key) ? key.key[kKeyObject] : key.key;
  }
  if (isKeyObject(key)) {
    if (key.type === "secret") throw errInvalidKeyObjectType(key.type, "public or private");
    return key;
  }
  // a private key input stays private (verify works with either)
  try {
    return createPrivateKey(key);
  } catch {
    return createPublicKey(key);
  }
}

function errSignKeyRequired() {
  return makeError(Error, "ERR_CRYPTO_SIGN_KEY_REQUIRED", "No key provided to sign");
}

function doSign(algorithm, data, key, callback) {
  const opts = signingOptions(key);
  const ko = asPrivateKeyObject(key);
  return binding.sign(ko[kHandle], data, algorithm == null ? "" : algorithm,
    opts.padding, opts.saltLength, opts.dsaEncoding, opts.context, callback);
}

function doVerify(algorithm, data, key, signature, callback) {
  const opts = signingOptions(key);
  const ko = asPublicOrPrivateKeyObject(key);
  return binding.verify(ko[kHandle], data, signature, algorithm == null ? "" : algorithm,
    opts.padding, opts.saltLength, opts.dsaEncoding, opts.context, callback);
}

function checkSignDigest(algorithm) {
  if (algorithm == null) return;
  validateString(algorithm, "algorithm");
  const d = binding.digestInfo(algorithm);
  if (!d || JSON.parse(d).xof) throw makeError(TypeError, "ERR_CRYPTO_INVALID_DIGEST", "Invalid digest: " + algorithm);
}

function sign(algorithm, data, key, callback) {
  if (algorithm != null) validateString(algorithm, "algorithm");
  if (callback !== undefined) validateFunction(callback, "callback");
  data = getArrayBufferOrView(data, "data");
  if (!key) throw errSignKeyRequired();
  checkSignDigest(algorithm);
  if (callback === undefined) return doSign(algorithm, data, key);
  doSign(algorithm, data, key, (err, sig) => callback(err, sig));
}

function verify(algorithm, data, key, signature, callback) {
  if (algorithm != null) validateString(algorithm, "algorithm");
  if (callback !== undefined) validateFunction(callback, "callback");
  data = getArrayBufferOrView(data, "data");
  if (!isArrayBufferView(data)) throw errInvalidArgType("data", "an instance of Buffer, TypedArray, or DataView", data);
  if (!key) throw errSignKeyRequired();
  signature = getArrayBufferOrView(signature, "signature");
  checkSignDigest(algorithm);
  if (callback === undefined) return doVerify(algorithm, data, key, signature);
  doVerify(algorithm, data, key, signature, (err, ok) => callback(err, ok));
}

function signUpdate(data, encoding) {
  if (this[kState].done) throw makeError(Error, "ERR_CRYPTO_INVALID_STATE", "Not initialised");
  this[kState].chunks.push(Buffer.from(decodeInput(data, encoding)));
  return this;
}

function signWrite(chunk, encoding, callback) {
  this.update(chunk, encoding);
  callback();
}

function Sign(algorithm, options) {
  if (!new.target) return new Sign(algorithm, options);
  validateString(algorithm, "algorithm");
  const d = binding.digestInfo(algorithm);
  if (!d) throw makeError(TypeError, "ERR_CRYPTO_INVALID_DIGEST", "Invalid digest");
  const self = Reflect.construct(Writable, [options], new.target);
  self[kState] = { algorithm, chunks: [], done: false };
  return self;
}
inheritStream(Sign, Writable);
Sign.prototype._write = signWrite;
Sign.prototype.update = signUpdate;
Sign.prototype.sign = function sign(privateKey, encoding) {
  if (!privateKey) throw errSignKeyRequired();
  const st = this[kState];
  if (st.done) throw makeError(Error, "ERR_CRYPTO_INVALID_STATE", "Not initialised");
  const sig = doSign(st.algorithm, Buffer.concat(st.chunks), privateKey);
  st.done = true;
  return encodeOutput(sig, encoding);
};

function Verify(algorithm, options) {
  if (!new.target) return new Verify(algorithm, options);
  validateString(algorithm, "algorithm");
  const d = binding.digestInfo(algorithm);
  if (!d) throw makeError(TypeError, "ERR_CRYPTO_INVALID_DIGEST", "Invalid digest");
  const self = Reflect.construct(Writable, [options], new.target);
  self[kState] = { algorithm, chunks: [], done: false };
  return self;
}
inheritStream(Verify, Writable);
Verify.prototype._write = signWrite;
Verify.prototype.update = signUpdate;
Verify.prototype.verify = function verify(object, signature, sigEncoding) {
  const st = this[kState];
  if (st.done) throw makeError(Error, "ERR_CRYPTO_INVALID_STATE", "Not initialised");
  signature = getArrayBufferOrView(signature, "signature", sigEncoding);
  const ok = doVerify(st.algorithm, Buffer.concat(st.chunks), object, signature);
  st.done = true;
  return ok;
};

function createSign(algorithm, options) {
  return new Sign(algorithm, options);
}

function createVerify(algorithm, options) {
  return new Verify(algorithm, options);
}

// ---- RSA encryption ---------------------------------------------------------------------

function rsaCryptFn(op, needPrivate) {
  return function (key, buffer) {
    let padding, oaepHash, oaepLabel;
    if (key !== null && typeof key === "object" && !isKeyObject(key) && !isCryptoKey(key) && !isArrayBufferView(key)) {
      padding = key.padding;
      if (padding !== undefined && padding !== (padding | 0)) throw errInvalidArgValue("options.padding", padding);
      oaepHash = key.oaepHash;
      if (oaepHash !== undefined) validateString(oaepHash, "key.oaepHash");
      if (key.oaepLabel !== undefined) oaepLabel = getArrayBufferOrView(key.oaepLabel, "key.oaepLabel", key.encoding);
    }
    const ko = needPrivate ? asPrivateKeyObject(key) : asPublicOrPrivateKeyObject(key);
    buffer = getArrayBufferOrView(buffer, "buffer", key && key.encoding);
    return binding.rsaCrypt(op, ko[kHandle], buffer, padding, oaepHash, oaepLabel);
  };
}

const publicEncrypt = rsaCryptFn("pubenc", false);
const publicDecrypt = rsaCryptFn("pubdec", false);
const privateEncrypt = rsaCryptFn("privenc", true);
const privateDecrypt = rsaCryptFn("privdec", true);

// ---- Diffie-Hellman -------------------------------------------------------------------

function diffieHellman(options, callback) {
  validateObject(options, "options");
  if (callback !== undefined) validateFunction(callback, "callback");
  const { privateKey, publicKey } = options;
  if (!isKeyObject(privateKey) && !isCryptoKey(privateKey)) {
    throw errInvalidArgValue("options.privateKey", privateKey);
  }
  if (!isKeyObject(publicKey) && !isCryptoKey(publicKey)) {
    throw errInvalidArgValue("options.publicKey", publicKey);
  }
  const priv = isCryptoKey(privateKey) ? privateKey[kKeyObject] : privateKey;
  const pub = isCryptoKey(publicKey) ? publicKey[kKeyObject] : publicKey;
  if (priv.type !== "private") throw errInvalidKeyObjectType(priv.type, "private");
  if (pub.type !== "public" && pub.type !== "private") throw errInvalidKeyObjectType(pub.type, "public or private");
  if (callback === undefined) return binding.diffieHellman(priv[kHandle], pub[kHandle]);
  let secret, err = null;
  try {
    secret = binding.diffieHellman(priv[kHandle], pub[kHandle]);
  } catch (e) {
    err = e;
  }
  defer(() => callback(err, secret));
}

function validateECDHFormat(format) {
  if (format === undefined || format === null) return "uncompressed";
  if (format === "compressed" || format === "uncompressed" || format === "hybrid") return format;
  throw makeError(TypeError, "ERR_CRYPTO_ECDH_INVALID_FORMAT", "Invalid ECDH format: " + format);
}

function ECDH(curve) {
  if (!new.target) return new ECDH(curve);
  validateString(curve, "curve");
  if (!getCurves().includes(curve)) {
    throw makeError(TypeError, "ERR_CRYPTO_INVALID_CURVE", "Invalid EC curve name");
  }
  this[kState] = { curve, priv: null, pub: null };
}

ECDH.prototype.generateKeys = function generateKeys(encoding, format) {
  const st = this[kState];
  st.priv = binding.ecdhGenerate(st.curve);
  st.pub = binding.ecdhPublicFromPrivate(st.curve, st.priv);
  return this.getPublicKey(encoding, format);
};

ECDH.prototype.computeSecret = function computeSecret(key, inEnc, outEnc) {
  const st = this[kState];
  key = getArrayBufferOrView(key, "key", inEnc);
  if (!st.priv) throw makeError(Error, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation computeSecret");
  return encodeOutput(binding.ecdhCompute(st.curve, st.priv, key), outEnc);
};

ECDH.prototype.getPrivateKey = function getPrivateKey(encoding) {
  const st = this[kState];
  if (!st.priv) throw makeError(Error, "ERR_CRYPTO_OPERATION_FAILED", "Failed to get ECDH private key");
  return encodeOutput(Buffer.from(st.priv), encoding);
};

ECDH.prototype.getPublicKey = function getPublicKey(encoding, format) {
  const st = this[kState];
  const f = validateECDHFormat(format);
  if (!st.pub) throw makeError(Error, "ERR_CRYPTO_OPERATION_FAILED", "Failed to get ECDH public key");
  return encodeOutput(binding.ecPointConvert(st.curve, st.pub, f), encoding);
};

ECDH.prototype.setPrivateKey = function setPrivateKey(key, encoding) {
  const st = this[kState];
  key = getArrayBufferOrView(key, "key", encoding);
  st.pub = binding.ecdhPublicFromPrivate(st.curve, key);
  st.priv = Buffer.from(key);
};

ECDH.prototype.setPublicKey = function setPublicKey(key, encoding) {
  const st = this[kState];
  key = getArrayBufferOrView(key, "key", encoding);
  st.pub = binding.ecPointConvert(st.curve, key, "uncompressed");
};

ECDH.convertKey = function convertKey(key, curve, inEnc, outEnc, format) {
  validateString(curve, "curve");
  key = getArrayBufferOrView(key, "key", inEnc);
  const f = validateECDHFormat(format);
  return encodeOutput(binding.ecPointConvert(curve, key, f), outEnc);
};

function createECDH(curve) {
  return new ECDH(curve);
}

function toBufferArg(v, name, enc) {
  if (typeof v === "number") {
    // generator given as a number
    const bytes = [];
    let n = v;
    while (n > 0) {
      bytes.unshift(n & 0xff);
      n = Math.floor(n / 256);
    }
    return Buffer.from(bytes.length ? bytes : [0]);
  }
  return Buffer.from(getArrayBufferOrView(v, name, enc));
}

function stripLeadingZeros(b) {
  let i = 0;
  while (i < b.length - 1 && b[i] === 0) i++;
  return b.subarray(i);
}

function dhInit(self, prime, generator) {
  self[kState] = { prime: stripLeadingZeros(prime), generator: stripLeadingZeros(generator), priv: null, pub: null };
  Object.defineProperty(self, "verifyError", {
    value: binding.dhVerifyError(self[kState].prime, self[kState].generator),
    enumerable: true,
    configurable: true,
  });
}

function DiffieHellman(sizeOrKey, keyEncoding, generator, genEncoding) {
  if (!new.target) return new DiffieHellman(sizeOrKey, keyEncoding, generator, genEncoding);
  if (typeof sizeOrKey !== "number" && typeof sizeOrKey !== "string" && !isArrayBufferView(sizeOrKey) && !isAnyArrayBuffer(sizeOrKey)) {
    throw errInvalidArgType("sizeOrKey", "of type number or string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView", sizeOrKey);
  }
  if (typeof keyEncoding === "number" || isArrayBufferView(keyEncoding)) {
    genEncoding = generator;
    generator = keyEncoding;
    keyEncoding = false;
  }
  if (generator === undefined) generator = 2;
  let prime;
  if (typeof sizeOrKey === "number") {
    validateInt32(sizeOrKey, "sizeOrKey");
    prime = Buffer.from(binding.generatePrime(sizeOrKey, undefined, undefined, true));
  } else {
    prime = toBufferArg(sizeOrKey, "sizeOrKey", keyEncoding);
  }
  dhInit(this, prime, toBufferArg(generator, "generator", genEncoding));
}

function dhGenerateKeys(encoding) {
  const st = this[kState];
  if (!st.priv) st.priv = binding.dhGenerateKey(st.prime);
  st.pub = stripLeadingZeros(binding.modPow(st.generator, st.priv, st.prime));
  return encodeOutput(Buffer.from(st.pub), encoding);
}

function dhComputeSecret(key, inEnc, outEnc) {
  const st = this[kState];
  key = Buffer.from(getArrayBufferOrView(key, "key", inEnc));
  const k = stripLeadingZeros(key);
  const p = st.prime;
  const cmp = (a, b) => (a.length !== b.length ? a.length - b.length : compareBytes(a, b));
  if (k.length === 1 && k[0] <= 1) {
    throw makeError(RangeError, "ERR_CRYPTO_INVALID_KEYLEN", "Supplied key is too small");
  }
  const pm1 = Buffer.from(p);
  for (let i = pm1.length - 1; i >= 0; i--) {
    if (pm1[i] > 0) { pm1[i]--; break; }
    pm1[i] = 0xff;
  }
  if (cmp(k, stripLeadingZeros(pm1)) >= 0) {
    throw makeError(RangeError, "ERR_CRYPTO_INVALID_KEYLEN", "Supplied key is too large");
  }
  return encodeOutput(binding.modPow(k, st.priv, p), outEnc);
}

function dhGetter(field, what) {
  return function (encoding) {
    const v = this[kState][field];
    if (!v) throw makeError(Error, "ERR_CRYPTO_INVALID_STATE", "No " + what + " - did you forget to generate one?");
    return encodeOutput(Buffer.from(v), encoding);
  };
}

DiffieHellman.prototype.generateKeys = dhGenerateKeys;
DiffieHellman.prototype.computeSecret = dhComputeSecret;
DiffieHellman.prototype.getPrime = dhGetter("prime", "prime");
DiffieHellman.prototype.getGenerator = dhGetter("generator", "generator");
DiffieHellman.prototype.getPublicKey = dhGetter("pub", "public key");
DiffieHellman.prototype.getPrivateKey = dhGetter("priv", "private key");
DiffieHellman.prototype.setPublicKey = function setPublicKey(key, encoding) {
  this[kState].pub = stripLeadingZeros(Buffer.from(getArrayBufferOrView(key, "key", encoding)));
  return this;
};
DiffieHellman.prototype.setPrivateKey = function setPrivateKey(key, encoding) {
  this[kState].priv = stripLeadingZeros(Buffer.from(getArrayBufferOrView(key, "key", encoding)));
  return this;
};

function DiffieHellmanGroup(name) {
  if (!new.target) return new DiffieHellmanGroup(name);
  validateString(name, "name");
  const prime = binding.dhGroup(name);
  if (!prime) throw makeError(Error, "ERR_CRYPTO_UNKNOWN_DH_GROUP", "Unknown DH group");
  dhInit(this, prime, Buffer.from([2]));
}
DiffieHellmanGroup.prototype.generateKeys = dhGenerateKeys;
DiffieHellmanGroup.prototype.computeSecret = dhComputeSecret;
DiffieHellmanGroup.prototype.getPrime = DiffieHellman.prototype.getPrime;
DiffieHellmanGroup.prototype.getGenerator = DiffieHellman.prototype.getGenerator;
DiffieHellmanGroup.prototype.getPublicKey = DiffieHellman.prototype.getPublicKey;
DiffieHellmanGroup.prototype.getPrivateKey = DiffieHellman.prototype.getPrivateKey;

function createDiffieHellman(sizeOrKey, keyEncoding, generator, genEncoding) {
  return new DiffieHellman(sizeOrKey, keyEncoding, generator, genEncoding);
}

function getDiffieHellman(name) {
  return new DiffieHellmanGroup(name);
}

const createDiffieHellmanGroup = getDiffieHellman;

// ---- primes ------------------------------------------------------------------------

function bigintToBytes(n) {
  let hex = n.toString(16);
  if (hex.length % 2) hex = "0" + hex;
  return Buffer.from(hex, "hex");
}

function primeCandidate(candidate) {
  if (typeof candidate === "bigint") {
    if (candidate < 0n) throw errOutOfRange("candidate", ">= 0", candidate);
    return bigintToBytes(candidate);
  }
  if (!isAnyArrayBuffer(candidate) && !isArrayBufferView(candidate)) {
    throw errInvalidArgType("candidate", "of type bigint or an instance of ArrayBuffer, TypedArray, Buffer, or DataView", candidate);
  }
  return toU8(candidate);
}

function primeChecks(options) {
  if (options === undefined) return 0;
  validateObject(options, "options");
  const { checks = 0 } = options;
  validateInt32(checks, "options.checks", 0);
  return checks;
}

function checkPrimeSync(candidate, options = {}) {
  const c = primeCandidate(candidate);
  return binding.checkPrime(c, primeChecks(options));
}

function checkPrime(candidate, options = {}, callback) {
  if (typeof options === "function") {
    callback = options;
    options = {};
  }
  const c = primeCandidate(candidate);
  const checks = primeChecks(options);
  validateFunction(callback, "callback");
  binding.checkPrime(c, checks, (err, ok) => callback(err || undefined, ok));
}

function primeOptions(size, options) {
  validateInt32(size, "size", 1);
  validateObject(options, "options");
  let { safe = false, bigint = false, add, rem } = options;
  validateBoolean(safe, "options.safe");
  validateBoolean(bigint, "options.bigint");
  const conv = (v, name) => {
    if (v === undefined) return undefined;
    if (typeof v === "bigint") return bigintToBytes(v);
    if (!isAnyArrayBuffer(v) && !isArrayBufferView(v)) {
      throw errInvalidArgType(name, "of type bigint or an instance of ArrayBuffer, TypedArray, Buffer, or DataView", v);
    }
    return toU8(v);
  };
  return { add: conv(add, "options.add"), rem: conv(rem, "options.rem"), safe, bigint };
}

function primeResult(ab, bigint) {
  if (!bigint) return ab;
  const hex = Buffer.from(ab).toString("hex");
  return hex ? BigInt("0x" + hex) : 0n;
}

function generatePrimeSync(size, options = {}) {
  const o = primeOptions(size, options);
  return primeResult(binding.generatePrime(size, o.add, o.rem, o.safe), o.bigint);
}

function generatePrime(size, options, callback) {
  if (typeof options === "function") {
    callback = options;
    options = {};
  }
  const o = primeOptions(size, options === undefined ? {} : options);
  validateFunction(callback, "callback");
  binding.generatePrime(size, o.add, o.rem, o.safe, (err, ab) => {
    if (err) return callback(err);
    callback(undefined, primeResult(ab, o.bigint));
  });
}

// ---- KEM -----------------------------------------------------------------------------

function encapsulate(key, callback) {
  if (callback !== undefined) validateFunction(callback, "callback");
  const ko = asPublicOrPrivateKeyObject(key);
  const run = () => {
    const [sharedKey, ciphertext] = binding.encapsulate(ko[kHandle]);
    return { sharedKey, ciphertext };
  };
  if (callback === undefined) return run();
  let res, err = null;
  try {
    res = run();
  } catch (e) {
    err = e;
  }
  defer(() => callback(err, res));
}

function decapsulate(key, ciphertext, callback) {
  if (callback !== undefined) validateFunction(callback, "callback");
  const ko = asPrivateKeyObject(key);
  ciphertext = getArrayBufferOrView(ciphertext, "ciphertext");
  if (callback === undefined) return binding.decapsulate(ko[kHandle], ciphertext);
  let res, err = null;
  try {
    res = binding.decapsulate(ko[kHandle], ciphertext);
  } catch (e) {
    err = e;
  }
  defer(() => callback(err, res));
}
