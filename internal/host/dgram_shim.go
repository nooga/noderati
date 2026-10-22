package host

// dgramShim is node:dgram's JS half, a port of lib/dgram.js over the UDP
// handle from dgram.go: the bind/connect state machines, the queue that
// holds send()/connect()/close() until an implicit bind finishes, address
// lookups through dns.lookup, and node's error objects.
const dgramShim = `import { EventEmitter } from "events";
import dns from "dns";
import "net";

const binding = globalThis.__noderatiDgramBinding;
const NI = globalThis.__noderatiNetInternals;
const { makeErr, errnoException, exceptionWithHostPort, validatePort, validateString, validateNumber,
  validateFunction, validateAbortSignal, describe } = NI;

const BIND_STATE_UNBOUND = 0, BIND_STATE_BINDING = 1, BIND_STATE_BOUND = 2;
const CONNECT_STATE_DISCONNECTED = 0, CONNECT_STATE_CONNECTING = 1, CONNECT_STATE_CONNECTED = 2;
const RECV_BUFFER = true, SEND_BUFFER = false;
const UV_UDP_IPV6ONLY = 1, UV_UDP_REUSEADDR = 4, UV_UDP_REUSEPORT = 8;
const kStateSymbol = Symbol("state symbol");

const ERR_SOCKET_BAD_TYPE = () => makeErr(TypeError, "ERR_SOCKET_BAD_TYPE", "Bad socket type specified. Valid types are: udp4, udp6");
const ERR_SOCKET_ALREADY_BOUND = () => makeErr(Error, "ERR_SOCKET_ALREADY_BOUND", "Socket is already bound");
const ERR_SOCKET_DGRAM_NOT_RUNNING = () => makeErr(Error, "ERR_SOCKET_DGRAM_NOT_RUNNING", "Not running");
const ERR_SOCKET_DGRAM_IS_CONNECTED = () => makeErr(Error, "ERR_SOCKET_DGRAM_IS_CONNECTED", "Already connected");
const ERR_SOCKET_DGRAM_NOT_CONNECTED = () => makeErr(Error, "ERR_SOCKET_DGRAM_NOT_CONNECTED", "Not connected");
const ERR_SOCKET_BAD_BUFFER_SIZE = () => makeErr(TypeError, "ERR_SOCKET_BAD_BUFFER_SIZE", "Buffer size must be a positive integer");
const ERR_MISSING_ARGS = (name) => makeErr(TypeError, "ERR_MISSING_ARGS", 'The "' + name + '" argument must be specified');
const ERR_BUFFER_OUT_OF_BOUNDS = (name) => makeErr(RangeError, "ERR_BUFFER_OUT_OF_BOUNDS", '"' + name + '" is outside of buffer bounds');
const ERR_INVALID_BUFFER_ARG = (name, actual) => makeErr(TypeError, "ERR_INVALID_ARG_TYPE",
  'The "' + name + '" argument must be of type string or an instance of Buffer, TypedArray, or DataView.' + describe(actual));

class SystemError extends Error {}
Object.defineProperty(SystemError.prototype, "name", { value: "SystemError", writable: true, configurable: true });
function ERR_SOCKET_BUFFER_SIZE(info, recv) {
  const syscall = recv ? "uv_recv_buffer_size" : "uv_send_buffer_size";
  const desc = globalThis.__noderatiNetBinding.errname(info.code) || info.code.toLowerCase();
  const e = new SystemError("Could not get or set buffer size: " + syscall + " returned " + info.code + " (" + desc + ")");
  const ctx = { errno: info.errno, code: info.code, message: desc, syscall };
  Object.defineProperty(e, "code", { value: "ERR_SOCKET_BUFFER_SIZE", enumerable: true, writable: true, configurable: true });
  Object.defineProperty(e, "info", { value: ctx, enumerable: true, configurable: true });
  Object.defineProperty(e, "errno", { get() { return ctx.errno; }, set(v) { ctx.errno = v; }, enumerable: true, configurable: true });
  Object.defineProperty(e, "syscall", { get() { return ctx.syscall; }, set(v) { ctx.syscall = v; }, enumerable: true, configurable: true });
  return e;
}

const isArrayBufferView = (v) => ArrayBuffer.isView(v);

function newHandle(type, lookup) {
  if (lookup === undefined) {
    lookup = dns.lookup;
  } else {
    validateFunction(lookup, "lookup");
  }
  let handle;
  if (type === "udp4") {
    handle = binding.newHandle("udp4");
    handle.lookup = (address, callback) => lookup(address || "127.0.0.1", 4, callback);
  } else if (type === "udp6") {
    handle = binding.newHandle("udp6");
    handle.lookup = (address, callback) => lookup(address || "::1", 6, callback);
  } else {
    throw ERR_SOCKET_BAD_TYPE();
  }
  return handle;
}

class Socket extends EventEmitter {
  constructor(type, listener) {
    super();
    let lookup, recvBufferSize, sendBufferSize, options;
    if (type !== null && typeof type === "object") {
      options = type;
      type = options.type;
      lookup = options.lookup;
      if (options.recvBufferSize) recvBufferSize = options.recvBufferSize;
      if (options.sendBufferSize) sendBufferSize = options.sendBufferSize;
    }
    const handle = newHandle(type, lookup);
    handle[kOwnerSymbol] = this;
    this.type = type;
    if (typeof listener === "function") this.on("message", listener);
    this[kStateSymbol] = {
      handle,
      receiving: false,
      bindState: BIND_STATE_UNBOUND,
      connectState: CONNECT_STATE_DISCONNECTED,
      queue: undefined,
      reuseAddr: options && options.reuseAddr,
      reusePort: options && options.reusePort,
      ipv6Only: options && options.ipv6Only,
      recvBufferSize,
      sendBufferSize,
    };
    if (options?.signal !== undefined) {
      const { signal } = options;
      validateAbortSignal(signal, "options.signal");
      const onAborted = () => {
        if (this[kStateSymbol].handle) this.close();
      };
      if (signal.aborted) {
        onAborted();
      } else {
        signal.addEventListener("abort", onAborted, { once: true });
        this.once("close", () => signal.removeEventListener("abort", onAborted));
      }
    }
  }

  bind(port_, address_ /* , callback */) {
    let port = port_;
    healthCheck(this);
    const state = this[kStateSymbol];
    if (state.bindState !== BIND_STATE_UNBOUND) throw ERR_SOCKET_ALREADY_BOUND();
    state.bindState = BIND_STATE_BINDING;

    const cb = arguments.length && arguments[arguments.length - 1];
    if (typeof cb === "function") {
      const removeListeners = () => {
        this.removeListener("error", removeListeners);
        this.removeListener("listening", onListening);
      };
      const onListening = () => {
        removeListeners();
        cb.call(this);
      };
      this.on("error", removeListeners);
      this.on("listening", onListening);
    }

    let address;
    let exclusive;
    if (port !== null && typeof port === "object") {
      address = port.address || "";
      exclusive = !!port.exclusive;
      port = port.port;
    } else {
      address = typeof address_ === "function" ? "" : address_;
      exclusive = false;
    }
    if (!address) address = this.type === "udp4" ? "0.0.0.0" : "::";

    state.handle.lookup(address, (err, ip) => {
      if (!state.handle) return; // closed in the meantime
      if (err) {
        state.bindState = BIND_STATE_UNBOUND;
        this.emit("error", err);
        return;
      }
      let flags = 0;
      if (state.reuseAddr) flags |= UV_UDP_REUSEADDR;
      if (state.ipv6Only) flags |= UV_UDP_IPV6ONLY;
      if (state.reusePort) flags |= UV_UDP_REUSEPORT;
      const p = Number(port) || 0;
      const e = state.handle.bind(ip, p, flags);
      if (e) {
        const ex = exceptionWithHostPort(e, "bind", ip, port);
        state.bindState = BIND_STATE_UNBOUND;
        this.emit("error", ex);
        return;
      }
      startListening(this);
    });
    return this;
  }

  connect(port, address, callback) {
    port = validatePort(port, "Port", false);
    if (typeof address === "function") {
      callback = address;
      address = "";
    } else if (address === undefined) {
      address = "";
    }
    validateString(address, "address");
    const state = this[kStateSymbol];
    if (state.connectState !== CONNECT_STATE_DISCONNECTED) throw ERR_SOCKET_DGRAM_IS_CONNECTED();
    state.connectState = CONNECT_STATE_CONNECTING;
    if (state.bindState === BIND_STATE_UNBOUND) this.bind({ port: 0, exclusive: true }, null);
    if (state.bindState !== BIND_STATE_BOUND) {
      enqueue(this, _connect.bind(this, port, address, callback));
      return;
    }
    _connect.call(this, port, address, callback);
  }

  disconnect() {
    const state = this[kStateSymbol];
    if (state.connectState !== CONNECT_STATE_CONNECTED) throw ERR_SOCKET_DGRAM_NOT_CONNECTED();
    const err = state.handle.disconnect();
    if (err) throw errnoException(err, "connect");
    state.connectState = CONNECT_STATE_DISCONNECTED;
  }

  sendto(buffer, offset, length, port, address, callback) {
    validateNumber(offset, "offset");
    validateNumber(length, "length");
    validateNumber(port, "port");
    validateString(address, "address");
    this.send(buffer, offset, length, port, address, callback);
  }

  send(buffer, offset, length, port, address, callback) {
    let list;
    const state = this[kStateSymbol];
    const connected = state.connectState === CONNECT_STATE_CONNECTED;
    if (!connected) {
      if (address || (port && typeof port !== "function")) {
        buffer = sliceBuffer(buffer, offset, length);
      } else {
        callback = port;
        port = offset;
        address = length;
      }
    } else {
      if (typeof length === "number") {
        buffer = sliceBuffer(buffer, offset, length);
        if (typeof port === "function") {
          callback = port;
          port = null;
        }
      } else {
        callback = offset;
      }
      if (port || address) throw ERR_SOCKET_DGRAM_IS_CONNECTED();
    }

    if (!Array.isArray(buffer)) {
      if (typeof buffer === "string") {
        list = [Buffer.from(buffer)];
      } else if (!isArrayBufferView(buffer)) {
        throw ERR_INVALID_BUFFER_ARG("buffer", buffer);
      } else {
        list = [buffer];
      }
    } else if (!(list = fixBufferList(buffer))) {
      throw ERR_INVALID_BUFFER_ARG("buffer list arguments", buffer);
    }

    if (!connected) port = validatePort(port, "Port", false);

    if (typeof callback !== "function") callback = undefined;

    if (typeof address === "function") {
      callback = address;
      address = undefined;
    } else if (address != null) {
      validateString(address, "address");
    }

    healthCheck(this);

    if (state.bindState === BIND_STATE_UNBOUND) this.bind({ port: 0, exclusive: true }, null);

    if (list.length === 0) list.push(Buffer.alloc(0));

    if (state.bindState !== BIND_STATE_BOUND) {
      enqueue(this, this.send.bind(this, list, port, address, callback));
      return;
    }

    const afterDns = (ex, ip) => doSend(ex, this, ip, list, address, port, callback);
    if (!connected) {
      state.handle.lookup(address, afterDns);
    } else {
      afterDns(null, null);
    }
  }

  close(callback) {
    const state = this[kStateSymbol];
    const queue = state.queue;
    if (typeof callback === "function") this.on("close", callback);
    if (queue !== undefined) {
      queue.push(this.close.bind(this));
      return this;
    }
    healthCheck(this);
    stopReceiving(this);
    state.handle.close();
    state.handle = null;
    process.nextTick(socketCloseNT, this);
    return this;
  }

  async [Symbol.asyncDispose]() {
    if (!this[kStateSymbol].handle) return;
    await new Promise((resolve, reject) => {
      this.close((err) => (err ? reject(err) : resolve()));
    });
  }

  address() {
    healthCheck(this);
    const out = {};
    const err = this[kStateSymbol].handle.getsockname(out);
    if (err) throw errnoException(err, "getsockname");
    return out;
  }

  remoteAddress() {
    healthCheck(this);
    const state = this[kStateSymbol];
    if (state.connectState !== CONNECT_STATE_CONNECTED) throw ERR_SOCKET_DGRAM_NOT_CONNECTED();
    const out = {};
    const err = state.handle.getpeername(out);
    if (err) throw errnoException(err, "getpeername");
    return out;
  }

  setBroadcast(arg) {
    const err = this[kStateSymbol].handle.setBroadcast(arg ? 1 : 0);
    if (err) throw errnoException(err, "setBroadcast");
  }

  setTTL(ttl) {
    validateNumber(ttl, "ttl");
    const err = this[kStateSymbol].handle.setTTL(ttl);
    if (err) throw errnoException(err, "setTTL");
    return ttl;
  }

  setMulticastTTL(ttl) {
    validateNumber(ttl, "ttl");
    const err = this[kStateSymbol].handle.setMulticastTTL(ttl);
    if (err) throw errnoException(err, "setMulticastTTL");
    return ttl;
  }

  setMulticastLoopback(arg) {
    const err = this[kStateSymbol].handle.setMulticastLoopback(arg ? 1 : 0);
    if (err) throw errnoException(err, "setMulticastLoopback");
    return arg;
  }

  setMulticastInterface(interfaceAddress) {
    healthCheck(this);
    validateString(interfaceAddress, "interfaceAddress");
    const err = this[kStateSymbol].handle.setMulticastInterface(interfaceAddress);
    if (err) throw errnoException(err, "setMulticastInterface");
  }

  addMembership(multicastAddress, interfaceAddress) {
    healthCheck(this);
    if (!multicastAddress) throw ERR_MISSING_ARGS("multicastAddress");
    const err = this[kStateSymbol].handle.addMembership(multicastAddress, interfaceAddress);
    if (err) throw errnoException(err, "addMembership");
  }

  dropMembership(multicastAddress, interfaceAddress) {
    healthCheck(this);
    if (!multicastAddress) throw ERR_MISSING_ARGS("multicastAddress");
    const err = this[kStateSymbol].handle.dropMembership(multicastAddress, interfaceAddress);
    if (err) throw errnoException(err, "dropMembership");
  }

  addSourceSpecificMembership(sourceAddress, groupAddress, interfaceAddress) {
    healthCheck(this);
    validateString(sourceAddress, "sourceAddress");
    validateString(groupAddress, "groupAddress");
    const err = this[kStateSymbol].handle.addSourceSpecificMembership(sourceAddress, groupAddress, interfaceAddress);
    if (err) throw errnoException(err, "addSourceSpecificMembership");
  }

  dropSourceSpecificMembership(sourceAddress, groupAddress, interfaceAddress) {
    healthCheck(this);
    validateString(sourceAddress, "sourceAddress");
    validateString(groupAddress, "groupAddress");
    const err = this[kStateSymbol].handle.dropSourceSpecificMembership(sourceAddress, groupAddress, interfaceAddress);
    if (err) throw errnoException(err, "dropSourceSpecificMembership");
  }

  ref() {
    const handle = this[kStateSymbol].handle;
    if (handle) handle.ref();
    return this;
  }

  unref() {
    const handle = this[kStateSymbol].handle;
    if (handle) handle.unref();
    return this;
  }

  setRecvBufferSize(size) { bufferSize(this, size, RECV_BUFFER); }
  setSendBufferSize(size) { bufferSize(this, size, SEND_BUFFER); }
  getRecvBufferSize() { return bufferSize(this, 0, RECV_BUFFER); }
  getSendBufferSize() { return bufferSize(this, 0, SEND_BUFFER); }
  // Sends complete synchronously, so nothing is ever queued in the handle.
  getSendQueueSize() { return 0; }
  getSendQueueCount() { return 0; }
}
Object.defineProperty(Socket, "name", { value: "Socket", configurable: true });
const kOwnerSymbol = Symbol("owner symbol");

function healthCheck(socket) {
  if (!socket[kStateSymbol].handle) throw ERR_SOCKET_DGRAM_NOT_RUNNING();
}

function stopReceiving(socket) {
  const state = socket[kStateSymbol];
  if (!state.receiving) return;
  state.handle.recvStop();
  state.receiving = false;
}

function socketCloseNT(self) {
  self.emit("close");
}

function startListening(socket) {
  const state = socket[kStateSymbol];
  state.handle.onmessage = onMessage;
  state.handle.onerror = onError;
  state.handle.recvStart();
  state.receiving = true;
  state.bindState = BIND_STATE_BOUND;
  if (state.recvBufferSize) bufferSize(socket, state.recvBufferSize, RECV_BUFFER);
  if (state.sendBufferSize) bufferSize(socket, state.sendBufferSize, SEND_BUFFER);
  socket.emit("listening");
}

function onMessage(buf, rinfo) {
  const self = this[kOwnerSymbol];
  rinfo.size = buf.length;
  self.emit("message", buf, rinfo);
}

function onError(err) {
  const self = this[kOwnerSymbol];
  self.emit("error", errnoException(err, "recvmsg"));
}

function bufferSize(self, size, buffer) {
  if (size >>> 0 !== size) throw ERR_SOCKET_BAD_BUFFER_SIZE();
  const handle = self[kStateSymbol].handle;
  const ret = handle ? handle.bufferSize(size, buffer) : { code: "EBADF", errno: -9 };
  if (typeof ret !== "number") throw ERR_SOCKET_BUFFER_SIZE(ret, buffer);
  return ret;
}

function enqueue(self, toEnqueue) {
  const state = self[kStateSymbol];
  if (state.queue === undefined) {
    state.queue = [];
    self.once(EventEmitter.errorMonitor, onListenError);
    self.once("listening", onListenSuccess);
  }
  state.queue.push(toEnqueue);
}

function onListenSuccess() {
  this.removeListener(EventEmitter.errorMonitor, onListenError);
  clearQueue.call(this);
}

function onListenError() {
  this.removeListener("listening", onListenSuccess);
  this[kStateSymbol].queue = undefined;
}

function clearQueue() {
  const state = this[kStateSymbol];
  const queue = state.queue;
  state.queue = undefined;
  for (const entry of queue) entry();
}

function _connect(port, address, callback) {
  const state = this[kStateSymbol];
  if (callback) this.once("connect", callback);
  state.handle.lookup(address, (ex, ip) => doConnect(ex, this, ip, address, port, callback));
}

function doConnect(ex, self, ip, address, port, callback) {
  const state = self[kStateSymbol];
  if (!state.handle) return;
  if (!ex) {
    const err = state.handle.connect(ip, port);
    if (err) ex = exceptionWithHostPort(err, "connect", address, port);
  }
  if (ex) {
    state.connectState = CONNECT_STATE_DISCONNECTED;
    return process.nextTick(() => {
      if (callback) {
        self.removeListener("connect", callback);
        callback(ex);
      } else {
        self.emit("error", ex);
      }
    });
  }
  state.connectState = CONNECT_STATE_CONNECTED;
  process.nextTick(() => self.emit("connect"));
}

function sliceBuffer(buffer, offset, length) {
  if (typeof buffer === "string") {
    buffer = Buffer.from(buffer);
  } else if (!isArrayBufferView(buffer)) {
    throw ERR_INVALID_BUFFER_ARG("buffer", buffer);
  }
  offset = offset >>> 0;
  length = length >>> 0;
  if (offset > buffer.byteLength) throw ERR_BUFFER_OUT_OF_BOUNDS("offset");
  if (offset + length > buffer.byteLength) throw ERR_BUFFER_OUT_OF_BOUNDS("length");
  return Buffer.from(buffer.buffer, buffer.byteOffset + offset, length);
}

function fixBufferList(list) {
  const newlist = new Array(list.length);
  for (let i = 0, l = list.length; i < l; i++) {
    const buf = list[i];
    if (typeof buf === "string") newlist[i] = Buffer.from(buf);
    else if (!isArrayBufferView(buf)) return null;
    else newlist[i] = buf;
  }
  return newlist;
}

function doSend(ex, self, ip, list, address, port, callback) {
  const state = self[kStateSymbol];
  if (ex) {
    if (typeof callback === "function") {
      process.nextTick(callback, ex);
      return;
    }
    process.nextTick(() => self.emit("error", ex));
    return;
  } else if (!state.handle) {
    return;
  }
  const data = list.length === 1 && Buffer.isBuffer(list[0]) ? list[0]
    : Buffer.concat(list.map((b) => Buffer.isBuffer(b) ? b : Buffer.from(b.buffer, b.byteOffset, b.byteLength)));
  const ret = port ? state.handle.send(data, port, ip) : state.handle.send(data);
  if (typeof ret === "number") {
    if (callback) process.nextTick(callback, null, ret - 1);
    return;
  }
  if (callback) {
    // Not emitted as 'error' without a callback, like node.
    const err = exceptionWithHostPort(ret, "send", address, port);
    process.nextTick(callback, err);
  }
}

function createSocket(type, listener) {
  return new Socket(type, listener);
}

export { createSocket, Socket };
export default { createSocket, Socket };
`
