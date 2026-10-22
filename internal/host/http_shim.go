package host

// http_shim.go is node:http/node:https's JS layer. The wire work stays in
// Go (http.go: net/http.Client; http_server.go: net/http.Server); this
// layer gives it Node's object model on top: OutgoingMessage/
// ClientRequest with real header bookkeeping (headers are only handed to
// Go once the request is actually sent - first write()/end()/
// flushHeaders(), exactly when Node would put them on the wire), Agent/
// globalAgent, Server as a constructible class over the native server,
// METHODS/STATUS_CODES and the header validators.
//
// maxHeaderSize is http-only (https has no such export in real Node);
// real undici reads http.maxHeaderSize at Client construction and throws
// without it.

const httpShim = `import EventEmitter from "events";
import net from "net";
import { Stream, Readable } from "stream";
import { Buffer } from "buffer";

const NativeAgent = globalThis.__noderatiHTTPAgentCtor;
const nativeRequest = globalThis.__noderatiHTTPRequest;
const nativeCreateServer = globalThis.__noderatiHTTPCreateServer;
const I = globalThis.__noderatiNetInternals;
const { makeErr, ERR_INVALID_ARG_TYPE, validateString } = I;

const maxHeaderSize = 16384;

const METHODS = ["ACL", "BIND", "CHECKOUT", "CONNECT", "COPY", "DELETE", "GET", "HEAD", "LINK", "LOCK", "M-SEARCH", "MERGE", "MKACTIVITY", "MKCALENDAR", "MKCOL", "MOVE", "NOTIFY", "OPTIONS", "PATCH", "POST", "PROPFIND", "PROPPATCH", "PURGE", "PUT", "QUERY", "REBIND", "REPORT", "SEARCH", "SOURCE", "SUBSCRIBE", "TRACE", "UNBIND", "UNLINK", "UNLOCK", "UNSUBSCRIBE"];
const STATUS_CODES = {
  100: "Continue", 101: "Switching Protocols", 102: "Processing", 103: "Early Hints",
  200: "OK", 201: "Created", 202: "Accepted", 203: "Non-Authoritative Information", 204: "No Content", 205: "Reset Content", 206: "Partial Content", 207: "Multi-Status", 208: "Already Reported", 226: "IM Used",
  300: "Multiple Choices", 301: "Moved Permanently", 302: "Found", 303: "See Other", 304: "Not Modified", 305: "Use Proxy", 307: "Temporary Redirect", 308: "Permanent Redirect",
  400: "Bad Request", 401: "Unauthorized", 402: "Payment Required", 403: "Forbidden", 404: "Not Found", 405: "Method Not Allowed", 406: "Not Acceptable", 407: "Proxy Authentication Required", 408: "Request Timeout", 409: "Conflict", 410: "Gone", 411: "Length Required", 412: "Precondition Failed", 413: "Payload Too Large", 414: "URI Too Long", 415: "Unsupported Media Type", 416: "Range Not Satisfiable", 417: "Expectation Failed", 418: "I'm a Teapot", 421: "Misdirected Request", 422: "Unprocessable Entity", 423: "Locked", 424: "Failed Dependency", 425: "Too Early", 426: "Upgrade Required", 428: "Precondition Required", 429: "Too Many Requests", 431: "Request Header Fields Too Large", 451: "Unavailable For Legal Reasons",
  500: "Internal Server Error", 501: "Not Implemented", 502: "Bad Gateway", 503: "Service Unavailable", 504: "Gateway Timeout", 505: "HTTP Version Not Supported", 506: "Variant Also Negotiates", 507: "Insufficient Storage", 508: "Loop Detected", 509: "Bandwidth Limit Exceeded", 510: "Not Extended", 511: "Network Authentication Required",
};

const tokenRegExp = /^[\^_\x60a-zA-Z\-0-9!#$%&'*+.|~]+$/;
const headerCharRegex = /[^\t\x20-\x7e\x80-\xff]/;
function checkIsHttpToken(val) { return tokenRegExp.test(val); }
function validateHeaderName(name, label = "Header name") {
  if (typeof name !== "string" || !name || !checkIsHttpToken(name)) {
    throw makeErr(TypeError, "ERR_INVALID_HTTP_TOKEN", label + ' must be a valid HTTP token ["' + name + '"]');
  }
}
function validateHeaderValue(name, value) {
  if (value === undefined) {
    throw makeErr(TypeError, "ERR_HTTP_INVALID_HEADER_VALUE", 'Invalid value "' + value + '" for header "' + name + '"');
  }
  const vals = Array.isArray(value) ? value : [value];
  for (const v of vals) {
    if (headerCharRegex.test(String(v))) {
      throw makeErr(TypeError, "ERR_INVALID_CHAR", 'Invalid character in header content ["' + name + '"]');
    }
  }
}
function headersSentError(op) {
  return makeErr(Error, "ERR_HTTP_HEADERS_SENT", "Cannot " + op + " headers after they are sent to the client");
}
function setMaxIdleHTTPParsers(max) {
  if (typeof max !== "number") throw ERR_INVALID_ARG_TYPE("max", "number", max);
  if (!Number.isInteger(max) || max < 1) {
    throw makeErr(RangeError, "ERR_OUT_OF_RANGE", 'The value of "max" is out of range. It must be >= 1 && <= 9007199254740991. Received ' + max);
  }
}

// ---- OutgoingMessage -----------------------------------------------
const kOutHeaders = Symbol("kOutHeaders");
const kCorked = Symbol("corked");
class OutgoingMessage extends Stream {
  constructor(options) {
    super();
    this.outputData = [];
    this.outputSize = 0;
    this.writable = true;
    this.destroyed = false;
    this._last = false;
    this.chunkedEncoding = false;
    this.shouldKeepAlive = true;
    this.maxRequestsOnConnectionReached = false;
    this._defaultKeepAlive = true;
    this.useChunkedEncodingByDefault = true;
    this.sendDate = false;
    this._removedConnection = false;
    this._removedContLen = false;
    this._removedTE = false;
    this.strictContentLength = false;
    this._contentLength = null;
    this._hasBody = true;
    this._trailer = "";
    this.finished = false;
    this._headerSent = false;
    this._closed = false;
    this.socket = null;
    this._header = null;
    this[kOutHeaders] = null;
    this[kCorked] = 0;
    this._keepAliveTimeout = 0;
  }
  get writableFinished() { return this.finished && this.outputSize === 0 && (!this.socket || this.socket.writableLength === 0 || this.socket.writableLength === undefined); }
  get writableObjectMode() { return false; }
  get writableLength() { return this.outputSize; }
  get writableHighWaterMark() { return 16384; }
  get writableCorked() { return this[kCorked]; }
  get writableEnded() { return this.finished; }
  get writableNeedDrain() { return false; }
  get closed() { return this._closed; }
  get errored() { return null; }
  get headersSent() { return !!this._header; }
  get connection() { return this.socket; }
  cork() { this[kCorked]++; }
  uncork() { if (this[kCorked]) this[kCorked]--; }
  setTimeout(msecs, callback) {
    if (callback) this.on("timeout", callback);
    if (this.socket) this.socket.setTimeout(msecs);
    else this.once("socket", (s) => s.setTimeout(msecs));
    return this;
  }
  setHeader(name, value) {
    if (this._header) throw headersSentError("set");
    validateHeaderName(name);
    validateHeaderValue(name, value);
    let headers = this[kOutHeaders];
    if (headers === null) this[kOutHeaders] = headers = Object.create(null);
    headers[name.toLowerCase()] = [name, value];
    return this;
  }
  setHeaders(headers) {
    if (this._header) throw headersSentError("set");
    if (!headers || Array.isArray(headers) || typeof headers.keys !== "function" || typeof headers.get !== "function") {
      throw ERR_INVALID_ARG_TYPE("headers", ["Headers", "Map"], headers);
    }
    for (const key of headers.keys()) this.setHeader(key, headers.get(key));
    return this;
  }
  appendHeader(name, value) {
    if (this._header) throw headersSentError("append");
    validateHeaderName(name);
    validateHeaderValue(name, value);
    const field = name.toLowerCase();
    const headers = this[kOutHeaders];
    if (headers === null || !headers[field]) return this.setHeader(name, value);
    const existing = headers[field][1];
    const list = Array.isArray(existing) ? existing : [existing];
    headers[field][1] = list.concat(value);
    return this;
  }
  getHeader(name) {
    validateString(name, "name");
    const headers = this[kOutHeaders];
    if (headers === null) return;
    const entry = headers[name.toLowerCase()];
    return entry && entry[1];
  }
  getHeaderNames() { return this[kOutHeaders] !== null ? Object.keys(this[kOutHeaders]) : []; }
  getRawHeaderNames() {
    const headersMap = this[kOutHeaders];
    if (headersMap === null) return [];
    return Object.values(headersMap).map((e) => e[0]);
  }
  getHeaders() {
    const headers = this[kOutHeaders];
    const ret = Object.create(null);
    if (headers) {
      for (const key of Object.keys(headers)) ret[key] = headers[key][1];
    }
    return ret;
  }
  hasHeader(name) {
    validateString(name, "name");
    return this[kOutHeaders] !== null && !!this[kOutHeaders][name.toLowerCase()];
  }
  removeHeader(name) {
    validateString(name, "name");
    if (this._header) throw headersSentError("remove");
    const key = name.toLowerCase();
    if (this[kOutHeaders] !== null) delete this[kOutHeaders][key];
  }
  addTrailers(headers) { this._trailer = headers; }
  flushHeaders() { this._implicitHeader(); }
  _implicitHeader() {
    throw makeErr(Error, "ERR_METHOD_NOT_IMPLEMENTED", "The _implicitHeader() method is not implemented");
  }
  pipe() {
    this.emit("error", makeErr(Error, "ERR_STREAM_CANNOT_PIPE", "Cannot pipe, not readable"));
  }
  write(chunk, encoding, cb) { return this._writeImpl(chunk, encoding, cb); }
  end(chunk, encoding, cb) { return this._endImpl(chunk, encoding, cb); }
  destroy(err) { return this._destroyImpl(err); }
  _send() { return this._sendImpl(); }
}

// ---- Agent ----------------------------------------------------------
class Agent extends EventEmitter {
  constructor(options) {
    super();
    this.defaultPort = 80;
    this.protocol = "http:";
    this.options = { __proto__: null, ...options };
    if (this.options.noDelay === undefined) this.options.noDelay = true;
    this.options.path = null;
    this.requests = Object.create(null);
    this.sockets = Object.create(null);
    this.freeSockets = Object.create(null);
    this.keepAliveMsecs = (this.options.keepAliveMsecs) || 1000;
    this.keepAlive = this.options.keepAlive || false;
    this.maxSockets = this.options.maxSockets || Agent.defaultMaxSockets;
    this.maxFreeSockets = this.options.maxFreeSockets || 256;
    this.scheduling = this.options.scheduling || "lifo";
    this.maxTotalSockets = this.options.maxTotalSockets ?? Infinity;
    this.totalSocketCount = 0;
    Object.defineProperty(this, "_native", { value: new NativeAgent({ keepAlive: this.keepAlive }), enumerable: false });
  }
  getName(options = {}) {
    let name = options.host || "localhost";
    name += ":";
    if (options.port) name += options.port;
    name += ":";
    if (options.localAddress) name += options.localAddress;
    if (options.family === 4 || options.family === 6) name += ":" + options.family;
    if (options.socketPath) name += ":" + options.socketPath;
    return name;
  }
  createConnection(...args) { return net.createConnection(...args); }
  keepSocketAlive(socket) {
    socket.setKeepAlive(true, this.keepAliveMsecs);
    socket.unref();
    return true;
  }
  reuseSocket(socket, req) {
    req.reusedSocket = true;
    socket.ref();
  }
  addRequest(req, options) {}
  removeSocket(s, options) {}
  destroy() { this._native.destroy(); }
}
Agent.defaultMaxSockets = Infinity;
const globalAgent = new Agent({ keepAlive: true, scheduling: "lifo", timeout: 5000 });

// ---- ClientRequest --------------------------------------------------
// The socket handed out by 'socket' stands in for the pooled Go
// connection: connect/timeout/destroy behave like node's, the bytes go
// through net/http.
class ClientSocket extends EventEmitter {
  constructor(req) {
    super();
    this._req = req;
    this.connecting = true;
    this.destroyed = false;
    this.readable = true;
    this.writable = true;
    this.timeout = 0;
    this._timer = null;
  }
  setTimeout(msecs, cb) {
    if (this._timer) clearTimeout(this._timer);
    this._timer = null;
    this.timeout = msecs;
    if (cb) this.once("timeout", cb);
    if (msecs > 0) {
      const arm = () => {
        this._timer = setTimeout(() => { this._timer = null; this.emit("timeout"); }, msecs);
        if (this._timer.unref) this._timer.unref();
      };
      arm();
      this._rearm = arm;
    }
    return this;
  }
  _activity() {
    if (this._timer && this._rearm) { clearTimeout(this._timer); this._rearm(); }
  }
  setKeepAlive() { return this; }
  setNoDelay() { return this; }
  ref() { return this; }
  unref() { return this; }
  address() { return {}; }
  destroy(err) {
    if (this.destroyed) return this;
    this.destroyed = true;
    if (this._timer) clearTimeout(this._timer);
    this._req.destroy(err);
    process.nextTick(() => this.emit("close", !!err));
    return this;
  }
  end() { return this; }
  write() { return true; }
  get writableLength() { return 0; }
}

function urlToHttpOptions(url) {
  const options = {
    protocol: url.protocol,
    hostname: url.hostname.startsWith("[") ? url.hostname.slice(1, -1) : url.hostname,
    hash: url.hash,
    search: url.search,
    pathname: url.pathname,
    path: (url.pathname || "") + (url.search || ""),
    href: url.href,
  };
  if (url.port !== "") options.port = Number(url.port);
  if (url.username || url.password) options.auth = decodeURIComponent(url.username) + ":" + decodeURIComponent(url.password);
  return options;
}

const INVALID_PATH_REGEX = /[^!-ÿ]/;
const kNative = Symbol("native");

function hangUp() {
  const e = makeErr(Error, "ECONNRESET", "socket hang up");
  return e;
}

class ClientRequest extends OutgoingMessage {
  constructor(input, options, cb) {
    super();
    if (typeof input === "string") {
      input = urlToHttpOptions(new URL(input));
    } else if (input instanceof URL) {
      input = urlToHttpOptions(input);
    } else {
      cb = options;
      options = input;
      input = null;
    }
    if (typeof options === "function") {
      cb = options;
      options = input || {};
    } else {
      options = Object.assign(input || {}, options);
    }
    let agent = options.agent;
    const defaultAgent = options._defaultAgent || globalAgent;
    if (agent === false) {
      agent = new defaultAgent.constructor();
    } else if (agent === null || agent === undefined) {
      if (typeof options.createConnection !== "function") agent = defaultAgent;
    } else if (typeof agent.addRequest !== "function") {
      throw ERR_INVALID_ARG_TYPE("options.agent", ["Agent-like Object", "undefined", "false"], agent);
    }
    this.agent = agent;
    const protocol = options.protocol || defaultAgent.protocol;
    let expectedProtocol = defaultAgent.protocol;
    if (this.agent && this.agent.protocol) expectedProtocol = this.agent.protocol;
    if (options.path) {
      const path = String(options.path);
      if (INVALID_PATH_REGEX.test(path)) throw makeErr(TypeError, "ERR_UNESCAPED_CHARACTERS", "Request path contains unescaped characters");
    }
    if (protocol !== expectedProtocol) {
      throw makeErr(TypeError, "ERR_INVALID_PROTOCOL", 'Protocol "' + protocol + '" not supported. Expected "' + expectedProtocol + '"');
    }
    const defaultPort = options.defaultPort || (this.agent && this.agent.defaultPort);
    const port = options.port = options.port || defaultPort || 80;
    const host = options.host = (typeof options.hostname === "string" && options.hostname) || (typeof options.host === "string" && options.host) || "localhost";
    const setHost = options.setHost === undefined || Boolean(options.setHost);
    this.socketPath = options.socketPath;
    if (options.timeout !== undefined) this.timeout = options.timeout;
    let method = options.method;
    const methodIsString = typeof method === "string";
    if (method !== null && method !== undefined && !methodIsString) throw ERR_INVALID_ARG_TYPE("options.method", "string", method);
    if (methodIsString && method) {
      if (!checkIsHttpToken(method)) throw makeErr(TypeError, "ERR_INVALID_HTTP_TOKEN", 'Method must be a valid HTTP token ["' + method + '"]');
      method = this.method = method.toUpperCase();
    } else {
      method = this.method = "GET";
    }
    this.maxHeaderSize = options.maxHeaderSize;
    this.insecureHTTPParser = options.insecureHTTPParser;
    this.joinDuplicateHeaders = options.joinDuplicateHeaders;
    this.path = options.path || "/";
    if (cb) this.once("response", cb);
    this.useChunkedEncodingByDefault = !(method === "GET" || method === "HEAD" || method === "DELETE" || method === "OPTIONS" || method === "TRACE" || method === "CONNECT");
    this._ended = false;
    this.res = null;
    this.aborted = false;
    this.timeoutCb = null;
    this.upgradeOrConnect = false;
    this.parser = null;
    this.maxHeadersCount = null;
    this.reusedSocket = false;
    this.host = host;
    this.protocol = protocol;
    if (this.agent) {
      if (!this.agent.keepAlive && !Number.isFinite(this.agent.maxSockets)) {
        this._last = true;
        this.shouldKeepAlive = false;
      } else {
        this._last = false;
        this.shouldKeepAlive = true;
      }
    }
    const headersArray = Array.isArray(options.headers);
    if (!headersArray) {
      if (options.headers) {
        for (const key of Object.keys(options.headers)) this.setHeader(key, options.headers[key]);
      }
      if (host && !this.getHeader("host") && setHost) {
        let hostHeader = host;
        const posColon = hostHeader.indexOf(":");
        if (posColon !== -1 && hostHeader.includes(":", posColon + 1) && hostHeader.charCodeAt(0) !== 91) hostHeader = "[" + hostHeader + "]";
        if (port && +port !== defaultPort) hostHeader += ":" + port;
        this.setHeader("Host", hostHeader);
      }
      if (options.auth && !this.getHeader("Authorization")) {
        this.setHeader("Authorization", "Basic " + Buffer.from(options.auth).toString("base64"));
      }
    } else {
      for (let i = 0; i + 1 < options.headers.length; i += 2) this.appendHeader(options.headers[i], options.headers[i + 1]);
    }
    this._options = options;
    this[kNative] = null;
    const sock = new ClientSocket(this);
    sock.on("timeout", () => this.emit("timeout"));
    if (options.signal) {
      const signal = options.signal;
      const onAbort = () => this.destroy(I.abortError(signal));
      if (signal.aborted) process.nextTick(onAbort);
      else signal.addEventListener("abort", onAbort, { once: true });
    }
    process.nextTick(() => {
      if (this.destroyed) return;
      this.socket = sock;
      this.emit("socket", sock);
      if (this.timeout !== undefined) sock.setTimeout(this.timeout);
    });
    this._sock = sock;
  }

  _implicitHeader() { this._send(); }

  _sendImpl() {
    if (this[kNative] || this.destroyed) return this[kNative];
    const o = this._options;
    const headers = {};
    for (const [name, value] of Object.values(this[kOutHeaders] || {})) headers[name] = Array.isArray(value) ? value.map(String) : String(value);
    this._header = this.method + " " + this.path + " HTTP/1.1\r\n";
    this._headerSent = true;
    const scheme = this.protocol === "https:" ? "https" : "http";
    const native = nativeRequest(scheme, {
      hostname: this.host, port: o.port, path: this.path, method: this.method, headers,
      agent: this.agent && this.agent._native, socketPath: o.socketPath,
      ca: o.ca, rejectUnauthorized: o.rejectUnauthorized, servername: o.servername,
    });
    this[kNative] = native;
    const sock = this._sock;
    if (native.socket && typeof native.socket.on === "function") {
      native.socket.on("connect", () => {
        sock.connecting = false;
        sock.emit("connect");
      });
    }
    native.on("response", (nres) => {
      if (this.destroyed && this.aborted) return;
      sock._activity();
      const res = new ClientIncomingMessage(nres, this);
      this.res = res;
      res.on("end", () => {
        if (!this._closed) {
          this._closed = true;
          process.nextTick(() => this.emit("close"));
        }
      });
      if (!this.emit("response", res)) {
        if (typeof res.resume === "function") res.resume();
      }
    });
    native.on("error", (err) => {
      if (this.destroyed) return;
      this.destroyed = true;
      this.emit("error", err);
      this._closed = true;
      process.nextTick(() => this.emit("close"));
    });
    return native;
  }

  _writeImpl(chunk, encoding, cb) {
    if (typeof encoding === "function") { cb = encoding; encoding = undefined; }
    if (this.finished) {
      const err = makeErr(Error, "ERR_STREAM_WRITE_AFTER_END", "write after end");
      process.nextTick(() => { if (cb) cb(err); this.emit("error", err); });
      return true;
    }
    if (chunk === null) throw makeErr(TypeError, "ERR_STREAM_NULL_VALUES", "May not write null values to stream");
    if (typeof chunk !== "string" && !(chunk instanceof Uint8Array)) {
      throw ERR_INVALID_ARG_TYPE("chunk", ["string", "Buffer", "Uint8Array"], chunk);
    }
    const native = this._send();
    if (!native) return false;
    const buf = typeof chunk === "string" ? Buffer.from(chunk, encoding || "utf8") : chunk;
    native.write(buf);
    if (cb) process.nextTick(cb);
    return true;
  }

  _endImpl(chunk, encoding, cb) {
    if (typeof chunk === "function") { cb = chunk; chunk = null; encoding = null; }
    else if (typeof encoding === "function") { cb = encoding; encoding = null; }
    if (this.finished) {
      if (cb) process.nextTick(cb);
      return this;
    }
    const native = this._send();
    if (chunk !== null && chunk !== undefined && native) {
      const buf = typeof chunk === "string" ? Buffer.from(chunk, encoding || "utf8") : chunk;
      native.write(buf);
    }
    this.finished = true;
    if (native) native.end();
    process.nextTick(() => {
      if (cb) cb();
      this.emit("prefinish");
      this.emit("finish");
    });
    return this;
  }

  destroy(err) { return this._destroyImpl(err); }

  abort() {
    if (this.aborted) return;
    this.aborted = true;
    process.nextTick(() => this.emit("abort"));
    this.destroy();
  }

  _destroyImpl(err) {
    if (this.destroyed) return this;
    this.destroyed = true;
    if (this.res && typeof this.res.destroy === "function") this.res.destroy(err);
    if (this[kNative]) this[kNative].destroy();
    if (this._sock._timer) clearTimeout(this._sock._timer);
    if (!err && !this.res && !this.aborted) err = hangUp();
    process.nextTick(() => {
      if (err && !(this.aborted && err.code === "ECONNRESET")) this.emit("error", err);
      if (!this._closed) {
        this._closed = true;
        this.emit("close");
      }
    });
    return this;
  }

  onSocket(socket) {}
  clearTimeout(cb) { this.setTimeout(0, cb); }
  setTimeout(msecs, callback) {
    if (callback) this.once("timeout", callback);
    this._sock.setTimeout(msecs);
    return this;
  }
  setNoDelay(noDelay) { return this; }
  setSocketKeepAlive(enable, initialDelay) { return this; }
}

function request(url, options, cb) {
  return new ClientRequest(url, options, cb);
}
function get(url, options, cb) {
  const req = request(url, options, cb);
  req.end();
  return req;
}

// Client-side responses: a real Readable fed from the native body pump.
class ClientIncomingMessage extends Readable {
  constructor(native, req) {
    super();
    this._native = native;
    this.req = req;
    this.socket = req._sock;
    this.httpVersion = native.httpVersion;
    const [maj, min] = String(native.httpVersion || "1.1").split(".");
    this.httpVersionMajor = Number(maj);
    this.httpVersionMinor = Number(min || 0);
    this.complete = false;
    this.headers = native.headers;
    this.rawHeaders = native.rawHeaders || [];
    this.trailers = {};
    this.rawTrailers = [];
    this.aborted = false;
    this.upgrade = false;
    this.url = "";
    this.method = null;
    this.statusCode = native.statusCode;
    this.statusMessage = native.statusMessage;
    native.on("data", (chunk) => {
      req._sock._activity();
      if (typeof chunk === "string") chunk = Buffer.from(chunk);
      if (!this.push(chunk) && typeof native.pause === "function") native.pause();
    });
    native.on("end", () => {
      this.complete = true;
      this.push(null);
    });
    native.on("error", (err) => this.destroy(typeof err === "string" ? new Error(err) : err));
  }
  get connection() { return this.socket; }
  get headersDistinct() {
    const out = Object.create(null);
    for (const [k, v] of Object.entries(this.headers)) out[k] = Array.isArray(v) ? v : [v];
    return out;
  }
  get trailersDistinct() { return Object.create(null); }
  _read() {
    if (typeof this._native.resume === "function") this._native.resume();
  }
  _destroy(err, cb) {
    if (!this.readableEnded || !this.complete) this.aborted = true;
    if (typeof this._native.destroy === "function") this._native.destroy();
    cb(err);
  }
  setTimeout(msecs, cb) {
    if (this.req) this.req.setTimeout(msecs, cb);
    return this;
  }
}
Object.defineProperty(ClientIncomingMessage, "name", { value: "IncomingMessage", configurable: true });

// ---- Server ---------------------------------------------------------
class IncomingMessage {}
class ServerResponse {}

class Server {
  constructor(options, requestListener) {
    if (typeof options === "function") {
      requestListener = options;
      options = {};
    } else if (options == null) {
      options = {};
    } else if (typeof options !== "object") {
      throw ERR_INVALID_ARG_TYPE("options", "object", options);
    }
    const srv = nativeCreateServer(requestListener, { ...options, secureContext: options.__secureContext });
    Object.setPrototypeOf(srv, new.target.prototype);
    srv.timeout = 0;
    srv.keepAliveTimeout = options.keepAliveTimeout ?? 5000;
    srv.keepAliveTimeoutBuffer = 1000;
    srv.headersTimeout = options.headersTimeout ?? 60000;
    srv.requestTimeout = options.requestTimeout ?? 300000;
    srv.maxRequestsPerSocket = options.maxRequestsPerSocket ?? 0;
    srv.maxHeadersCount = null;
    srv.maxHeaderSize = options.maxHeaderSize;
    srv.httpAllowHalfOpen = false;
    return srv;
  }
  // The native server installs these as own methods; the prototype
  // copies only document the API surface.
  close(cb) { return net.Server.prototype.close.call(this, cb); }
  closeAllConnections() {}
  closeIdleConnections() {}
  setTimeout(msecs, cb) { return this; }
  [Symbol.asyncDispose]() {
    return new Promise((resolve, reject) => this.close((err) => (err ? reject(err) : resolve())));
  }
}
Object.setPrototypeOf(Server.prototype, net.Server.prototype);

function createServer(options, requestListener) {
  return new Server(options, requestListener);
}
function _connectionListener(socket) {
  throw makeErr(Error, "ERR_METHOD_NOT_IMPLEMENTED", "http._connectionListener is not supported in noderati");
}

const WebSocket = globalThis.WebSocket;
const MessageEvent = globalThis.MessageEvent;
const CloseEvent = globalThis.CloseEvent;


// paserati: a subclass inherits its parent's name when the parent's name
// was set with defineProperty (EventEmitter's is) - pin our own names.
Object.defineProperty(OutgoingMessage, "name", { value: "OutgoingMessage", configurable: true });
Object.defineProperty(Agent, "name", { value: "Agent", configurable: true });
Object.defineProperty(ClientSocket, "name", { value: "ClientSocket", configurable: true });
Object.defineProperty(ClientRequest, "name", { value: "ClientRequest", configurable: true });
Object.defineProperty(Server, "name", { value: "Server", configurable: true });

globalThis.__noderatiHTTPInternals = { ClientRequest, Agent, Server, urlToHttpOptions };

export {
  Agent, ClientRequest, IncomingMessage, METHODS, OutgoingMessage, STATUS_CODES, Server, ServerResponse,
  _connectionListener, createServer, get, globalAgent, maxHeaderSize, request, setMaxIdleHTTPParsers,
  validateHeaderName, validateHeaderValue, WebSocket, MessageEvent, CloseEvent,
};
export default {
  Agent, ClientRequest, IncomingMessage, METHODS, OutgoingMessage, STATUS_CODES, Server, ServerResponse,
  _connectionListener, createServer, get, globalAgent, maxHeaderSize, request, setMaxIdleHTTPParsers,
  validateHeaderName, validateHeaderValue, WebSocket, MessageEvent, CloseEvent,
};
`

