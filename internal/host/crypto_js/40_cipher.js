
// ---- Cipheriv / Decipheriv ---------------------------------------------------------

// OutputDecoder turns successive output Buffers into strings without
// splitting multi-byte units across update()/final() calls, like the
// StringDecoder node's ciphers use.
class OutputDecoder {
  constructor(encoding) {
    this.encoding = encoding;
    this.carry = Buffer.alloc(0);
  }

  write(buf) {
    let data = this.carry.length ? Buffer.concat([this.carry, buf]) : buf;
    let keep = 0;
    switch (this.encoding) {
      case "base64":
      case "base64url":
        keep = data.length % 3;
        break;
      case "utf16le":
        keep = data.length % 2;
        break;
      case "utf8": {
        // keep an incomplete trailing UTF-8 sequence
        for (let i = 1; i <= Math.min(3, data.length); i++) {
          const b = data[data.length - i];
          if ((b & 0xc0) === 0x80) continue;
          const need = b >= 0xf0 ? 4 : b >= 0xe0 ? 3 : b >= 0xc0 ? 2 : 1;
          if (need > i) keep = i;
          break;
        }
        break;
      }
    }
    this.carry = keep ? Buffer.from(data.subarray(data.length - keep)) : Buffer.alloc(0);
    return encodeOutput(keep ? data.subarray(0, data.length - keep) : data, this.encoding);
  }

  end(buf) {
    let data = this.carry.length ? Buffer.concat([this.carry, buf || Buffer.alloc(0)]) : buf || Buffer.alloc(0);
    this.carry = Buffer.alloc(0);
    return encodeOutput(data, this.encoding);
  }
}

function getDecoder(decoder, encoding) {
  const enc = normalizeEncoding(encoding);
  if (!enc) throw makeError(TypeError, "ERR_UNKNOWN_ENCODING", "Unknown encoding: " + encoding);
  if (decoder && decoder.encoding === enc) return decoder;
  if (decoder) {
    throw makeError(Error, "ERR_CRYPTO_INVALID_STATE", "Cannot change encoding");
  }
  return new OutputDecoder(enc);
}

function decodeInput(data, encoding, name = "data") {
  if (typeof data === "string") {
    const enc = normalizeEncoding(encoding) || "utf8";
    if (enc === "hex" && data.length % 2 !== 0) {
      throw errInvalidArgValue("encoding", encoding, "is invalid for data of length " + data.length);
    }
    return Buffer.from(data, enc);
  }
  if (!isArrayBufferView(data)) {
    throw errInvalidArgType(name, "of type string or an instance of Buffer, TypedArray, or DataView", data);
  }
  return toU8(data);
}

function getUIntOption(options, key) {
  let value;
  if (options && (value = options[key]) != null) {
    if (value >>> 0 !== value) throw errInvalidArgValue("options." + key, value);
    return value;
  }
  return -1;
}

function getStringOption(options, key) {
  let value;
  if (options && (value = options[key]) != null) validateString(value, "options." + key);
  return value;
}

function initCipher(self, cipher, key, iv, options, decipher) {
  validateString(cipher, "cipher");
  const encoding = getStringOption(options, "encoding");
  key = prepareSecretKey(key, encoding);
  iv = iv === null ? null : getArrayBufferOrView(iv, "iv");
  const authTagLength = getUIntOption(options, "authTagLength");
  self[kHandle] = binding.cipherInit(cipher, key, iv, decipher, authTagLength);
  self._decoder = null;
}

function cipherUpdate(data, inputEncoding, outputEncoding) {
  const input = decodeInput(data, inputEncoding);
  const ret = binding.cipherUpdate(this[kHandle], input);
  if (outputEncoding && outputEncoding !== "buffer") {
    this._decoder = getDecoder(this._decoder, outputEncoding);
    return this._decoder.write(ret);
  }
  return ret;
}

function cipherFinal(outputEncoding) {
  const ret = binding.cipherFinal(this[kHandle]);
  if (outputEncoding && outputEncoding !== "buffer") {
    this._decoder = getDecoder(this._decoder, outputEncoding);
    return this._decoder.end(ret);
  }
  return ret;
}

