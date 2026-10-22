// ---- X509Certificate ----------------------------------------------------------------

const kX509 = Symbol("kX509");

function x509Info(cert) {
  const s = cert[kX509];
  if (!s.info) s.info = JSON.parse(binding.x509Info(s.handle));
  return s.info;
}

function nullIfMissing(v) {
  return v === null ? undefined : v;
}

function legacyObject(pairs) {
  const o = Object.create(null);
  for (const [k, v] of pairs) {
    if (k in o) {
      if (Array.isArray(o[k])) o[k].push(v);
      else o[k] = [o[k], v];
    } else {
      o[k] = v;
    }
  }
  return o;
}

function validateX509CheckOptions(options) {
  if (options === undefined) return { subject: "default", wildcards: true, partialWildcards: true, multiLabelWildcards: false };
  validateObject(options, "options");
  const {
    subject = "default",
    wildcards = true,
    partialWildcards = true,
    multiLabelWildcards = false,
    singleLabelSubdomains = false,
  } = options;
  validateBoolean(wildcards, "options.wildcards");
  validateBoolean(partialWildcards, "options.partialWildcards");
  validateBoolean(multiLabelWildcards, "options.multiLabelWildcards");
  validateBoolean(singleLabelSubdomains, "options.singleLabelSubdomains");
  if (subject !== "default" && subject !== "always" && subject !== "never") {
    throw errInvalidArgValue("options.subject", subject);
  }
  return { subject, wildcards, partialWildcards, multiLabelWildcards };
}

function errInvalidPkey(pkey) {
  return makeError(TypeError, "ERR_INVALID_ARG_VALUE",
    "The argument 'pkey' is invalid. Received " + pkey.constructor.name + " {}");
}

class X509Certificate {
  constructor(buffer) {
    if (typeof buffer === "string") buffer = Buffer.from(buffer);
    if (!isArrayBufferView(buffer)) {
      throw errInvalidArgType("buffer", "of type string or an instance of Buffer, TypedArray, or DataView", buffer);
    }
    this[kX509] = { handle: binding.x509Parse(toU8(buffer)), info: null };
  }

  get subject() { return x509Info(this).subject; }
  get subjectAltName() { return nullIfMissing(x509Info(this).subjectAltName); }
  get issuer() { return x509Info(this).issuer; }
  get issuerCertificate() { return undefined; }
  get infoAccess() { return nullIfMissing(x509Info(this).infoAccess); }
  get validFrom() { return x509Info(this).validFrom; }
  get validTo() { return x509Info(this).validTo; }
  get validFromDate() { return new Date(x509Info(this).validFromMs); }
  get validToDate() { return new Date(x509Info(this).validToMs); }
  get fingerprint() { return x509Info(this).fingerprint; }
  get fingerprint256() { return x509Info(this).fingerprint256; }
  get fingerprint512() { return x509Info(this).fingerprint512; }
  get keyUsage() {
    const k = x509Info(this).keyUsage;
    return k ? k.slice() : undefined;
  }
  get serialNumber() { return x509Info(this).serialNumber; }
  get signatureAlgorithm() { return x509Info(this).signatureAlgorithm; }
  get signatureAlgorithmOid() { return x509Info(this).signatureAlgorithmOid; }
  get raw() { return binding.x509Raw(this[kX509].handle, "raw"); }
  get publicKey() {
    const r = binding.x509PublicKey(this[kX509].handle);
    return newAsymmetricKeyObject("public", r.handle);
  }
  get ca() { return x509Info(this).ca; }

  toString() { return x509Info(this).pem; }
  toJSON() { return this.toString(); }

  checkHost(name, options) {
    validateString(name, "name");
    const o = validateX509CheckOptions(options);
    return binding.x509Check(this[kX509].handle, "host", name, o.subject, o.wildcards, o.partialWildcards, o.multiLabelWildcards);
  }

