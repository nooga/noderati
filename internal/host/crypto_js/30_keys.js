
// ---- KeyObject -------------------------------------------------------------------

// KeyHandle is the internal token a KeyObject is built from: secret keys
// keep their bytes in JS, asymmetric keys hold a Go key handle.
class KeyHandle {
  constructor(type, data) {
    this.type = type;
    this.data = data;
  }
}

function errInvalidKeyObjectType(type, expected) {
  return makeError(TypeError, "ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE",
    "Invalid key object type " + type + ", expected " + expected + ".");
}

function errIncompatibleKeyOptions(a, b) {
  return makeError(Error, "ERR_CRYPTO_INCOMPATIBLE_KEY_OPTIONS", "The selected key encoding " + a + " " + b + ".");
}

function isKeyObject(v) {
  return v != null && typeof v === "object" && kKeyType in v && v instanceof KeyObject;
}

function isCryptoKey(v) {
  return v != null && typeof v === "object" && kKeyObject in v && typeof CryptoKey === "function" && v instanceof CryptoKey;
}

class KeyObject {
  constructor(type, handle) {
    if (type !== "secret" && type !== "public" && type !== "private") {
      throw errInvalidArgValue("type", type);
    }
    if (!(handle instanceof KeyHandle)) {
      throw errInvalidArgType("handle", "of type object", handle);
    }
    this[kKeyType] = type;
    if (type === "secret") this[kKeyData] = handle.data;
    else this[kHandle] = handle.data;
  }

  get type() {
    return this[kKeyType];
  }

  static from(key) {
    if (!isCryptoKey(key)) throw errInvalidArgType("key", "an instance of CryptoKey", key);
    return key[kKeyObject];
  }

  equals(otherKeyObject) {
    if (!isKeyObject(otherKeyObject)) {
      throw errInvalidArgType("otherKeyObject", "an instance of KeyObject", otherKeyObject);
    }
    if (otherKeyObject.type !== this.type) return false;
    if (this.type === "secret") {
      const a = this[kKeyData];
      const b = otherKeyObject[kKeyData];
      return a.length === b.length && timingSafeEqual(a, b);
    }
    return binding.keyEquals(this[kHandle], otherKeyObject[kHandle]);
  }
}
Object.defineProperty(KeyObject.prototype, Symbol.toStringTag, { value: "KeyObject", configurable: true });

class SecretKeyObject extends KeyObject {
  constructor(handle) {
    super("secret", handle);
  }

  get symmetricKeySize() {
    return this[kKeyData].length;
  }

  export(options) {
    if (options !== undefined) {
      validateObject(options, "options");
      validateOneOf(options.format, "options.format", [undefined, "buffer", "jwk"]);
      if (options.format === "jwk") {
        return { kty: "oct", k: encodeOutput(this[kKeyData], "base64url") };
      }
    }
    return Buffer.from(this[kKeyData]);
  }

  toCryptoKey(algorithm, extractable, keyUsages) {
    return webcryptoImportKeyObject(this, algorithm, extractable, keyUsages);
  }
}

function keyInfo(key) {
  if (!key[kState]) key[kState] = JSON.parse(binding.keyInfo(key[kHandle]));
  return key[kState];
}

class AsymmetricKeyObject extends KeyObject {
  get asymmetricKeyType() {
    return keyInfo(this).asymmetricKeyType;
  }

  get asymmetricKeyDetails() {
    const d = keyInfo(this).details;
    if (!d) return {};
    const out = {};
    for (const k of ["modulusLength", "publicExponent", "hashAlgorithm", "mgf1HashAlgorithm", "saltLength", "divisorLength", "namedCurve"]) {
      if (d[k] !== undefined) out[k] = k === "publicExponent" ? BigInt(d[k]) : d[k];
    }
    return out;
  }

  toCryptoKey(algorithm, extractable, keyUsages) {
    return webcryptoImportKeyObject(this, algorithm, extractable, keyUsages);
  }
}

class PublicKeyObject extends AsymmetricKeyObject {
  constructor(handle) {
    super("public", handle);
  }