function cipherSetAutoPadding(ap) {
  binding.cipherSetAutoPadding(this[kHandle], ap === undefined ? true : !!ap);
  return this;
}

function cipherSetAAD(aadbuf, options) {
  const encoding = getStringOption(options, "encoding");
  const plaintextLength = getUIntOption(options, "plaintextLength");
  aadbuf = getArrayBufferOrView(aadbuf, "aadbuf", encoding);
  binding.cipherSetAAD(this[kHandle], aadbuf, plaintextLength);
  return this;
}

function cipherTransform(chunk, encoding, callback) {
  try {
    this.push(binding.cipherUpdate(this[kHandle], decodeInput(chunk, encoding)));
  } catch (err) {
    callback(err);
    return;
  }
  callback();
}

function cipherFlush(callback) {
  try {
    this.push(binding.cipherFinal(this[kHandle]));
  } catch (err) {
    callback(err);
    return;
  }
  callback();
}

function Cipheriv(cipher, key, iv, options) {
  if (!new.target) return new Cipheriv(cipher, key, iv, options);
  const self = makeStream(new.target, options);
  initCipher(self, cipher, key, iv, options, false);
  return self;
}
inheritStream(Cipheriv, Transform);
Cipheriv.prototype._transform = cipherTransform;
Cipheriv.prototype._flush = cipherFlush;
Cipheriv.prototype.update = cipherUpdate;
Cipheriv.prototype.final = cipherFinal;
Cipheriv.prototype.setAutoPadding = cipherSetAutoPadding;
Cipheriv.prototype.getAuthTag = function getAuthTag() {
  return binding.cipherGetAuthTag(this[kHandle]);
};
Cipheriv.prototype.setAAD = cipherSetAAD;

function Decipheriv(cipher, key, iv, options) {
  if (!new.target) return new Decipheriv(cipher, key, iv, options);
  const self = makeStream(new.target, options);
  initCipher(self, cipher, key, iv, options, true);
  return self;
}
inheritStream(Decipheriv, Transform);
Decipheriv.prototype._transform = cipherTransform;
Decipheriv.prototype._flush = cipherFlush;
Decipheriv.prototype.update = cipherUpdate;
Decipheriv.prototype.final = cipherFinal;
Decipheriv.prototype.setAutoPadding = cipherSetAutoPadding;
Decipheriv.prototype.setAuthTag = function setAuthTag(tagbuf, encoding) {
  tagbuf = getArrayBufferOrView(tagbuf, "buffer", encoding);
  binding.cipherSetAuthTag(this[kHandle], tagbuf);
  return this;
};
Decipheriv.prototype.setAAD = cipherSetAAD;

function createCipheriv(cipher, key, iv, options) {
  return new Cipheriv(cipher, key, iv, options);
}

function createDecipheriv(cipher, key, iv, options) {
  return new Decipheriv(cipher, key, iv, options);
}

function getCiphers() {
  return binding.getCiphers();
}

function getCipherInfo(nameOrNid, options) {
  if (typeof nameOrNid !== "string" && typeof nameOrNid !== "number") {
    throw errInvalidArgType("nameOrNid", "of type string or number", nameOrNid);
  }
  if (typeof nameOrNid === "number") validateInt32(nameOrNid, "nameOrNid");
  let keyLength, ivLength;
  if (options !== undefined) {
    validateObject(options, "options");
    ({ keyLength, ivLength } = options);
    if (keyLength !== undefined) validateInt32(keyLength, "options.keyLength");
    if (ivLength !== undefined) validateInt32(ivLength, "options.ivLength");
  }
  const json = binding.cipherInfo(nameOrNid, keyLength === undefined ? -1 : keyLength, ivLength === undefined ? -1 : ivLength);
  if (json === undefined) return undefined;
  const info = JSON.parse(json);
  const out = { mode: info.mode, name: info.name, nid: info.nid, keyLength: info.keyLength };
  if (info.blockSize !== undefined) out.blockSize = info.blockSize;
  if (info.ivLength !== undefined) out.ivLength = info.ivLength;
  return out;
}
