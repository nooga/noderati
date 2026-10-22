package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// fs_shim.go: the parts of node:fs and node:fs/promises written in JS on
// top of the Go-backed calls - Dir/opendir, cp, exists, appendFile,
// mkdtempDisposable, openAsBlob, Utf8Stream, and the whole promise API
// beyond the original handful (FileHandle included). Streams live in a
// separate shim (fs_streams_shim.go) so using any of these doesn't pay for
// loading node:stream. Both load lazily, on first use of one of their
// exports (installLazyJSExports).

const fsExtraShim = `import fs, { Dir as DirForward, Utf8Stream as Utf8StreamForward } from "fs";
import { EventEmitter } from "events";

const kFd = Symbol("kFd");
const kDirEntries = Symbol("kDirEntries");
const kDirClosed = Symbol("kDirClosed");

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
function nextTick(fn, ...args) { process.nextTick(() => fn(...args)); }
function maybeCallback(cb) {
  if (typeof cb !== "function") {
    const err = new TypeError('The "cb" argument must be of type function. Received ' + (cb === undefined ? "undefined" : typeof cb));
    err.code = "ERR_INVALID_ARG_TYPE";
    throw err;
  }
  return cb;
}
// callbackify runs a synchronous body and reports to a Node-style
// callback on the next tick.
function callbackify(body, args) {
  const cb = maybeCallback(args[args.length - 1]);
  let res, err = null;
  try { res = body(...args.slice(0, -1)); } catch (e) { err = e; }
  nextTick(() => err ? cb(err) : res === undefined ? cb(null) : cb(null, res));
}
function codeError(Ctor, code, message, props) {
  const err = new Ctor(message);
  err.code = code;
  if (props) Object.assign(err, props);
  return err;
}
function abortError(signal) {
  const err = new Error("The operation was aborted");
  err.name = "AbortError";
  err.code = "ABORT_ERR";
  if (signal && signal.reason !== undefined) err.cause = signal.reason;
  return err;
}
function toPath(p) {
  if (p && typeof p === "object" && typeof p.href === "string" && p.protocol === "file:") {
    return decodeURIComponent(p.pathname);
  }
  return Buffer.isBuffer(p) ? p.toString() : p;
}

// ---------- callback-only additions ----------

export function exists(path, cb) {
  maybeCallback(cb);
  let ok = false;
  try { ok = fs.existsSync(path); } catch {}
  nextTick(cb, ok);
}

export function appendFile(path, data, ...rest) {
  callbackify((p, d, opts) => { appendFileSyncImpl(p, d, opts); }, [path, data, ...rest]);
}

function encodingOf(options, def) {
  if (typeof options === "string") return options;
  if (options && options.encoding) return options.encoding;
  return def;
}
function toBuffer(data, encoding) {
  if (typeof data === "string") return Buffer.from(data, encoding || "utf8");
  if (Buffer.isBuffer(data)) return data;
  if (ArrayBuffer.isView(data)) return Buffer.from(data.buffer, data.byteOffset, data.byteLength);
  return Buffer.from(String(data), encoding || "utf8");
}
function writeAll(fd, buf, position) {
  let off = 0;
  while (off < buf.length) {
    const n = fs.writeSync(fd, buf, off, buf.length - off, position == null ? null : position + off);
    off += n;
  }
  return off;
}
function appendFileSyncImpl(path, data, options) {
  const opts = typeof options === "string" ? { encoding: options } : (options || {});
  const buf = toBuffer(data, opts.encoding);
  if (typeof path === "number") { writeAll(path, buf, null); return; }
  const fd = fs.openSync(path, opts.flag || "a", opts.mode === undefined ? 0o666 : opts.mode);
  try { writeAll(fd, buf, null); } finally { fs.closeSync(fd); }
}

// ---------- Dir ----------

function dirClosedError() {
  return codeError(Error, "ERR_DIR_CLOSED", "Directory handle was closed");
}

export class Dir {
  constructor(path, entries, options) {
    this[kDirEntries] = entries;
    this[kDirClosed] = false;
    this._path = path;
    this._options = options || {};
  }
  get path() { return this._path; }
  readSync() {
    if (this[kDirClosed]) throw dirClosedError();
    return this[kDirEntries].length ? this[kDirEntries].shift() : null;
  }
  read(cb) {
    if (cb === undefined) {
      try { return Promise.resolve(this.readSync()); } catch (e) { return Promise.reject(e); }
    }
    maybeCallback(cb);
    let ent = null, err = null;
    try { ent = this.readSync(); } catch (e) { err = e; }
    nextTick(() => err ? cb(err) : cb(null, ent));
  }
  closeSync() {
    if (this[kDirClosed]) throw dirClosedError();
    this[kDirClosed] = true;
    this[kDirEntries] = [];
  }
  close(cb) {
    if (cb === undefined) {
      try { this.closeSync(); return Promise.resolve(); } catch (e) { return Promise.reject(e); }
    }
    maybeCallback(cb);
    let err = null;
    try { this.closeSync(); } catch (e) { err = e; }
    nextTick(() => cb(err));
  }
  async *entries() {
    try {
      for (;;) {
        const ent = this.readSync();
        if (ent === null) break;
        yield ent;
      }
    } finally {
      if (!this[kDirClosed]) this.closeSync();
    }
  }
  [Symbol.asyncIterator]() { return this.entries(); }
  [Symbol.dispose]() { if (!this[kDirClosed]) this.closeSync(); }
  async [Symbol.asyncDispose]() { if (!this[kDirClosed]) this.closeSync(); }
}

function opendirImpl(path, options) {
  path = toPath(path);
  const opts = typeof options === "string" ? { encoding: options } : (options || {});
  let entries;
  try {
    entries = fs.readdirSync(path, { withFileTypes: true, recursive: !!opts.recursive });
  } catch (e) {
    // Real Node reports opendir failures without the path.
    if (e && e.syscall === "scandir") {
      const msg = String(e.message).replace(/, scandir .*$/, ", opendir");
      const err = new Error(msg);
      err.errno = e.errno; err.code = e.code; err.syscall = "opendir";
      throw err;
    }
    throw e;
  }
  return new Dir(path, entries, opts);
}
export function opendirSync(path, options) { return opendirImpl(path, options); }
export function opendir(path, ...rest) { callbackify((p, o) => opendirImpl(p, o), [path, ...rest]); }

// ---------- cp ----------

function cpError(code, message, extra) {
  const isSys = code === "ERR_FS_CP_EEXIST" || code === "ERR_FS_EISDIR_SYS";
  const err = new Error(message);
  err.code = code;
  if (extra) Object.assign(err, extra);
  if (isSys) Object.defineProperty(err, "name", { value: "SystemError", enumerable: false, writable: true, configurable: true });
  return err;
}
function isSrcSubdir(src, dest) {
  const pathMod = { resolve: (p) => p };
  const s = src.split("/").filter(Boolean), d = dest.split("/").filter(Boolean);
  return s.every((cur, i) => d[i] === cur);
}
function resolvePath(p) {
  if (p.startsWith("/")) return p;
  return process.cwd().replace(/\/$/, "") + "/" + p;
}
// cpGen holds cp's logic once for cpSync, cp and fs.promises.cp: it
// yields each filter() result, which the sync driver uses as-is and the
// async drivers await.
function* cpGen(src, dest, opts) {
  src = toPath(src); dest = toPath(dest);
  if (opts.filter) {
    const keep = yield opts.filter(src, dest);
    if (!keep) return;
  }
  const srcStat = opts.dereference ? fs.statSync(src) : fs.lstatSync(src);
  let destStat = null;
  try { destStat = fs.statSync(dest); } catch (e) { if (e.code !== "ENOENT") throw e; }
  if (srcStat.isDirectory() && !opts.recursive) {
    throw cpError("ERR_FS_EISDIR", "Recursive option not enabled, cannot copy a directory: " + src + "/");
  }
  if (destStat) {
    if (srcStat.ino === destStat.ino && srcStat.dev === destStat.dev) {
      throw cpError("ERR_FS_CP_EINVAL", "src and dest cannot be the same " + dest);
    }
    if (srcStat.isDirectory() && !destStat.isDirectory()) {
      throw cpError("ERR_FS_CP_DIR_TO_NON_DIR", "Cannot overwrite non-directory " + dest + " with directory " + src);
    }
    if (!srcStat.isDirectory() && destStat.isDirectory()) {
      throw cpError("ERR_FS_CP_NON_DIR_TO_DIR", "Cannot overwrite directory " + dest + " with non-directory " + src);
    }
  }
  if (srcStat.isDirectory() && isSrcSubdir(resolvePath(src), resolvePath(dest))) {
    throw cpError("ERR_FS_CP_EINVAL", "Cannot copy " + src + "/ to a subdirectory of self " + dest);
  }
  yield* cpEntry(srcStat, destStat, src, dest, opts);
}
function* cpEntry(srcStat, destStat, src, dest, opts) {
  if (srcStat.isDirectory()) {
    if (!destStat) fs.mkdirSync(dest, { mode: srcStat.mode & 0o7777 });
    for (const name of fs.readdirSync(src)) {
      const s = src + "/" + name, d = dest + "/" + name;
      if (opts.filter) {
        const keep = yield opts.filter(s, d);
        if (!keep) continue;
      }
      const st = opts.dereference ? fs.statSync(s) : fs.lstatSync(s);
      let dst = null;
      try { dst = fs.lstatSync(d); } catch (e) { if (e.code !== "ENOENT") throw e; }
      yield* cpEntry(st, dst, s, d, opts);
    }
    return;
  }
  if (srcStat.isSymbolicLink()) {
    let target = fs.readlinkSync(src);
    if (!opts.verbatimSymlinks && !target.startsWith("/")) {
      target = resolvePath(src).replace(/\/[^/]*$/, "") + "/" + target;
    }
    if (destStat) fs.unlinkSync(dest);
    fs.symlinkSync(target, dest);
    return;
  }
  if (destStat) {
    if (opts.force) fs.unlinkSync(dest);
    else if (opts.errorOnExist) {
      throw cpError("ERR_FS_CP_EEXIST", "Target already exists: cp returned EEXIST (" + dest + " already exists) " + dest,
        { errno: 17, syscall: "cp", path: dest });
    } else return;
  }
  fs.copyFileSync(src, dest, opts.mode || 0);
  if (opts.preserveTimestamps) fs.utimesSync(dest, srcStat.atime, srcStat.mtime);
}
function cpOptions(opts) {
  if (typeof opts === "function") opts = { filter: opts };
  opts = Object.assign({ dereference: false, errorOnExist: false, force: true, preserveTimestamps: false, recursive: false, verbatimSymlinks: false }, opts || {});
  if (opts.force === undefined) opts.force = true;
  return opts;
}
export function cpSync(src, dest, options) {
  const gen = cpGen(src, dest, cpOptions(options));
  let r = gen.next();
  while (!r.done) r = gen.next(r.value);
}
async function cpAsync(src, dest, options) {
  const gen = cpGen(src, dest, cpOptions(options));
  let r = gen.next();
  while (!r.done) r = gen.next(await r.value);
}
export function cp(src, dest, options, cb) {
  if (typeof options === "function") { cb = options; options = undefined; }
  maybeCallback(cb);
  cpAsync(src, dest, options).then((res) => cb(null, res), (err) => cb(err));
}

// ---------- mkdtempDisposable / openAsBlob ----------

export function mkdtempDisposableSync(prefix, options) {
  const path = fs.mkdtempSync(prefix, options);
  const remove = () => fs.rmSync(path, { recursive: true, force: true });
  return { path, remove, [Symbol.dispose]: remove };
}

export async function openAsBlob(path, options) {
  const opts = options || {};
  const data = fs.readFileSync(toPath(path));
  return new Blob([data], { type: opts.type || "" });
}

// ---------- Utf8Stream ----------

// A small port of real Node's (sonic-boom derived) fs.Utf8Stream: a
// buffered, string-oriented file writer. Writes are done synchronously
// once minLength bytes are pending (or on flush/end).
export class Utf8Stream extends EventEmitter {
  constructor(opts = {}) {
    super();
    const { fd, dest, minLength = 0, maxLength = 0, maxWrite = 16384, sync = false, append = true, mkdir = false, fsync = false, mode, contentMode = "utf8" } = opts;
    this.minLength = minLength;
    this.maxLength = maxLength;
    this.maxWrite = maxWrite;
    this.sync = sync;
    this.append = append;
    this.mode = mode;
    this.contentMode = contentMode;
    this.fsync = fsync;
    this._mkdir = mkdir;
    this._buf = [];
    this._len = 0;
    this._writing = false;
    this._ending = false;
    this._destroyed = false;
    this.fd = -1;
    this.file = null;
    const target = fd !== undefined ? fd : dest;
    if (typeof target === "number") {
      this.fd = target;
      nextTick(() => this.emit("ready"));
    } else if (typeof target === "string") {
      this._open(target);
    } else {
      throw codeError(Error, "ERR_INVALID_ARG_VALUE", "Utf8Stream requires a fd or dest option");
    }
  }
  get writing() { return this._writing; }
  _open(file) {
    this.file = file;
    const doOpen = () => {
      try {
        if (this._mkdir) fs.mkdirSync(file.replace(/\/[^/]*$/, "") || "/", { recursive: true });
        this.fd = fs.openSync(file, this.append ? "a" : "w", this.mode);
        this.emit("ready");
      } catch (err) { this.emit("error", err); }
    };
    if (this.sync) { doOpen(); } else nextTick(doOpen);
  }
  write(data) {
    if (this._destroyed) throw new Error("Utf8Stream destroyed");
    const chunk = this.contentMode === "buffer" ? toBuffer(data) : String(data);
    const len = Buffer.byteLength(chunk);
    if (this.maxLength && this._len + len > this.maxLength) { this.emit("drop", data); return this._len < this.minLength; }
    this._buf.push(chunk);
    this._len += len;
    if (this.fd >= 0 && this._len >= this.minLength) this._flushNow();
    return this._len < 16384;
  }
  _flushNow() {
    if (this.fd < 0 || this._len === 0) return;
    const data = this.contentMode === "buffer" ? Buffer.concat(this._buf.map((c) => toBuffer(c))) : Buffer.from(this._buf.join(""));
    this._buf = [];
    this._len = 0;
    this._writing = true;
    try {
      writeAll(this.fd, data, null);
      if (this.fsync) fs.fsyncSync(this.fd);
    } catch (err) { this._writing = false; this.emit("error", err); return; }
    this._writing = false;
    this.emit("write", data.length);
  }
  flushSync() {
    if (this.fd < 0) throw new Error("Utf8Stream is not ready yet");
    this._flushNow();
  }
  flush(cb) {
    if (this.fd < 0) { const fn = () => this.flush(cb); this.once("ready", fn); return; }
    let err = null;
    try { this._flushNow(); if (this.fsync) fs.fsyncSync(this.fd); } catch (e) { err = e; }
    if (typeof cb === "function") nextTick(cb, err);
  }
  reopen(file) {
    if (this._destroyed) throw new Error("Utf8Stream destroyed");
    this._flushNow();
    if (this.fd > 2) { try { fs.closeSync(this.fd); } catch {} }
    this.fd = -1;
    this._open(file || this.file);
  }
  end() {
    if (this._destroyed) throw new Error("Utf8Stream destroyed");
    if (this._ending) return;
    this._ending = true;
    const finish = () => {
      this._flushNow();
      this.emit("finish");
      this._close();
    };
    if (this.fd < 0) this.once("ready", finish); else nextTick(finish);
  }
  _close() {
    if (this._destroyed) return;
    this._destroyed = true;
    if (this.fd > 2) { try { fs.closeSync(this.fd); } catch {} }
    nextTick(() => this.emit("close"));
  }
  destroy() {
    if (this._destroyed) return;
    this._close();
  }
}

linkLazyClass(Dir, DirForward);
linkLazyClass(Utf8Stream, Utf8StreamForward);

// ---------- fs/promises ----------

function fileClosed(syscall) {
  const err = new Error("file closed");
  err.code = "EBADF";
  err.syscall = syscall;
  return err;
}

class FileHandle extends EventEmitter {
  constructor(fd) {
    super();
    this[kFd] = fd;
  }
  get fd() { return this[kFd]; }
  _fd(syscall) {
    if (this[kFd] === -1) throw fileClosed(syscall);
    return this[kFd];
  }
  async appendFile(data, options) {
    writeAll(this._fd("write"), toBuffer(data, encodingOf(options)), null);
  }
  async chmod(mode) { fs.fchmodSync(this._fd("fchmod"), mode); }
  async chown(uid, gid) { fs.fchownSync(this._fd("fchown"), uid, gid); }
  async close() {
    if (this[kFd] === -1) return;
    const fd = this[kFd];
    this[kFd] = -1;
    fs.closeSync(fd);
    this.emit("close");
  }
  async datasync() { fs.fdatasyncSync(this._fd("fdatasync")); }
  async sync() { fs.fsyncSync(this._fd("fsync")); }
  async read(buffer, offset, length, position) {
    const fd = this._fd("read");
    let opts = {};
    if (buffer != null && !ArrayBuffer.isView(buffer) && typeof buffer === "object") {
      opts = buffer;
      buffer = opts.buffer;
    } else if (offset != null && typeof offset === "object") {
      opts = offset;
      offset = undefined;
    }
    if (buffer == null) buffer = Buffer.alloc(16384);
    if (offset == null) offset = opts.offset != null ? opts.offset : 0;
    if (length == null) length = opts.length != null ? opts.length : buffer.byteLength - offset;
    if (position === undefined) position = opts.position;
    if (position === undefined) position = null;
    if (length === 0) return { bytesRead: 0, buffer };
    const bytesRead = fs.readSync(fd, buffer, offset, length, position);
    return { bytesRead, buffer };
  }
  async readv(buffers, position) {
    const bytesRead = fs.readvSync(this._fd("read"), buffers, position);
    return { bytesRead, buffers };
  }
  async write(buffer, offsetOrOptions, length, position) {
    const fd = this._fd("write");
    if (typeof buffer === "string") {
      const bytesWritten = fs.writeSync(fd, buffer, offsetOrOptions, length);
      return { bytesWritten, buffer };
    }
    let offset = offsetOrOptions;
    if (offset != null && typeof offset === "object") {
      ({ offset = 0, length = buffer.byteLength - offset, position = null } = offset);
    }
    if (offset == null) offset = 0;
    if (length == null) length = buffer.byteLength - offset;
    if (position === undefined) position = null;
    const bytesWritten = fs.writeSync(fd, buffer, offset, length, position);
    return { bytesWritten, buffer };
  }
  async writev(buffers, position) {
    const bytesWritten = fs.writevSync(this._fd("write"), buffers, position);
    return { bytesWritten, buffers };
  }
  async readFile(options) {
    const fd = this._fd("read");
    if (options && options.signal && options.signal.aborted) throw abortError(options.signal);
    const chunks = [];
    for (;;) {
      const buf = Buffer.alloc(65536);
      const n = fs.readSync(fd, buf, 0, buf.length, null);
      if (n === 0) break;
      chunks.push(n === buf.length ? buf : buf.subarray(0, n));
    }
    const all = Buffer.concat(chunks);
    const enc = encodingOf(options);
    return enc ? all.toString(enc) : all;
  }
  async writeFile(data, options) {
    const fd = this._fd("write");
    const enc = encodingOf(options);
    if (typeof data === "string" || ArrayBuffer.isView(data)) { writeAll(fd, toBuffer(data, enc), null); return; }
    if (data && (typeof data[Symbol.asyncIterator] === "function" || typeof data[Symbol.iterator] === "function")) {
      for await (const chunk of data) writeAll(fd, toBuffer(chunk, enc), null);
      return;
    }
    writeAll(fd, toBuffer(data, enc), null);
  }
  async stat(options) { return fs.fstatSync(this._fd("fstat"), options); }
  async truncate(len) { fs.ftruncateSync(this._fd("ftruncate"), len || 0); }
  async utimes(atime, mtime) { fs.futimesSync(this._fd("futime"), atime, mtime); }
  createReadStream(options) { return fs.createReadStream(null, Object.assign({}, options, { fd: this })); }
  createWriteStream(options) { return fs.createWriteStream(null, Object.assign({}, options, { fd: this })); }
  readLines(options) {
    const input = this.createReadStream(options);
    return fs.ReadStream[Symbol.for("noderati.fs.readLines")](input);
  }
  readableWebStream(options) {
    const handle = this;
    const autoClose = options && options.autoClose;
    return new ReadableStream({
      pull(controller) {
        let buf = Buffer.alloc(16384), n;
        try { n = fs.readSync(handle._fd("read"), buf, 0, buf.length, null); } catch (e) { controller.error(e); return; }
        if (n === 0) {
          controller.close();
          if (autoClose) handle.close();
          return;
        }
        controller.enqueue(new Uint8Array(buf.buffer, buf.byteOffset, n));
      },
      cancel() { if (autoClose) return handle.close(); },
    });
  }
  async [Symbol.asyncDispose]() { return this.close(); }
}

export const promises_open = async function open(path, flags, mode) {
  const fd = fs.openSync(path, flags == null ? "r" : flags, mode == null ? 0o666 : mode);
  return new FileHandle(fd);
};
export async function promises_appendFile(path, data, options) {
  if (path instanceof FileHandle) return path.appendFile(data, options);
  appendFileSyncImpl(path, data, options);
}
export async function promises_chmod(path, mode) { fs.chmodSync(path, mode); }
export async function promises_chown(path, uid, gid) { fs.chownSync(path, uid, gid); }
export async function promises_lchown(path, uid, gid) { fs.lchownSync(path, uid, gid); }
export async function promises_lchmod(path, mode) { fs.lchmodSync(path, mode); }
export async function promises_copyFile(src, dest, mode) { fs.copyFileSync(src, dest, mode); }
export async function promises_cp(src, dest, options) { return cpAsync(src, dest, options); }
export async function* promises_glob(pattern, options) {
  for (const m of fs.globSync(pattern, options)) yield m;
}
export async function promises_link(existing, newPath) { fs.linkSync(existing, newPath); }
export async function promises_lutimes(path, atime, mtime) { fs.lutimesSync(path, atime, mtime); }
export async function promises_mkdtemp(prefix, options) { return fs.mkdtempSync(prefix, options); }
export async function promises_mkdtempDisposable(prefix, options) {
  const path = fs.mkdtempSync(prefix, options);
  const remove = async () => { fs.rmSync(path, { recursive: true, force: true }); };
  return { path, remove, [Symbol.asyncDispose]: remove };
}
export async function promises_opendir(path, options) { return opendirImpl(path, options); }
export async function promises_readlink(path, options) { return fs.readlinkSync(path, options); }
export async function promises_rename(from, to) { fs.renameSync(from, to); }
export async function promises_rmdir(path, options) { fs.rmdirSync(path, options); }
export async function promises_statfs(path, options) { return fs.statfsSync(path, options); }
export async function promises_symlink(target, path, type) { fs.symlinkSync(target, path, type); }
export async function promises_truncate(path, len) { fs.truncateSync(path, len); }
export async function promises_utimes(path, atime, mtime) { fs.utimesSync(path, atime, mtime); }
export async function* promises_watch(filename, options) {
  const opts = typeof options === "string" ? { encoding: options } : (options || {});
  const { signal } = opts;
  if (signal && signal.aborted) throw abortError(signal);
  const watcher = fs.watch(filename, { persistent: opts.persistent !== false, recursive: !!opts.recursive, encoding: opts.encoding });
  const queue = [];
  let wake = null, error = null, aborted = false;
  const notify = () => { if (wake) { const w = wake; wake = null; w(); } };
  watcher.on("change", (eventType, name) => { queue.push({ eventType, filename: name }); notify(); });
  watcher.on("error", (e) => { error = e; notify(); });
  const onAbort = () => { aborted = true; notify(); };
  if (signal) signal.addEventListener("abort", onAbort, { once: true });
  try {
    for (;;) {
      if (error) throw error;
      if (aborted) throw abortError(signal);
      if (queue.length) { yield queue.shift(); continue; }
      await new Promise((resolve) => { wake = resolve; });
    }
  } finally {
    watcher.close();
    if (signal) signal.removeEventListener("abort", onAbort);
  }
}
`

