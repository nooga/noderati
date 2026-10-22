// ---- Web Crypto -------------------------------------------------------------------
// Crypto, SubtleCrypto and CryptoKey over the node:crypto pieces above,
// following node's lib/internal/crypto/webcrypto.js: the same algorithm
// normalization, usage rules and DOMException names and messages.

const kBrand = Symbol("kBrand");
const kUsageOrder = ["encrypt", "decrypt", "sign", "verify", "deriveKey", "deriveBits", "wrapKey", "unwrapKey"];
const kOperationFailed = "The operation failed for an operation-specific reason";

function illegalConstructor() {
  return makeError(TypeError, "ERR_ILLEGAL_CONSTRUCTOR", "Illegal constructor");
}

function errInvalidThis(type) {
  return makeError(TypeError, "ERR_INVALID_THIS", 'Value of "this" must be of type ' + type);
}

function opError(msg) {
  return domException(msg || kOperationFailed, "OperationError");
}

const ordinals = ["1st", "2nd", "3rd", "4th", "5th", "6th", "7th"];

function webidlPrefix(method) {
  return "Failed to execute '" + method + "' on 'SubtleCrypto': ";
}

function requireArgs(method, args, n) {
  if (args.length < n) {
    throw makeError(TypeError, "ERR_MISSING_ARGS",
      webidlPrefix(method) + n + " argument" + (n === 1 ? "" : "s") + " required, but only " + args.length + " present.");
  }
}

function bufferSourceArg(method, v, i) {
  if (!isAnyArrayBuffer(v) && !isArrayBufferView(v)) {
    throw makeError(TypeError, "ERR_INVALID_ARG_TYPE",
      webidlPrefix(method) + ordinals[i] + " argument is not instance of ArrayBuffer, Buffer, TypedArray, or DataView.");
  }
  return Buffer.from(toU8(v));
}

function cryptoKeyArg(method, v, i) {
  if (!isCryptoKey(v)) {
    throw makeError(TypeError, "ERR_INVALID_ARG_TYPE", webidlPrefix(method) + ordinals[i] + " argument is not of type CryptoKey.");
  }
  return v;
}

function enumArg(method, v, i, values, type) {
  const s = String(v);
  if (!values.includes(s)) {
    throw makeError(TypeError, "ERR_INVALID_ARG_VALUE",
      webidlPrefix(method) + ordinals[i] + " argument '" + s + "' is not a valid enum value of type " + type + ".");
  }
  return s;
}

function usagesArg(method, v, i) {
  if (v == null || typeof v !== "object" || typeof v[Symbol.iterator] !== "function") {
    throw makeError(TypeError, "ERR_INVALID_ARG_TYPE", webidlPrefix(method) + ordinals[i] + " argument cannot be converted to sequence.");
  }
  const out = [];
  let idx = 0;
  for (const u of v) {
    const str = String(u);
    if (!kUsageOrder.includes(str)) {
      throw makeError(TypeError, "ERR_INVALID_ARG_VALUE",
        webidlPrefix(method) + ordinals[i] + " argument[" + idx + "] '" + str + "' is not a valid enum value of type KeyUsage.");
    }
    out.push(str);
    idx++;
  }
  return out;
}

const kKeyFormats = ["raw", "pkcs8", "spki", "jwk"];

// ---- algorithm normalization -------------------------------------------------------

const kAlgorithmNames = [
  "AES-CTR", "AES-CBC", "AES-GCM", "AES-KW", "RSA-OAEP", "RSASSA-PKCS1-v1_5", "RSA-PSS", "ECDSA", "ECDH",
  "Ed25519", "X25519", "HMAC", "HKDF", "PBKDF2", "SHA-1", "SHA-256", "SHA-384", "SHA-512",
  "SHA3-256", "SHA3-384", "SHA3-512", "cSHAKE128", "cSHAKE256",
];
const kCanonicalName = Object.create(null);
for (const n of kAlgorithmNames) kCanonicalName[n.toLowerCase()] = n;

const kAES = { "AES-CTR": 0, "AES-CBC": 0, "AES-GCM": 0, "AES-KW": 0 };
function aesTable(dict, withKW = true) {
  const t = {};
  for (const n of Object.keys(kAES)) if (withKW || n !== "AES-KW") t[n] = dict;
  return t;
}

const kSupported = {
  digest: {
    "SHA-1": null, "SHA-256": null, "SHA-384": null, "SHA-512": null,
    "SHA3-256": null, "SHA3-384": null, "SHA3-512": null, "cSHAKE128": "CShakeParams", "cSHAKE256": "CShakeParams",
  },
  generateKey: Object.assign({
    "RSASSA-PKCS1-v1_5": "RsaHashedKeyGenParams", "RSA-PSS": "RsaHashedKeyGenParams", "RSA-OAEP": "RsaHashedKeyGenParams",
    "ECDSA": "EcKeyGenParams", "ECDH": "EcKeyGenParams", "Ed25519": null, "X25519": null, "HMAC": "HmacKeyGenParams",
  }, aesTable("AesKeyGenParams")),
  importKey: Object.assign({
    "RSASSA-PKCS1-v1_5": "RsaHashedImportParams", "RSA-PSS": "RsaHashedImportParams", "RSA-OAEP": "RsaHashedImportParams",
    "ECDSA": "EcKeyImportParams", "ECDH": "EcKeyImportParams", "Ed25519": null, "X25519": null, "HMAC": "HmacImportParams",
    "HKDF": null, "PBKDF2": null,
  }, aesTable(null)),
  sign: { "RSASSA-PKCS1-v1_5": null, "RSA-PSS": "RsaPssParams", "ECDSA": "EcdsaParams", "Ed25519": null, "HMAC": null },
  encrypt: { "RSA-OAEP": "RsaOaepParams", "AES-CTR": "AesCtrParams", "AES-CBC": "AesCbcParams", "AES-GCM": "AeadParams" },
  deriveBits: { "ECDH": "EcdhKeyDeriveParams", "X25519": "EcdhKeyDeriveParams", "HKDF": "HkdfParams", "PBKDF2": "Pbkdf2Params" },
  wrapKey: { "AES-KW": null },
  "get key length": Object.assign({ "HMAC": "HmacImportParams", "HKDF": null, "PBKDF2": null }, aesTable("AesDerivedKeyParams")),
};
kSupported.verify = kSupported.sign;
kSupported.decrypt = kSupported.encrypt;
kSupported.unwrapKey = kSupported.wrapKey;
kSupported.exportKey = Object.assign({}, kSupported.importKey);
delete kSupported.exportKey.HKDF;
delete kSupported.exportKey.PBKDF2;