  checkEmail(email, options) {
    validateString(email, "email");
    const o = validateX509CheckOptions(options);
    return binding.x509Check(this[kX509].handle, "email", email, o.subject);
  }

  checkIP(ip, options) {
    validateString(ip, "ip");
    if (options !== undefined) validateObject(options, "options");
    if (!isIPString(ip)) throw makeError(TypeError, "ERR_INVALID_ARG_VALUE", "Invalid IP");
    return binding.x509Check(this[kX509].handle, "ip", ip, "");
  }

  checkIssued(otherCert) {
    if (!(otherCert instanceof X509Certificate)) {
      throw errInvalidArgType("otherCert", "an instance of X509Certificate", otherCert);
    }
    return binding.x509CheckIssued(this[kX509].handle, otherCert[kX509].handle);
  }

  checkPrivateKey(pkey) {
    if (!isKeyObject(pkey)) throw errInvalidArgType("pkey", "an instance of KeyObject", pkey);
    if (pkey.type !== "private") throw errInvalidPkey(pkey);
    return binding.x509CheckPrivateKey(this[kX509].handle, pkey[kHandle]);
  }

  verify(pkey) {
    if (!isKeyObject(pkey)) throw errInvalidArgType("pkey", "an instance of KeyObject", pkey);
    if (pkey.type !== "public") throw errInvalidPkey(pkey);
    return binding.x509Verify(this[kX509].handle, pkey[kHandle]);
  }

  toLegacyObject() {
    const i = x509Info(this);
    return {
      subject: legacyObject(i.subjectLegacy),
      issuer: legacyObject(i.issuerLegacy),
      subjectaltname: nullIfMissing(i.subjectAltName),
      infoAccess: i.infoAccessLegacy ? legacyInfoAccess(i.infoAccessLegacy) : undefined,
      ca: i.ca,
      modulus: i.modulus,
      exponent: i.exponent,
      pubkey: binding.x509Raw(this[kX509].handle, "pubkey"),
      bits: i.bits,
      valid_from: i.validFrom,
      valid_to: i.validTo,
      fingerprint: i.fingerprint,
      fingerprint256: i.fingerprint256,
      fingerprint512: i.fingerprint512,
      ext_key_usage: i.keyUsage ? i.keyUsage.slice() : undefined,
      serialNumber: i.serialNumber,
      raw: this.raw,
      asn1Curve: i.asn1Curve,
      nistCurve: i.nistCurve,
    };
  }
}

function legacyInfoAccess(pairs) {
  const o = Object.create(null);
  for (const [k, v] of pairs) {
    if (o[k]) o[k].push(v);
    else o[k] = [v];
  }
  return o;
}

// ---- Certificate (SPKAC) -----------------------------------------------------------

function spkacArg(spkac, encoding) {
  if (typeof spkac !== "string" && !isAnyArrayBuffer(spkac) && !isArrayBufferView(spkac)) {
    throw errInvalidArgType("spkac", "of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView", spkac);
  }
  return getArrayBufferOrView(spkac, "spkac", encoding);
}

function certVerifySpkac(spkac, encoding) {
  return binding.spkac("verify", spkacArg(spkac, encoding));
}

function certExportPublicKey(spkac, encoding) {
  return binding.spkac("pubkey", spkacArg(spkac, encoding));
}

function certExportChallenge(spkac, encoding) {
  return binding.spkac("challenge", spkacArg(spkac, encoding));
}

function Certificate() {
  if (!new.target) return new Certificate();
}
Certificate.prototype.verifySpkac = certVerifySpkac;
Certificate.prototype.exportPublicKey = certExportPublicKey;
Certificate.prototype.exportChallenge = certExportChallenge;
Certificate.exportChallenge = certExportChallenge;
Certificate.exportPublicKey = certExportPublicKey;
Certificate.verifySpkac = certVerifySpkac;

function isIPString(s) {
  if (/^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/.test(s)) return true;
  if (s.indexOf(":") === -1) return false;
  return binding.isIPv6(s);
}