func declareFSShims() {
	registerJSShim("noderati-internal:fs-extra", fsExtraShim)
	registerJSShim("noderati-internal:fs-streams", fsStreamsShim)
}

// installFSLazyExtras wires the JS-implemented fs and fs/promises exports
// in. Must run after installFSPromisesAlias, which rebuilds fs's default
// export object.
func installFSLazyExtras(p *driver.Paserati) {
	installLazyJSExports(p, "fs", "noderati-internal:fs-extra",
		[]string{"exists", "appendFile", "opendir", "opendirSync", "cp", "cpSync", "mkdtempDisposableSync", "openAsBlob"},
		nil)
	installLazyJSClasses(p, "fs", "noderati-internal:fs-extra", []string{"Dir", "Utf8Stream"})
	installLazyJSExports(p, "fs", "noderati-internal:fs-streams",
		[]string{"createReadStream", "createWriteStream"}, nil)
	installLazyJSClasses(p, "fs", "noderati-internal:fs-streams", []string{"ReadStream", "WriteStream", "FileReadStream", "FileWriteStream"})
	promiseFns := []string{"open", "appendFile", "chmod", "chown", "lchown", "copyFile", "cp", "glob", "link",
		"lutimes", "mkdtemp", "mkdtempDisposable", "opendir", "readlink", "rename", "rmdir", "statfs",
		"symlink", "truncate", "utimes", "watch"}
	if fsHasLchmod {
		promiseFns = append(promiseFns, "lchmod")
	}
	installLazyJSExportsRenamed(p, "fs/promises", "noderati-internal:fs-extra", "promises_", promiseFns)
	setNativeExport(p, "fs/promises", "constants", fsConstantsObject(p.GetVM()))
}