// Dictionary members: [name, kind, required]. Kinds: "hash" (a digest
// algorithm), "buffer" (BufferSource), "number", "key", "any".
const kDicts = {
  CShakeParams: [["outputLength", "number", true], ["functionName", "buffer", false], ["customization", "buffer", false]],
  RsaHashedKeyGenParams: [["modulusLength", "number", true], ["publicExponent", "buffer", true], ["hash", "hash", true]],
  RsaHashedImportParams: [["hash", "hash", true]],
  EcKeyGenParams: [["namedCurve", "string", true]],
  EcKeyImportParams: [["namedCurve", "string", true]],
  HmacKeyGenParams: [["hash", "hash", true], ["length", "number", false]],
  HmacImportParams: [["hash", "hash", true], ["length", "number", false]],
  AesKeyGenParams: [["length", "number", true]],
  AesDerivedKeyParams: [["length", "number", true]],
  RsaPssParams: [["saltLength", "number", true]],
  EcdsaParams: [["hash", "hash", true]],
  RsaOaepParams: [["label", "buffer", false]],
  AesCtrParams: [["counter", "buffer", true], ["length", "number", true]],
  AesCbcParams: [["iv", "buffer", true]],
  AeadParams: [["additionalData", "buffer", false], ["iv", "buffer", true], ["tagLength", "number", false]],
  EcdhKeyDeriveParams: [["public", "key", true]],
  HkdfParams: [["hash", "hash", true], ["info", "buffer", true], ["salt", "buffer", true]],
  Pbkdf2Params: [["hash", "hash", true], ["iterations", "number", true], ["salt", "buffer", true]],
};

function missingMember(dict, member) {
  return makeError(TypeError, "ERR_MISSING_OPTION",
    "Failed to normalize algorithm: passed algorithm cannot be converted to '" + dict +
    "' because '" + member + "' is required in '" + dict + "'.");
}

const kExperimental = { "SHA3-256": 1, "SHA3-384": 1, "SHA3-512": 1, "cSHAKE128": 1, "cSHAKE256": 1 };
const warnedExperimental = new Set();

function normalizeAlgorithm(algorithm, op) {
  if (typeof algorithm === "string") return normalizeAlgorithm({ name: algorithm }, op);
  if (algorithm === null || (typeof algorithm !== "object" && typeof algorithm !== "function")) {
    throw domException("Unrecognized algorithm name", "NotSupportedError");
  }
  if (algorithm.name === undefined) throw missingMember("Algorithm", "name");
  const canonical = kCanonicalName[String(algorithm.name).toLowerCase()];
  const table = kSupported[op];
  if (canonical === undefined || !(canonical in table)) {
    throw domException("Unrecognized algorithm name", "NotSupportedError");
  }
  if (kExperimental[canonical] && !warnedExperimental.has(canonical)) {
    warnedExperimental.add(canonical);
    if (typeof process === "object" && process && typeof process.emitWarning === "function") {
      process.emitWarning("The " + canonical + " Web Crypto API algorithm is an experimental feature and might change at any time",
        "ExperimentalWarning");
    }
  }
  const out = { name: canonical };
  const dict = table[canonical];
  if (!dict) return out;
  for (const [member, kind, required] of kDicts[dict]) {
    const v = algorithm[member];
    if (v === undefined) {
      if (required) throw missingMember(dict, member);
      continue;
    }
    switch (kind) {
      case "hash":
        out[member] = normalizeAlgorithm(v, "digest");
        break;
      case "buffer":
        if (!isAnyArrayBuffer(v) && !isArrayBufferView(v)) {
          throw makeError(TypeError, "ERR_INVALID_ARG_TYPE",
            "Failed to normalize algorithm: '" + member + "' of '" + dict + "' is not instance of ArrayBuffer, Buffer, TypedArray, or DataView.");
        }
        out[member] = Buffer.from(toU8(v));
        break;
      case "number":
        out[member] = Number(v);
        break;
      case "string":
        out[member] = String(v);
        break;
      case "key":
        if (!isCryptoKey(v)) {
          throw makeError(TypeError, "ERR_INVALID_ARG_TYPE",
            "Failed to normalize algorithm: " + member + " in passed algorithm is not of type CryptoKey.");
        }
        out[member] = v;
        break;
      default:
        out[member] = v;
    }
  }
  return out;
}

const kNodeHash = {
  "SHA-1": "sha1", "SHA-256": "sha256", "SHA-384": "sha384", "SHA-512": "sha512",
  "SHA3-256": "sha3-256", "SHA3-384": "sha3-384", "SHA3-512": "sha3-512",
};
const kHashBlockBits = { "SHA-1": 512, "SHA-256": 512, "SHA-384": 1024, "SHA-512": 1024, "SHA3-256": 1088, "SHA3-384": 832, "SHA3-512": 576 };
const kHashSuffix = { "SHA-1": "1", "SHA-256": "256", "SHA-384": "384", "SHA-512": "512" };

function toArrayBuffer(buf) {
  const u = new Uint8Array(buf.length);
  u.set(buf);
  return u.buffer;
}

// ---- CryptoKey ------------------------------------------------------------------------

class CryptoKey {
  constructor() {
    throw illegalConstructor();
  }

  get type() {
    if (!isCryptoKey(this)) throw errInvalidThis("CryptoKey");
    return this[kKeyObject].type;
  }

  get extractable() {
    if (!isCryptoKey(this)) throw errInvalidThis("CryptoKey");
    return this[kExtractable];
  }

  get algorithm() {
    if (!isCryptoKey(this)) throw errInvalidThis("CryptoKey");
    return this[kAlgorithm];
  }

  get usages() {
    if (!isCryptoKey(this)) throw errInvalidThis("CryptoKey");
    return this[kKeyUsages];
  }
}
Object.defineProperty(CryptoKey.prototype, Symbol.toStringTag, { value: "CryptoKey", configurable: true });

function sortUsages(usages) {
  return kUsageOrder.filter((u) => usages.includes(u));
}

function createCryptoKey(keyObject, algorithm, extractable, usages) {
  const k = Object.create(CryptoKey.prototype);
  k[kKeyObject] = keyObject;
  k[kAlgorithm] = algorithm;
  k[kExtractable] = !!extractable;
  k[kKeyUsages] = sortUsages(usages);
  return k;
}

// ---- usages ---------------------------------------------------------------------------