  export(options) {
    if (options && options.format === "jwk") {
      return JSON.parse(binding.keyExport(this[kHandle], "jwk", "", "", undefined));
    }
    const { format, type } = parsePublicKeyEncoding(options, this.asymmetricKeyType);
    return binding.keyExport(this[kHandle], format, type, "", undefined);
  }
}

class PrivateKeyObject extends AsymmetricKeyObject {
  constructor(handle) {
    super("private", handle);
  }

  export(options) {
    if (options && options.format === "jwk") {
      if (options.passphrase !== undefined) {
        throw makeError(Error, "ERR_CRYPTO_INCOMPATIBLE_KEY_OPTIONS",
          "The selected key encoding jwk does not support encryption.");
      }
      return JSON.parse(binding.keyExport(this[kHandle], "jwk", "", "", undefined));
    }
    const { format, type, cipher, passphrase } = parsePrivateKeyEncoding(options, this.asymmetricKeyType);
    return binding.keyExport(this[kHandle], format, type, cipher || "", passphrase);
  }
}

function newAsymmetricKeyObject(type, goHandle) {
  const h = new KeyHandle(type, goHandle);
  return type === "public" ? new PublicKeyObject(h) : new PrivateKeyObject(h);
}

function newSecretKeyObject(bytes) {
  return new SecretKeyObject(new KeyHandle("secret", Buffer.from(bytes)));
}

// ---- key encoding options ----------------------------------------------------------

function optName(ctx, name) {
  return ctx ? ctx + "." + name : name;
}

function parseKeyFormat(format, def, name) {
  if (format === undefined && def !== undefined) return def;
  if (format === "pem" || format === "der" || format === "jwk") return format;
  throw errInvalidArgValue(name, format);
}

function parsePublicKeyEncoding(enc, keyType, objName = "options") {
  validateObject(enc, objName);
  const format = parseKeyFormat(enc.format, undefined, optName(objName, "format"));
  if (format === "jwk") return { format };
  const type = enc.type;
  if (type === "pkcs1") {
    if (keyType !== undefined && keyType !== "rsa") {
      throw errIncompatibleKeyOptions("pkcs1", "can only be used for RSA keys");
    }
  } else if (type !== "spki") {
    throw errInvalidArgValue(optName(objName, "type"), type);
  }
  return { format, type };
}

function parsePrivateKeyEncoding(enc, keyType, objName = "options") {
  validateObject(enc, objName);
  const format = parseKeyFormat(enc.format, undefined, optName(objName, "format"));
  if (format === "jwk") return { format };
  const type = enc.type;
  if (type === "pkcs1") {
    if (keyType !== undefined && keyType !== "rsa") {
      throw errIncompatibleKeyOptions("pkcs1", "can only be used for RSA keys");
    }
  } else if (type === "sec1") {
    if (keyType !== undefined && keyType !== "ec") {
      throw errIncompatibleKeyOptions("sec1", "can only be used for EC keys");
    }
  } else if (type !== "pkcs8") {
    throw errInvalidArgValue(optName(objName, "type"), type);
  }
  let { cipher, passphrase, encoding } = enc;
  if (cipher != null) {
    if (typeof cipher !== "string") throw errInvalidArgValue(optName(objName, "cipher"), cipher);
    if (format === "der" && (type === "pkcs1" || type === "sec1")) {
      throw errIncompatibleKeyOptions(type, "does not support encryption");
    }
    if (passphrase === undefined) throw errInvalidArgValue(optName(objName, "passphrase"), passphrase);
    passphrase = getArrayBufferOrView(passphrase, optName(objName, "passphrase"), encoding);
  } else if (passphrase !== undefined) {
    throw errInvalidArgValue(optName(objName, "cipher"), cipher);
  }
  return { format, type, cipher, passphrase };
}

// ---- key input -------------------------------------------------------------------------

const kKeyInputTypes = "of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView";

