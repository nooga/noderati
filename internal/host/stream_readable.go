package host

// streamReadableJS: Readable and ReadableState, following the state
// machine of Node's lib/internal/streams/readable.js (paused/flowing
// modes, _read() pulled against highWaterMark, objectMode, read(n),
// 'readable', setEncoding via string_decoder, pipe() with multi-dest
// awaitDrain backpressure, wrap(), the async iterator, Readable.from).
//
// Consumers this has to keep working: undici's fetch() body
// ("new Readable({ read: resume })", push()ed from the dispatcher,
// consumed via [Symbol.asyncIterator]) and undici's own onError, which
// calls destroy(err) from inside an 'error' listener (so destroy() must
// stay re-entrancy safe); readdirp (chokidar), a pull-driven Readable.
const streamReadableJS = `
function ReadableState(options, stream, isDuplex) {
  this.objectMode = !!(options && options.objectMode);
  if (isDuplex && options && options.readableObjectMode) this.objectMode = true;
  this.highWaterMark = options ?
    getHighWaterMark(this, options, "readableHighWaterMark", isDuplex) :
    getDefaultHighWaterMark(false);
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
  this.emitClose = !(options && options.emitClose === false);
  this.autoDestroy = !(options && options.autoDestroy === false);
  this.destroyed = false;
  this.errored = null;
  this.closed = false;
  this.closeEmitted = false;
  const defaultEncoding = options ? options.defaultEncoding : undefined;
  if (defaultEncoding == null || defaultEncoding === "utf8" || defaultEncoding === "utf-8") {
    this.defaultEncoding = "utf8";
  } else if (Buffer.isEncoding(defaultEncoding)) {
    this.defaultEncoding = defaultEncoding;
  } else {
    throw new ERR_UNKNOWN_ENCODING(defaultEncoding);
  }
  this.awaitDrainWriters = null;
  this.multiAwaitDrain = false;
  this.readingMore = false;
  this.dataEmitted = false;
  this.dataListening = false;
  this.decoder = null;
  this.encoding = null;
  // null: pause()/resume() never called; otherwise the last one's verdict.
  this.pausedState = null;
  if (options && options.encoding) {
    this.decoder = new StringDecoder(options.encoding);
    this.encoding = options.encoding;
  }
}

Object.defineProperties(ReadableState.prototype, {
  pipesCount: {
    get() { return this.pipes.length; },
  },
  paused: {
    get() { return this.pausedState === true; },
    set(value) { this.pausedState = !!value; },
  },
});

ReadableState.prototype[kOnConstructed] = function onConstructed(stream) {
  if (this.needReadable) maybeReadMore(stream, this);
};

function Readable(options) {
  if (!(this instanceof Readable)) return new Readable(options);
  this._readableState = new ReadableState(options, this, false);
  if (options) {
    if (typeof options.read === "function") this._read = options.read;
    if (typeof options.destroy === "function") this._destroy = options.destroy;
    if (typeof options.construct === "function") this._construct = options.construct;
    if (options.signal) addAbortSignal(options.signal, this);
  }
  Stream.call(this, options);
  if (this._construct != null) {
    construct(this, () => {
      this._readableState[kOnConstructed](this);
    });
  }
}
Object.setPrototypeOf(Readable.prototype, Stream.prototype);
Object.setPrototypeOf(Readable, Stream);
Readable.ReadableState = ReadableState;

Readable.prototype.destroy = destroyStream;
Readable.prototype._undestroy = undestroy;
Readable.prototype._destroy = function (err, cb) {
  cb(err);
};
Readable.prototype[EventEmitter.captureRejectionSymbol] = function (err) {
  this.destroy(err);
};
Readable.prototype[Symbol.asyncDispose] = async function () {
  let error;
  if (!this.destroyed) {
    error = this.readableEnded ? null : new AbortError();
    this.destroy(error);
  }
  await new Promise((resolve, reject) => eos(this, (err) => (err && err !== error ? reject(err) : resolve(null))));
};

Readable.prototype.push = function (chunk, encoding) {
  return readableAddChunk(this, chunk, encoding, false);
};
Readable.prototype.unshift = function (chunk, encoding) {
  return readableAddChunk(this, chunk, encoding, true);
};

function invalidChunkError(chunk) {
  return new ERR_INVALID_ARG_TYPE("chunk", ["string", "Buffer", "TypedArray", "DataView"], chunk);
}

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
      if (state.encoding !== encoding) {
        if (addToFront && state.encoding) {
          chunk = Buffer.from(chunk, encoding).toString(state.encoding);
        } else {
          chunk = Buffer.from(chunk, encoding);
          encoding = "";
        }
      }
    } else if (chunk instanceof Buffer) {
      encoding = "";
    } else if (isArrayBufferView(chunk)) {
      chunk = uint8ArrayToBuffer(chunk);
      encoding = "";
    } else if (chunk !== undefined) {
      errorOrDestroy(stream, invalidChunkError(chunk));
      return false;
    }
  }
  if (addToFront) {
    if (!state.objectMode && !(chunk && chunk.length > 0)) return canPushMore(state);
    if (state.endEmitted) errorOrDestroy(stream, new ERR_STREAM_UNSHIFT_AFTER_END_EVENT());
    else if (state.destroyed || state.errored) return false;
    else addChunk(stream, state, chunk, true);
    return canPushMore(state);
  }
  if (!state.objectMode && (!chunk || chunk.length <= 0)) {
    state.reading = false;
    maybeReadMore(stream, state);
    return canPushMore(state);
  }
  if (state.ended) {
    errorOrDestroy(stream, new ERR_STREAM_PUSH_AFTER_EOF());
    return false;
  }
  if (state.destroyed || state.errored) return false;
  state.reading = false;
  if (state.decoder && !encoding) {
    chunk = state.decoder.write(chunk);
    if (!state.objectMode && chunk.length === 0) {
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

function clearAwaitDrain(state) {
  if (state.multiAwaitDrain) state.awaitDrainWriters.clear();
  else state.awaitDrainWriters = null;
}

function addChunk(stream, state, chunk, addToFront) {
  if (state.flowing && !state.sync && state.dataListening && state.length === 0) {
    clearAwaitDrain(state);
    state.dataEmitted = true;
    stream.emit("data", chunk);
  } else {
    state.length += state.objectMode ? 1 : chunk.length;
    if (addToFront) state.buffer.unshift(chunk);
    else state.buffer.push(chunk);
    if (state.needReadable) emitReadable(stream);
  }
  maybeReadMore(stream, state);
}

Readable.prototype.isPaused = function () {
  const state = this._readableState;
  return state.pausedState === true || (state.flowing === false);
};

Readable.prototype.setEncoding = function (enc) {
  const state = this._readableState;
  const decoder = new StringDecoder(enc);
  state.decoder = decoder;
  state.encoding = decoder.encoding ?? normalizeEncoding(enc);
  let content = "";
  for (const data of state.buffer) content += decoder.write(data);
  state.buffer = [];
  if (content !== "") state.buffer.push(content);
  state.length = content.length;
  return this;
};

const MAX_HWM = 0x40000000;
function computeNewHighWaterMark(n) {
  if (n > MAX_HWM) throw new ERR_OUT_OF_RANGE("size", "<= 1GiB", n);
  n--;
  n |= n >>> 1;
  n |= n >>> 2;
  n |= n >>> 4;
  n |= n >>> 8;
  n |= n >>> 16;
  n++;
  return n;
}

function howMuchToRead(n, state) {
  if (n <= 0 || (state.length === 0 && state.ended)) return 0;
  if (state.objectMode) return 1;
  if (Number.isNaN(n)) {
    if ((!state.decoder || state.flowing) && state.length) return state.buffer[0].length;
    return state.length;
  }
  if (n <= state.length) return n;
  return state.ended ? state.length : 0;
}

Readable.prototype.read = function (n) {
  if (n === undefined) n = NaN;
  else if (!Number.isInteger(n)) n = Number.parseInt(n, 10);
  const state = this._readableState;
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
  if (state.reading || state.ended || state.destroyed || state.errored || !state.constructed) {
    doRead = false;
  } else if (doRead) {
    state.reading = true;
    state.sync = true;
    if (state.length === 0) state.needReadable = true;
    try {
      this._read(state.highWaterMark);
    } catch (err) {
      errorOrDestroy(this, err);
    }
    state.sync = false;
    if (!state.reading) n = howMuchToRead(nOrig, state);
  }
  let ret = n > 0 ? fromList(n, state) : null;
  if (ret === null) {
    if (state.length <= state.highWaterMark) state.needReadable = true;
    n = 0;
  } else {
    state.length -= n;
    clearAwaitDrain(state);
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
};

function onEofChunk(stream, state) {
  if (state.ended) return;
  if (state.decoder) {
    const chunk = state.decoder.end();
    if (chunk && chunk.length) {
      state.buffer.push(chunk);
      state.length += state.objectMode ? 1 : chunk.length;
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
    process.nextTick(emitReadable_, stream);
  }
}

function emitReadable_(stream) {
  const state = stream._readableState;
  if (!state.destroyed && !state.errored && (state.length || state.ended)) {
    stream.emit("readable");
    state.emittedReadable = false;
  }
  if (!state.flowing && !state.ended && state.length <= state.highWaterMark) state.needReadable = true;
  flow(stream);
}

function maybeReadMore(stream, state) {
  if (!state.readingMore && !state.reading && state.constructed) {
    state.readingMore = true;
    process.nextTick(maybeReadMore_, stream, state);
  }
}

function maybeReadMore_(stream, state) {
  while (!state.reading && !state.ended &&
         (state.length < state.highWaterMark || (state.flowing && state.length === 0))) {
    const len = state.length;
    stream.read(0);
    if (len === state.length) break;
  }
  state.readingMore = false;
}

Readable.prototype._read = function (n) {
  throw new ERR_METHOD_NOT_IMPLEMENTED("_read()");
};

Readable.prototype.pipe = function (dest, pipeOpts) {
  const src = this;
  const state = this._readableState;
  if (state.pipes.length === 1 && !state.multiAwaitDrain) {
    state.multiAwaitDrain = true;
    state.awaitDrainWriters = new Set(state.awaitDrainWriters ? [state.awaitDrainWriters] : []);
  }
  state.pipes.push(dest);
  const doEnd = (!pipeOpts || pipeOpts.end !== false) &&
    dest !== process.stdout && dest !== process.stderr;
  const endFn = doEnd ? onend : unpipe;
  if (state.endEmitted) process.nextTick(endFn);
  else src.once("end", endFn);
  dest.on("unpipe", onunpipe);
  function onunpipe(readable, unpipeInfo) {
    if (readable === src && unpipeInfo && unpipeInfo.hasUnpiped === false) {
      unpipeInfo.hasUnpiped = true;
      cleanup();
    }
  }
  function onend() {
    dest.end();
  }
  let ondrain;
  let cleanedUp = false;
  function cleanup() {
    dest.removeListener("close", onclose);
    dest.removeListener("finish", onfinish);
    if (ondrain) dest.removeListener("drain", ondrain);
    dest.removeListener("error", onerror);
    dest.removeListener("unpipe", onunpipe);
    src.removeListener("end", onend);
    src.removeListener("end", unpipe);
    src.removeListener("data", ondata);
    cleanedUp = true;
    if (ondrain && state.awaitDrainWriters && (!dest._writableState || dest._writableState.needDrain)) ondrain();
  }
  function pause() {
    if (!cleanedUp) {
      if (state.pipes.length === 1 && state.pipes[0] === dest) {
        state.awaitDrainWriters = dest;
        state.multiAwaitDrain = false;
      } else if (state.pipes.length > 1 && state.pipes.includes(dest)) {
        state.awaitDrainWriters.add(dest);
      }
      src.pause();
    }
    if (!ondrain) {
      ondrain = pipeOnDrain(src, dest);
      dest.on("drain", ondrain);
    }
  }
  src.on("data", ondata);
  function ondata(chunk) {
    try {
      const ret = dest.write(chunk);
      if (ret === false) pause();
    } catch (error) {
      dest.destroy(error);
    }
  }
  function onerror(er) {
    unpipe();
    dest.removeListener("error", onerror);
    if (dest.listenerCount("error") === 0) {
      const s = dest._writableState || dest._readableState;
      if (s && !s.errorEmitted) errorOrDestroy(dest, er);
      else dest.emit("error", er);
    }
  }
  prependListener(dest, "error", onerror);
  function onclose() {
    dest.removeListener("finish", onfinish);
    unpipe();
  }
  dest.once("close", onclose);
  function onfinish() {
    dest.removeListener("close", onclose);
    unpipe();
  }
  dest.once("finish", onfinish);
  function unpipe() {
    src.unpipe(dest);
  }
  dest.emit("pipe", src);
  if (dest.writableNeedDrain === true) pause();
  else if (!state.flowing) src.resume();
  return dest;
};

function pipeOnDrain(src, dest) {
  return function pipeOnDrainFunctionResult() {
    const state = src._readableState;
    if (state.awaitDrainWriters === dest) state.awaitDrainWriters = null;
    else if (state.multiAwaitDrain) state.awaitDrainWriters.delete(dest);
    if ((!state.awaitDrainWriters || state.awaitDrainWriters.size === 0) && state.dataListening) src.resume();
  };
}

Readable.prototype.unpipe = function (dest) {
  const state = this._readableState;
  const unpipeInfo = { hasUnpiped: false };
  if (state.pipes.length === 0) return this;
  if (!dest) {
    const dests = state.pipes;
    state.pipes = [];
    this.pause();
    for (let i = 0; i < dests.length; i++) dests[i].emit("unpipe", this, { hasUnpiped: false });
    return this;
  }
  const index = state.pipes.indexOf(dest);
  if (index === -1) return this;
  state.pipes.splice(index, 1);
  if (state.pipes.length === 0) this.pause();
  dest.emit("unpipe", this, unpipeInfo);
  return this;
};

Readable.prototype.on = function (ev, fn) {
  const res = Stream.prototype.on.call(this, ev, fn);
  const state = this._readableState;
  if (ev === "data") {
    state.dataListening = true;
    if (this.listenerCount("readable") > 0) state.readableListening = true;
    if (state.flowing !== false) this.resume();
  } else if (ev === "readable") {
    if (!state.endEmitted && !state.readableListening) {
      state.readableListening = state.needReadable = true;
      state.flowing = false;
      state.emittedReadable = false;
      if (state.length) emitReadable(this);
      else if (!state.reading) process.nextTick(nReadingNextTick, this);
    }
  }
  return res;
};
Readable.prototype.addListener = Readable.prototype.on;

Readable.prototype.removeListener = function (ev, fn) {
  const state = this._readableState;
  const res = Stream.prototype.removeListener.call(this, ev, fn);
  if (ev === "readable") process.nextTick(updateReadableListening, this);
  else if (ev === "data" && this.listenerCount("data") === 0) state.dataListening = false;
  return res;
};
Readable.prototype.off = Readable.prototype.removeListener;

Readable.prototype.removeAllListeners = function (ev) {
  const res = Reflect.apply(Stream.prototype.removeAllListeners, this, arguments);
  if (ev === "readable" || ev === undefined) process.nextTick(updateReadableListening, this);
  return res;
};

function updateReadableListening(self) {
  const state = self._readableState;
  state.readableListening = self.listenerCount("readable") > 0;
  if (state.pausedState === false && state.resumeScheduled) state.flowing = true;
  else if (state.dataListening) self.resume();
  else if (!state.readableListening) state.flowing = null;
}

function nReadingNextTick(self) {
  self.read(0);
}

Readable.prototype.resume = function () {
  const state = this._readableState;
  if (state.destroyed) return this;
  if (!state.flowing) {
    state.flowing = !state.readableListening;
    resume(this, state);
  }
  state.pausedState = false;
  return this;
};

function resume(stream, state) {
  if (!state.resumeScheduled) {
    state.resumeScheduled = true;
    process.nextTick(resume_, stream, state);
  }
}

function resume_(stream, state) {
  if (!state.reading) stream.read(0);
  state.resumeScheduled = false;
  stream.emit("resume");
  flow(stream);
  if (state.flowing && !state.reading) stream.read(0);
}

Readable.prototype.pause = function () {
  const state = this._readableState;
  if (state.destroyed) return this;
  if (state.flowing !== false) {
    state.flowing = false;
    this.emit("pause");
  }
  state.pausedState = true;
  return this;
};

function flow(stream) {
  const state = stream._readableState;
  while (state.flowing && stream.read() !== null);
}

Readable.prototype.wrap = function (stream) {
  let paused = false;
  stream.on("data", (chunk) => {
    if (!this.push(chunk) && stream.pause) {
      paused = true;
      stream.pause();
    }
  });
  stream.on("end", () => {
    this.push(null);
  });
  stream.on("error", (err) => {
    errorOrDestroy(this, err);
  });
  stream.on("close", () => {
    this.destroy();
  });
  stream.on("destroy", () => {
    this.destroy();
  });
  this._read = () => {
    if (paused && stream.resume) {
      paused = false;
      stream.resume();
    }
  };
  const streamKeys = Object.keys(stream);
  for (let j = 1; j < streamKeys.length; j++) {
    const i = streamKeys[j];
    if (this[i] === undefined && typeof stream[i] === "function") this[i] = stream[i].bind(stream);
  }
  return this;
};

Readable.prototype[Symbol.asyncIterator] = function () {
  return streamToAsyncIterator(this);
};

Readable.prototype.iterator = function (options) {
  if (options !== undefined) validateObject(options, "options");
  return streamToAsyncIterator(this, options);
};

function streamToAsyncIterator(stream, options) {
  if (typeof stream.read !== "function") stream = Readable.wrap(stream, { objectMode: true });
  const iter = createAsyncIterator(stream, options);
  iter.stream = stream;
  return iter;
}

async function* createAsyncIterator(stream, options) {
  let callback = nop;
  function next(resolve) {
    if (this === stream) {
      callback();
      callback = nop;
    } else {
      callback = resolve;
    }
  }
  stream.on("readable", next);
  let error;
  const cleanup = eos(stream, { writable: false }, (err) => {
    error = err ? aggregateTwoErrors(error, err) : null;
    callback();
    callback = nop;
  });
  try {
    while (true) {
      const chunk = stream.destroyed ? null : stream.read();
      if (chunk !== null) {
        yield chunk;
      } else if (error) {
        throw error;
      } else if (error === null) {
        return;
      } else {
        await new Promise(next);
      }
    }
  } catch (err) {
    error = aggregateTwoErrors(error, err);
    throw error;
  } finally {
    if ((error || !options || options.destroyOnReturn !== false) &&
        (error === undefined || stream._readableState.autoDestroy)) {
      destroyer(stream, null);
    } else {
      stream.off("readable", next);
      cleanup();
    }
  }
}

Readable.prototype.compose = function compose(stream, options) {
  if (options != null) validateObject(options, "options");
  if (options && options.signal != null) validateAbortSignal(options.signal, "options.signal");
  const composedStream = composeStreams(this, stream);
  if (options && options.signal) addAbortSignalNoValidate(options.signal, composedStream);
  return composedStream;
};

Object.defineProperties(Readable.prototype, {
  readable: {
    get() {
      const r = this._readableState;
      return !!r && r.readable !== false && !r.destroyed && !r.errorEmitted && !r.endEmitted;
    },
    set(val) {
      if (this._readableState) this._readableState.readable = !!val;
    },
  },
  readableDidRead: {
    get() { return this._readableState.dataEmitted; },
  },
  readableAborted: {
    get() {
      const r = this._readableState;
      return !!(r.readable !== false && (r.destroyed || r.errored) && !r.endEmitted);
    },
  },
  readableHighWaterMark: {
    get() { return this._readableState.highWaterMark; },
  },
  readableBuffer: {
    get() { return this._readableState && this._readableState.buffer; },
  },
  readableFlowing: {
    get() { return this._readableState.flowing; },
    set(state) {
      if (this._readableState) this._readableState.flowing = state;
    },
  },
  readableLength: {
    get() { return this._readableState.length; },
  },
  readableObjectMode: {
    get() { return this._readableState ? this._readableState.objectMode : false; },
  },
  readableEncoding: {
    get() { return this._readableState ? this._readableState.encoding : null; },
  },
  errored: {
    get() { return this._readableState ? this._readableState.errored : null; },
  },
  closed: {
    get() { return this._readableState ? this._readableState.closed : false; },
  },
  destroyed: {
    get() { return this._readableState ? this._readableState.destroyed : false; },
    set(value) {
      if (this._readableState) this._readableState.destroyed = value;
    },
  },
  readableEnded: {
    get() { return this._readableState ? this._readableState.endEmitted : false; },
  },
});

function fromList(n, state) {
  if (state.length === 0) return null;
  const buf = state.buffer;
  let ret;
  if (state.objectMode) {
    ret = buf.shift();
  } else if (!n || n >= state.length) {
    if (state.decoder) ret = buf.join("");
    else if (buf.length === 1) ret = buf[0];
    else ret = Buffer.concat(buf, state.length);
    state.buffer = [];
  } else if (n < buf[0].length) {
    ret = buf[0].slice(0, n);
    buf[0] = buf[0].slice(n);
  } else if (n === buf[0].length) {
    ret = buf.shift();
  } else if (state.decoder) {
    ret = "";
    while (buf.length) {
      const str = buf[0];
      if (n > str.length) {
        ret += str;
        n -= str.length;
        buf.shift();
      } else {
        if (n === str.length) {
          ret += str;
          buf.shift();
        } else {
          ret += str.slice(0, n);
          buf[0] = str.slice(n);
        }
        break;
      }
    }
  } else {
    ret = Buffer.allocUnsafe(n);
    const retLen = n;
    while (buf.length) {
      const data = buf[0];
      if (n > data.length) {
        ret.set(data, retLen - n);
        n -= data.length;
        buf.shift();
      } else {
        if (n === data.length) {
          ret.set(data, retLen - n);
          buf.shift();
        } else {
          ret.set(data.subarray(0, n), retLen - n);
          buf[0] = data.subarray(n);
        }
        break;
      }
    }
  }
  return ret;
}
Readable._fromList = fromList;

function endReadable(stream) {
  const state = stream._readableState;
  if (!state.endEmitted) {
    state.ended = true;
    process.nextTick(endReadableNT, state, stream);
  }
}

function endReadableNT(state, stream) {
  if (!state.errored && !state.closeEmitted && !state.endEmitted && state.length === 0) {
    state.endEmitted = true;
    stream.emit("end");
    if (stream.writable && stream.allowHalfOpen === false) {
      process.nextTick(endWritableNT, stream);
    } else if (state.autoDestroy) {
      const wState = stream._writableState;
      const autoDestroy = !wState || (wState.autoDestroy && (wState.finished || wState.writable === false));
      if (autoDestroy) stream.destroy();
    }
  }
}

function endWritableNT(stream) {
  const writable = stream.writable && !stream.writableEnded && !stream.destroyed;
  if (writable) stream.end();
}

Readable.from = function (iterable, opts) {
  return readableFrom(Readable, iterable, opts);
};
Readable.fromWeb = function (readableStream, options) {
  return newStreamReadableFromReadableStream(readableStream, options);
};
Readable.toWeb = function (streamReadable, options) {
  return newReadableStreamFromStreamReadable(streamReadable, options);
};
Readable.wrap = function (src, options) {
  return new Readable({
    objectMode: src.readableObjectMode ?? src.objectMode ?? true,
    ...options,
    destroy(err, callback) {
      destroyer(src, err);
      callback(err);
    },
  }).wrap(src);
};

// Readable.from (lib/internal/streams/from.js).
function readableFrom(Ctor, iterable, opts) {
  let iterator;
  if (typeof iterable === "string" || iterable instanceof Buffer) {
    return new Ctor({
      objectMode: true,
      ...opts,
      read() {
        this.push(iterable);
        this.push(null);
      },
    });
  }
  let isAsync;
  if (iterable && iterable[Symbol.asyncIterator]) {
    isAsync = true;
    iterator = iterable[Symbol.asyncIterator]();
  } else if (iterable && iterable[Symbol.iterator]) {
    isAsync = false;
    iterator = iterable[Symbol.iterator]();
  } else {
    throw new ERR_INVALID_ARG_TYPE("iterable", ["Iterable"], iterable);
  }
  const readable = new Ctor({
    objectMode: true,
    highWaterMark: 1,
    ...opts,
  });
  const originalDestroy = readable._destroy;
  let reading = false;
  let isAsyncValues = false;
  readable._read = function () {
    if (!reading) {
      reading = true;
      if (isAsync) nextAsync();
      else if (isAsyncValues) nextSyncWithAsyncValues();
      else nextSyncWithSyncValues();
    }
  };
  readable._destroy = function (error, cb) {
    originalDestroy.call(this, error, (destroyError) => {
      const combinedError = destroyError || error;
      close(combinedError).then(
        () => process.nextTick(cb, combinedError),
        (closeError) => process.nextTick(cb, aggregateTwoErrors(combinedError, closeError)));
    });
  };
  async function close(error) {
    const hadError = error !== undefined && error !== null;
    if (hadError && typeof iterator.throw === "function") {
      const { value, done } = await iterator.throw(error);
      await value;
      if (done) return;
    }
    if (typeof iterator.return === "function") {
      const { value } = await iterator.return();
      await value;
    }
  }
  function nextSyncWithSyncValues() {
    for (;;) {
      try {
        const { value, done } = iterator.next();
        if (done) {
          readable.push(null);
          return;
        }
        if (value && typeof value.then === "function") return changeToAsyncValues(value);
        if (value === null) {
          reading = false;
          throw new ERR_STREAM_NULL_VALUES();
        }
        if (readable.push(value)) continue;
        reading = false;
      } catch (err) {
        readable.destroy(err);
      }
      break;
    }
  }
  async function changeToAsyncValues(value) {
    isAsyncValues = true;
    try {
      const res = await value;
      if (res === null) {
        reading = false;
        throw new ERR_STREAM_NULL_VALUES();
      }
      if (readable.push(res)) {
        nextSyncWithAsyncValues();
        return;
      }
      reading = false;
    } catch (err) {
      readable.destroy(err);
    }
  }
  async function nextSyncWithAsyncValues() {
    for (;;) {
      try {
        const { value, done } = iterator.next();
        if (done) {
          readable.push(null);
          return;
        }
        const res = (value && typeof value.then === "function") ? await value : value;
        if (res === null) {
          reading = false;
          throw new ERR_STREAM_NULL_VALUES();
        }
        if (readable.push(res)) continue;
        reading = false;
      } catch (err) {
        readable.destroy(err);
      }
      break;
    }
  }
  async function nextAsync() {
    for (;;) {
      try {
        const { value, done } = await iterator.next();
        if (done) {
          readable.push(null);
          return;
        }
        if (value === null) {
          reading = false;
          throw new ERR_STREAM_NULL_VALUES();
        }
        if (readable.push(value)) continue;
        reading = false;
      } catch (err) {
        readable.destroy(err);
      }
      break;
    }
  }
  return readable;
}
`