const kUsageRules = {
  AES: { all: ["encrypt", "decrypt", "wrapKey", "unwrapKey"], msg: "an AES key" },
  "AES-KW": { all: ["wrapKey", "unwrapKey"], msg: "an AES key" },
  HMAC: { all: ["sign", "verify"], msg: "HMAC key" },
  "RSASSA-PKCS1-v1_5": { pub: ["verify"], priv: ["sign"], msg: "an RSASSA-PKCS1-v1_5 key", gen: "a RSA key" },
  "RSA-PSS": { pub: ["verify"], priv: ["sign"], msg: "an RSA-PSS key", gen: "a RSA key" },
  "RSA-OAEP": { pub: ["encrypt", "wrapKey"], priv: ["decrypt", "unwrapKey"], msg: "an RSA-OAEP key", gen: "a RSA key" },
  ECDSA: { pub: ["verify"], priv: ["sign"], msg: "a ECDSA key" },
  ECDH: { pub: [], priv: ["deriveKey", "deriveBits"], msg: "a ECDH key" },
  Ed25519: { pub: ["verify"], priv: ["sign"], msg: "a Ed25519 key", gen: "an Ed25519 key" },
  X25519: { pub: [], priv: ["deriveKey", "deriveBits"], msg: "a X25519 key", gen: "an X25519 key" },
  HKDF: { all: ["deriveKey", "deriveBits"], msg: "a HKDF key" },
  PBKDF2: { all: ["deriveKey", "deriveBits"], msg: "a PBKDF2 key" },
};

function usageRule(name) {
  return kUsageRules[name] || (name in kAES ? kUsageRules.AES : undefined);
}

function checkUsages(name, usages, type, generating) {
  const rule = usageRule(name);
  const allowed = rule.all || (type === "public" ? rule.pub : type === "private" ? rule.priv : rule.pub.concat(rule.priv));
  for (const u of usages) {
    if (!allowed.includes(u)) {
      throw domException("Unsupported key usage for " + (generating && rule.gen ? rule.gen : rule.msg), "SyntaxError");
    }
  }
}

function checkNotEmptyUsages(key, generating) {
  if ((key.type === "secret" || key.type === "private") && key.usages.length === 0) {
    throw domException(generating ? "Usages cannot be empty when creating a key."
      : "Usages cannot be empty when importing a " + key.type + " key.", "SyntaxError");
  }
}

// ---- JWK helpers -------------------------------------------------------------------------

function jwkAlgFor(alg, keyLenBits) {
  switch (alg.name) {
    case "HMAC": return "HS" + kHashSuffix[alg.hash.name];
    case "RSASSA-PKCS1-v1_5": return "RS" + kHashSuffix[alg.hash.name];
    case "RSA-PSS": return "PS" + kHashSuffix[alg.hash.name];
    case "RSA-OAEP": return alg.hash.name === "SHA-1" ? "RSA-OAEP" : "RSA-OAEP-" + kHashSuffix[alg.hash.name];
    case "AES-GCM": case "AES-CBC": case "AES-CTR": case "AES-KW":
      return "A" + keyLenBits + alg.name.slice(4);
    case "ECDSA": return { "P-256": "ES256", "P-384": "ES384", "P-521": "ES512" }[alg.namedCurve];
    case "Ed25519": return "Ed25519";
  }
  return undefined;
}

function checkJwkCommon(jwk, extractable, usages, use) {
  if (usages.length > 0 && jwk.use !== undefined && jwk.use !== use) {
    throw domException('Invalid JWK "use" Parameter', "DataError");
  }
  if (jwk.key_ops !== undefined) {
    if (!Array.isArray(jwk.key_ops)) throw domException('Invalid JWK "key_ops" Parameter', "DataError");
    for (const u of usages) {
      if (!jwk.key_ops.includes(u)) throw domException("Key operations and usage mismatch", "DataError");
    }
  }
  if (jwk.ext === false && extractable) {
    throw domException('JWK "ext" Parameter and extractable mismatch', "DataError");
  }
}

function b64urlDecode(s) {
  if (typeof s !== "string") throw domException("Invalid keyData", "DataError");
  return Buffer.from(s, "base64url");
}

// ---- key import ----------------------------------------------------------------------------

const kNodeCurve = { "P-256": "prime256v1", "P-384": "secp384r1", "P-521": "secp521r1" };
const kWebCurve = { prime256v1: "P-256", secp384r1: "P-384", secp521r1: "P-521" };

function importSecretBytes(format, keyData, alg, extractable, usages, use) {
  if (format === "KeyObject") {
    if (keyData.type !== "secret") throw domException("Invalid key type", "DataError");
    return keyData[kKeyData];
  }
  if (format === "raw") return keyData;
  if (format === "jwk") {
    if (jwk_kty(keyData) !== "oct") throw domException('Invalid JWK "kty" Parameter', "DataError");
    checkJwkCommon(keyData, extractable, usages, use);
    const bytes = b64urlDecode(keyData.k);
    const expected = jwkAlgFor(alg, bytes.length * 8);
    if (keyData.alg !== undefined && keyData.alg !== expected) {
      throw domException('JWK "alg" does not match the requested algorithm', "DataError");
    }
    return bytes;
  }
  throw domException("Unable to import " + alg.name + " using " + format + " format", "NotSupportedError");
}

function jwk_kty(jwk) {
  return jwk && jwk.kty;
}

function asymKeyFromData(format, keyData, alg, extractable, usages, use) {
  try {
    if (format === "KeyObject") return keyData;
    if (format === "spki") return createPublicKey({ key: keyData, format: "der", type: "spki" });
    if (format === "pkcs8") return createPrivateKey({ key: keyData, format: "der", type: "pkcs8" });
  } catch (e) {
    throw domException("Invalid keyData", "DataError");
  }
  if (format === "jwk") {
    checkJwkCommon(keyData, extractable, usages, use);
    try {
      return keyData.d !== undefined ? createPrivateKey({ key: keyData, format: "jwk" }) : createPublicKey({ key: keyData, format: "jwk" });
    } catch (e) {
      throw domException("Invalid keyData", "DataError");
    }
  }
  throw domException("Unable to import " + alg.name + " using " + format + " format", "NotSupportedError");
}

function publicExponentBytes(e) {
  const out = [];
  let v = BigInt(e);
  while (v > 0n) {
    out.unshift(Number(v & 0xffn));
    v >>= 8n;
  }
  return new Uint8Array(out);
}