// installLazyJSExportsRenamed is installLazyJSExports for functions the
// shim exports under prefix+name (fs-extra serves both fs and fs/promises,
// whose same-named functions differ).
func installLazyJSExportsRenamed(p *driver.Paserati, module, spec, prefix string, functions []string) {
	vmInst := p.GetVM()
	lazy := &lazyJSModule{p: p, spec: spec}
	rec, err := p.LoadModule(module, ".")
	if err != nil {
		return
	}
	exports := rec.GetExportValues()
	var def *vm.PlainObject
	if d, ok := exports["default"]; ok && d.Type() == vm.TypeObject {
		def = d.AsPlainObject()
	}
	yes := true
	for _, name := range functions {
		name, inner := name, prefix+name
		exports[name] = vm.NewNativeFunction(0, true, name, func(args []vm.Value) (vm.Value, error) {
			fn, err := lazy.get(inner)
			if err != nil {
				return vm.Undefined, err
			}
			return vmInst.Call(fn, vmInst.GetThis(), args)
		})
		if def == nil {
			continue
		}
		getter := vm.NewNativeFunction(0, false, "get "+name, func(_ []vm.Value) (vm.Value, error) {
			return lazy.get(inner)
		})
		setter := vm.NewNativeFunction(1, false, "set "+name, func(args []vm.Value) (vm.Value, error) {
			def.DeleteOwn(name)
			def.SetOwn(name, argAt(args, 0))
			return vm.Undefined, nil
		})
		def.DefineAccessorProperty(name, getter, true, setter, true, &yes, &yes)
	}
}

