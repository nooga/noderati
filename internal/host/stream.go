package host

const streamShim = `import EventEmitter from "events";

// The legacy base class real Node's own 'stream' module actually
// exports as its default/module value (require('stream') === Stream,
// with Readable/Writable/etc attached to it as static properties - see
// this file's own exports at the bottom) - missing entirely until now,
// since Readable/Writable/Duplex/Transform below were flattened
// straight onto EventEmitter directly (round 90) rather than each
// extending this shared base, which is fine for those classes' own
// behavior but left real code that extends the base *itself* with
// nothing to inherit from. Found via real, unmodified 'send' (a real,
// direct dependency of express's own static-file/sendFile support):
// send/index.js does 'util.inherits(SendStream, Stream)' where
// 'Stream' is the whole 'require(\"stream\")' module value - with that
// value previously a plain \"{ Readable, Writable, ... }\" namespace
// object (no '.prototype' at all), util.inherits' own
// 'Object.setPrototypeOf(SendStream.prototype, Stream.prototype)' call
// threw \"Object prototype may only be an Object or null\" immediately
// at require-time, before a single route could even be registered.
// pipe() is real Node's actual legacy implementation (lib/internal/
// streams/legacy.js), not a stub - SendStream (and any other class
// that extends this base directly rather than Readable) relies on
// inheriting a real .pipe() rather than defining its own, exactly the
// way real Node's own docs describe the classic \"custom Stream\"
// pattern (emit 'data'/'end' manually, get .pipe() for free).
class Stream extends EventEmitter {
  pipe(dest, options) {
    const source = this;
    function ondata(chunk) {
      if (dest.writable && dest.write(chunk) === false && source.pause) {
        source.pause();
      }
    }
    source.on("data", ondata);
    function ondrain() {
      if (source.readable && source.resume) source.resume();
    }
    dest.on("drain", ondrain);
    let didOnEnd = false;
    function onend() {
      if (didOnEnd) return;
      didOnEnd = true;
      if (dest.end) dest.end();
    }
    function onclose() {
      if (didOnEnd) return;
      didOnEnd = true;
      if (typeof dest.destroy === "function") dest.destroy();
    }
    if (!(options && options.end === false)) {
      source.on("end", onend);
      source.on("close", onclose);
    }
    function onerror(er) {
      cleanup();
      if (source.listenerCount("error") === 0) throw er;
    }
    source.on("error", onerror);
    function cleanup() {
      source.removeListener("data", ondata);
      dest.removeListener("drain", ondrain);
      source.removeListener("end", onend);
      source.removeListener("close", onclose);
      source.removeListener("error", onerror);
      source.removeListener("end", cleanup);
      source.removeListener("close", cleanup);
    }
    source.on("end", cleanup);
    source.on("close", cleanup);
    dest.emit("pipe", source);
    return dest;
  }
}

// This used to hand-roll its own, separate class EventEmitter { ... }
// here - a verbatim copy-paste of events.go's real one (down to the
// same method set), which is exactly the "same thing implemented
// twice" pattern docs/real-node-plan.md's ledger flagged (group A:
// "stream.go also hand-rolls its own EventEmitter instead of reusing
// events.go's - pick one"). Two copies didn't just duplicate code, they
// silently drifted: events.go's own emit() was fixed to invoke every
// listener with the emitter bound as its 'this' (real Node's own
// behavior - see emitter.go's emitOnObject and the c8007e3 commit
// fixing this for the Go-native EventEmitter object), but that fix
// only ever touched events.go's copy - this file's own duplicate
// emit() still called listeners with 'this' as undefined, a real,
// silent behavioral divergence between "the EventEmitter you get from
// require('events')" and "the EventEmitter every stream here actually
// inherits from". Importing the real one instead - real Node's own
// stream module itself does exactly this (Readable/Writable are built
// on top of require('events')'s EventEmitter) - deletes the duplicate
// and its drift risk in one move, and picks up that 'this'-binding fix
// for free rather than needing its own separate copy of it.

// Readable follows real Node's own state machine (lib/internal/streams/
// readable.js): paused/flowing modes, _read() pulled against
// highWaterMark, objectMode, read(n), 'readable', setEncoding,
// destroy()/_destroy() with next-tick 'error'/'close', autoDestroy after
// 'end', and an async iterator built on read(). The previous push-only
// version never called _read() at all, so any pull-driven Readable
// (readdirp - chokidar's directory scanner - is one) never produced a
// single chunk.
//
// Consumers this has to keep working: undici's fetch() body
// ("new Readable({ read: resume })", push()ed from the dispatcher,
// consumed via [Symbol.asyncIterator]) and undici's own onError, which
// calls destroy(err) from inside an 'error' listener (so destroy() must
// stay re-entrancy safe).
function defaultHighWaterMark(objectMode) {
  return objectMode ? 16 : 65536;
}

function readableHighWaterMark(opts, objectMode) {
  const hwm = opts == null ? undefined : (opts.readableHighWaterMark ?? opts.highWaterMark);
  if (hwm == null) return defaultHighWaterMark(objectMode);
  if (!Number.isInteger(hwm) || hwm < 0) {
    const err = new RangeError('The value of "options.highWaterMark" is invalid. Received ' + hwm);
    err.code = "ERR_INVALID_ARG_VALUE";
    throw err;
  }
  return hwm;
}

function chunkLength(state, chunk) {
  return state.objectMode ? 1 : chunk.length;
}

class ReadableState {
  constructor(opts, stream) {
    opts = opts || {};
    this.objectMode = !!(opts.objectMode || opts.readableObjectMode);
    this.highWaterMark = readableHighWaterMark(opts, this.objectMode);
    this.buffer = [];
    this.length = 0;
    this.pipes = [];
    this.flowing = null;
    this.ended = false;
    this.endEmitted = false;
    this.reading = false;
    this.constructed = true;
    this.sync = true;
    this.needReadable = false;
    this.emittedReadable = false;
    this.readableListening = false;
    this.resumeScheduled = false;
    this.errorEmitted = false;
    this.emitClose = opts.emitClose !== false;
    this.autoDestroy = opts.autoDestroy !== false;
    this.destroyed = false;
    this.errored = null;
    this.closed = false;
    this.closeEmitted = false;
    this.defaultEncoding = opts.defaultEncoding || "utf8";
    this.readingMore = false;
    this.dataEmitted = false;
    this.encoding = null;
    this.decoder = null;
    if (opts.encoding) {
      this.encoding = opts.encoding;
    }
  }
}

function decodeChunk(state, chunk) {
  if (state.encoding === null) return chunk;
  const b = typeof chunk === "string" ? Buffer.from(chunk) : chunk;
  if (!state.decoder) state.decoder = { pending: Buffer.alloc(0) };
  let all = state.decoder.pending.length ? Buffer.concat([state.decoder.pending, b]) : b;
  let cut = all.length;
  if (state.encoding === "utf8" || state.encoding === "utf-8") {
    // Hold back an incomplete trailing UTF-8 sequence for the next chunk.
    let i = all.length - 1, back = 0;
    while (i >= 0 && back < 4 && (all[i] & 0xc0) === 0x80) { i--; back++; }
    if (i >= 0) {
      const lead = all[i];
      const need = lead >= 0xf0 ? 4 : lead >= 0xe0 ? 3 : lead >= 0xc0 ? 2 : 1;
      if (need > back + 1) cut = i;
    }
  }
  state.decoder.pending = all.subarray(cut);
  return all.subarray(0, cut).toString(state.encoding);
}

class Readable extends EventEmitter {
  constructor(opts) {
    super();
    this._readableState = new ReadableState(opts, this);
    if (opts) {
      if (typeof opts.read === "function") this._read = opts.read;
      if (typeof opts.destroy === "function") this._destroy = opts.destroy;
      if (opts.signal) {
        const onAbort = () => this.destroy(Object.assign(new Error("The operation was aborted"), { name: "AbortError", code: "ABORT_ERR" }));
        if (opts.signal.aborted) onAbort();
        else opts.signal.addEventListener("abort", onAbort, { once: true });
      }
    }
  }

  get readable() {
    const s = this._readableState;
    return !!s && !s.destroyed && !s.errorEmitted && !s.endEmitted && this._readableOverride !== false;
  }
  set readable(v) {
    this._readableOverride = !!v;
  }
  get readableFlowing() { return this._readableState.flowing; }
  set readableFlowing(v) { this._readableState.flowing = v; }
  get readableEnded() { return this._readableState.endEmitted; }
  get readableLength() { return this._readableState.length; }
  get readableHighWaterMark() { return this._readableState.highWaterMark; }
  get readableObjectMode() { return this._readableState.objectMode; }
  get readableEncoding() { return this._readableState.encoding; }
  get readableDidRead() { return this._readableState.dataEmitted; }
  get destroyed() { return this._readableState.destroyed; }
  set destroyed(v) { this._readableState.destroyed = v; }
  get closed() { return this._readableState.closed; }
  get errored() { return this._readableState.errored; }
  // Kept for noderati's own isDisturbed()/isErrored() helpers below.
  get _disturbed() { return this._readableState.dataEmitted; }
  get _error() { return this._readableState.errored; }

  _read(_n) {
    const err = new Error("The _read() method is not implemented");
    err.code = "ERR_METHOD_NOT_IMPLEMENTED";
    throw err;
  }

  push(chunk, encoding) {
    return readableAddChunk(this, chunk, encoding, false);
  }
  unshift(chunk, encoding) {
    return readableAddChunk(this, chunk, encoding, true);
  }

  isPaused() {
    const s = this._readableState;
    return s.flowing === false;
  }

  setEncoding(enc) {
    const s = this._readableState;
    s.encoding = (enc || "utf8").toLowerCase();
    s.decoder = null;
    if (s.buffer.length) {
      const joined = s.buffer.map((c) => typeof c === "string" ? c : decodeChunk(s, c)).join("");
      s.buffer = joined.length ? [joined] : [];
      s.length = joined.length;
    }
    return this;
  }

  read(n) {
    const state = this._readableState;
    if (n === undefined) n = NaN;
    else if (!Number.isInteger(n)) n = Number.parseInt(n, 10);
    const nOrig = n;
    if (n > state.highWaterMark) state.highWaterMark = computeNewHighWaterMark(n);
    if (n !== 0) state.emittedReadable = false;
    if (n === 0 && state.needReadable &&
        ((state.highWaterMark !== 0 ? state.length >= state.highWaterMark : state.length > 0) || state.ended)) {
      if (state.length === 0 && state.ended) endReadable(this);
      else emitReadable(this);
      return null;
    }
    n = howMuchToRead(n, state);
    if (n === 0 && state.ended) {
      if (state.length === 0) endReadable(this);
      return null;
    }
    let doRead = state.needReadable;
    if (state.length === 0 || state.length - n < state.highWaterMark) doRead = true;
    if (state.ended || state.reading || state.destroyed || state.errored || !state.constructed) doRead = false;
    else if (doRead) {
      state.reading = true;
      state.sync = true;
      if (state.length === 0) state.needReadable = true;
      try {
        this._read(state.highWaterMark);
      } catch (err) {
        this.destroy(err);
      }
      state.sync = false;
      if (!state.reading) n = howMuchToRead(nOrig, state);
    }
    let ret = n > 0 ? fromList(n, state) : null;
    if (ret === null) {
      state.needReadable = state.length <= state.highWaterMark;
      n = 0;
    } else {
      state.length -= state.objectMode ? 1 : n;
    }
    if (state.length === 0) {
      if (!state.ended) state.needReadable = true;
      if (nOrig !== n && state.ended) endReadable(this);
    }
    if (ret !== null && !state.errorEmitted && !state.closeEmitted) {
      state.dataEmitted = true;
      this.emit("data", ret);
    }
    return ret;
  }

  on(ev, fn) {
    const res = super.on(ev, fn);
    const state = this._readableState;
    if (ev === "data") {
      state.readableListening = this.listenerCount("readable") > 0;
      if (state.flowing !== false) this.resume();
    } else if (ev === "readable") {
      if (!state.endEmitted && !state.readableListening) {
        state.readableListening = state.needReadable = true;
        state.flowing = false;
        state.emittedReadable = false;
        if (state.length) emitReadable(this);
        else if (!state.reading) process.nextTick(() => this.read(0));
      }
    }
    return res;
  }
  removeListener(ev, fn) {
    const res = super.removeListener(ev, fn);
    if (ev === "readable") process.nextTick(() => updateReadableListening(this));
    return res;
  }
  removeAllListeners(ev) {
    const res = super.removeAllListeners(ev);
    if (ev === "readable" || ev === undefined) process.nextTick(() => updateReadableListening(this));
    return res;
  }

  resume() {
    const state = this._readableState;
    if (!state.flowing) {
      state.flowing = !state.readableListening;
      if (!state.resumeScheduled) {
        state.resumeScheduled = true;
        process.nextTick(() => {
          if (!state.reading) this.read(0);
          state.resumeScheduled = false;
          this.emit("resume");
          flow(this);
          if (state.flowing && !state.reading) this.read(0);
        });
      }
    }
    return this;
  }

  pause() {
    const state = this._readableState;
    if (state.flowing !== false) {
      state.flowing = false;
      this.emit("pause");
    }
    return this;
  }

  pipe(dest, pipeOpts) {
    const src = this;
    const state = this._readableState;
    state.pipes.push(dest);
    const doEnd = (!pipeOpts || pipeOpts.end !== false) &&
      !(typeof process !== "undefined" && (dest === process.stdout || dest === process.stderr));
    const onend = () => { if (typeof dest.end === "function") dest.end(); };
    const endFn = doEnd ? onend : unpipe;
    if (state.endEmitted) process.nextTick(endFn);
    else src.once("end", endFn);
    let ondrain = null;
    function ondata(chunk) {
      const ret = dest.write(chunk);
      if (ret === false) {
        if (!ondrain) {
          ondrain = () => { if (src.isPaused()) src.resume(); };
          if (typeof dest.on === "function") dest.on("drain", ondrain);
        }
        src.pause();
      }
    }
    src.on("data", ondata);
    function onerror(er) {
      unpipe();
      if (typeof dest.removeListener === "function") dest.removeListener("error", onerror);
      if (typeof dest.listenerCount === "function" && dest.listenerCount("error") === 0) {
        if (typeof dest.destroy === "function") dest.destroy(er);
        else throw er;
      }
    }
    if (typeof dest.prependListener === "function") dest.prependListener("error", onerror);
    else if (typeof dest.on === "function") dest.on("error", onerror);
    function onclose() { unpipe(); }
    if (typeof dest.once === "function") {
      dest.once("close", onclose);
      dest.once("finish", onclose);
    }
    function unpipe() {
      src.unpipe(dest);
    }
    dest._noderatiUnpipeCleanup = () => {
      src.removeListener("data", ondata);
      src.removeListener("end", endFn);
      if (typeof dest.removeListener === "function") {
        if (ondrain) dest.removeListener("drain", ondrain);
        dest.removeListener("error", onerror);
        dest.removeListener("close", onclose);
        dest.removeListener("finish", onclose);
      }
    };
    if (typeof dest.emit === "function") dest.emit("pipe", src);
    if (!state.flowing) src.resume();
    return dest;
  }

  unpipe(dest) {
    const state = this._readableState;
    const targets = dest === undefined ? state.pipes.slice() : [dest];
    for (const d of targets) {
      const i = state.pipes.indexOf(d);
      if (i === -1) continue;
      state.pipes.splice(i, 1);
      if (typeof d._noderatiUnpipeCleanup === "function") d._noderatiUnpipeCleanup();
      if (typeof d.emit === "function") d.emit("unpipe", this);
    }
    if (state.pipes.length === 0) this.pause();
    return this;
  }

  destroy(err, cb) {
    const state = this._readableState;
    if (state.destroyed) {
      if (typeof cb === "function") cb(err);
      return this;
    }
    state.destroyed = true;
    if (err && !state.errored) state.errored = err;
    const done = (er) => {
      if (er && !state.errored) state.errored = er;
      state.closed = true;
      if (typeof cb === "function") cb(er);
      process.nextTick(() => {
        if (er && !state.errorEmitted) {
          state.errorEmitted = true;
          this.emit("error", er);
        }
        if (state.emitClose && !state.closeEmitted) {
          state.closeEmitted = true;
          this.emit("close");
        }
      });
    };
    try {
      this._destroy(err || null, done);
    } catch (e) {
      done(e);
    }
    return this;
  }
  _destroy(err, cb) {
    cb(err);
  }

  [Symbol.asyncIterator]() {
    return createAsyncIterator(this);
  }
  iterator() {
    return createAsyncIterator(this);
  }

  static from(iterable, opts) {
    return readableFrom(iterable, opts);
  }
}

Readable.prototype.addListener = Readable.prototype.on;
Readable.prototype.off = Readable.prototype.removeListener;

function readableAddChunk(stream, chunk, encoding, addToFront) {
  const state = stream._readableState;
  if (chunk === null) {
    state.reading = false;
    onEofChunk(stream, state);
    return false;
  }
  if (!state.objectMode) {
    if (typeof chunk === "string") {
      encoding = encoding || state.defaultEncoding;
      if (state.encoding !== null && state.encoding === encoding) {
        // already in the requested decoded form
      } else {
        chunk = Buffer.from(chunk, encoding);
      }
    } else if (chunk instanceof Uint8Array && !Buffer.isBuffer(chunk)) {
      chunk = Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
    } else if (chunk !== undefined && !(chunk instanceof Uint8Array)) {
      const err = new TypeError('The "chunk" argument must be of type string or an instance of Buffer or Uint8Array');
      err.code = "ERR_INVALID_ARG_TYPE";
      stream.destroy(err);
      return false;
    }
  }
  if (chunk === undefined || (!state.objectMode && chunk.length === 0)) {
    state.reading = false;
    maybeReadMore(stream, state);
    return canPushMore(state);
  }
  if (addToFront) {
    if (state.endEmitted) {
      const err = new Error("stream.unshift() after end event");
      err.code = "ERR_STREAM_UNSHIFT_AFTER_END_EVENT";
      stream.destroy(err);
      return false;
    }
    if (state.destroyed || state.errored) return false;
    addChunk(stream, state, chunk, true);
    return canPushMore(state);
  }
  if (state.ended) {
    const err = new Error("stream.push() after EOF");
    err.code = "ERR_STREAM_PUSH_AFTER_EOF";
    stream.destroy(err);
    return false;
  }
  if (state.destroyed || state.errored) return false;
  state.reading = false;
  if (state.encoding !== null && !state.objectMode && typeof chunk !== "string") {
    chunk = decodeChunk(state, chunk);
    if (chunk.length === 0) {
      maybeReadMore(stream, state);
      return canPushMore(state);
    }
  }
  addChunk(stream, state, chunk, false);
  return canPushMore(state);
}

function canPushMore(state) {
  return !state.ended && (state.length < state.highWaterMark || state.length === 0);
}

function addChunk(stream, state, chunk, addToFront) {
  if (state.flowing && state.length === 0 && !state.sync && stream.listenerCount("data") > 0) {
    state.dataEmitted = true;
    stream.emit("data", chunk);
  } else {
    state.length += chunkLength(state, chunk);
    if (addToFront) state.buffer.unshift(chunk);
    else state.buffer.push(chunk);
    if (state.needReadable) emitReadable(stream);
  }
  maybeReadMore(stream, state);
}

function onEofChunk(stream, state) {
  if (state.ended) return;
  if (state.decoder && state.decoder.pending.length) {
    const rest = state.decoder.pending.toString(state.encoding);
    state.decoder.pending = Buffer.alloc(0);
    if (rest.length) {
      state.buffer.push(rest);
      state.length += rest.length;
    }
  }
  state.ended = true;
  if (state.sync) {
    emitReadable(stream);
  } else {
    state.needReadable = false;
    state.emittedReadable = true;
    emitReadable_(stream);
  }
}

function emitReadable(stream) {
  const state = stream._readableState;
  state.needReadable = false;
  if (!state.emittedReadable) {
    state.emittedReadable = true;
    process.nextTick(() => emitReadable_(stream));
  }
}

function emitReadable_(stream) {
  const state = stream._readableState;
  if (!state.destroyed && !state.errored && (state.length || state.ended)) {
    stream.emit("readable");
    state.emittedReadable = false;
  }
  state.needReadable = !state.flowing && !state.ended && state.length <= state.highWaterMark;
  flow(stream);
}

function maybeReadMore(stream, state) {
  if (!state.readingMore && state.constructed) {
    state.readingMore = true;
    process.nextTick(() => {
      while (!state.reading && !state.ended &&
             (state.length < state.highWaterMark || (state.flowing && state.length === 0))) {
        const len = state.length;
        stream.read(0);
        if (len === state.length) break;
      }
      state.readingMore = false;
    });
  }
}

function updateReadableListening(stream) {
  const state = stream._readableState;
  state.readableListening = stream.listenerCount("readable") > 0;
  if (state.resumeScheduled && state.flowing === false) state.flowing = true;
  else if (stream.listenerCount("data") > 0) stream.resume();
  else if (!state.readableListening) state.flowing = null;
}

function flow(stream) {
  const state = stream._readableState;
  while (state.flowing && stream.read() !== null);
}

function computeNewHighWaterMark(n) {
  if (n > 0x40000000) return 0x40000000;
  n--;
  n |= n >>> 1; n |= n >>> 2; n |= n >>> 4; n |= n >>> 8; n |= n >>> 16;
  return n + 1;
}

function howMuchToRead(n, state) {
  if (n <= 0 || (state.length === 0 && state.ended)) return 0;
  if (state.objectMode) return 1;
  if (Number.isNaN(n)) {
    if (state.flowing && state.length) return chunkLength(state, state.buffer[0]);
    return state.length;
  }
  if (n <= state.length) return n;
  return state.ended ? state.length : 0;
}

function fromList(n, state) {
  if (state.length === 0) return null;
  if (state.objectMode) return state.buffer.shift();
  if (!n || n >= state.length) {
    let ret;
    if (state.buffer.length === 1) ret = state.buffer[0];
    else if (typeof state.buffer[0] === "string") ret = state.buffer.join("");
    else ret = Buffer.concat(state.buffer, state.length);
    state.buffer = [];
    return ret;
  }
  const first = state.buffer[0];
  if (n < first.length) {
    state.buffer[0] = first.slice(n);
    return first.slice(0, n);
  }
  if (n === first.length) return state.buffer.shift();
  const isString = typeof first === "string";
  const parts = [];
  let need = n;
  while (need > 0) {
    const c = state.buffer[0];
    if (c.length <= need) {
      parts.push(c);
      need -= c.length;
      state.buffer.shift();
    } else {
      parts.push(c.slice(0, need));
      state.buffer[0] = c.slice(need);
      need = 0;
    }
  }
  return isString ? parts.join("") : Buffer.concat(parts, n);
}

function endReadable(stream) {
  const state = stream._readableState;
  if (!state.endEmitted) {
    state.ended = true;
    process.nextTick(() => {
      if (!state.errorEmitted && !state.closeEmitted && !state.endEmitted && state.length === 0) {
        state.endEmitted = true;
        stream.emit("end");
        const ws = stream._writableState;
        if (state.autoDestroy && (!ws || (ws.autoDestroy !== false && (ws.finished || ws.writable === false)))) {
          stream.destroy();
        }
      }
    });
  }
}

function createAsyncIterator(stream) {
  const state = stream._readableState;
  let wake = null;
  let error = null;
  let finished = false;
  const notify = () => { if (wake) { const w = wake; wake = null; w(); } };
  stream.on("readable", notify);
  const onEnd = () => { finished = true; notify(); };
  const onError = (err) => { error = err; finished = true; notify(); };
  stream.on("end", onEnd);
  stream.on("error", onError);
  stream.on("close", onEnd);
  const cleanup = () => {
    stream.removeListener("readable", notify);
    stream.removeListener("end", onEnd);
    stream.removeListener("error", onError);
    stream.removeListener("close", onEnd);
  };
  const iter = {
    async next() {
      for (;;) {
        if (error) { cleanup(); throw error; }
        const chunk = state.destroyed ? null : stream.read();
        if (chunk !== null) return { value: chunk, done: false };
        if (state.errored) { cleanup(); throw state.errored; }
        if (finished || state.endEmitted || (state.destroyed && state.length === 0)) {
          cleanup();
          return { value: undefined, done: true };
        }
        await new Promise((r) => { wake = r; });
      }
    },
    async return(value) {
      cleanup();
      if (!state.endEmitted) stream.destroy();
      return { value, done: true };
    },
    async throw(err) {
      cleanup();
      stream.destroy(err);
      throw err;
    },
    [Symbol.asyncIterator]() { return this; },
  };
  return iter;
}

function readableFrom(iterable, opts) {
  if (typeof iterable === "string" || Buffer.isBuffer(iterable)) {
    return new Readable({ objectMode: true, ...opts, read() { this.push(iterable); this.push(null); } });
  }
  let iterator;
  let isAsync;
  if (iterable && typeof iterable[Symbol.asyncIterator] === "function") {
    isAsync = true;
    iterator = iterable[Symbol.asyncIterator]();
  } else if (iterable && typeof iterable[Symbol.iterator] === "function") {
    isAsync = false;
    iterator = iterable[Symbol.iterator]();
  } else {
    const err = new TypeError('The "iterable" argument must be an instance of Iterable');
    err.code = "ERR_INVALID_ARG_TYPE";
    throw err;
  }
  let reading = false;
  const readable = new Readable({
    objectMode: true,
    highWaterMark: 1,
    ...opts,
    read() {
      if (reading) return;
      reading = true;
      (async () => {
        try {
          for (;;) {
            const { value, done } = isAsync ? await iterator.next() : iterator.next();
            if (done) { readable.push(null); break; }
            const chunk = value && typeof value.then === "function" ? await value : value;
            if (chunk === null) {
              const err = new TypeError("May not write null values to stream");
              err.code = "ERR_STREAM_NULL_VALUES";
              throw err;
            }
            if (!readable.push(chunk)) break;
          }
        } catch (err) {
          readable.destroy(err);
        } finally {
          reading = false;
        }
      })();
    },
    destroy(err, cb) {
      const ret = typeof iterator.return === "function" ? iterator.return() : undefined;
      Promise.resolve(ret).then(() => cb(err), (e) => cb(e || err));
    },
  });
  return readable;
}

// write()/end() used to just emit("data")/emit("end") directly instead
// of calling a subclass's own _write()/_final() overrides - completely
// wrong for a base Writable (real Node's Writable never emits "data" at
// all; that's a Readable-only event) and, more seriously, a real
// correctness bug found the hard way chasing the real Bedrock
// investigation (docs/real-node-plan.md, round 101): real
// @smithy/node-http-handler's own streamCollector does exactly
// class Collector extends Writable, overriding _write(chunk, encoding,
// callback) to push each chunk into its own buffer, then does
// stream.pipe(collector) to read a real HTTP response body - since
// write() never called this subclass's real _write() override, every
// piped chunk silently vanished (emitted as an unheard "data" event on
// the destination itself, which nothing subscribes to) and the
// collected response body was always empty. Matches Duplex's own
// already-correct write()/end() (this same file, above) - a plain
// Writable is exactly that same writable half, just without a readable
// side to go with it.
// notImplementedWriteError matches real Node's own exact behavior for a
// Writable/Duplex whose _write() is never overridden - confirmed
// directly (not assumed): real Node throws a real
// ERR_METHOD_NOT_IMPLEMENTED error the instant something is actually
// written, rather than silently succeeding. Every real subclass in this
// codebase's own dependency tree does override _write, so this is
// purely about not silently misrepresenting "nothing happened" as
// success on the one hypothetical caller that doesn't.
function nodeError(Base, code, message) {
  const err = new Base(message);
  err.code = code;
  return err;
}
function streamReceived(v) {
  if (v == null) return " Received " + v;
  if (typeof v === "function") return " Received function " + (v.name || "<anonymous>");
  if (typeof v === "object") return " Received an instance of " + ((v.constructor && v.constructor.name) || "Object");
  let s = typeof v === "string" ? "'" + v + "'" : String(v);
  if (typeof v === "string" && v.length > 28) s = "'" + v.slice(0, 25) + "'...";
  return " Received type " + typeof v + " (" + s + ")";
}
function notImplementedWriteError() {
  return nodeError(Error, "ERR_METHOD_NOT_IMPLEMENTED", "The _write() method is not implemented");
}
const kOnFinished = Symbol("kOnFinished");
function nop() {}

function writableHighWaterMark(opts, objectMode) {
  const hwm = opts == null ? undefined : (opts.writableHighWaterMark ?? opts.highWaterMark);
  if (hwm == null) return defaultHighWaterMark(objectMode);
  if (!Number.isInteger(hwm) || hwm < 0) {
    throw nodeError(RangeError, "ERR_INVALID_ARG_VALUE", 'The property \'options.highWaterMark\' is invalid. Received ' + hwm);
  }
  return hwm;
}

class WritableState {
  constructor(opts, stream) {
    opts = opts || {};
    this.objectMode = !!(opts.objectMode || opts.writableObjectMode);
    this.highWaterMark = writableHighWaterMark(opts, this.objectMode);
    this.finalCalled = false;
    this.needDrain = false;
    this.ending = false;
    this.ended = false;
    this.finished = false;
    this.destroyed = false;
    this.decodeStrings = opts.decodeStrings !== false;
    this.defaultEncoding = opts.defaultEncoding || "utf8";
    this.length = 0;
    this.writing = false;
    this.corked = 0;
    this.sync = true;
    this.bufferProcessing = false;
    this.onwrite = (er) => onwrite(stream, er);
    this.writecb = null;
    this.writelen = 0;
    this.afterWriteTickInfo = null;
    this.buffered = [];
    this.pendingcb = 0;
    this.constructed = true;
    this.prefinished = false;
    this.errorEmitted = false;
    this.emitClose = opts.emitClose !== false;
    this.autoDestroy = opts.autoDestroy !== false;
    this.errored = null;
    this.closed = false;
    this.closeEmitted = false;
    this[kOnFinished] = [];
  }
}

class Writable extends EventEmitter {
  constructor(opts) {
    super();
    opts = opts || {};
    this._writableState = new WritableState(opts, this);
    if (typeof opts.write === "function") this._write = opts.write;
    if (typeof opts.writev === "function") this._writev = opts.writev;
    if (typeof opts.destroy === "function") this._destroy = opts.destroy;
    if (typeof opts.final === "function") this._final = opts.final;
    if (typeof opts.construct === "function") this._construct = opts.construct;
    if (typeof this._construct === "function") {
      const state = this._writableState;
      state.constructed = false;
      process.nextTick(() => {
        let called = false;
        this._construct((err) => {
          if (called) return errorOrDestroyW(this, nodeError(Error, "ERR_MULTIPLE_CALLBACK", "Callback called multiple times"));
          called = true;
          state.constructed = true;
          this.emit("__noderatiConstructed");
          if (err) errorOrDestroyW(this, err, true);
          else if (!state.destroyed) {
            clearBuffer(this, state);
            finishMaybe(this, state);
          }
        });
      });
    }
  }
  get writable() {
    const w = this._writableState;
    return !!w && w.writable !== false && !w.destroyed && !w.errored && !w.ending && !w.ended;
  }
  set writable(val) {
    if (this._writableState) this._writableState.writable = !!val;
  }
  get writableFinished() { return this._writableState ? this._writableState.finished : false; }
  get writableObjectMode() { return this._writableState ? this._writableState.objectMode : false; }
  get writableBuffer() { return this._writableState && this._writableState.buffered.map((b) => b.chunk); }
  get writableEnded() { return this._writableState ? this._writableState.ending : false; }
  get writableNeedDrain() {
    const w = this._writableState;
    return w ? !w.destroyed && !w.ending && w.needDrain : false;
  }
  get writableHighWaterMark() { return this._writableState && this._writableState.highWaterMark; }
  get writableCorked() { return this._writableState ? this._writableState.corked : 0; }
  get writableLength() { return this._writableState && this._writableState.length; }
  get errored() { return this._writableState ? this._writableState.errored : null; }
  get closed() { return this._writableState ? this._writableState.closed : false; }
  get destroyed() { return this._writableState ? this._writableState.destroyed : false; }
  set destroyed(v) { if (this._writableState) this._writableState.destroyed = v; }
  get writableAborted() {
    const w = this._writableState;
    return !!(w.writable !== false && (w.destroyed || w.errored) && !w.finished);
  }

  _write(chunk, encoding, cb) {
    if (this._writev) this._writev([{ chunk, encoding }], cb);
    else throw notImplementedWriteError();
  }
  write(chunk, encoding, cb) {
    return writeInternal(this, chunk, encoding, cb) === true;
  }
  cork() {
    this._writableState.corked++;
  }
  uncork() {
    const state = this._writableState;
    if (state.corked) {
      state.corked--;
      if (!state.writing) clearBuffer(this, state);
    }
  }
  setDefaultEncoding(encoding) {
    if (typeof encoding === "string") encoding = encoding.toLowerCase();
    if (!Buffer.isEncoding(encoding)) throw nodeError(TypeError, "ERR_UNKNOWN_ENCODING", "Unknown encoding: " + encoding);
    this._writableState.defaultEncoding = encoding;
    return this;
  }
  end(chunk, encoding, cb) {
    const state = this._writableState;
    if (typeof chunk === "function") {
      cb = chunk;
      chunk = null;
      encoding = null;
    } else if (typeof encoding === "function") {
      cb = encoding;
      encoding = null;
    }
    let err;
    if (chunk !== null && chunk !== undefined) {
      const ret = writeInternal(this, chunk, encoding);
      if (ret instanceof Error) err = ret;
    }
    if (state.corked) {
      state.corked = 1;
      this.uncork();
    }
    if (err) {
      // the write already failed
    } else if (!state.errored && !state.ending) {
      state.ending = true;
      finishMaybe(this, state, true);
      state.ended = true;
    } else if (state.finished) {
      err = nodeError(Error, "ERR_STREAM_ALREADY_FINISHED", "Cannot call end after a stream was finished");
    } else if (state.destroyed) {
      err = nodeError(Error, "ERR_STREAM_DESTROYED", "Cannot call end after a stream was destroyed");
    }
    if (typeof cb === "function") {
      if (err) process.nextTick(cb, err);
      else if (state.finished) process.nextTick(cb, null);
      else state[kOnFinished].push(cb);
    }
    return this;
  }
  destroy(err, cb) {
    const state = this._writableState;
    if (!state.destroyed && (state.buffered.length || state[kOnFinished].length)) process.nextTick(errorBuffer, state);
    if (state.destroyed) {
      if (typeof cb === "function") cb();
      return this;
    }
    if (err && !state.errored) state.errored = err;
    state.destroyed = true;
    const run = () => {
      let called = false;
      const onDestroy = (e) => {
        if (called) return;
        called = true;
        if (e && !state.errored) state.errored = e;
        state.closed = true;
        if (typeof cb === "function") cb(e);
        process.nextTick(() => {
          if (e && !state.errorEmitted) {
            state.errorEmitted = true;
            this.emit("error", e);
          }
          state.closeEmitted = true;
          if (state.emitClose) this.emit("close");
        });
      };
      try {
        this._destroy(err || null, onDestroy);
      } catch (e) {
        onDestroy(e);
      }
    };
    if (!state.constructed) this.once("__noderatiConstructed", run);
    else run();
    return this;
  }
  _destroy(err, cb) {
    cb(err);
  }
  [Symbol.asyncDispose]() {
    return new Promise((resolve, reject) => {
      this.destroy(null, (err) => (err ? reject(err) : resolve()));
    });
  }
}

function errorOrDestroyW(stream, err, sync) {
  const w = stream._writableState;
  if (w.destroyed) return stream;
  if (w.autoDestroy) {
    stream.destroy(err);
  } else if (err) {
    if (!w.errored) w.errored = err;
    if (sync) process.nextTick(() => emitErrorW(stream, err));
    else emitErrorW(stream, err);
  }
  return stream;
}

function emitErrorW(stream, err) {
  const w = stream._writableState;
  if (w.errorEmitted) return;
  w.errorEmitted = true;
  stream.emit("error", err);
}

function writeInternal(stream, chunk, encoding, cb) {
  const state = stream._writableState;
  if (typeof encoding === "function") {
    cb = encoding;
    encoding = state.objectMode ? undefined : state.defaultEncoding;
  } else {
    if (!encoding) encoding = state.objectMode ? undefined : state.defaultEncoding;
    else if (encoding !== "buffer" && !Buffer.isEncoding(encoding)) throw nodeError(TypeError, "ERR_UNKNOWN_ENCODING", "Unknown encoding: " + encoding);
    if (typeof cb !== "function") cb = nop;
  }
  if (chunk === null) {
    throw nodeError(TypeError, "ERR_STREAM_NULL_VALUES", "May not write null values to stream");
  } else if (!state.objectMode) {
    if (typeof chunk === "string") {
      if (state.decodeStrings !== false) {
        chunk = Buffer.from(chunk, encoding);
        encoding = "buffer";
      }
    } else if (chunk instanceof Buffer) {
      encoding = "buffer";
    } else if (ArrayBuffer.isView(chunk)) {
      chunk = Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
      encoding = "buffer";
    } else {
      throw nodeError(TypeError, "ERR_INVALID_ARG_TYPE",
        'The "chunk" argument must be of type string or an instance of Buffer, TypedArray, or DataView.' + streamReceived(chunk));
    }
  }
  let err;
  if (state.ending) err = nodeError(Error, "ERR_STREAM_WRITE_AFTER_END", "write after end");
  else if (state.destroyed) err = nodeError(Error, "ERR_STREAM_DESTROYED", "Cannot call write after a stream was destroyed");
  if (err) {
    process.nextTick(cb, err);
    errorOrDestroyW(stream, err, true);
    return err;
  }
  state.pendingcb++;
  return writeOrBuffer(stream, state, chunk, encoding, cb);
}

function writeOrBuffer(stream, state, chunk, encoding, callback) {
  const len = state.objectMode ? 1 : chunk.length;
  state.length += len;
  const ret = state.length < state.highWaterMark;
  if (!ret) state.needDrain = true;
  if (state.writing || state.corked || state.errored || !state.constructed) {
    state.buffered.push({ chunk, encoding, callback });
  } else {
    state.writelen = len;
    state.writecb = callback;
    state.writing = true;
    state.sync = true;
    stream._write(chunk, encoding, state.onwrite);
    state.sync = false;
  }
  return ret && !state.errored && !state.destroyed;
}

function doWrite(stream, state, writev, len, chunk, encoding, cb) {
  state.writelen = len;
  state.writecb = cb;
  state.writing = true;
  state.sync = true;
  if (state.destroyed) state.onwrite(nodeError(Error, "ERR_STREAM_DESTROYED", "Cannot call write after a stream was destroyed"));
  else if (writev) stream._writev(chunk, state.onwrite);
  else stream._write(chunk, encoding, state.onwrite);
  state.sync = false;
}

function onwrite(stream, er) {
  const state = stream._writableState;
  const sync = state.sync;
  const cb = state.writecb;
  if (typeof cb !== "function") {
    errorOrDestroyW(stream, nodeError(Error, "ERR_MULTIPLE_CALLBACK", "Callback called multiple times"));
    return;
  }
  state.writing = false;
  state.writecb = null;
  state.length -= state.writelen;
  state.writelen = 0;
  if (er) {
    if (!state.errored) state.errored = er;
    if (sync) process.nextTick(onwriteError, stream, state, er, cb);
    else onwriteError(stream, state, er, cb);
  } else {
    if (state.buffered.length > 0) clearBuffer(stream, state);
    if (sync) {
      if (state.afterWriteTickInfo !== null && state.afterWriteTickInfo.cb === cb) {
        state.afterWriteTickInfo.count++;
      } else {
        state.afterWriteTickInfo = { count: 1, cb, stream, state };
        process.nextTick(afterWriteTick, state.afterWriteTickInfo);
      }
    } else {
      afterWrite(stream, state, 1, cb);
    }
  }
}

function afterWriteTick(info) {
  info.state.afterWriteTickInfo = null;
  return afterWrite(info.stream, info.state, info.count, info.cb);
}

function afterWrite(stream, state, count, cb) {
  const needDrain = !state.ending && !stream.destroyed && state.length === 0 && state.needDrain;
  if (needDrain) {
    state.needDrain = false;
    stream.emit("drain");
  }
  while (count-- > 0) {
    state.pendingcb--;
    cb(null);
  }
  if (state.destroyed) errorBuffer(state);
  finishMaybe(stream, state);
}

function errorBuffer(state) {
  if (state.writing) return;
  const buffered = state.buffered.splice(0);
  for (const { chunk, callback } of buffered) {
    state.length -= state.objectMode ? 1 : chunk.length;
    callback(state.errored ?? nodeError(Error, "ERR_STREAM_DESTROYED", "Cannot call write after a stream was destroyed"));
  }
  for (const cb of state[kOnFinished].splice(0)) {
    cb(state.errored ?? nodeError(Error, "ERR_STREAM_DESTROYED", "Cannot call end after a stream was destroyed"));
  }
}

function onwriteError(stream, state, er, cb) {
  --state.pendingcb;
  cb(er);
  errorBuffer(state);
  errorOrDestroyW(stream, er);
}

function clearBuffer(stream, state) {
  if (state.corked || state.bufferProcessing || state.destroyed || !state.constructed) return;
  const buffered = state.buffered;
  if (buffered.length === 0) return;
  state.bufferProcessing = true;
  if (buffered.length > 1 && stream._writev) {
    state.pendingcb -= buffered.length - 1;
    const callbacks = buffered.map((b) => b.callback);
    const callback = (err) => {
      for (const c of callbacks) c(err);
    };
    const chunks = buffered.map(({ chunk, encoding }) => ({ chunk, encoding }));
    state.buffered = [];
    doWrite(stream, state, true, state.length, chunks, "", callback);
  } else {
    let i = 0;
    while (i < buffered.length) {
      const { chunk, encoding, callback } = buffered[i++];
      doWrite(stream, state, false, state.objectMode ? 1 : chunk.length, chunk, encoding, callback);
      if (state.writing) break;
    }
    state.buffered = buffered.slice(i);
  }
  state.bufferProcessing = false;
}

function needFinish(state) {
  return state.ending && !state.destroyed && state.constructed && state.length === 0 && !state.errored &&
    state.buffered.length === 0 && !state.finished && !state.writing && !state.errorEmitted && !state.closeEmitted;
}

function callFinal(stream, state) {
  let called = false;
  function onFinish(err) {
    if (called) return errorOrDestroyW(stream, nodeError(Error, "ERR_MULTIPLE_CALLBACK", "Callback called multiple times"));
    called = true;
    state.pendingcb--;
    if (err) {
      for (const cb of state[kOnFinished].splice(0)) cb(err);
      errorOrDestroyW(stream, err, state.sync);
    } else if (needFinish(state)) {
      state.prefinished = true;
      stream.emit("prefinish");
      state.pendingcb++;
      process.nextTick(finish, stream, state);
    }
  }
  state.sync = true;
  state.pendingcb++;
  try {
    stream._final(onFinish);
  } catch (err) {
    onFinish(err);
  }
  state.sync = false;
}

function prefinish(stream, state) {
  if (!state.prefinished && !state.finalCalled) {
    if (typeof stream._final === "function" && !state.destroyed) {
      state.finalCalled = true;
      callFinal(stream, state);
    } else {
      state.prefinished = true;
      stream.emit("prefinish");
    }
  }
}

function finishMaybe(stream, state, sync) {
  if (needFinish(state)) {
    prefinish(stream, state);
    if (state.pendingcb === 0) {
      if (sync) {
        state.pendingcb++;
        process.nextTick(() => {
          if (needFinish(state)) finish(stream, state);
          else state.pendingcb--;
        });
      } else if (needFinish(state)) {
        state.pendingcb++;
        finish(stream, state);
      }
    }
  }
}

function finish(stream, state) {
  state.pendingcb--;
  state.finished = true;
  for (const cb of state[kOnFinished].splice(0)) cb(null);
  stream.emit("finish");
  if (state.autoDestroy) {
    const rState = stream._readableState;
    const autoDestroy = !rState || (rState.autoDestroy && (rState.endEmitted || rState.readable === false));
    if (autoDestroy) stream.destroy();
  }
}

// Duplex was missing entirely - found the hard way while chasing the
// Bedrock "@smithy/core/protocols" blocker's actual next real failure
// (docs/real-node-plan.md, Round 93): real, unmodified
// @smithy/core's own dist-cjs submodules/serde/index.js does
// "class ChecksumStream extends node_stream.Duplex" at real Bedrock's
// actual checksum-verification call site, not a hypothetical one - a
// missing Duplex throws "Class extends value undefined is not a
// constructor or null" immediately at require() time, same failure
// shape Transform's own missing-entirely gap (above) once did.
//
// A real Duplex is genuinely two independent sides - readable and
// writable - not one side driving the other the way Transform's
// write->_transform->push pipeline does; ChecksumStream is exactly
// that shape: an upstream source is .pipe()'d into it (driving _write,
// which independently calls this.push() to feed its OWN readable
// side), and something else reads from it as a Readable. So this
// combines Readable's push()/destroy()/asyncIterator/pipe() (this
// file's own Readable, above, duplicated rather than shared - matching
// how Writable/Transform are already each self-contained here) with a
// Writable-shaped write()/end() that calls the real per-subclass
// override points (_write, _final) real Duplex subclasses expect,
// plus a default no-op _read(size) so a subclass that (like
// ChecksumStream) only reacts to _read for backpressure release still
// has one to override.
class Duplex extends EventEmitter {
  constructor(_opts) {
    super();
    this.readable = true;
    this.writable = true;
    this._queue = [];
    this._ended = false;
    this._error = null;
    this._waiters = [];
    this._disturbed = false;
    this.destroyed = false;
  }
  _read(_size) {}
  _write(chunk, _encoding, callback) {
    callback(notImplementedWriteError());
  }
  _final(callback) {
    callback();
  }
  _settleWaiters() {
    while (this._waiters.length && (this._queue.length || this._ended || this._error)) {
      const { resolve, reject } = this._waiters.shift();
      if (this._error) {
        reject(this._error);
      } else if (this._queue.length) {
        resolve({ value: this._queue.shift(), done: false });
      } else {
        resolve({ value: undefined, done: true });
      }
    }
  }
  push(chunk) {
    if (chunk === undefined || chunk === null) {
      this._ended = true;
      this._settleWaiters();
      this.emit("end");
      return false;
    }
    if (this._events.data && this._events.data.length) this._disturbed = true;
    this._queue.push(chunk);
    this.emit("data", chunk);
    this._settleWaiters();
    this._read(chunk.length);
    return true;
  }
  destroy(err) {
    if (this.destroyed) return this;
    this.destroyed = true;
    if (err) {
      this._error = err;
      this.emit("error", err);
    } else {
      this._ended = true;
    }
    this._settleWaiters();
    this.emit("close");
    return this;
  }
  [Symbol.asyncIterator]() {
    return {
      next: () => {
        this._disturbed = true;
        if (this._queue.length) {
          return Promise.resolve({ value: this._queue.shift(), done: false });
        }
        if (this._error) {
          return Promise.reject(this._error);
        }
        if (this._ended) {
          return Promise.resolve({ value: undefined, done: true });
        }
        return new Promise((resolve, reject) => {
          this._waiters.push({ resolve, reject });
        });
      },
    };
  }
  write(chunk, encoding, cb) {
    if (typeof encoding === "function") {
      cb = encoding;
      encoding = undefined;
    }
    this._write(chunk, encoding, (err) => {
      if (err) {
        this.emit("error", err);
        if (typeof cb === "function") cb(err);
        return;
      }
      if (typeof cb === "function") cb();
    });
    return true;
  }
  end(chunk, encoding, cb) {
    if (typeof chunk === "function") {
      cb = chunk;
      chunk = undefined;
    } else if (typeof encoding === "function") {
      cb = encoding;
    }
    const finishUp = (err) => {
      if (err) {
        this.emit("error", err);
        if (typeof cb === "function") cb(err);
        return;
      }
      this.emit("finish");
      if (typeof cb === "function") cb();
    };
    if (chunk !== undefined) {
      this._write(chunk, encoding, (err) => {
        if (err) {
          finishUp(err);
          return;
        }
        this._final(finishUp);
      });
    } else {
      this._final(finishUp);
    }
    return this;
  }
  pipe(dest) {
    this.on("data", (chunk) => {
      if (dest && typeof dest.write === "function") dest.write(chunk);
    });
    this.on("end", () => {
      if (dest && typeof dest.end === "function") dest.end();
    });
    return dest;
  }
}

// Transform was missing entirely - found the hard way while probing
// real undici (round 73, docs/real-node-plan.md): real undici's own
// lib/web/eventsource/eventsource-stream.js does
// "class EventSourceStream extends Transform", so a missing Transform
// throws "Class extends value undefined is not a constructor or null"
// immediately at require() time. A real Transform genuinely is both a
// Writable and a Readable joined by a per-chunk _transform() step - a
// subclass overrides _transform(chunk, encoding, callback) (and
// optionally _flush(callback)) to push whatever it produces; the base
// class here provides an honest identity default (pass the chunk
// through unchanged) only for a hypothetical caller that doesn't
// override it - every real subclass in this dependency tree does.
class Transform extends EventEmitter {
  constructor(_opts) {
    super();
    this.readable = true;
    this.writable = true;
  }
  _transform(chunk, _encoding, callback) {
    callback(null, chunk);
  }
  _flush(callback) {
    callback();
  }
  // _write, calling _transform by default, exists so an instance-level
  // _write override (not a subclass's own _transform override) still
  // gets consulted - real Node's own Transform genuinely is a Writable
  // (its _write internally drives _transform), so write()/end() below
  // call this._write rather than this._transform directly. Found the
  // hard way chasing real vite's own dependency (fast-glob's
  // ReaderStream.static(), bundled into vite's real dep chunk) under
  // noderati: it does const stream = new PassThrough({objectMode:
  // true}); stream._write = (index, enc, done) => {...} - a plain,
  // real Node idiom (assign _write directly on the instance, no
  // subclassing) - and this write()/end() calling this._transform
  // straight through meant that override was never consulted at all:
  // every write() silently ran PassThrough's own default identity
  // _transform instead, the real override's own done() (which is
  // what was supposed to eventually call stream.end()) never ran, and
  // the stream never emitted "end" - hanging a real vite dev-server
  // startup (dependency pre-bundling) forever with no error, not just
  // a synthetic repro.
  _write(chunk, encoding, callback) {
    this._transform(chunk, encoding, (err, data) => {
      if (err) {
        callback(err);
        return;
      }
      if (data !== undefined && data !== null) this.emit("data", data);
      callback();
    });
  }
  write(chunk, encoding, cb) {
    if (typeof encoding === "function") {
      cb = encoding;
      encoding = undefined;
    }
    this._write(chunk, encoding, (err) => {
      if (err) {
        this.emit("error", err);
        if (typeof cb === "function") cb(err);
        return;
      }
      if (typeof cb === "function") cb();
    });
    return true;
  }
  push(chunk) {
    if (chunk !== undefined && chunk !== null) this.emit("data", chunk);
    return true;
  }
  end(chunk, encoding, cb) {
    if (typeof chunk === "function") {
      cb = chunk;
      chunk = undefined;
    } else if (typeof encoding === "function") {
      cb = encoding;
    }
    const finishUp = () => {
      this.emit("end");
      this.emit("finish");
      if (typeof cb === "function") cb();
    };
    if (chunk !== undefined) {
      this._write(chunk, encoding, (err) => {
        if (err) {
          this.emit("error", err);
          return;
        }
        this._flush(finishUp);
      });
    } else {
      this._flush(finishUp);
    }
    return this;
  }
  pipe(dest) {
    this.on("data", (chunk) => {
      if (dest && typeof dest.write === "function") dest.write(chunk);
    });
    this.on("end", () => {
      if (dest && typeof dest.end === "function") dest.end();
    });
    return dest;
  }
}

// PassThrough was missing entirely - found alongside Duplex above,
// same round, same real @smithy/core/dist-cjs dependency tree
// (submodules/serde/index.js's own real ChecksumStream usage sits
// right next to real PassThrough usage elsewhere in that file). Real
// Node's own PassThrough is exactly this: a Transform with no
// overrides at all - Transform's own default _transform(chunk, enc,
// cb) already does the identity pass-through, so there's nothing left
// to add.
class PassThrough extends Transform {}

// pipelineStreams was previously a silent no-op (both node:stream and
// node:stream/promises exported 'async function pipeline(..._streams) {}')
// - it resolved immediately without piping a single byte between any of
// the streams passed to it, a real Node stream.pipeline() call that
// appeared to succeed while silently discarding all data. Zero direct call
// sites for it exist anywhere in the real pi dependency tree (checked
// before writing this, not assumed), so it wasn't reachable today - but a
// lying no-op is worse than an honest gap precisely because it's *not*
// reachable yet: the first real caller to exercise it would get silent
// data loss instead of a clear signal something's missing. Given this
// file's own Readable/Writable already implement real pipe(), a correct
// minimal pipeline is straightforward to build on top of them: chain
// .pipe() calls between consecutive streams, resolve when the last one
// finishes, reject on the first "error" from any stream in the chain.
function pipelineStreams(streams) {
  return new Promise((resolve, reject) => {
    if (streams.length < 2) {
      reject(new TypeError("pipeline requires at least 2 streams"));
      return;
    }
    let settled = false;
    const fail = (err) => {
      if (settled) return;
      settled = true;
      reject(err);
    };
    for (const s of streams) {
      if (s && typeof s.on === "function") s.on("error", fail);
    }
    for (let i = 0; i < streams.length - 1; i++) {
      streams[i].pipe(streams[i + 1]);
    }
    const last = streams[streams.length - 1];
    const succeed = () => {
      if (settled) return;
      settled = true;
      resolve(last);
    };
    if (last && typeof last.on === "function") {
      last.on("finish", succeed);
      last.on("end", succeed);
    } else {
      succeed();
    }
  });
}

// Real Node's node:stream.pipeline() takes an optional trailing callback
// instead of always returning a promise (that form is stream/promises'
// job); support both call shapes here since real code uses either.
function pipeline(...args) {
  const callback = typeof args[args.length - 1] === "function" ? args.pop() : undefined;
  const promise = pipelineStreams(args);
  if (callback) {
    promise.then((result) => callback(undefined, result), (err) => callback(err));
    return undefined;
  }
  return promise;
}

// isDisturbed was missing entirely - found the hard way while
// re-probing real undici's fetch() end to end after paserati#384 was
// fixed (round 80, docs/real-node-plan.md): real undici's own
// lib/core/util.js#isDisturbed does
// "stream.isDisturbed(body) || body[kBodyUsed]" as part of every
// consumeBody() call (the shared implementation behind
// response.text()/json()/etc) - a real, unavoidable call site on the
// very first body read, not a hypothetical one. Missing it entirely
// threw "undefined is not a function" the instant any fetch() response
// body was consumed via .text()/.json()/.arrayBuffer()/.blob().
//
// Traced (not assumed) which object actually reaches this function on
// the real fetch()-response path: it's paserati's own built-in WHATWG
// ReadableStream, not this file's Readable - undici's ReadableStreamFrom
// wraps a Readable into one before any of this project's own code is
// reachable again, and consumeBody()/bodyUnusable() both check
// body.stream (the wrapped web stream), not the original. A bare
// ReadableStream has no _disturbed property, so this always answers
// false on that path - which is the correct answer for a body nothing
// has disturbed yet, and *existence* (returning anything instead of
// throwing "undefined is not a function") is what actually unblocked
// consumeBody() this round, not the flag-tracking below.
//
// The _disturbed flag on this file's own Readable (set in push()/the
// async iterator above) isn't dead code, though: extractBody() (used
// when *constructing* a body from a Readable, e.g. passing one as
// RequestInit.body) calls util.isDisturbed() on the pre-wrap
// async-iterable directly, before any ReadableStreamFrom wrapping
// happens - that path does see this file's real flag. Not exercised by
// the GET-with-response-body probe that found this gap; left in place
// on the reasonable expectation that a body-as-request-input path will
// need it for real, correctly, the same way every other "flagged, not
// glossed over" gap in this file is - rather than ripped out for only
// covering half the real isDisturbed() call sites today.
function isDisturbed(stream) {
  return !!(stream && stream._disturbed);
}

// isErrored: same real call site as isDisturbed above
// (undici's core/util.js does "const { isErrored, isDisturbed } =
// require('node:stream')"), found in the same pass rather than waiting
// to hit it as a separate gap later - real undici's own
// extractBody()'s ReadableStream pull() checks "!isErrored(stream)"
// before every enqueue. Same _error field this file's Readable already
// tracks (set by destroy(err)); same caveat as isDisturbed above - the
// real fetch()-response path checks this against paserati's own
// built-in WHATWG ReadableStream, not this class, so existence (a
// safe, consistent "false") is what matters there, not this flag.
function isErrored(stream) {
  return !!(stream && stream._error);
}

// Real Node's require('stream') is the Stream constructor itself, not a
// plain namespace object - every other export hangs off it as a static
// property (require('stream').Readable, .Writable, etc). Matters for any
// real code that does 'var Stream = require(\"stream\")' and then uses
// Stream directly (as util.inherits' own base, or 'new Stream()') rather
// than destructuring a named export - see this file's own Stream class
// doc comment above for the real package (send) that hit this.
Stream.Readable = Readable;
Stream.Writable = Writable;
Stream.Duplex = Duplex;
Stream.Transform = Transform;
Stream.PassThrough = PassThrough;
Stream.pipeline = pipeline;
Stream.isDisturbed = isDisturbed;
Stream.isErrored = isErrored;

export { Stream, Readable, Writable, Duplex, Transform, PassThrough, pipeline, isDisturbed, isErrored, pipelineStreams as _pipelineStreams };
export default Stream;
`

const streamPromisesShim = `import { _pipelineStreams } from "stream";

export async function pipeline(...streams) {
  return _pipelineStreams(streams);
}
export default { pipeline };
`

func declareStream() {
	registerJSShim("stream", streamShim)
	registerJSShim("stream/promises", streamPromisesShim)
}