// prepareAsymmetricKey resolves every accepted key input shape to either
// an existing KeyObject or {data, format, type, passphrase} for the parser.
function prepareAsymmetricKey(key, kind) {
  if (isKeyObject(key)) return { keyObject: key };
  if (isCryptoKey(key)) return { keyObject: key[kKeyObject] };
  if (typeof key === "string" || isArrayBufferView(key) || isAnyArrayBuffer(key)) {
    return { data: getArrayBufferOrView(key, "key"), format: "pem" };
  }
  if (key !== null && typeof key === "object") {
    const { key: data, encoding, format } = key;
    if (isKeyObject(data)) return { keyObject: data };
    if (isCryptoKey(data)) return { keyObject: data[kKeyObject] };
    if (format === "jwk") {
      if (data === null || typeof data !== "object") throw errInvalidArgType("key.key", "of type object", data);
      return { jwk: data, format: "jwk" };
    }
    if (typeof data !== "string" && !isArrayBufferView(data) && !isAnyArrayBuffer(data)) {
      throw errInvalidArgType("key.key", kKeyInputTypes, data);
    }
    const fmt = parseKeyFormat(format, "pem", "key.format");
    let type = key.type;
    if (fmt === "der") {
      const allowed = kind === "private" ? ["pkcs1", "pkcs8", "sec1"] : ["pkcs1", "spki", "pkcs8", "sec1"];
      if (!allowed.includes(type)) throw errInvalidArgValue("key.type", type);
    } else {
      type = undefined;
    }
    let passphrase;
    if (key.passphrase !== undefined) passphrase = getArrayBufferOrView(key.passphrase, "key.passphrase", encoding);
    return { data: getArrayBufferOrView(data, "key", encoding), format: fmt, type, passphrase };
  }
  throw errInvalidArgType("key", kKeyInputTypes, key);
}

function parseKeyFromInput(input, kind) {
  if (input.format === "jwk") {
    const r = binding.keyParseJWK(JSON.stringify(input.jwk), kind);
    if (r.secret) return newSecretKeyObject(r.secret);
    return newAsymmetricKeyObject(r.type, r.handle);
  }
  const r = binding.keyParse(input.data, input.format, input.type || "", kind, input.passphrase);
  return newAsymmetricKeyObject(r.type, r.handle);
}

function createPublicKey(key) {
  const input = prepareAsymmetricKey(key, "public");
  if (input.keyObject) {
    const ko = input.keyObject;
    if (ko.type === "public") return ko;
    if (ko.type !== "private") throw errInvalidKeyObjectType(ko.type, "private");
    return newAsymmetricKeyObject("public", binding.keyToPublic(ko[kHandle]));
  }
  if (input.format === "jwk") {
    const jwk = input.jwk;
    const pubOnly = { ...jwk };
    for (const k of ["d", "p", "q", "dp", "dq", "qi", "oth"]) delete pubOnly[k];
    input.jwk = pubOnly;
  }
  const ko = parseKeyFromInput(input, "public");
  if (ko.type === "private") return newAsymmetricKeyObject("public", binding.keyToPublic(ko[kHandle]));
  return ko;
}

function createPrivateKey(key) {
  if (isKeyObject(key) || isCryptoKey(key)) {
    throw errInvalidArgType("key", kKeyInputTypes, key);
  }
  const input = prepareAsymmetricKey(key, "private");
  if (input.keyObject) {
    const ko = input.keyObject;
    if (ko.type !== "private") throw errInvalidKeyObjectType(ko.type, "private");
    return ko;
  }
  return parseKeyFromInput(input, "private");
}

function prepareSecretKey(key, encoding, name = "key") {
  if (isKeyObject(key)) {
    if (key.type !== "secret") throw errInvalidKeyObjectType(key.type, "secret");
    return key[kKeyData];
  }
  if (isCryptoKey(key)) {
    if (key.type !== "secret") throw errInvalidKeyObjectType(key.type, "secret");
    return key[kKeyObject][kKeyData];
  }
  if (typeof key !== "string" && !isArrayBufferView(key) && !isAnyArrayBuffer(key)) {
    throw errInvalidArgType(name,
      "of type string or an instance of ArrayBuffer, Buffer, TypedArray, DataView, KeyObject, or CryptoKey", key);
  }
  return getArrayBufferOrView(key, name, encoding);
}

function createSecretKey(key, encoding) {
  if (typeof key !== "string" && !isArrayBufferView(key) && !isAnyArrayBuffer(key)) {
    throw errInvalidArgType("key", kBinaryTypes, key);
  }
  return newSecretKeyObject(getArrayBufferOrView(key, "key", encoding));
}
