package host

// fsStreamsShim is fs.ReadStream / fs.WriteStream, following real Node's
// lib/internal/fs/streams.js on top of the public stream API (_read/push,
// _write/_writev/_final/_destroy). The file is opened asynchronously
// after construction ('open' then 'ready'); reads/writes requested before
// that wait for it, and destroy() during the open closes the fd once it
// arrives. `fd` may be a number or a fs/promises FileHandle; `fs` may
// supply replacement open/read/write/writev/close/fsync implementations.
const fsStreamsShim = `import fs, { ReadStream as RSForward, FileReadStream as FRSForward, WriteStream as WSForward, FileWriteStream as FWSForward } from "fs";
import { Readable, Writable } from "stream";
import { createInterface } from "readline";

const kFs = Symbol("kFs");
const kHandle = Symbol("kHandle");
const kIsPerformingIO = Symbol("kIsPerformingIO");
const kOpening = Symbol("kOpening");
const kOpenDone = Symbol("kOpenDone");
const kIoDone = Symbol("kIoDone");

function copyOptions(options) {
  if (options == null) return {};
  if (typeof options === "string") return { encoding: options };
  return Object.assign({}, options);
}
function toPath(p) {
  if (p && typeof p === "object" && typeof p.href === "string" && p.protocol === "file:") {
    return decodeURIComponent(p.pathname);
  }
  return p;
}
function outOfRange(name, range, value) {
  const err = new RangeError('The value of "' + name + '" is out of range. It must be ' + range + ". Received " + String(value).replace(/\B(?=(\d{3})+(?!\d))/g, "_"));
  err.code = "ERR_OUT_OF_RANGE";
  return err;
}
function invalidType(name, expected, value) {
  const err = new TypeError('The "' + name + '" argument must be ' + expected + ". Received " + (value === null ? "null" : typeof value === "string" ? "type string ('" + value + "')" : "type " + typeof value + " (" + String(value) + ")"));
  err.code = "ERR_INVALID_ARG_TYPE";
  return err;
}
function validateInteger(v, name, min) {
  if (typeof v !== "number") throw invalidType(name, "of type number", v);
  if (!Number.isInteger(v)) throw outOfRange(name, "an integer", v);
  if (v < min || v > Number.MAX_SAFE_INTEGER) throw outOfRange(name, ">= " + min + " && <= " + Number.MAX_SAFE_INTEGER, v);
}

function fsImpl(options, names) {
  const custom = options.fs;
  if (!custom) return fs;
  const impl = {};
  for (const n of names) impl[n] = typeof custom[n] === "function" ? custom[n] : fs[n];
  return impl;
}

function importFd(stream, options) {
  if (options.fd == null) return false;
  const fd = options.fd;
  if (typeof fd === "number") {
    stream.fd = fd;
    return true;
  }
  if (fd && typeof fd === "object" && typeof fd.fd === "number" && typeof fd.close === "function") {
    stream[kHandle] = fd;
    stream.fd = fd.fd;
    return true;
  }
  throw invalidType("options.fd", "of type number or an instance of FileHandle", fd);
}

function openStream(stream) {
  stream[kOpening] = true;
  stream[kFs].open(stream.path, stream.flags, stream.mode, (er, fd) => {
    stream[kOpening] = false;
    if (er) {
      stream.emit(kOpenDone, er);
      if (!stream.destroyed) stream.destroy(er);
      return;
    }
    stream.fd = fd;
    if (!stream.destroyed) {
      stream.emit("open", fd);
      stream.emit("ready");
    }
    stream.emit(kOpenDone, null);
  });
}

function closeStream(stream, err, cb) {
  if (stream.fd === null || stream.fd === undefined) {
    cb(err);
  } else if (stream[kHandle]) {
    const handle = stream[kHandle];
    stream.fd = null;
    handle.close().then(() => cb(err), (er) => cb(er || err));
  } else {
    const fd = stream.fd;
    stream.fd = null;
    stream[kFs].close(fd, (er) => cb(er || err));
  }
}

function destroyImpl(stream, err, cb) {
  if (stream[kOpening]) {
    stream.once(kOpenDone, (er) => (er ? cb(err || er) : closeStream(stream, err, cb)));
  } else if (stream[kIsPerformingIO]) {
    stream.once(kIoDone, (er) => closeStream(stream, err || er, cb));
  } else {
    closeStream(stream, err, cb);
  }
}

function whenOpen(stream, fn) {
  if (stream.fd !== null) return true;
  stream.once(kOpenDone, (er) => { if (!er) fn(); });
  return false;
}

function closeWithCallback(stream, cb) {
  if (typeof cb === "function") {
    if (stream.closed) process.nextTick(cb);
    else {
      const onClose = () => { stream.removeListener("error", onError); cb(); };
      const onError = (err) => { stream.removeListener("close", onClose); cb(err); };
      stream.once("close", onClose);
      stream.once("error", onError);
    }
  }
}

export class ReadStream extends Readable {
  constructor(path, options) {
    options = copyOptions(options);
    if (options.highWaterMark === undefined) options.highWaterMark = 64 * 1024;
    if (options.autoDestroy === undefined) options.autoDestroy = options.autoClose === undefined ? true : options.autoClose;
    super(options);
    this[kFs] = fsImpl(options, ["open", "read", "close"]);
    this[kIsPerformingIO] = false;
    this[kOpening] = false;
    this.fd = null;
    this.path = toPath(path);
    this.flags = options.flags === undefined ? "r" : options.flags;
    this.mode = options.mode === undefined ? 0o666 : options.mode;
    const hasFd = importFd(this, options);
    this.start = options.start;
    this.end = options.end === undefined ? Infinity : options.end;
    this.pos = undefined;
    this.bytesRead = 0;
    if (this.start !== undefined) {
      validateInteger(this.start, "start", 0);
      this.pos = this.start;
    }
    if (this.end !== Infinity) {
      validateInteger(this.end, "end", 0);
      if (this.start !== undefined && this.start > this.end) {
        throw outOfRange("start", '<= "end" (here: ' + this.end + ")", this.start);
      }
    }
    this.autoClose = options.autoClose === undefined ? true : options.autoClose;
    if (!hasFd) process.nextTick(() => { if (!this.destroyed) openStream(this); });
  }
  get pending() { return this.fd === null; }
  _read(n) {
    if (!whenOpen(this, () => this._read(n))) return;
    n = this.pos !== undefined ? Math.min(this.end - this.pos + 1, n) : Math.min(this.end - this.bytesRead + 1, n);
    if (n <= 0) {
      this.push(null);
      return;
    }
    const buf = Buffer.allocUnsafeSlow ? Buffer.allocUnsafeSlow(n) : Buffer.alloc(n);
    this[kIsPerformingIO] = true;
    const done = (er, bytesRead) => {
      this[kIsPerformingIO] = false;
      if (this.destroyed) {
        this.emit(kIoDone, er);
        return;
      }
      if (er) {
        if (this.autoClose) this.destroy(er);
        else this.emit("error", er);
      } else if (bytesRead > 0) {
        if (this.pos !== undefined) this.pos += bytesRead;
        this.bytesRead += bytesRead;
        this.push(bytesRead !== buf.length ? Buffer.from(buf.subarray(0, bytesRead)) : buf);
      } else {
        this.push(null);
      }
    };
    if (this[kHandle]) {
      this[kHandle].read(buf, 0, n, this.pos === undefined ? null : this.pos).then((r) => done(null, r.bytesRead), (er) => done(er));
    } else {
      this[kFs].read(this.fd, buf, 0, n, this.pos === undefined ? null : this.pos, (er, bytesRead) => done(er, bytesRead));
    }
  }
  _destroy(err, cb) { destroyImpl(this, err, cb); }
  close(cb) {
    closeWithCallback(this, cb);
    this.destroy();
  }
}

export class WriteStream extends Writable {
  constructor(path, options) {
    options = copyOptions(options);
    options.decodeStrings = true;
    if (options.autoDestroy === undefined) options.autoDestroy = options.autoClose === undefined ? true : options.autoClose;
    super(options);
    this[kFs] = fsImpl(options, ["open", "write", "writev", "close", "fsync"]);
    this[kIsPerformingIO] = false;
    this[kOpening] = false;
    this.fd = null;
    this.path = toPath(path);
    this.flags = options.flags === undefined ? "w" : options.flags;
    this.mode = options.mode === undefined ? 0o666 : options.mode;
    this.flush = options.flush == null ? false : !!options.flush;
    const hasFd = importFd(this, options);
    this.start = options.start;
    this.pos = undefined;
    this.bytesWritten = 0;
    if (this.start !== undefined) {
      validateInteger(this.start, "start", 0);
      this.pos = this.start;
    }
    this.autoClose = options.autoClose === undefined ? true : options.autoClose;
    if (options.encoding) this.setDefaultEncoding(options.encoding);
    if (!hasFd) process.nextTick(() => { if (!this.destroyed) openStream(this); });
  }
  get pending() { return this.fd === null; }
  _write(data, encoding, cb) {
    if (!whenOpen(this, () => this._write(data, encoding, cb))) return;
    if (typeof data === "string") data = Buffer.from(data, encoding);
    this[kIsPerformingIO] = true;
    const done = (er, bytes) => {
      this[kIsPerformingIO] = false;
      if (this.destroyed) {
        cb(er);
        this.emit(kIoDone, er);
        return;
      }
      if (er) return cb(er);
      this.bytesWritten += bytes;
      cb();
    };
    const position = this.pos === undefined ? null : this.pos;
    if (this[kHandle]) {
      this[kHandle].write(data, 0, data.length, position).then((r) => done(null, r.bytesWritten), (er) => done(er));
    } else {
      this[kFs].write(this.fd, data, 0, data.length, position, (er, bytes) => done(er, bytes));
    }
    if (this.pos !== undefined) this.pos += data.length;
  }
  _writev(chunks, cb) {
    if (!whenOpen(this, () => this._writev(chunks, cb))) return;
    const bufs = chunks.map((c) => (typeof c.chunk === "string" ? Buffer.from(c.chunk, c.encoding) : c.chunk));
    let size = 0;
    for (const b of bufs) size += b.length;
    this[kIsPerformingIO] = true;
    const done = (er, bytes) => {
      this[kIsPerformingIO] = false;
      if (this.destroyed) {
        cb(er);
        this.emit(kIoDone, er);
        return;
      }
      if (er) return cb(er);
      this.bytesWritten += bytes;
      cb();
    };
    const position = this.pos === undefined ? null : this.pos;
    if (this[kHandle]) {
      this[kHandle].writev(bufs, position).then((r) => done(null, r.bytesWritten), (er) => done(er));
    } else if (typeof this[kFs].writev === "function") {
      this[kFs].writev(this.fd, bufs, position, (er, bytes) => done(er, bytes));
    } else {
      const all = Buffer.concat(bufs);
      this[kFs].write(this.fd, all, 0, all.length, position, (er, bytes) => done(er, bytes));
    }
    if (this.pos !== undefined) this.pos += size;
  }
  _final(cb) {
    if (!whenOpen(this, () => this._final(cb))) return;
    if (!this.flush) return cb();
    if (this[kHandle]) return this[kHandle].sync().then(() => cb(), cb);
    this[kFs].fsync(this.fd, (er) => cb(er));
  }
  _destroy(err, cb) { destroyImpl(this, err, cb); }
  close(cb) {
    if (cb) {
      if (this.closed) {
        process.nextTick(cb);
        return;
      }
      this.on("close", cb);
    }
    if (!this.autoClose) this.on("finish", this.destroy);
    this.end();
  }
  destroySoon(...args) { return this.end(...args); }
}

Object.defineProperty(ReadStream, Symbol.for("noderati.fs.readLines"), {
  value: (input) => createInterface({ input, crlfDelay: Infinity }),
});

// linkLazyClass splices the placeholder prototypes of fs's lazy
// named-export forwarders (fs_shim.go installLazyJSClasses) into cls's
// prototype chain, so instanceof against the forwarder works.
function linkLazyClass(cls, ...forwards) {
  let proto = cls.prototype;
  for (const f of forwards) {
    if (typeof f !== "function" || f === cls) continue;
    const fp = f.prototype;
    if (!fp || typeof fp !== "object" || fp === cls.prototype) continue;
    Object.setPrototypeOf(fp, Object.getPrototypeOf(proto));
    Object.setPrototypeOf(proto, fp);
    proto = fp;
  }
}
linkLazyClass(ReadStream, RSForward, FRSForward);
linkLazyClass(WriteStream, WSForward, FWSForward);

export const FileReadStream = ReadStream;
export const FileWriteStream = WriteStream;

export function createReadStream(path, options) {
  return new ReadStream(path, options);
}
export function createWriteStream(path, options) {
  return new WriteStream(path, options);
}
`
