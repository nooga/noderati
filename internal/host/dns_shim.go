package host

// dnsShim is node:dns's JS half (lib/dns.js + lib/internal/dns/*): argument
// validation, result shapes, error objects, Resolver and the promises
// API, over dns.go's binding.
const dnsShim = `import net from "net";

const binding = globalThis.__noderatiDNSBinding;
// net and dns import each other; read net's helpers lazily.
const NI = () => globalThis.__noderatiNetInternals;
const makeErr = (...a) => NI().makeErr(...a);
const ERR_INVALID_ARG_TYPE = (...a) => NI().ERR_INVALID_ARG_TYPE(...a);
const ERR_INVALID_ARG_VALUE = (...a) => NI().ERR_INVALID_ARG_VALUE(...a);
const validateString = (...a) => NI().validateString(...a);
const validateFunction = (...a) => NI().validateFunction(...a);
const validateBoolean = (...a) => NI().validateBoolean(...a);
const validateNumber = (...a) => NI().validateNumber(...a);

const codes = {
  NODATA: "ENODATA", FORMERR: "EFORMERR", SERVFAIL: "ESERVFAIL", NOTFOUND: "ENOTFOUND", NOTIMP: "ENOTIMP",
  REFUSED: "EREFUSED", BADQUERY: "EBADQUERY", BADNAME: "EBADNAME", BADFAMILY: "EBADFAMILY", BADRESP: "EBADRESP",
  CONNREFUSED: "ECONNREFUSED", TIMEOUT: "ETIMEOUT", EOF: "EOF", FILE: "EFILE", NOMEM: "ENOMEM",
  DESTRUCTION: "EDESTRUCTION", BADSTR: "EBADSTR", BADFLAGS: "EBADFLAGS", NONAME: "ENONAME", BADHINTS: "EBADHINTS",
  NOTINITIALIZED: "ENOTINITIALIZED", LOADIPHLPAPI: "ELOADIPHLPAPI", ADDRGETNETWORKPARAMS: "EADDRGETNETWORKPARAMS",
  CANCELLED: "ECANCELLED",
};
const ADDRCONFIG = 1024, V4MAPPED = 2048, ALL = 256;
const getaddrinfoErrno = { ENOTFOUND: -3008, EAI_AGAIN: -3001, EAI_FAIL: -3004 };

function validateOneOf(value, name, list) {
  if (!list.includes(value)) {
    const allowed = list.map((v) => (typeof v === "string" ? "'" + v + "'" : String(v))).join(", ");
    throw ERR_INVALID_ARG_VALUE(name, value, "must be one of: " + allowed);
  }
}

function dnsException(code, syscall, hostname) {
  const ex = new Error(syscall + " " + code + (hostname ? " " + hostname : ""));
  if (syscall === "getaddrinfo" && getaddrinfoErrno[code] !== undefined) ex.errno = getaddrinfoErrno[code];
  ex.code = code;
  ex.syscall = syscall;
  if (hostname) ex.hostname = hostname;
  return ex;
}

let defaultResultOrder = "verbatim";
function getDefaultResultOrder() { return defaultResultOrder; }
function setDefaultResultOrder(order) {
  validateOneOf(order, "dnsOrder", ["verbatim", "ipv4first", "ipv6first"]);
  defaultResultOrder = order;
}

function orderAddresses(list, order) {
  if (order === "ipv4first") return list.filter((a) => a.family === 4).concat(list.filter((a) => a.family === 6));
  if (order === "ipv6first") return list.filter((a) => a.family === 6).concat(list.filter((a) => a.family === 4));
  return list;
}

function lookup(hostname, options, callback) {
  let family = 0;
  let all = false;
  let dnsOrder = defaultResultOrder;
  if (hostname) validateString(hostname, "hostname");
  else if (hostname !== undefined && hostname !== null && hostname !== "" && hostname !== false) validateString(hostname, "hostname");
  if (typeof options === "function") {
    callback = options;
  } else if (typeof options === "number") {
    validateFunction(callback, "callback");
    validateOneOf(options, "family", [0, 4, 6]);
    family = options;
  } else if (options !== undefined && typeof options !== "object") {
    validateFunction(arguments.length === 2 ? options : callback, "callback");
    throw ERR_INVALID_ARG_TYPE("options", ["integer", "object"], options);
  } else {
    validateFunction(callback, "callback");
    if (options && options.hints != null) validateNumber(options.hints, "options.hints");
    if (options && options.family != null) {
      switch (options.family) {
        case "IPv4": family = 4; break;
        case "IPv6": family = 6; break;
        default:
          validateOneOf(options.family, "options.family", [0, 4, 6]);
          family = options.family;
      }
    }
    if (options && options.all != null) {
      validateBoolean(options.all, "options.all");
      all = options.all;
    }
    if (options && options.verbatim != null) {
      validateBoolean(options.verbatim, "options.verbatim");
      dnsOrder = options.verbatim ? "verbatim" : "ipv4first";
    }
    if (options && options.order != null) {
      validateOneOf(options.order, "options.order", ["ipv4first", "ipv6first", "verbatim"]);
      dnsOrder = options.order;
    }
  }
  if (!hostname) {
    if (all) process.nextTick(callback, null, []);
    else process.nextTick(callback, null, null, family === 6 ? 6 : 4);
    return {};
  }
  const matchedFamily = net.isIP(hostname);
  if (matchedFamily) {
    if (all) process.nextTick(callback, null, [{ address: hostname, family: matchedFamily }]);
    else process.nextTick(callback, null, hostname, matchedFamily);
    return {};
  }
  binding.lookup(hostname, family, (err, list) => {
    if (err) return callback(dnsException(err, "getaddrinfo", hostname));
    list = orderAddresses(list, dnsOrder);
    if (all) callback(null, list);
    else callback(null, list[0].address, list[0].family);
  });
  return {};
}

function lookupService(address, port, callback) {
  if (arguments.length !== 3) throw makeErr(TypeError, "ERR_MISSING_ARGS", 'The "address", "port", and "callback" arguments must be specified');
  validateString(address, "address");
  if (net.isIP(address) === 0) throw ERR_INVALID_ARG_VALUE("address", address);
  NI().validatePort(port);
  validateFunction(callback, "callback");
  binding.lookupService(address, +port, (err, hostname, service) => {
    if (err) return callback(dnsException(err, "getnameinfo", address));
    callback(null, hostname, service);
  });
  return {};
}

// ---- Resolver -------------------------------------------------------
const rrSyscall = {
  A: "queryA", AAAA: "queryAaaa", ANY: "queryAny", CAA: "queryCaa", CNAME: "queryCname", MX: "queryMx",
  NAPTR: "queryNaptr", NS: "queryNs", PTR: "queryPtr", SOA: "querySoa", SRV: "querySrv", TLSA: "queryTlsa", TXT: "queryTxt",
};

function stripType(r) {
  const { type, ttl, ...rest } = r;
  return rest;
}
function formatRecords(rrtype, records, ttl) {
  switch (rrtype) {
    case "A": case "AAAA":
      return ttl ? records.map((r) => ({ address: r.address, ttl: r.ttl })) : records.map((r) => r.address);
    case "CNAME": case "NS": case "PTR":
      return records.map((r) => r.value);
    case "MX":
      return records.map((r) => ({ exchange: r.exchange, priority: r.priority, type: "MX" }));
    case "TXT":
      return records.map((r) => r.entries);
    case "SRV":
      return records.map((r) => ({ name: r.name, port: r.port, priority: r.priority, weight: r.weight, type: "SRV" }));
    case "NAPTR":
      return records.map((r) => ({ flags: r.flags, service: r.service, regexp: r.regexp, replacement: r.replacement, order: r.order, preference: r.preference }));
    case "CAA":
      return records.map((r) => ({ critical: r.critical, type: "CAA", [r.tag]: r.value }));
    case "TLSA":
      return records.map((r) => ({ certUsage: r.certUsage, selector: r.selector, match: r.match, data: r.data.buffer.slice(r.data.byteOffset, r.data.byteOffset + r.data.byteLength) }));
    case "SOA":
      return records.length ? stripType(records[0]) : undefined;
    case "ANY":
      // c-ares doesn't decode TLSA inside ANY answers; neither does node.
      return records.filter((r) => r.type !== "TLSA").map((r) => {
        const t = r.type;
        switch (t) {
          case "A": case "AAAA": return { address: r.address, ttl: r.ttl, type: t };
          case "CNAME": case "NS": case "PTR": return { value: r.value, type: t };
          case "TXT": return { entries: r.entries, type: t };
          case "CAA": return { critical: r.critical, type: t, [r.tag]: r.value };
          case "MX": return { exchange: r.exchange, priority: r.priority, type: t };
          case "SRV": return { name: r.name, port: r.port, priority: r.priority, weight: r.weight, type: t };
          default: return { ...stripType(r), type: t };
        }
      });
  }
  return records;
}

function parseServer(s, i) {
  validateString(s, "servers[" + i + "]");
  let ip, port = 53;
  let m;
  if ((m = /^\[([^\]]+)\](?::(\d+))?$/.exec(s))) {
    ip = m[1];
    if (m[2]) port = Number(m[2]);
  } else if (net.isIP(s)) {
    ip = s;
  } else if ((m = /^([^:]+):(\d+)$/.exec(s))) {
    ip = m[1];
    port = Number(m[2]);
  } else {
    ip = s;
  }
  if (!net.isIP(ip)) throw makeErr(TypeError, "ERR_INVALID_IP_ADDRESS", "Invalid IP address: " + s);
  return { ip, port };
}
function formatServer({ ip, port }) {
  if (port === 53) return ip;
  return net.isIPv6(ip) ? "[" + ip + "]:" + port : ip + ":" + port;
}

const kServers = Symbol("servers");
const kPending = Symbol("pending");
class Resolver {
  constructor(options = undefined) {
    if (options !== undefined && (options === null || typeof options !== "object")) throw ERR_INVALID_ARG_TYPE("options", "Object", options);
    const timeout = options && options.timeout !== undefined ? options.timeout : -1;
    if (options && options.timeout !== undefined) validateNumber(timeout, "options.timeout");
    if (options && options.tries !== undefined) validateNumber(options.tries, "options.tries");
    this._timeout = timeout > 0 ? timeout : 5000;
    this._tries = (options && options.tries) || 4;
    this[kServers] = null;
    this[kPending] = new Set();
  }
  _servers() {
    if (this[kServers] === null) this[kServers] = binding.systemServers().map((s, i) => { try { return parseServer(s, i); } catch { return null; } }).filter(Boolean);
    return this[kServers];
  }
  getServers() { return this._servers().map(formatServer); }
  setServers(servers) {
    if (!Array.isArray(servers)) throw ERR_INVALID_ARG_TYPE("servers", "Array", servers);
    this[kServers] = servers.map(parseServer);
  }
  setLocalAddress(ipv4, ipv6) {
    validateString(ipv4, "ipv4");
    if (!net.isIPv4(ipv4)) throw ERR_INVALID_ARG_VALUE("ipv4", ipv4);
    if (ipv6 !== undefined) {
      validateString(ipv6, "ipv6");
      if (!net.isIPv6(ipv6)) throw ERR_INVALID_ARG_VALUE("ipv6", ipv6);
    }
    this._local = [ipv4, ipv6];
  }
  cancel() {
    for (const p of this[kPending]) p.cancel();
    this[kPending].clear();
  }
  _query(name, rrtype, ttl, callback) {
    const syscall = rrSyscall[rrtype];
    const servers = this._servers().map(({ ip, port }) => (net.isIPv6(ip) ? "[" + ip + "]:" + port : ip + ":" + port));
    let done = false;
    const pending = {
      cancel: () => {
        if (done) return;
        done = true;
        process.nextTick(callback, dnsException("ECANCELLED", syscall, name));
      },
    };
    this[kPending].add(pending);
    binding.query(servers, name, rrtype, this._timeout, this._tries, (err, records) => {
      this[kPending].delete(pending);
      if (done) return;
      done = true;
      if (err) return callback(dnsException(err, syscall, name));
      if (rrtype !== "ANY" && records.length === 0) return callback(dnsException("ENODATA", syscall, name));
      callback(null, formatRecords(rrtype, records, ttl));
    });
    return {};
  }
}

function defineResolveMethod(name, rrtype) {
  Resolver.prototype[name] = function (hostname, options, callback) {
    validateString(hostname, "name");
    let ttl = false;
    if (typeof options === "function") {
      callback = options;
    } else if (options !== undefined && options !== null) {
      ttl = !!options.ttl;
    }
    validateFunction(callback, "callback");
    return this._query(hostname, rrtype, ttl, callback);
  };
}
const resolveMethods = {
  resolveAny: "ANY", resolve4: "A", resolve6: "AAAA", resolveCaa: "CAA", resolveCname: "CNAME", resolveMx: "MX",
  resolveNs: "NS", resolveTlsa: "TLSA", resolveTxt: "TXT", resolveSrv: "SRV", resolvePtr: "PTR",
  resolveNaptr: "NAPTR", resolveSoa: "SOA",
};
for (const [name, rrtype] of Object.entries(resolveMethods)) defineResolveMethod(name, rrtype);
Resolver.prototype.reverse = function reverse(ip, callback) {
  validateString(ip, "ip");
  validateFunction(callback, "callback");
  if (!net.isIP(ip)) {
    process.nextTick(callback, dnsException("EINVAL", "getHostByAddr", ip));
    return {};
  }
  return this._query(ip, "PTR", false, (err, names) => {
    if (err) {
      err.syscall = "getHostByAddr";
      err.message = "getHostByAddr " + err.code + " " + ip;
      return callback(err);
    }
    callback(null, names);
  });
};
Resolver.prototype.resolve = function resolve(hostname, rrtype, callback) {
  if (typeof rrtype === "string") {
    if (!Object.prototype.hasOwnProperty.call(rrSyscall, rrtype)) throw ERR_INVALID_ARG_VALUE("rrtype", rrtype);
  } else if (typeof rrtype === "function") {
    callback = rrtype;
    rrtype = "A";
  } else {
    throw ERR_INVALID_ARG_TYPE("rrtype", "string", rrtype);
  }
  validateString(hostname, "name");
  validateFunction(callback, "callback");
  return this._query(hostname, rrtype, false, callback);
};

const defaultResolver = new Resolver();
function getServers() { return defaultResolver.getServers(); }
function setServers(servers) { defaultResolver.setServers(servers); }
const bound = {};
for (const name of [...Object.keys(resolveMethods), "reverse", "resolve"]) {
  bound[name] = function (...args) { return Resolver.prototype[name].apply(defaultResolver, args); };
  Object.defineProperty(bound[name], "name", { value: name });
}

// ---- promises ------------------------------------------------------
function promisifyCb(fn) {
  return (...args) => new Promise((resolve, reject) => fn(...args, (err, ...rest) => (err ? reject(err) : resolve(rest.length > 1 ? rest : rest[0]))));
}
class PromisesResolver {
  constructor(options) { this._r = new Resolver(options); Object.defineProperty(this, "_r", { enumerable: false }); }
  getServers() { return this._r.getServers(); }
  setServers(s) { return this._r.setServers(s); }
  setLocalAddress(a, b) { return this._r.setLocalAddress(a, b); }
  cancel() { return this._r.cancel(); }
}
for (const name of [...Object.keys(resolveMethods), "reverse"]) {
  PromisesResolver.prototype[name] = function (hostname, options) {
    return new Promise((resolve, reject) => {
      const args = name === "reverse" ? [hostname] : [hostname, options];
      Resolver.prototype[name].call(this._r, ...args, (err, res) => (err ? reject(err) : resolve(res)));
    });
  };
}
PromisesResolver.prototype.resolve = function (hostname, rrtype = "A") {
  return new Promise((resolve, reject) => Resolver.prototype.resolve.call(this._r, hostname, rrtype, (err, res) => (err ? reject(err) : resolve(res))));
};
Object.defineProperty(PromisesResolver, "name", { value: "Resolver" });

function lookupPromise(hostname, options) {
  if (hostname) validateString(hostname, "hostname");
  let opts = options;
  if (typeof options === "number") opts = { family: options };
  else if (options !== undefined && (options === null || typeof options !== "object")) throw ERR_INVALID_ARG_TYPE("options", ["integer", "object"], options);
  return new Promise((resolve, reject) => {
    lookup(hostname, opts || {}, (err, address, family) => {
      if (err) return reject(err);
      resolve(opts && opts.all ? address : { address, family });
    });
  });
}
function lookupServicePromise(address, port) {
  if (arguments.length !== 2) throw makeErr(TypeError, "ERR_MISSING_ARGS", 'The "address" and "port" arguments must be specified');
  return new Promise((resolve, reject) => {
    lookupService(address, port, (err, hostname, service) => (err ? reject(err) : resolve({ hostname, service })));
  });
}
const promiseDefault = new PromisesResolver();
promiseDefault._r = defaultResolver;
const promises = {
  ...codes,
  Resolver: PromisesResolver,
  getDefaultResultOrder, setDefaultResultOrder, getServers, setServers,
  lookup: lookupPromise, lookupService: lookupServicePromise,
};
for (const name of [...Object.keys(resolveMethods), "reverse", "resolve"]) {
  promises[name] = (...args) => PromisesResolver.prototype[name].apply(promiseDefault, args);
}
globalThis.__noderatiDNSPromises = promises;

const {
  resolveAny, resolve4, resolve6, resolveCaa, resolveCname, resolveMx, resolveNs, resolveTlsa, resolveTxt,
  resolveSrv, resolvePtr, resolveNaptr, resolveSoa, reverse, resolve,
} = bound;
const {
  NODATA, FORMERR, SERVFAIL, NOTFOUND, NOTIMP, REFUSED, BADQUERY, BADNAME, BADFAMILY, BADRESP, CONNREFUSED,
  TIMEOUT, EOF, FILE, NOMEM, DESTRUCTION, BADSTR, BADFLAGS, NONAME, BADHINTS, NOTINITIALIZED, LOADIPHLPAPI,
  ADDRGETNETWORKPARAMS, CANCELLED,
} = codes;

const dnsModule = {
  ADDRCONFIG, ALL, V4MAPPED, ...codes, Resolver, getDefaultResultOrder, setDefaultResultOrder, getServers, setServers,
  lookup, lookupService, promises, ...bound,
};

export {
  ADDRCONFIG, ALL, V4MAPPED, NODATA, FORMERR, SERVFAIL, NOTFOUND, NOTIMP, REFUSED, BADQUERY, BADNAME, BADFAMILY,
  BADRESP, CONNREFUSED, TIMEOUT, EOF, FILE, NOMEM, DESTRUCTION, BADSTR, BADFLAGS, NONAME, BADHINTS, NOTINITIALIZED,
  LOADIPHLPAPI, ADDRGETNETWORKPARAMS, CANCELLED, Resolver, getDefaultResultOrder, setDefaultResultOrder, getServers,
  setServers, lookup, lookupService, promises, resolveAny, resolve4, resolve6, resolveCaa, resolveCname, resolveMx,
  resolveNs, resolveTlsa, resolveTxt, resolveSrv, resolvePtr, resolveNaptr, resolveSoa, reverse, resolve,
};
export default dnsModule;
`

// node:dns/promises is the same object as dns.promises.
const dnsPromisesShim = `import "node:dns";
const p = globalThis.__noderatiDNSPromises;
export const {
  NODATA, FORMERR, SERVFAIL, NOTFOUND, NOTIMP, REFUSED, BADQUERY, BADNAME, BADFAMILY, BADRESP, CONNREFUSED,
  TIMEOUT, EOF, FILE, NOMEM, DESTRUCTION, BADSTR, BADFLAGS, NONAME, BADHINTS, NOTINITIALIZED, LOADIPHLPAPI,
  ADDRGETNETWORKPARAMS, CANCELLED, Resolver, getDefaultResultOrder, setDefaultResultOrder, getServers, setServers,
  lookup, lookupService, resolveAny, resolve4, resolve6, resolveCaa, resolveCname, resolveMx, resolveNs, resolveTlsa,
  resolveTxt, resolveSrv, resolvePtr, resolveNaptr, resolveSoa, reverse, resolve,
} = p;
export default p;
`