function importKeySync(format, keyData, alg, extractable, usages) {
  const name = alg.name;
  if (format === "KeyObject" && keyData.type === "secret" && !(name in kAES) && name !== "HMAC" && name !== "HKDF" && name !== "PBKDF2") {
    throw domException("Invalid key type", "DataError");
  }
  if (name in kAES) {
    checkUsages(name, usages, "secret");
    const bytes = importSecretBytes(format, keyData, alg, extractable, usages, "enc");
    if (bytes.length !== 16 && bytes.length !== 24 && bytes.length !== 32) throw domException("Invalid key length", "DataError");
    return createCryptoKey(newSecretKeyObject(bytes), { name, length: bytes.length * 8 }, extractable, usages);
  }
  if (name === "HMAC") {
    checkUsages(name, usages, "secret");
    const hashName = alg.hash.name;
    if (format === "jwk" && keyData && keyData.kty === "oct" && keyData.alg !== undefined && keyData.alg !== jwkAlgFor(alg)) {
      checkJwkCommon(keyData, extractable, usages, "sig");
      throw domException('JWK "alg" does not match the requested algorithm', "DataError");
    }
    const bytes = importSecretBytes(format, keyData, alg, extractable, usages, "sig");
    if (bytes.length === 0) throw domException("Zero-length key is not supported", "DataError");
    const bits = bytes.length * 8;
    if (alg.length !== undefined && alg.length !== bits) {
      throw domException("Unsupported HmacImportParams.length", "NotSupportedError");
    }
    return createCryptoKey(newSecretKeyObject(bytes), { name, length: bits, hash: { name: hashName } }, extractable, usages);
  }
  if (name === "HKDF" || name === "PBKDF2") {
    checkUsages(name, usages, "secret");
    if (format !== "raw" && format !== "KeyObject") {
      throw domException("Unable to import " + name + " using " + format + " format", "NotSupportedError");
    }
    if (extractable) throw domException(name + " keys are not extractable", "SyntaxError");
    const bytes = format === "KeyObject" ? keyData[kKeyData] : keyData;
    return createCryptoKey(newSecretKeyObject(bytes), { name }, false, usages);
  }
  if (name === "RSASSA-PKCS1-v1_5" || name === "RSA-PSS" || name === "RSA-OAEP") {
    if (format === "jwk") {
      if (!keyData || keyData.kty !== "RSA") throw domException('Invalid JWK "kty" Parameter', "DataError");
      if (keyData.alg !== undefined && keyData.alg !== jwkAlgFor(alg)) {
        throw domException('JWK "alg" does not match the requested algorithm', "DataError");
      }
    }
    const type = format === "spki" ? "public" : format === "pkcs8" ? "private"
      : format === "jwk" ? (keyData.d !== undefined ? "private" : "public") : format === "KeyObject" ? keyData.type : "public";
    if (format !== "raw") checkUsages(name, usages, type);
    const ko = asymKeyFromData(format, keyData, alg, extractable, usages, name === "RSA-OAEP" ? "enc" : "sig");
    if (ko.asymmetricKeyType !== "rsa") throw domException("Invalid key type", "DataError");
    const d = ko.asymmetricKeyDetails;
    return createCryptoKey(ko, {
      name, modulusLength: d.modulusLength, publicExponent: publicExponentBytes(d.publicExponent), hash: { name: alg.hash.name },
    }, extractable, usages);
  }
  if (name === "ECDSA" || name === "ECDH") {
    const curve = alg.namedCurve;
    if (!kNodeCurve[curve]) throw domException("Unrecognized namedCurve", "NotSupportedError");
    let ko;
    if (format === "raw") {
      checkUsages(name, usages, "public");
      try {
        ko = newAsymmetricKeyObject("public", binding.keyFromRaw("ec", "public", keyData, kNodeCurve[curve]).handle);
      } catch (e) {
        throw domException("Invalid keyData", "DataError");
      }
    } else {
      if (format === "jwk") {
        if (!keyData || keyData.kty !== "EC") throw domException('Invalid JWK "kty" Parameter', "DataError");
        if (keyData.crv !== curve) throw domException("JWK \"crv\" does not match the requested algorithm", "DataError");
        if (name === "ECDSA" && keyData.alg !== undefined && keyData.alg !== jwkAlgFor(alg)) {
          throw domException('JWK "alg" does not match the requested algorithm', "DataError");
        }
      }
      const type = format === "spki" ? "public" : format === "pkcs8" ? "private"
        : format === "jwk" ? (keyData.d !== undefined ? "private" : "public") : keyData.type;
      checkUsages(name, usages, type);
      ko = asymKeyFromData(format, keyData, alg, extractable, usages, name === "ECDH" ? "enc" : "sig");
      if (ko.asymmetricKeyType !== "ec") throw domException("Invalid key type", "DataError");
      if (kWebCurve[ko.asymmetricKeyDetails.namedCurve] !== curve) throw domException("Named curve mismatch", "DataError");
    }
    return createCryptoKey(ko, { name, namedCurve: curve }, extractable, usages);
  }
  if (name === "Ed25519" || name === "X25519") {
    const t = name.toLowerCase();
    let ko;
    if (format === "raw") {
      checkUsages(name, usages, "public");
      if (keyData.length !== 32) throw domException("Invalid keyData", "DataError");
      try {
        ko = newAsymmetricKeyObject("public", binding.keyFromRaw(t, "public", keyData).handle);
      } catch (e) {
        throw domException("Invalid keyData", "DataError");
      }
    } else {
      if (format === "jwk") {
        if (!keyData || keyData.kty !== "OKP") throw domException('Invalid JWK "kty" Parameter', "DataError");
        if (keyData.crv !== name) throw domException('JWK "crv" Parameter and algorithm name mismatch', "DataError");
        if (name === "Ed25519" && keyData.alg !== undefined && keyData.alg !== "Ed25519" && keyData.alg !== "EdDSA") {
          throw domException('JWK "alg" does not match the requested algorithm', "DataError");
        }
      }
      const type = format === "spki" ? "public" : format === "pkcs8" ? "private"
        : format === "jwk" ? (keyData.d !== undefined ? "private" : "public") : keyData.type;
      checkUsages(name, usages, type);
      ko = asymKeyFromData(format, keyData, alg, extractable, usages, name === "X25519" ? "enc" : "sig");
      if (ko.asymmetricKeyType !== t) throw domException("Invalid key type", "DataError");
    }
    return createCryptoKey(ko, { name }, extractable, usages);
  }
  throw domException("Unrecognized algorithm name", "NotSupportedError");
}

function webcryptoImportKeyObject(keyObject, algorithm, extractable, keyUsages) {
  const alg = normalizeAlgorithm(algorithm, "importKey");
  const usages = usagesArg("importKey", keyUsages, 2);
  const key = importKeySync("KeyObject", keyObject, alg, extractable, usages);
  checkNotEmptyUsages(key, false);
  return key;
}