// installLazyJSClasses is installLazyJSExports's class form. Each
// named-export forwarder (what `import { WriteStream } from "fs"` binds)
// gets a placeholder prototype object; the shim, once loaded, splices
// those placeholders into the real class's prototype chain
// (linkLazyClass), so `x instanceof WriteStream` holds for real instances
// without loading the shim up front.
func installLazyJSClasses(p *driver.Paserati, module, spec string, classes []string) {
	vmInst := p.GetVM()
	lazy := &lazyJSModule{p: p, spec: spec}
	rec, err := p.LoadModule(module, ".")
	if err != nil {
		return
	}
	exports := rec.GetExportValues()
	var def *vm.PlainObject
	if d, ok := exports["default"]; ok && d.Type() == vm.TypeObject {
		def = d.AsPlainObject()
	}
	yes := true
	for _, name := range classes {
		name := name
		forward := vm.NewConstructorWithProps(0, true, name, func(args []vm.Value) (vm.Value, error) {
			cls, err := lazy.get(name)
			if err != nil {
				return vm.Undefined, err
			}
			return vmInst.Construct(cls, args)
		})
		if props := forward.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
			placeholder := vm.NewObject(vmInst.ObjectPrototype)
			props.Properties.DefineFixedProperty("prototype", placeholder)
		}
		exports[name] = forward
		if def == nil {
			continue
		}
		getter := vm.NewNativeFunction(0, false, "get "+name, func(_ []vm.Value) (vm.Value, error) {
			return lazy.get(name)
		})
		setter := vm.NewNativeFunction(1, false, "set "+name, func(args []vm.Value) (vm.Value, error) {
			def.DeleteOwn(name)
			def.SetOwn(name, argAt(args, 0))
			return vm.Undefined, nil
		})
		def.DefineAccessorProperty(name, getter, true, setter, true, &yes, &yes)
	}
}
