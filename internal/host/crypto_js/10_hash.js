
// ---- Hash / Hmac -------------------------------------------------------------

// Hash and Hmac are callable without `new` like node's, yet are real
// subclasses of stream.Transform: the constructor builds the Transform via
// Reflect.construct with new.target, so the instance gets the subclass's
// prototype.
function makeStream(newTarget, options) {
  return Reflect.construct(Transform, [options], newTarget);
}

function errHashFinalized() {
  return makeError(Error, "ERR_CRYPTO_HASH_FINALIZED", "Digest already called");
}

function hashUpdateData(data, encoding) {
  return decodeInput(data, encoding);
}

function Hash(algorithm, options) {
  if (!new.target) return new Hash(algorithm, options);
  const copyFrom = algorithm instanceof Hash ? algorithm : null;
  if (!copyFrom) validateString(algorithm, "algorithm");
  const xofLen = typeof options === "object" && options !== null ? options.outputLength : undefined;
  if (xofLen !== undefined) validateUint32(xofLen, "options.outputLength");
  const self = makeStream(new.target, options);
  self[kHandle] = copyFrom
    ? binding.hashCopy(copyFrom[kHandle], xofLen === undefined ? -1 : xofLen)
    : binding.hashCreate(algorithm, xofLen === undefined ? -1 : xofLen);
  self[kState] = { finalized: false };
  return self;
}
inheritStream(Hash, Transform);

Hash.prototype.copy = function copy(options) {
  if (this[kState].finalized) throw errHashFinalized();
  return new Hash(this, options);
};

Hash.prototype._transform = function _transform(chunk, encoding, callback) {
  binding.hashUpdate(this[kHandle], hashUpdateData(chunk, encoding));
  callback();
};

Hash.prototype._flush = function _flush(callback) {
  this.push(binding.hashDigest(this[kHandle]));
  callback();
};

Hash.prototype.update = function update(data, encoding) {
  if (this[kState].finalized) throw errHashFinalized();
  binding.hashUpdate(this[kHandle], hashUpdateData(data, encoding));
  return this;
};

Hash.prototype.digest = function digest(outputEncoding) {
  if (this[kState].finalized) throw errHashFinalized();
  const out = binding.hashDigest(this[kHandle]);
  this[kState].finalized = true;
  return encodeOutput(out, outputEncoding && String(outputEncoding));
};

function Hmac(hmac, key, options) {
  if (!new.target) return new Hmac(hmac, key, options);
  validateString(hmac, "hmac");
  const encoding = options && typeof options === "object" ? options.encoding : undefined;
  const keyBytes = prepareSecretKey(key, encoding, "key");
  const self = makeStream(new.target, options);
  self[kHandle] = binding.hmacCreate(hmac, keyBytes);
  self[kState] = { finalized: false };
  return self;
}
inheritStream(Hmac, Transform);

Hmac.prototype.update = Hash.prototype.update;

Hmac.prototype.digest = function digest(outputEncoding) {
  const state = this[kState];
  const enc = outputEncoding && String(outputEncoding);
  if (state.finalized) return encodeOutput(Buffer.alloc(0), enc);
  state.finalized = true;
  return encodeOutput(binding.hashDigest(this[kHandle]), enc);
};

Hmac.prototype._flush = Hash.prototype._flush;
Hmac.prototype._transform = Hash.prototype._transform;

function createHash(algorithm, options) {
  return new Hash(algorithm, options);
}

function createHmac(hmac, key, options) {
  return new Hmac(hmac, key, options);
}

// crypto.hash(algorithm, data[, outputEncoding]) one-shot digest.
function hash(algorithm, input, outputEncoding = "hex") {
  validateString(algorithm, "algorithm");
  if (typeof input !== "string" && !isArrayBufferView(input)) {
    throw errInvalidArgType("input", "of type string or an instance of Buffer, TypedArray, or DataView", input);
  }
  let outputLength;
  if (outputEncoding !== null && typeof outputEncoding === "object") {
    outputLength = outputEncoding.outputLength;
    outputEncoding = outputEncoding.outputEncoding === undefined ? "hex" : outputEncoding.outputEncoding;
    if (outputLength !== undefined) validateUint32(outputLength, "options.outputLength");
  }
  validateString(outputEncoding, "outputEncoding");
  const enc = outputEncoding.toLowerCase();
  if (enc !== "buffer" && !normalizeEncoding(enc)) {
    throw errInvalidArgValue("outputEncoding", outputEncoding);
  }
  const h = binding.hashCreate(algorithm, outputLength === undefined ? -1 : outputLength);
  binding.hashUpdate(h, typeof input === "string" ? Buffer.from(input, "utf8") : toU8(input));
  return encodeOutput(binding.hashDigest(h), enc);
}

function getHashes() {
  return binding.getHashes();
}