// ---- key export --------------------------------------------------------------------------------

function exportKeySync(format, key) {
  const name = key.algorithm.name;
  const ko = key[kKeyObject];
  const type = key.type;
  const unable = () => domException("Unable to export " + name + " " + type + " key using " + format + " format", "NotSupportedError");
  if (name === "HKDF" || name === "PBKDF2") throw domException(name + " key export is not supported", "NotSupportedError");
  if (format === "jwk") {
    const jwk = { key_ops: key.usages.slice(), ext: key.extractable };
    const alg = jwkAlgFor(key.algorithm, type === "secret" ? ko[kKeyData].length * 8 : 0);
    if (alg !== undefined && name !== "ECDSA") jwk.alg = alg;
    return Object.assign(jwk, ko.export({ format: "jwk" }));
  }
  if (type === "secret") {
    if (format !== "raw") throw unable();
    return toArrayBuffer(ko[kKeyData]);
  }
  if (format === "spki") {
    if (type !== "public") throw unable();
    return toArrayBuffer(ko.export({ format: "der", type: "spki" }));
  }
  if (format === "pkcs8") {
    if (type !== "private") throw unable();
    return toArrayBuffer(ko.export({ format: "der", type: "pkcs8" }));
  }
  if (format === "raw") {
    if (type !== "public" || name.startsWith("RSA")) throw unable();
    return toArrayBuffer(binding.keyRaw(ko[kHandle], "public"));
  }
  throw unable();
}

// ---- operations ------------------------------------------------------------------------------

function checkKeyFor(alg, key, usage, mismatchMsg = "Key algorithm mismatch") {
  if (alg.name !== key.algorithm.name) throw domException(mismatchMsg, "InvalidAccessError");
  if (!key.usages.includes(usage)) throw domException("Unable to use this key to " + usage, "InvalidAccessError");
}

function digestSync(alg, data) {
  if (alg.name === "cSHAKE128" || alg.name === "cSHAKE256") {
    if ((alg.functionName && alg.functionName.length) || (alg.customization && alg.customization.length)) {
      throw domException("Unsupported CShakeParams", "NotSupportedError");
    }
    if (alg.outputLength % 8 !== 0) throw opError("Unsupported CShakeParams outputLength");
    if (alg.outputLength === 0) return new ArrayBuffer(0);
    return toArrayBuffer(hash(alg.name === "cSHAKE128" ? "shake128" : "shake256", data, { outputEncoding: "buffer", outputLength: alg.outputLength / 8 }));
  }
  return toArrayBuffer(hash(kNodeHash[alg.name], data, "buffer"));
}

function signSync(alg, key, data) {
  const ko = key[kKeyObject];
  switch (alg.name) {
    case "HMAC":
      return toArrayBuffer(createHmac(kNodeHash[key.algorithm.hash.name], ko[kKeyData]).update(data).digest());
    case "RSASSA-PKCS1-v1_5":
      return toArrayBuffer(binding.sign(ko[kHandle], data, kNodeHash[key.algorithm.hash.name], 1));
    case "RSA-PSS":
      return toArrayBuffer(binding.sign(ko[kHandle], data, kNodeHash[key.algorithm.hash.name], 6, alg.saltLength));
    case "ECDSA":
      return toArrayBuffer(binding.sign(ko[kHandle], data, kNodeHash[alg.hash.name], undefined, undefined, "ieee-p1363"));
    case "Ed25519":
      return toArrayBuffer(binding.sign(ko[kHandle], data, ""));
  }
  throw domException("Unrecognized algorithm name", "NotSupportedError");
}

function verifySync(alg, key, sig, data) {
  const ko = key[kKeyObject];
  switch (alg.name) {
    case "HMAC": {
      const mac = createHmac(kNodeHash[key.algorithm.hash.name], ko[kKeyData]).update(data).digest();
      return mac.length === sig.length && timingSafeEqual(mac, sig);
    }
    case "RSASSA-PKCS1-v1_5":
      return binding.verify(ko[kHandle], data, sig, kNodeHash[key.algorithm.hash.name], 1);
    case "RSA-PSS":
      return binding.verify(ko[kHandle], data, sig, kNodeHash[key.algorithm.hash.name], 6, alg.saltLength);
    case "ECDSA":
      return binding.verify(ko[kHandle], data, sig, kNodeHash[alg.hash.name], undefined, undefined, "ieee-p1363");
    case "Ed25519":
      return binding.verify(ko[kHandle], data, sig, "");
  }
  throw domException("Unrecognized algorithm name", "NotSupportedError");
}

const kGcmTagLengths = [32, 64, 96, 104, 112, 120, 128];

function concatBufs(list) {
  return Buffer.concat(list.filter((b) => b && b.length));
}

function aesCtr(keyBytes, counter, length, data, decrypt) {
  const name = "aes-" + keyBytes.length * 8 + "-ctr";
  const run = (iv, chunk) => {
    const c = createCipheriv(name, keyBytes, iv);
    return concatBufs([c.update(chunk), c.final()]);
  };
  if (length >= 128) return run(counter, data);
  // Only the low `length` bits of the counter block count; when they wrap
  // the rest of the block stays fixed.
  let ctr = 0n;
  for (const b of counter) ctr = (ctr << 8n) | BigInt(b);
  const mask = (1n << BigInt(length)) - 1n;
  const low = ctr & mask;
  const blocksLeft = mask - low + 1n;
  const nBlocks = BigInt(Math.ceil(data.length / 16));
  if (nBlocks <= blocksLeft) return run(counter, data);
  const firstLen = Number(blocksLeft) * 16;
  const wrapped = ctr & ~mask;
  const iv2 = Buffer.alloc(16);
  let w = wrapped;
  for (let i = 15; i >= 0; i--) {
    iv2[i] = Number(w & 0xffn);
    w >>= 8n;
  }
  return concatBufs([run(counter, data.subarray(0, firstLen)), run(iv2, data.subarray(firstLen))]);
}

