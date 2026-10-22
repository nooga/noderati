
// ---- key generation --------------------------------------------------------------------

const kSupportedKeyPairTypes = ["rsa", "rsa-pss", "ec", "ed25519", "x25519", "ml-dsa-44", "ml-dsa-65", "ml-dsa-87", "ml-kem-768", "ml-kem-1024"];

function errUnsupportedKeyType(type) {
  return errInvalidArgValue("type", type, "must be a supported key type");
}

function parseKeyPairEncoding(keyType, options) {
  const { publicKeyEncoding, privateKeyEncoding } = options || {};
  let pub, priv;
  if (publicKeyEncoding != null) {
    if (typeof publicKeyEncoding !== "object") throw errInvalidArgValue("options.publicKeyEncoding", publicKeyEncoding);
    pub = parsePublicKeyEncoding(publicKeyEncoding, keyType, "options.publicKeyEncoding");
  }
  if (privateKeyEncoding != null) {
    if (typeof privateKeyEncoding !== "object") throw errInvalidArgValue("options.privateKeyEncoding", privateKeyEncoding);
    priv = parsePrivateKeyEncoding(privateKeyEncoding, keyType, "options.privateKeyEncoding");
  }
  return { pub, priv };
}

function keyGenParams(type, options) {
  const params = {};
  switch (type) {
    case "rsa":
    case "rsa-pss": {
      validateObject(options, "options");
      const { modulusLength } = options;
      validateUint32(modulusLength, "options.modulusLength");
      params.modulusLength = modulusLength;
      let { publicExponent } = options;
      if (publicExponent === undefined) publicExponent = 0x10001;
      else validateUint32(publicExponent, "options.publicExponent");
      params.publicExponent = publicExponent;
      if (modulusLength < 512) {
        throw makeError(Error, "ERR_OSSL_RSA_KEY_SIZE_TOO_SMALL", "error:1C8000BB:Provider routines::key size too small");
      }
      if (publicExponent < 3 || publicExponent % 2 === 0) {
        throw makeError(Error, "ERR_OSSL_RSA_BAD_E_VALUE", "error:1C8000BC:Provider routines::bad e value");
      }
      if (type === "rsa-pss") {
        const { hashAlgorithm, mgf1HashAlgorithm, saltLength } = options;
        if (saltLength !== undefined) {
          validateInt32(saltLength, "options.saltLength", 0);
          params.saltLength = saltLength;
        }
        if (hashAlgorithm !== undefined) {
          validateString(hashAlgorithm, "options.hashAlgorithm");
          if (!binding.digestInfo(hashAlgorithm)) {
            throw makeError(TypeError, "ERR_CRYPTO_INVALID_DIGEST", "Invalid digest: " + hashAlgorithm);
          }
          params.hashAlgorithm = hashAlgorithm;
        }
        if (mgf1HashAlgorithm !== undefined) {
          validateString(mgf1HashAlgorithm, "options.mgf1HashAlgorithm");
          if (!binding.digestInfo(mgf1HashAlgorithm)) {
            throw makeError(TypeError, "ERR_CRYPTO_INVALID_DIGEST", "Invalid MGF1 digest: " + mgf1HashAlgorithm);
          }
          params.mgf1HashAlgorithm = mgf1HashAlgorithm;
        }
      }
      break;
    }
    case "ec": {
      validateObject(options, "options");
      const { namedCurve, paramEncoding } = options;
      validateString(namedCurve, "options.namedCurve");
      if (paramEncoding !== undefined && paramEncoding !== "named") {
        if (paramEncoding === "explicit") {
          throw makeError(Error, "ERR_FEATURE_UNAVAILABLE_ON_PLATFORM",
            "Explicit EC parameter encoding is not supported by noderati");
        }
        throw errInvalidArgValue("options.paramEncoding", paramEncoding);
      }
      if (!getCurves().includes(normalizeCurveName(namedCurve))) {
        throw makeError(TypeError, "ERR_CRYPTO_INVALID_CURVE", "Invalid EC curve name");
      }
      params.namedCurve = namedCurve;
      break;
    }
    default:
      if (!kSupportedKeyPairTypes.includes(type)) throw errUnsupportedKeyType(type);
      if (options !== undefined) validateObject(options, "options");
  }
  return params;
}

function normalizeCurveName(name) {
  const n = String(name).toLowerCase();
  if (n === "p-256" || n === "secp256r1") return "prime256v1";
  if (n === "p-384") return "secp384r1";
  if (n === "p-521") return "secp521r1";
  return n;
}

function encodeGeneratedKey(ko, enc) {
  if (!enc) return ko;
  return ko.export(enc);
}

function generateKeyPairSync(type, options) {
  validateString(type, "type");
  const params = keyGenParams(type, options);
  const { pub, priv } = parseKeyPairEncoding(type, options);
  const [p, s] = binding.keyGenerate(type, JSON.stringify(params));
  return {
    publicKey: encodeGeneratedKey(newAsymmetricKeyObject(p.type, p.handle), pub),
    privateKey: encodeGeneratedKey(newAsymmetricKeyObject(s.type, s.handle), priv),
  };
}

function generateKeyPair(type, options, callback) {
  if (typeof options === "function") {
    callback = options;
    options = undefined;
  }
  validateFunction(callback, "callback");
  validateString(type, "type");
  const params = keyGenParams(type, options);
  const { pub, priv } = parseKeyPairEncoding(type, options);
  binding.keyGenerate(type, JSON.stringify(params), (err, pair) => {
    if (err) return callback(err);
    let publicKey, privateKey;
    try {
      publicKey = encodeGeneratedKey(newAsymmetricKeyObject(pair[0].type, pair[0].handle), pub);
      privateKey = encodeGeneratedKey(newAsymmetricKeyObject(pair[1].type, pair[1].handle), priv);
    } catch (e) {
      return callback(e);
    }
    callback(null, publicKey, privateKey);
  });
}

function secretKeyLength(type, options) {
  validateString(type, "type");
  validateObject(options, "options");
  const { length } = options;
  switch (type) {
    case "hmac":
      validateInteger(length, "options.length", 8, 2 ** 31 - 1);
      break;
    case "aes":
      validateOneOf(length, "options.length", [128, 192, 256]);
      break;
    default:
      throw errUnsupportedKeyType(type);
  }
  return length;
}

function generateKeySync(type, options) {
  const length = secretKeyLength(type, options);
  return newSecretKeyObject(binding.randomBytes(Math.floor(length / 8)));
}

function generateKey(type, options, callback) {
  if (typeof options === "function") {
    callback = options;
    options = undefined;
  }
  validateFunction(callback, "callback");
  const length = secretKeyLength(type, options);
  const key = newSecretKeyObject(binding.randomBytes(Math.floor(length / 8)));
  defer(() => callback(null, key));
}

function getCurves() {
  return binding.getCurves();
}