const httpsShim = `import http from "http";
import tls from "tls";

const H = globalThis.__noderatiHTTPInternals;

class Agent extends http.Agent {
  constructor(options) {
    super(options);
    this.defaultPort = 443;
    this.protocol = "https:";
    this.maxCachedSessions = this.options.maxCachedSessions;
    if (this.maxCachedSessions === undefined) this.maxCachedSessions = 100;
    this._sessionCache = { map: {}, list: [] };
  }
  getName(options = {}) {
    let name = super.getName(options);
    name += ":";
    if (options.ca) name += options.ca;
    name += ":";
    if (options.cert) name += options.cert;
    name += ":";
    if (options.clientCertEngine) name += options.clientCertEngine;
    name += ":";
    if (options.ciphers) name += options.ciphers;
    name += ":";
    if (options.key) name += options.key;
    name += ":";
    if (options.pfx) name += options.pfx;
    name += ":";
    if (options.rejectUnauthorized !== undefined) name += options.rejectUnauthorized;
    name += ":";
    if (options.servername && options.servername !== options.host) name += options.servername;
    name += ":";
    if (options.minVersion) name += options.minVersion;
    name += ":";
    if (options.maxVersion) name += options.maxVersion;
    name += ":";
    if (options.secureProtocol) name += options.secureProtocol;
    name += ":";
    if (options.crl) name += options.crl;
    name += ":";
    if (options.honorCipherOrder !== undefined) name += options.honorCipherOrder;
    name += ":";
    if (options.ecdhCurve) name += options.ecdhCurve;
    name += ":";
    if (options.dhparam) name += options.dhparam;
    name += ":";
    if (options.secureOptions !== undefined) name += options.secureOptions;
    name += ":";
    if (options.sessionIdContext) name += options.sessionIdContext;
    name += ":";
    if (options.sigalgs) name += JSON.stringify(options.sigalgs);
    name += ":";
    if (options.privateKeyIdentifier) name += options.privateKeyIdentifier;
    name += ":";
    if (options.privateKeyEngine) name += options.privateKeyEngine;
    return name;
  }
}
const globalAgent = new Agent({ keepAlive: true, scheduling: "lifo", timeout: 5000 });

function request(...args) {
  let options = {};
  if (typeof args[0] === "string") {
    const urlStr = args.shift();
    options = H.urlToHttpOptions(new URL(urlStr));
  } else if (args[0] instanceof URL) {
    options = H.urlToHttpOptions(args.shift());
  }
  if (args[0] && typeof args[0] !== "function") Object.assign(options, args.shift());
  options._defaultAgent = globalAgent;
  args.unshift(options);
  return new H.ClientRequest(...args);
}
function get(input, options, cb) {
  const req = request(input, options, cb);
  req.end();
  return req;
}

class Server extends http.Server {
  constructor(options, requestListener) {
    if (typeof options === "function") {
      requestListener = options;
      options = {};
    } else if (options == null) {
      options = {};
    }
    const ctx = options.secureContext || tls.createSecureContext(options);
    super({ ...options, __secureContext: ctx.context }, requestListener);
  }
}
Object.setPrototypeOf(Server.prototype, tls.Server.prototype);

Object.defineProperty(Agent, "name", { value: "Agent", configurable: true });
Object.defineProperty(Server, "name", { value: "Server", configurable: true });

function createServer(options, requestListener) {
  return new Server(options, requestListener);
}

export { Agent, Server, createServer, get, globalAgent, request };
export default { Agent, Server, createServer, get, globalAgent, request };
`