function cipherSync(alg, key, data, decrypt) {
  const ko = key[kKeyObject];
  switch (alg.name) {
    case "AES-GCM": {
      const tagLength = alg.tagLength === undefined ? 128 : alg.tagLength;
      if (!kGcmTagLengths.includes(tagLength)) throw opError(tagLength + " is not a valid AES-GCM tag length");
      if (alg.iv.length === 0) throw opError();
      const name = "aes-" + ko[kKeyData].length * 8 + "-gcm";
      const opts = { authTagLength: tagLength / 8 };
      try {
        if (!decrypt) {
          const c = createCipheriv(name, ko[kKeyData], alg.iv, opts);
          if (alg.additionalData) c.setAAD(alg.additionalData);
          return toArrayBuffer(concatBufs([c.update(data), c.final(), c.getAuthTag()]));
        }
        const tl = tagLength / 8;
        if (data.length < tl) throw opError();
        const d = createDecipheriv(name, ko[kKeyData], alg.iv, opts);
        if (alg.additionalData) d.setAAD(alg.additionalData);
        d.setAuthTag(data.subarray(data.length - tl));
        return toArrayBuffer(concatBufs([d.update(data.subarray(0, data.length - tl)), d.final()]));
      } catch (e) {
        throw opError();
      }
    }
    case "AES-CBC": {
      if (alg.iv.length !== 16) throw opError("algorithm.iv must contain exactly 16 bytes");
      const name = "aes-" + ko[kKeyData].length * 8 + "-cbc";
      try {
        const c = decrypt ? createDecipheriv(name, ko[kKeyData], alg.iv) : createCipheriv(name, ko[kKeyData], alg.iv);
        return toArrayBuffer(concatBufs([c.update(data), c.final()]));
      } catch (e) {
        throw opError();
      }
    }
    case "AES-CTR": {
      if (alg.counter.length !== 16) throw opError("algorithm.counter must contain exactly 16 bytes");
      if (!(alg.length >= 1 && alg.length <= 128)) throw opError("AES-CTR algorithm.length must be between 1 and 128");
      return toArrayBuffer(aesCtr(ko[kKeyData], alg.counter, alg.length, data, decrypt));
    }
    case "RSA-OAEP": {
      if (key.type !== (decrypt ? "private" : "public")) {
        throw domException("The requested operation is not valid for the provided key", "InvalidAccessError");
      }
      try {
        return toArrayBuffer(binding.rsaCrypt(decrypt ? "privdec" : "pubenc", ko[kHandle], data, 4,
          kNodeHash[key.algorithm.hash.name], alg.label));
      } catch (e) {
        throw opError();
      }
    }
  }
  throw domException("Unrecognized algorithm name", "NotSupportedError");
}

function aesKw(key, data, unwrap) {
  const ko = key[kKeyObject];
  const name = "id-aes" + ko[kKeyData].length * 8 + "-wrap";
  try {
    const iv = Buffer.from("A6A6A6A6A6A6A6A6", "hex");
    const c = unwrap ? createDecipheriv(name, ko[kKeyData], iv) : createCipheriv(name, ko[kKeyData], iv);
    return toArrayBuffer(concatBufs([c.update(data), c.final()]));
  } catch (e) {
    throw opError();
  }
}

function truncateBits(secret, length) {
  if (length === null) return toArrayBuffer(secret);
  if (length > secret.length * 8) throw opError("derived bit length is too small");
  const n = Math.ceil(length / 8);
  const out = Buffer.from(secret.subarray(0, n));
  const rem = length % 8;
  if (rem) out[n - 1] &= (0xff << (8 - rem)) & 0xff;
  return toArrayBuffer(out);
}

function deriveBitsSync(alg, key, length) {
  const ko = key[kKeyObject];
  switch (alg.name) {
    case "ECDH":
    case "X25519": {
      const pub = alg.public;
      if (!isCryptoKey(pub)) {
        throw makeError(TypeError, "ERR_INVALID_ARG_TYPE", "algorithm.public must be an instance of CryptoKey");
      }
      if (pub.type !== "public") throw domException("algorithm.public must be a public key", "InvalidAccessError");
      if (pub.algorithm.name !== key.algorithm.name) throw domException("key algorithm mismatch", "InvalidAccessError");
      if (alg.name === "ECDH" && pub.algorithm.namedCurve !== key.algorithm.namedCurve) {
        throw domException("Named curve mismatch", "InvalidAccessError");
      }
      let secret;
      try {
        secret = binding.diffieHellman(ko[kHandle], pub[kKeyObject][kHandle]);
      } catch (e) {
        throw opError();
      }
      return truncateBits(secret, length);
    }
    case "HKDF":
    case "PBKDF2": {
      if (length === null) throw opError("length cannot be null");
      if (length % 8 !== 0) throw opError("length must be a multiple of 8");
      if (alg.name === "PBKDF2" && alg.iterations === 0) throw opError("iterations cannot be zero");
      if (length === 0) return new ArrayBuffer(0);
      try {
        if (alg.name === "HKDF") {
          return toArrayBuffer(Buffer.from(hkdfSync(kNodeHash[alg.hash.name], ko[kKeyData], alg.salt, alg.info, length / 8)));
        }
        return toArrayBuffer(pbkdf2Sync(ko[kKeyData], alg.salt, alg.iterations, length / 8, kNodeHash[alg.hash.name]));
      } catch (e) {
        throw opError();
      }
    }
  }
  throw domException("Unrecognized algorithm name", "NotSupportedError");
}

function keyLength(alg) {
  if (alg.name in kAES) {
    if (alg.length !== 128 && alg.length !== 192 && alg.length !== 256) {
      throw opError("AES key length must be 128, 192, or 256 bits");
    }
    return alg.length;
  }
  if (alg.name === "HMAC") {
    if (alg.length === undefined) return kHashBlockBits[alg.hash.name];
    if (alg.length === 0) throw domException("Zero-length key is not supported", "TypeError");
    return alg.length;
  }
  return null;
}

