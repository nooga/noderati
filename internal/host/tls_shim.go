package host

// tlsShim is node:tls's JS half - a port of Node's lib/_tls_wrap.js and
// lib/tls.js: TLSSocket extends net.Socket (the TLS layer lives inside
// the native handle, see tls.go's startTLS), tls.Server extends
// net.Server and wraps each accepted socket.
const tlsShim = `import net from "net";
import { Buffer } from "buffer";

const I = globalThis.__noderatiNetInternals;
const tb = globalThis.__noderatiTLSBinding;
const { makeErr, ERR_INVALID_ARG_TYPE, ERR_INVALID_ARG_VALUE, validateFunction, validateString, kOwner } = I;

const DEFAULT_CIPHERS_STR = "TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256:TLS_AES_128_GCM_SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES256-GCM-SHA384:DHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-SHA256:DHE-RSA-AES128-SHA256:ECDHE-RSA-AES256-SHA384:DHE-RSA-AES256-SHA384:ECDHE-RSA-AES256-SHA256:DHE-RSA-AES256-SHA256:HIGH:!aNULL:!eNULL:!EXPORT:!DES:!RC4:!MD5:!PSK:!SRP:!CAMELLIA";
const tlsDefaults = {
  DEFAULT_CIPHERS: DEFAULT_CIPHERS_STR,
  DEFAULT_ECDH_CURVE: "auto",
  DEFAULT_MIN_VERSION: "TLSv1.2",
  DEFAULT_MAX_VERSION: "TLSv1.3",
};
const CLIENT_RENEG_LIMIT = 3;
const CLIENT_RENEG_WINDOW = 600;

function getCiphers() { return tb.getCiphers(); }

let rootCertificatesCache = null;
function getRootCertificates() {
  if (!rootCertificatesCache) rootCertificatesCache = Object.freeze(tb.rootCertificates());
  return rootCertificatesCache;
}

let defaultCACertificates = null;
function getCACertificates(type = "default") {
  validateString(type, "type");
  switch (type) {
    case "default": return defaultCACertificates ? defaultCACertificates.slice() : getRootCertificates().slice();
    case "system": return getRootCertificates().slice();
    case "bundled": return getRootCertificates().slice();
    case "extra": return [];
  }
  throw ERR_INVALID_ARG_VALUE("type", type);
}
function setDefaultCACertificates(certs) {
  if (!Array.isArray(certs)) throw ERR_INVALID_ARG_TYPE("certs", "Array", certs);
  const list = certs.map((c, i) => {
    if (typeof c === "string") return c;
    if (c instanceof Uint8Array) return Buffer.from(c).toString("latin1");
    throw ERR_INVALID_ARG_TYPE("certs[" + i + "]", ["string", "ArrayBufferView"], c);
  });
  defaultCACertificates = list;
  tb.setDefaultCA(list);
}

function convertALPNProtocols(protocols, out) {
  if (Array.isArray(protocols)) {
    const parts = [];
    for (const p of protocols) {
      const b = Buffer.from(p);
      if (b.length > 255) throw makeErr(RangeError, "ERR_OUT_OF_RANGE", 'The byte length of the protocol at index ' + parts.length + ' exceeds the maximum length. It must be <= 255. Received ' + b.length);
      parts.push(Buffer.from([b.length]), b);
    }
    out.ALPNProtocols = Buffer.concat(parts);
  } else if (protocols instanceof Uint8Array) {
    out.ALPNProtocols = Buffer.from(protocols);
  } else if (ArrayBuffer.isView(protocols)) {
    out.ALPNProtocols = Buffer.from(protocols.buffer.slice(protocols.byteOffset, protocols.byteOffset + protocols.byteLength));
  }
}
// The native side wants plain string lists.
function alpnList(protocols) {
  if (protocols == null) return undefined;
  if (Array.isArray(protocols)) return protocols.map(String);
  if (protocols instanceof Uint8Array) {
    const out = [];
    for (let i = 0; i < protocols.length;) {
      const n = protocols[i];
      out.push(Buffer.from(protocols.subarray(i + 1, i + 1 + n)).toString());
      i += 1 + n;
    }
    return out;
  }
  return undefined;
}

class SecureContext {
  constructor(secureProtocol, secureOptions, minVersion, maxVersion) {
    this.context = null;
    this._minVersion = minVersion;
    this._maxVersion = maxVersion;
  }
}
function createSecureContext(options) {
  if (options == null) options = {};
  else if (typeof options !== "object") throw ERR_INVALID_ARG_TYPE("options", "Object", options);
  const { minVersion, maxVersion } = options;
  for (const [k, v] of [["options.minVersion", minVersion], ["options.maxVersion", maxVersion]]) {
    if (v !== undefined && !["TLSv1", "TLSv1.1", "TLSv1.2", "TLSv1.3"].includes(v)) {
      throw makeErr(TypeError, "ERR_TLS_INVALID_PROTOCOL_VERSION", v + " is not a valid " + k.slice(8) + " TLS protocol version");
    }
  }
  const c = new SecureContext(options.secureProtocol, options.secureOptions, minVersion, maxVersion);
  try {
    c.context = tb.createSecureContext({ key: options.key, cert: options.cert, ca: options.ca, ciphers: options.ciphers });
  } catch (e) {
    const msg = String(e && e.message || e);
    const err = new Error(msg);
    if (/no private key/.test(msg)) err.code = "ERR_OSSL_SSL_NO_PRIVATE_KEY_ASSIGNED";
    else err.code = "ERR_OSSL_UNSUPPORTED";
    throw err;
  }
  return c;
}

// ---- checkServerIdentity --------------------------------------------
function unfqdn(host) { return host.replace(/[.]$/, ""); }
function splitHost(host) { return unfqdn(host).toLowerCase().split("."); }
function check(hostParts, pattern, wildcards) {
  if (!pattern) return false;
  const patternParts = splitHost(pattern);
  if (hostParts.length !== patternParts.length) return false;
  if (patternParts.includes("")) return false;
  const isBad = (s) => /[^!-\u007F]/u.test(s);
  if (patternParts.some(isBad)) return false;
  for (let i = hostParts.length - 1; i > 0; i -= 1) {
    if (hostParts[i] !== patternParts[i]) return false;
  }
  const hostSubdomain = hostParts[0];
  const patternSubdomain = patternParts[0];
  const patternSubdomainParts = patternSubdomain.split("*", 3);
  if (patternSubdomainParts.length === 1 || patternSubdomain.includes("xn--")) return hostSubdomain === patternSubdomain;
  if (!wildcards) return false;
  if (patternSubdomainParts.length > 2) return false;
  if (patternParts.length <= 2) return false;
  const [prefix, suffix] = patternSubdomainParts;
  if (prefix.length + suffix.length > hostSubdomain.length) return false;
  if (!hostSubdomain.startsWith(prefix)) return false;
  if (!hostSubdomain.endsWith(suffix)) return false;
  return true;
}
function canonicalizeIP(ip) {
  if (net.isIPv4(ip)) return ip;
  if (!net.isIPv6(ip)) return ip;
  try {
    const sa = new net.SocketAddress({ address: ip, family: "ipv6" });
    let v = 0n;
    const s = sa.address.toLowerCase();
    const [a, b] = s.includes("::") ? s.split("::") : [s, null];
    const ha = a ? a.split(":") : [];
    const hb = b === null ? [] : (b ? b.split(":") : []);
    const parts = b === null ? ha : [...ha, ...new Array(8 - ha.length - hb.length).fill("0"), ...hb];
    const words = parts.map((p) => parseInt(p, 16));
    let bestStart = -1, bestLen = 0;
    for (let i = 0; i < 8;) {
      if (words[i] !== 0) { i++; continue; }
      let j = i;
      while (j < 8 && words[j] === 0) j++;
      if (j - i > bestLen && j - i > 1) { bestStart = i; bestLen = j - i; }
      i = j;
    }
    const hex = words.map((w) => w.toString(16));
    if (bestStart < 0) return hex.join(":");
    return hex.slice(0, bestStart).join(":") + "::" + hex.slice(bestStart + bestLen).join(":");
  } catch {
    return ip;
  }
}
function splitEscapedAltNames(altNames) {
  const result = [];
  let currentToken = "";
  let offset = 0;
  while (offset !== altNames.length) {
    const nextSep = altNames.indexOf(",", offset);
    const nextQuote = altNames.indexOf('"', offset);
    if (nextQuote !== -1 && (nextSep === -1 || nextQuote < nextSep)) {
      currentToken += altNames.substring(offset, nextQuote);
      const match = /^"((?:[^"\\]|\\.)*)"/.exec(altNames.substring(nextQuote));
      if (!match) throw makeErr(Error, "ERR_TLS_CERT_ALTNAME_FORMAT", "Invalid subject alternative name string");
      currentToken += JSON.parse(match[0]);
      offset = nextQuote + match[0].length;
    } else if (nextSep !== -1) {
      currentToken += altNames.substring(offset, nextSep);
      result.push(currentToken);
      currentToken = "";
      offset = nextSep + 2;
    } else {
      currentToken += altNames.substring(offset);
      offset = altNames.length;
    }
  }
  result.push(currentToken);
  return result;
}
function checkServerIdentity(hostname, cert) {
  const subject = cert.subject;
  const altNames = cert.subjectaltname;
  const dnsNames = [];
  const ips = [];
  hostname = "" + hostname;
  if (altNames) {
    const splitAltNames = altNames.includes('"') ? splitEscapedAltNames(altNames) : altNames.split(", ");
    for (const name of splitAltNames) {
      if (name.startsWith("DNS:")) dnsNames.push(name.slice(4));
      else if (name.startsWith("IP Address:")) ips.push(canonicalizeIP(name.slice(11)));
    }
  }
  let valid = false;
  let reason = "Unknown reason";
  hostname = unfqdn(hostname);
  if (net.isIP(hostname)) {
    valid = ips.includes(canonicalizeIP(hostname));
    if (!valid) reason = "IP: " + hostname + " is not in the cert's list: " + ips.join(", ");
  } else if (dnsNames.length > 0 || (subject && subject.CN)) {
    const hostParts = splitHost(hostname);
    const wildcard = (pattern) => check(hostParts, pattern, true);
    if (dnsNames.length > 0) {
      valid = dnsNames.some(wildcard);
      if (!valid) reason = "Host: " + hostname + ". is not in the cert's altnames: " + altNames;
    } else {
      const cn = subject.CN;
      if (Array.isArray(cn)) valid = cn.some(wildcard);
      else if (cn) valid = wildcard(cn);
      if (!valid) reason = "Host: " + hostname + ". is not cert's CN: " + cn;
    }
  } else {
    reason = "Cert does not contain a DNS name";
  }
  if (!valid) {
    const err = makeErr(Error, "ERR_TLS_CERT_ALTNAME_INVALID", "Hostname/IP does not match certificate's altnames: " + reason);
    err.reason = reason;
    err.host = hostname;
    err.cert = cert;
    return err;
  }
}

// ---- TLSSocket ------------------------------------------------------
const kConnectOptions = Symbol("connect-options");
const kRes = Symbol("res");
const kPendingSession = Symbol("pendingSession");
const caHintCodes = new Set(["DEPTH_ZERO_SELF_SIGNED_CERT", "SELF_SIGNED_CERT_IN_CHAIN", "UNABLE_TO_GET_ISSUER_CERT_LOCALLY", "UNABLE_TO_VERIFY_LEAF_SIGNATURE"]);

function verifyErrorFromInfo(info) {
  if (!info) return null;
  let msg = info.message;
  if (caHintCodes.has(info.code)) msg += "; if the root CA is installed locally, try running Node.js with --use-system-ca";
  const e = new Error(msg);
  e.code = info.code;
  return e;
}
function tlsErrorFromInfo(info) {
  if (info.code === "ECONNRESET" || info.errno) {
    const e = new Error(info.message || (info.syscall ? info.syscall + " " + info.code : info.code));
    if (info.errno) e.errno = info.errno;
    e.code = info.code;
    if (info.syscall) e.syscall = info.syscall;
    return e;
  }
  const e = new Error(info.message);
  e.library = "SSL routines";
  e.reason = info.message.replace(/^.*::?/, "");
  e.code = info.code;
  return e;
}

class TLSSocket extends net.Socket {
  constructor(socket, opts) {
    const tlsOptions = { ...opts };
    let enableTrace = tlsOptions.enableTrace;
    if (enableTrace !== undefined && typeof enableTrace !== "boolean") throw ERR_INVALID_ARG_TYPE("options.enableTrace", "boolean", enableTrace);
    if (tlsOptions.ALPNProtocols) convertALPNProtocols(tlsOptions.ALPNProtocols, {});
    const wrapped = socket instanceof net.Socket ? socket : null;
    super({
      handle: wrapped && !wrapped.connecting && wrapped._handle ? wrapped._handle : undefined,
      allowHalfOpen: socket ? socket.allowHalfOpen : tlsOptions.allowHalfOpen,
      pauseOnCreate: tlsOptions.pauseOnConnect,
      manualStart: true,
      highWaterMark: tlsOptions.highWaterMark,
      signal: tlsOptions.signal,
    });
    this._tlsOptions = tlsOptions;
    this._secureEstablished = false;
    this._securePending = false;
    this._newSessionPending = false;
    this._controlReleased = false;
    this.secureConnecting = true;
    this._SNICallback = null;
    this.servername = null;
    this.alpnProtocol = null;
    this.authorized = false;
    this.authorizationError = null;
    this[kRes] = null;
    this.encrypted = true;
    this.ssl = null;
    this.on("error", this._tlsError);
    if (wrapped) {
      this._parent = wrapped;
      if (wrapped._server) {
        this._server = wrapped._server;
        this.server = wrapped._server;
        wrapped._server = null;
      }
      if (wrapped.connecting || !wrapped._handle) {
        wrapped.once("connect", () => {
          this._handle = wrapped._handle;
          this._handle[kOwner] = this;
          this.connecting = false;
          this._wrapStart();
          this.emit("connect");
        });
        this.connecting = true;
      } else {
        this._handle[kOwner] = this;
        if (tlsOptions.isServer) wrapped._handle = null;
        else wrapped.once("close", () => this.destroy());
        // Bytes the plain socket already buffered belong to the TLS stream.
        const rs = wrapped._readableState;
        if (rs && rs.length && !tlsOptions.isServer) {
          const parts = rs.buffer.map((c) => (typeof c === "string" ? Buffer.from(c, "latin1") : c));
          this._tlsPrefix = Buffer.concat(parts);
          rs.buffer = [];
          rs.length = 0;
        }
        if (tlsOptions.isServer) this._wrapStart();
        else process.nextTick(() => this._wrapStart());
      }
    }
    this.on("connect", () => {
      if (!this._tlsStarted) this._wrapStart();
    });
    if (!tlsOptions.isServer) this.on("end", onConnectEnd);
  }

  _tlsError(err) {
    this.emit("_tlsError", err);
    if (this._controlReleased) return err;
    return null;
  }

  _releaseControl() {
    if (this._controlReleased) return false;
    this._controlReleased = true;
    this.removeListener("error", this._tlsError);
    return true;
  }

  _wrapStart() {
    if (this._tlsStarted || !this._handle || this.destroyed) return;
    this._tlsStarted = true;
    const o = this._tlsOptions;
    const handle = this._handle;
    const ctx = o.secureContext && o.secureContext.context;
    if (o.isServer) {
      const server = o.server;
      this._requestCert = !!o.requestCert;
      this._rejectUnauthorized = o.rejectUnauthorized !== false;
      if (o.SNICallback) {
        handle.onsni = (servername, done) => {
          o.SNICallback(servername, (err, context) => {
            if (err) return done(String(err.message || err));
            done(null, context ? (context.context || context) : undefined);
          });
        };
      }
      const timeout = o.handshakeTimeout > 0 ? o.handshakeTimeout : 120000;
      const timer = setTimeout(() => {
        const err = makeErr(Error, "ERR_TLS_HANDSHAKE_TIMEOUT", "TLS handshake timeout");
        if (server) server.emit("tlsClientError", err, this);
        this.destroy(err);
      }, timeout);
      if (timer.unref) timer.unref();
      handle.startTLS({
        isServer: true,
        secureContext: ctx,
        requestCert: !!o.requestCert,
        rejectUnauthorized: o.rejectUnauthorized !== false,
        ALPNProtocols: alpnList(o.ALPNProtocols),
        hasSNICallback: !!o.SNICallback,
        minVersion: o.secureContext && o.secureContext._minVersion,
        maxVersion: o.secureContext && o.secureContext._maxVersion,
      }, (err) => {
        clearTimeout(timer);
        if (this.destroyed) return;
        if (err) {
          const e = tlsErrorFromInfo(err);
          if (server) server.emit("tlsClientError", e, this);
          this._hadError = true;
          this.destroy();
          return;
        }
        this._finishInit();
        this.emit("secure");
      });
    } else {
      const co = this[kConnectOptions] || {};
      const servername = this._servername !== undefined ? this._servername :
        (co.servername || (co.host && !net.isIP(co.host) ? co.host : (co.socket && co.socket._host && !net.isIP(co.socket._host) ? co.socket._host : "")));
      handle.startTLS({
        isServer: false,
        secureContext: ctx,
        prefix: this._tlsPrefix,
        servername: servername || "",
        verifyInHandshake: !!co.rejectUnauthorized,
        checkHost: co.checkServerIdentity === checkServerIdentity && !co.socket ?
          (co.servername || co.host || "localhost") : "",
        ALPNProtocols: alpnList(o.ALPNProtocols),
        minVersion: o.secureContext && o.secureContext._minVersion,
        maxVersion: o.secureContext && o.secureContext._maxVersion,
      }, (err) => {
        if (this.destroyed) return;
        if (err) {
          if (err.syscall === "verify") {
            this.authorized = false;
            if (err.code === "ERR_TLS_CERT_ALTNAME_INVALID") {
              const hostname = co.servername || co.host || "localhost";
              const verr = checkServerIdentity(hostname, this.getPeerCertificate(true)) ||
                makeErr(Error, "ERR_TLS_CERT_ALTNAME_INVALID", "Hostname/IP does not match certificate's altnames");
              this.authorizationError = verr.code;
              this.destroy(verr);
            } else {
              const verr = verifyErrorFromInfo(err);
              this.authorizationError = verr.code;
              this.destroy(verr);
            }
            return;
          }
          this.destroy(tlsErrorFromInfo(err));
          return;
        }
        this._finishInit();
        this.emit("secure");
      });
      if (!this._readableState.flowing && this._readableState.readableListening === false) {
        // nothing: reads start on demand
      }
    }
  }

  _finishInit() {
    if (!this._handle) return;
    this.alpnProtocol = this._handle.getALPN();
    const sn = this._handle.getServername();
    this.servername = sn;
    this._secureEstablished = true;
    this.secureConnecting = false;
    if (this._tlsOptions.isServer) return;
  }

  _start() {
    if (this.connecting) {
      this.once("connect", () => this._start());
      return;
    }
    this._wrapStart();
  }

  setServername(name) {
    validateString(name, "name");
    if (this._tlsOptions.isServer) throw makeErr(Error, "ERR_TLS_SNI_FROM_SERVER", "Cannot issue SNI from a TLS server-side socket");
    this._servername = name;
  }
  setSession(session) {
    this[kPendingSession] = session;
  }
  getPeerCertificate(detailed) {
    if (this._handle) {
      const c = this._handle.getPeerCertificate(!!detailed);
      return c || {};
    }
    return null;
  }
  getCertificate() {
    if (this._handle) return this._handle.getCertificate() || {};
    return null;
  }
  getPeerX509Certificate() {
    const raw = this._handle && this._handle.getPeerX509();
    if (!raw) return undefined;
    try {
      const { X509Certificate } = globalThis.__noderatiCryptoX509 || {};
      return X509Certificate ? new X509Certificate(raw) : undefined;
    } catch { return undefined; }
  }
  getX509Certificate() { return undefined; }
  getProtocol() { return this._handle ? this._handle.getProtocol() : null; }
  getCipher() { return this._handle ? this._handle.getCipher() : undefined; }
  getSession() { return undefined; }
  getTLSTicket() { return undefined; }
  isSessionReused() { return this._handle ? this._handle.isSessionReused() : false; }
  getFinished() { return undefined; }
  getPeerFinished() { return undefined; }
  getEphemeralKeyInfo() { return this._tlsOptions.isServer ? null : {}; }
  getSharedSigalgs() { return []; }
  exportKeyingMaterial(length, label, context) {
    if (typeof length !== "number") throw ERR_INVALID_ARG_TYPE("length", "number", length);
    validateString(label, "label");
    return this._handle.exportKeyingMaterial(length, label, context);
  }
  setMaxSendFragment(size) { return true; }
  renegotiate(options, callback) {
    const err = makeErr(Error, "ERR_TLS_RENEGOTIATION_DISABLED", "TLS session renegotiation disabled for this socket");
    if (typeof callback === "function") process.nextTick(callback, err);
    return false;
  }
  disableRenegotiation() { this._disableRenegotiation = true; }
  enableTrace() {}
  get _connecting() { return this.connecting; }
}

function onConnectEnd() {
  if (!this._secureEstablished) {
    const options = this[kConnectOptions] || {};
    const error = makeErr(Error, "ECONNRESET", "Client network socket disconnected before secure TLS connection was established");
    error.path = options.path;
    error.host = options.host;
    error.port = options.port;
    error.localAddress = options.localAddress;
    this.destroy(error);
  }
}

function onConnectSecure() {
  const options = this[kConnectOptions];
  let verifyError = verifyErrorFromInfo(this._handle && this._handle.verifyError());
  if (!verifyError && !this.isSessionReused()) {
    const hostname = options.servername || options.host || (options.socket && options.socket._host) || "localhost";
    const cert = this.getPeerCertificate(true);
    verifyError = options.checkServerIdentity(hostname, cert);
  }
  if (verifyError) {
    this.authorized = false;
    this.authorizationError = verifyError.code || verifyError.message;
    if (options.rejectUnauthorized) {
      this.destroy(verifyError);
      return;
    }
    this.emit("secureConnect");
  } else {
    this.authorized = true;
    this.emit("secureConnect");
  }
  this.removeListener("end", onConnectEnd);
}

function normalizeConnectArgs(listArgs) {
  const args = net._normalizeArgs(listArgs);
  const options = args[0];
  const cb = args[1];
  if (listArgs[1] !== null && typeof listArgs[1] === "object") Object.assign(options, listArgs[1]);
  else if (listArgs[2] !== null && typeof listArgs[2] === "object") Object.assign(options, listArgs[2]);
  return cb ? [options, cb] : [options];
}

function connect(...args) {
  args = normalizeConnectArgs(args);
  let options = args[0];
  const cb = args[1];
  const allowUnauthorized = typeof process !== "undefined" && process.env && process.env.NODE_TLS_REJECT_UNAUTHORIZED === "0";
  options = {
    rejectUnauthorized: !allowUnauthorized,
    ciphers: tlsDefaults.DEFAULT_CIPHERS,
    checkServerIdentity,
    minDHSize: 1024,
    ...options,
  };
  if (!options.keepAlive) options.singleUse = true;
  validateFunction(options.checkServerIdentity, "options.checkServerIdentity");
  const context = options.secureContext || createSecureContext(options);
  const tlssock = new TLSSocket(options.socket, {
    allowHalfOpen: options.allowHalfOpen,
    pipe: !!options.path,
    secureContext: context,
    isServer: false,
    requestCert: true,
    rejectUnauthorized: options.rejectUnauthorized !== false,
    session: options.session,
    ALPNProtocols: options.ALPNProtocols,
    requestOCSP: options.requestOCSP,
    enableTrace: options.enableTrace,
    highWaterMark: options.highWaterMark,
    signal: options.signal,
  });
  tlssock[kConnectOptions] = options;
  if (cb) tlssock.once("secureConnect", cb);
  if (!options.socket) {
    if (options.timeout) tlssock.setTimeout(options.timeout);
    tlssock.connect(options);
  }
  tlssock._releaseControl();
  if (options.session) tlssock.setSession(options.session);
  if (options.servername) tlssock.setServername(options.servername);
  tlssock.on("secure", onConnectSecure);
  return tlssock;
}

// ---- Server ---------------------------------------------------------
const kSNICallback = Symbol("snicallback");
function SNICallback(servername, callback) {
  const contexts = this.server._contexts;
  for (let i = contexts.length - 1; i >= 0; --i) {
    const elem = contexts[i];
    if (elem[0].test(servername)) {
      callback(null, elem[1]);
      return;
    }
  }
  callback(null, undefined);
}

function onServerSocketSecure() {
  if (this._requestCert) {
    const verifyError = verifyErrorFromInfo(this._handle && this._handle.verifyError());
    if (verifyError) {
      this.authorizationError = verifyError.code;
      if (this._rejectUnauthorized) this.destroy();
    } else {
      this.authorized = true;
    }
  }
  if (!this.destroyed && this._releaseControl()) {
    this._tlsOptions.server.emit("secureConnection", this);
  }
}

function tlsConnectionListener(rawSocket) {
  const server = this;
  const opts = {
    secureContext: server._sharedCreds,
    isServer: true,
    server,
    requestCert: server.requestCert,
    rejectUnauthorized: server.rejectUnauthorized,
    handshakeTimeout: server._handshakeTimeout,
    ALPNProtocols: server.ALPNProtocols,
    SNICallback: server[kSNICallback] || (server._contexts.length ? SNICallback : undefined),
    pauseOnConnect: server.pauseOnConnect,
  };
  if (opts.SNICallback) {
    const fn = opts.SNICallback;
    opts.SNICallback = (name, cb) => fn.call({ server }, name, cb);
  }
  const socket = new TLSSocket(rawSocket, opts);
  socket.on("secure", onServerSocketSecure);
}

class Server extends net.Server {
  constructor(options, listener) {
    if (typeof options === "function") {
      listener = options;
      options = {};
    } else if (options == null || typeof options === "object") {
      options = options ?? {};
    } else {
      throw ERR_INVALID_ARG_TYPE("options", "Object", options);
    }
    super(options, tlsConnectionListener);
    this._manualStart = true;
    this._contexts = [];
    this.requestCert = options.requestCert === true;
    this.rejectUnauthorized = options.rejectUnauthorized !== false;
    this.ALPNProtocols = options.ALPNProtocols;
    if (options.ALPNCallback !== undefined && options.ALPNProtocols) {
      throw makeErr(TypeError, "ERR_TLS_ALPN_CALLBACK_WITH_PROTOCOLS", "The ALPNCallback and ALPNProtocols TLS options are mutually exclusive");
    }
    if (options.sessionTimeout) this.sessionTimeout = options.sessionTimeout;
    if (options.ticketKeys) this._ticketKeys = options.ticketKeys;
    this._handshakeTimeout = options.handshakeTimeout || 120000;
    if (options.SNICallback !== undefined) {
      validateFunction(options.SNICallback, "options.SNICallback");
      this[kSNICallback] = options.SNICallback;
    }
    this.setSecureContext(options);
    if (listener) this.on("secureConnection", listener);
  }
  setSecureContext(options) {
    if (options == null || typeof options !== "object") throw ERR_INVALID_ARG_TYPE("options", "Object", options);
    this.key = options.key;
    this.cert = options.cert;
    this.ca = options.ca;
    this._sharedCreds = options.secureContext || createSecureContext({
      key: options.key, cert: options.cert, ca: options.ca, ciphers: options.ciphers,
      minVersion: options.minVersion, maxVersion: options.maxVersion, pfx: options.pfx, passphrase: options.passphrase,
    });
  }
  addContext(servername, context) {
    if (!servername) throw makeErr(TypeError, "ERR_TLS_REQUIRED_SERVER_NAME", '"servername" is required parameter for Server.addContext');
    const re = new RegExp("^" + servername.replace(/([.^$+?\-\\[\]{}])/g, "\\$1").replaceAll("*", "[^.]*") + "$");
    const secureContext = context instanceof SecureContext ? context : createSecureContext(context);
    this._contexts.push([re, secureContext.context ? secureContext : { context: secureContext }]);
  }
  getTicketKeys() {
    if (!this._ticketKeys) this._ticketKeys = globalThis.crypto && globalThis.crypto.getRandomValues ? Buffer.from(globalThis.crypto.getRandomValues(new Uint8Array(48))) : Buffer.alloc(48);
    return this._ticketKeys;
  }
  setTicketKeys(keys) {
    if (!(keys instanceof Uint8Array)) throw ERR_INVALID_ARG_TYPE("buffer", ["Buffer", "TypedArray", "DataView"], keys);
    if (keys.length !== 48) throw makeErr(RangeError, "ERR_OUT_OF_RANGE", 'The value of "keys.length" is out of range. It must be 48. Received ' + keys.length);
    this._ticketKeys = Buffer.from(keys);
  }
  setTimeout(msecs, callback) {
    this.timeout = msecs;
    if (callback) this.on("timeout", callback);
    return this;
  }
}

function createServer(options, listener) {
  return new Server(options, listener);
}


// paserati: a subclass inherits its parent's name when the parent's name
// was set with defineProperty (EventEmitter's is) - pin our own names.
Object.defineProperty(TLSSocket, "name", { value: "TLSSocket", configurable: true });
Object.defineProperty(Server, "name", { value: "Server", configurable: true });
Object.defineProperty(SecureContext, "name", { value: "SecureContext", configurable: true });

const tlsModule = {
  CLIENT_RENEG_LIMIT, CLIENT_RENEG_WINDOW,
  SecureContext, Server, TLSSocket, checkServerIdentity, connect, convertALPNProtocols,
  createSecureContext, createServer, getCACertificates, getCiphers, setDefaultCACertificates,
};
for (const k of Object.keys(tlsDefaults)) {
  Object.defineProperty(tlsModule, k, {
    enumerable: true, configurable: true,
    get() { return tlsDefaults[k]; },
    set(v) { tlsDefaults[k] = v; },
  });
}
Object.defineProperty(tlsModule, "rootCertificates", { enumerable: true, configurable: true, get: getRootCertificates });
globalThis.__noderatiTLSInternals = { TLSSocket, createSecureContext, checkServerIdentity };

export {
  CLIENT_RENEG_LIMIT, CLIENT_RENEG_WINDOW, SecureContext, Server, TLSSocket, checkServerIdentity, connect,
  convertALPNProtocols, createSecureContext, createServer, getCACertificates, getCiphers, setDefaultCACertificates,
};
export const DEFAULT_CIPHERS = tlsDefaults.DEFAULT_CIPHERS;
export const DEFAULT_ECDH_CURVE = tlsDefaults.DEFAULT_ECDH_CURVE;
export const DEFAULT_MIN_VERSION = tlsDefaults.DEFAULT_MIN_VERSION;
export const DEFAULT_MAX_VERSION = tlsDefaults.DEFAULT_MAX_VERSION;
export const rootCertificates = getRootCertificates();
export default tlsModule;
`