async function generateKeyImpl(alg, extractable, usages) {
  const name = alg.name;
  if (name in kAES || name === "HMAC") {
    checkUsages(name, usages, "secret");
    let bits;
    if (name === "HMAC") {
      bits = alg.length === undefined ? kHashBlockBits[alg.hash.name] : alg.length;
      if (bits === 0) throw opError("HmacKeyGenParams.length cannot be 0");
      if (bits % 8 !== 0) throw domException("Unsupported HmacKeyGenParams.length", "NotSupportedError");
    } else {
      if (alg.length !== 128 && alg.length !== 192 && alg.length !== 256) {
        throw opError("AES key length must be 128, 192, or 256 bits");
      }
      bits = alg.length;
    }
    const bytes = randomBytes(Math.ceil(bits / 8));
    const algo = name === "HMAC" ? { name, length: bits, hash: { name: alg.hash.name } } : { name, length: bits };
    const key = createCryptoKey(newSecretKeyObject(bytes), algo, extractable, usages);
    checkNotEmptyUsages(key, true);
    return key;
  }
  checkUsages(name, usages, undefined, true);
  const rule = usageRule(name);
  let pair;
  let algo;
  if (name.startsWith("RSA")) {
    let e = 0;
    for (const b of alg.publicExponent) e = e * 256 + b;
    pair = await new Promise((resolve, reject) => {
      generateKeyPair("rsa", { modulusLength: alg.modulusLength, publicExponent: e }, (err, publicKey, privateKey) => {
        if (err) reject(opError());
        else resolve({ publicKey, privateKey });
      });
    });
    algo = { name, modulusLength: alg.modulusLength, publicExponent: new Uint8Array(alg.publicExponent), hash: { name: alg.hash.name } };
  } else if (name === "ECDSA" || name === "ECDH") {
    if (!kNodeCurve[alg.namedCurve]) throw domException("Unrecognized namedCurve", "NotSupportedError");
    pair = generateKeyPairSync("ec", { namedCurve: kNodeCurve[alg.namedCurve] });
    algo = { name, namedCurve: alg.namedCurve };
  } else {
    pair = generateKeyPairSync(name.toLowerCase());
    algo = { name };
  }
  const publicKey = createCryptoKey(pair.publicKey, algo, true, usages.filter((u) => rule.pub.includes(u)));
  const privateKey = createCryptoKey(pair.privateKey, algo, extractable, usages.filter((u) => rule.priv.includes(u)));
  checkNotEmptyUsages(privateKey, true);
  return { publicKey, privateKey };
}

// ---- SubtleCrypto ----------------------------------------------------------------------------

function checkSubtleThis(self) {
  if (self === null || typeof self !== "object" || self[kBrand] !== "SubtleCrypto") throw errInvalidThis("SubtleCrypto");
}

function keyDataArg(method, format, v, i) {
  if (format === "jwk") {
    if (v === null || typeof v !== "object") {
      throw makeError(TypeError, "ERR_INVALID_ARG_TYPE", webidlPrefix(method) + ordinals[i] + " argument is not of type JsonWebKey.");
    }
    return v;
  }
  return bufferSourceArg(method, v, i);
}

function importKeyFull(format, keyData, algorithm, extractable, usages) {
  const alg = normalizeAlgorithm(algorithm, "importKey");
  const key = importKeySync(format, keyData, alg, extractable, usages);
  checkNotEmptyUsages(key, false);
  return key;
}

class SubtleCrypto {
  constructor() {
    throw illegalConstructor();
  }

  async digest(algorithm, data) {
    checkSubtleThis(this);
    requireArgs("digest", arguments, 2);
    data = bufferSourceArg("digest", data, 1);
    return digestSync(normalizeAlgorithm(algorithm, "digest"), data);
  }

  async generateKey(algorithm, extractable, keyUsages) {
    checkSubtleThis(this);
    requireArgs("generateKey", arguments, 3);
    const usages = usagesArg("generateKey", keyUsages, 2);
    return generateKeyImpl(normalizeAlgorithm(algorithm, "generateKey"), !!extractable, usages);
  }

  async importKey(format, keyData, algorithm, extractable, keyUsages) {
    checkSubtleThis(this);
    requireArgs("importKey", arguments, 5);
    format = enumArg("importKey", format, 0, kKeyFormats, "KeyFormat");
    keyData = keyDataArg("importKey", format, keyData, 1);
    const usages = usagesArg("importKey", keyUsages, 4);
    return importKeyFull(format, keyData, algorithm, !!extractable, usages);
  }

  async exportKey(format, key) {
    checkSubtleThis(this);
    requireArgs("exportKey", arguments, 2);
    format = enumArg("exportKey", format, 0, kKeyFormats, "KeyFormat");
    cryptoKeyArg("exportKey", key, 1);
    const kdf = key.algorithm.name;
    if (kdf === "HKDF" || kdf === "PBKDF2") throw domException(kdf + " key export is not supported", "NotSupportedError");
    if (!key.extractable) throw domException("key is not extractable", "InvalidAccessError");
    return exportKeySync(format, key);
  }

  async sign(algorithm, key, data) {
    checkSubtleThis(this);
    requireArgs("sign", arguments, 3);
    cryptoKeyArg("sign", key, 1);
    data = bufferSourceArg("sign", data, 2);
    const alg = normalizeAlgorithm(algorithm, "sign");
    checkKeyFor(alg, key, "sign");
    return signSync(alg, key, data);
  }

  async verify(algorithm, key, signature, data) {
    checkSubtleThis(this);
    requireArgs("verify", arguments, 4);
    cryptoKeyArg("verify", key, 1);
    signature = bufferSourceArg("verify", signature, 2);
    data = bufferSourceArg("verify", data, 3);
    const alg = normalizeAlgorithm(algorithm, "verify");
    checkKeyFor(alg, key, "verify");
    return verifySync(alg, key, signature, data);
  }

  async encrypt(algorithm, key, data) {
    checkSubtleThis(this);
    requireArgs("encrypt", arguments, 3);
    cryptoKeyArg("encrypt", key, 1);
    data = bufferSourceArg("encrypt", data, 2);
    const alg = normalizeAlgorithm(algorithm, "encrypt");
    checkKeyFor(alg, key, "encrypt");
    return cipherSync(alg, key, data, false);
  }

  async decrypt(algorithm, key, data) {
    checkSubtleThis(this);
    requireArgs("decrypt", arguments, 3);
    cryptoKeyArg("decrypt", key, 1);
    data = bufferSourceArg("decrypt", data, 2);
    const alg = normalizeAlgorithm(algorithm, "decrypt");
    checkKeyFor(alg, key, "decrypt");
    return cipherSync(alg, key, data, true);
  }

  async deriveBits(algorithm, baseKey, length = null) {
    checkSubtleThis(this);
    requireArgs("deriveBits", arguments, 2);
    cryptoKeyArg("deriveBits", baseKey, 1);
    if (length !== null) length = Number(length);
    const alg = normalizeAlgorithm(algorithm, "deriveBits");
    if (!baseKey.usages.includes("deriveBits")) {
      throw domException("baseKey does not have deriveBits usage", "InvalidAccessError");
    }
    if (baseKey.algorithm.name !== alg.name) throw domException("Key algorithm mismatch", "InvalidAccessError");
    return deriveBitsSync(alg, baseKey, length);
  }

  async deriveKey(algorithm, baseKey, derivedKeyType, extractable, keyUsages) {
    checkSubtleThis(this);
    requireArgs("deriveKey", arguments, 5);
    cryptoKeyArg("deriveKey", baseKey, 1);
    const usages = usagesArg("deriveKey", keyUsages, 4);
    const alg = normalizeAlgorithm(algorithm, "deriveBits");
    const derivedAlg = normalizeAlgorithm(derivedKeyType, "importKey");
    const length = keyLength(normalizeAlgorithm(derivedKeyType, "get key length"));
    if (!baseKey.usages.includes("deriveKey")) {
      throw domException("baseKey does not have deriveKey usage", "InvalidAccessError");
    }
    if (baseKey.algorithm.name !== alg.name) throw domException("Key algorithm mismatch", "InvalidAccessError");
    const bits = deriveBitsSync(alg, baseKey, length);
    const key = importKeySync("raw", Buffer.from(bits), derivedAlg, !!extractable, usages);
    checkNotEmptyUsages(key, false);
    return key;
  }

  async wrapKey(format, key, wrappingKey, wrapAlgorithm) {
    checkSubtleThis(this);
    requireArgs("wrapKey", arguments, 4);
    format = enumArg("wrapKey", format, 0, kKeyFormats, "KeyFormat");
    cryptoKeyArg("wrapKey", key, 1);
    cryptoKeyArg("wrapKey", wrappingKey, 2);
    let alg;
    try {
      alg = normalizeAlgorithm(wrapAlgorithm, "wrapKey");
    } catch (e) {
      alg = normalizeAlgorithm(wrapAlgorithm, "encrypt");
    }
    checkKeyFor(alg, wrappingKey, "wrapKey");
    if (!key.extractable) throw domException("key is not extractable", "InvalidAccessError");
    let bytes = exportKeySync(format, key);
    if (format === "jwk") {
      let raw = JSON.stringify(bytes);
      if (alg.name === "AES-KW" && raw.length % 8 !== 0) raw += " ".repeat(8 - (raw.length % 8));
      bytes = Buffer.from(raw, "utf8");
    } else {
      bytes = Buffer.from(bytes);
    }
    if (alg.name === "AES-KW") return aesKw(wrappingKey, bytes, false);
    return cipherSync(alg, wrappingKey, bytes, false);
  }

  async unwrapKey(format, wrappedKey, unwrappingKey, unwrapAlgorithm, unwrappedKeyAlgorithm, extractable, keyUsages) {
    checkSubtleThis(this);
    requireArgs("unwrapKey", arguments, 7);
    format = enumArg("unwrapKey", format, 0, kKeyFormats, "KeyFormat");
    wrappedKey = bufferSourceArg("unwrapKey", wrappedKey, 1);
    cryptoKeyArg("unwrapKey", unwrappingKey, 2);
    const usages = usagesArg("unwrapKey", keyUsages, 6);
    let alg;
    try {
      alg = normalizeAlgorithm(unwrapAlgorithm, "unwrapKey");
    } catch (e) {
      alg = normalizeAlgorithm(unwrapAlgorithm, "decrypt");
    }
    checkKeyFor(alg, unwrappingKey, "unwrapKey");
    const raw = Buffer.from(alg.name === "AES-KW" ? aesKw(unwrappingKey, wrappedKey, true) : cipherSync(alg, unwrappingKey, wrappedKey, true));
    let keyData = raw;
    if (format === "jwk") {
      try {
        keyData = JSON.parse(raw.toString("utf8"));
      } catch (e) {
        throw domException("Invalid wrapped JWK key", "DataError");
      }
    }
    return importKeyFull(format, keyData, unwrappedKeyAlgorithm, !!extractable, usages);
  }

  async getPublicKey(key, keyUsages) {
    checkSubtleThis(this);
    requireArgs("getPublicKey", arguments, 2);
    cryptoKeyArg("getPublicKey", key, 0);
    const usages = usagesArg("getPublicKey", keyUsages, 1);
    if (key.type !== "private") throw domException("key must be a private key", "InvalidAccessError");
    checkUsages(key.algorithm.name, usages, "public");
    return createCryptoKey(createPublicKey(key[kKeyObject]), key.algorithm, true, usages);
  }

  async encapsulateBits(algorithm) {
    checkSubtleThis(this);
    normalizeAlgorithm(algorithm, "encapsulate");
  }

  async encapsulateKey(algorithm) {
    checkSubtleThis(this);
    normalizeAlgorithm(algorithm, "encapsulate");
  }

  async decapsulateBits(algorithm) {
    checkSubtleThis(this);
    normalizeAlgorithm(algorithm, "decapsulate");
  }

  async decapsulateKey(algorithm) {
    checkSubtleThis(this);
    normalizeAlgorithm(algorithm, "decapsulate");
  }

  static supports(operation, algorithm, lengthOrAdditionalAlgorithm) {
    const op = String(operation);
    const table = op;
    if (!kSupported[table]) return false;
    try {
      const alg = normalizeAlgorithm(algorithm, table);
      if (op === "generateKey" && (alg.name in kAES)) keyLength(alg);
      return true;
    } catch (e) {
      return false;
    }
  }
}
kSupported.encapsulate = {};
kSupported.decapsulate = {};
Object.defineProperty(SubtleCrypto.prototype, Symbol.toStringTag, { value: "SubtleCrypto", configurable: true });

const subtle = Object.create(SubtleCrypto.prototype);
Object.defineProperty(subtle, kBrand, { value: "SubtleCrypto" });

// ---- Crypto ------------------------------------------------------------------------------------

class Crypto {
  constructor() {
    throw illegalConstructor();
  }

  get subtle() {
    if (this === null || typeof this !== "object" || this[kBrand] !== "Crypto") throw errInvalidThis("Crypto");
    return subtle;
  }

  getRandomValues(array) {
    if (this === null || typeof this !== "object" || this[kBrand] !== "Crypto") throw errInvalidThis("Crypto");
    if (arguments.length < 1) {
      throw makeError(TypeError, "ERR_MISSING_ARGS",
        "Failed to execute 'getRandomValues' on 'Crypto': 1 argument required, but only 0 present.");
    }
    return getRandomValues(array);
  }

  randomUUID() {
    if (this === null || typeof this !== "object" || this[kBrand] !== "Crypto") throw errInvalidThis("Crypto");
    return randomUUID();
  }
}
Object.defineProperty(Crypto.prototype, Symbol.toStringTag, { value: "Crypto", configurable: true });

const webcrypto = Object.create(Crypto.prototype);
Object.defineProperty(webcrypto, kBrand, { value: "Crypto" });

// The Go side reads these to install the web globals lazily.
binding.webGlobals = { crypto: webcrypto, Crypto, CryptoKey, SubtleCrypto };
