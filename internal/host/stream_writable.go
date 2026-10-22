package host

// streamWritableJS: Writable and WritableState, following Node's
// lib/internal/streams/writable.js: write()/end() drive the subclass's
// own _write/_writev/_final override points (real code - smithy's
// Collector, fast-glob's instance-level "stream._write = ..." override -
// relies on that), with buffering while a write is in flight or the
// stream is corked, _writev batching of the buffered chunks, 'drain'
// backpressure, prefinish/finish, and autoDestroy.
const streamWritableJS = `
function WritableState(options, stream, isDuplex) {
  this.objectMode = !!(options && options.objectMode);
  if (isDuplex && options && options.writableObjectMode) this.objectMode = true;
  this.highWaterMark = options ?
    getHighWaterMark(this, options, "writableHighWaterMark", isDuplex) :
    getDefaultHighWaterMark(false);
  this.finalCalled = false;
  this.needDrain = false;
  this.ending = false;
  this.ended = false;
  this.finished = false;
  this.destroyed = false;
  this.decodeStrings = !(options && options.decodeStrings === false);
  const defaultEncoding = options ? options.defaultEncoding : null;
  if (defaultEncoding == null || defaultEncoding === "utf8" || defaultEncoding === "utf-8") {
    this.defaultEncoding = "utf8";
  } else if (Buffer.isEncoding(defaultEncoding)) {
    this.defaultEncoding = defaultEncoding;
  } else {
    throw new ERR_UNKNOWN_ENCODING(defaultEncoding);
  }
  this.length = 0;
  this.writing = false;
  this.corked = 0;
  this.sync = true;
  this.bufferProcessing = false;
  this.onwrite = (er) => onwrite(stream, er);
  this.writecb = null;
  this.writelen = 0;
  this.expectWriteCb = false;
  this.afterWriteTickInfo = null;
  this.afterWritePending = false;
  resetBuffer(this);
  this.pendingcb = 0;
  this.constructed = true;
  this.prefinished = false;
  this.errorEmitted = false;
  this.emitClose = !(options && options.emitClose === false);
  this.autoDestroy = !(options && options.autoDestroy === false);
  this.errored = null;
  this.closed = false;
  this.closeEmitted = false;
  this.onFinished = [];
}

function resetBuffer(state) {
  state.buffered = [];
  state.bufferedIndex = 0;
  state.allBuffers = true;
  state.allNoop = true;
}

WritableState.prototype.getBuffer = function getBuffer() {
  return this.buffered.slice(this.bufferedIndex);
};
Object.defineProperty(WritableState.prototype, "bufferedRequestCount", {
  get() { return this.buffered.length - this.bufferedIndex; },
});

WritableState.prototype[kOnConstructed] = function onConstructed(stream) {
  if (!this.writing) clearBuffer(stream, this);
  if (this.ending) finishMaybe(stream, this);
};

function Writable(options) {
  if (!(this instanceof Writable)) return new Writable(options);
  this._writableState = new WritableState(options, this, false);
  if (options) {
    if (typeof options.write === "function") this._write = options.write;
    if (typeof options.writev === "function") this._writev = options.writev;
    if (typeof options.destroy === "function") this._destroy = options.destroy;
    if (typeof options.final === "function") this._final = options.final;
    if (typeof options.construct === "function") this._construct = options.construct;
    if (options.signal) addAbortSignal(options.signal, this);
  }
  Stream.call(this, options);
  if (this._construct != null) {
    construct(this, () => {
      this._writableState[kOnConstructed](this);
    });
  }
}
Object.setPrototypeOf(Writable.prototype, Stream.prototype);
Object.setPrototypeOf(Writable, Stream);
Writable.WritableState = WritableState;

// Node makes "duplex instanceof Writable" true via Symbol.hasInstance.
Object.defineProperty(Writable, Symbol.hasInstance, {
  value: function (object) {
    if (Function.prototype[Symbol.hasInstance].call(this, object)) return true;
    if (this !== Writable) return false;
    return !!(object && object._writableState instanceof WritableState);
  },
});

Writable.prototype.pipe = function () {
  errorOrDestroy(this, new ERR_STREAM_CANNOT_PIPE());
};

function _write(stream, chunk, encoding, cb) {
  const state = stream._writableState;
  if (cb == null || typeof cb !== "function") cb = nop;
  if (chunk === null) throw new ERR_STREAM_NULL_VALUES();
  if (!state.objectMode) {
    if (!encoding) encoding = state.defaultEncoding;
    else if (encoding !== "buffer" && !Buffer.isEncoding(encoding)) throw new ERR_UNKNOWN_ENCODING(encoding);
    if (typeof chunk === "string") {
      if (encoding === "buffer") throw new ERR_UNKNOWN_ENCODING(encoding);
      if (state.decodeStrings) {
        chunk = Buffer.from(chunk, encoding);
        encoding = "buffer";
      }
    } else if (chunk instanceof Buffer) {
      encoding = "buffer";
    } else if (isArrayBufferView(chunk)) {
      chunk = uint8ArrayToBuffer(chunk);
      encoding = "buffer";
    } else {
      throw new ERR_INVALID_ARG_TYPE("chunk", ["string", "Buffer", "TypedArray", "DataView"], chunk);
    }
  }
  let err;
  if (state.ending) err = new ERR_STREAM_WRITE_AFTER_END();
  else if (state.destroyed) err = new ERR_STREAM_DESTROYED("write");
  if (err) {
    process.nextTick(cb, err);
    errorOrDestroy(stream, err, true);
    return err;
  }
  state.pendingcb++;
  return writeOrBuffer(stream, state, chunk, encoding, cb);
}

Writable.prototype.write = function (chunk, encoding, cb) {
  if (encoding != null && typeof encoding === "function") {
    cb = encoding;
    encoding = null;
  }
  return _write(this, chunk, encoding, cb) === true;
};

Writable.prototype.cork = function () {
  this._writableState.corked++;
};

Writable.prototype.uncork = function () {
  const state = this._writableState;
  if (state.corked) {
    state.corked--;
    if (!state.writing) clearBuffer(this, state);
  }
};

Writable.prototype.setDefaultEncoding = function setDefaultEncoding(encoding) {
  if (typeof encoding === "string") encoding = encoding.toLowerCase();
  if (!Buffer.isEncoding(encoding)) throw new ERR_UNKNOWN_ENCODING(encoding);
  this._writableState.defaultEncoding = encoding;
  return this;
};

function writeOrBuffer(stream, state, chunk, encoding, callback) {
  const len = state.objectMode ? 1 : chunk.length;
  state.length += len;
  if (state.writing || state.errored || state.corked || !state.constructed) {
    state.buffered.push({ chunk, encoding, callback });
    if (state.allBuffers && encoding !== "buffer") state.allBuffers = false;
    if (state.allNoop && callback !== nop) state.allNoop = false;
  } else {
    state.writelen = len;
    state.writecb = callback;
    state.writing = true;
    state.sync = true;
    state.expectWriteCb = true;
    stream._write(chunk, encoding, state.onwrite);
    state.sync = false;
  }
  const ret = state.length < state.highWaterMark || state.length === 0;
  if (!ret) state.needDrain = true;
  return ret && !state.destroyed && !state.errored;
}

function doWrite(stream, state, writev, len, chunk, encoding, cb) {
  state.writelen = len;
  state.writecb = cb;
  state.writing = true;
  state.sync = true;
  state.expectWriteCb = true;
  if (state.destroyed) state.onwrite(new ERR_STREAM_DESTROYED("write"));
  else if (writev) stream._writev(chunk, state.onwrite);
  else stream._write(chunk, encoding, state.onwrite);
  state.sync = false;
}

function onwriteError(stream, state, er, cb) {
  --state.pendingcb;
  cb(er);
  errorBuffer(state);
  errorOrDestroy(stream, er);
}

function onwrite(stream, er) {
  const state = stream._writableState;
  if (!state.expectWriteCb) {
    errorOrDestroy(stream, new ERR_MULTIPLE_CALLBACK());
    return;
  }
  const sync = state.sync;
  const cb = state.writecb || nop;
  state.writecb = null;
  state.writing = false;
  state.expectWriteCb = false;
  state.length -= state.writelen;
  state.writelen = 0;
  if (er) {
    if (!state.errored) state.errored = er;
    if (stream._readableState && !stream._readableState.errored) stream._readableState.errored = er;
    if (sync) process.nextTick(onwriteError, stream, state, er, cb);
    else onwriteError(stream, state, er, cb);
  } else {
    if (state.buffered.length > state.bufferedIndex) clearBuffer(stream, state);
    if (sync) {
      const needDrain = state.needDrain && state.length === 0;
      // Node's own condition here also reads the objectMode bit (an
      // operator-precedence quirk upstream); mirrored for identical timing.
      const needTick = needDrain || state.objectMode || cb !== nop;
      if (cb === nop) {
        if (!state.afterWritePending && needTick) {
          process.nextTick(afterWrite, stream, state, 1, cb);
          state.afterWritePending = true;
        } else {
          state.pendingcb--;
          if (state.ending) finishMaybe(stream, state, true);
        }
      } else if (state.afterWriteTickInfo !== null && state.afterWriteTickInfo.cb === cb) {
        state.afterWriteTickInfo.count++;
      } else if (needTick) {
        state.afterWriteTickInfo = { count: 1, cb, stream, state };
        process.nextTick(afterWriteTick, state.afterWriteTickInfo);
        state.afterWritePending = true;
      } else {
        state.pendingcb--;
        if (state.ending) finishMaybe(stream, state, true);
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
  state.afterWritePending = false;
  const needDrain = !state.ending && !state.destroyed && state.needDrain && state.length === 0;
  if (needDrain) {
    state.needDrain = false;
    stream.emit("drain");
  }
  while (count-- > 0) {
    state.pendingcb--;
    cb(null);
  }
  if (state.destroyed) errorBuffer(state);
  if (state.ending) finishMaybe(stream, state, true);
}

function errorBuffer(state) {
  if (state.writing) return;
  for (let n = state.bufferedIndex; n < state.buffered.length; ++n) {
    const { chunk, callback } = state.buffered[n];
    const len = state.objectMode ? 1 : chunk.length;
    state.length -= len;
    callback(state.errored ?? new ERR_STREAM_DESTROYED("write"));
  }
  callFinishedCallbacks(state, state.errored ?? new ERR_STREAM_DESTROYED("end"));
  resetBuffer(state);
}

function clearBuffer(stream, state) {
  if (state.corked || state.bufferProcessing || state.destroyed || !state.constructed) return;
  const { buffered, bufferedIndex, objectMode } = state;
  const bufferedLength = buffered.length - bufferedIndex;
  if (!bufferedLength) return;
  let i = bufferedIndex;
  state.bufferProcessing = true;
  if (bufferedLength > 1 && stream._writev) {
    state.pendingcb -= bufferedLength - 1;
    const callback = state.allNoop ? nop : (err) => {
      for (let n = i; n < buffered.length; ++n) buffered[n].callback(err);
    };
    const chunks = state.allNoop && i === 0 ? buffered : buffered.slice(i);
    chunks.allBuffers = state.allBuffers;
    doWrite(stream, state, true, state.length, chunks, "", callback);
    resetBuffer(state);
  } else {
    do {
      const { chunk, encoding, callback } = buffered[i];
      buffered[i++] = null;
      const len = objectMode ? 1 : chunk.length;
      doWrite(stream, state, false, len, chunk, encoding, callback);
    } while (i < buffered.length && !state.writing);
    if (i === buffered.length) {
      resetBuffer(state);
    } else if (i > 256) {
      buffered.splice(0, i);
      state.bufferedIndex = 0;
    } else {
      state.bufferedIndex = i;
    }
  }
  state.bufferProcessing = false;
}

Writable.prototype._write = function (chunk, encoding, cb) {
  if (this._writev) this._writev([{ chunk, encoding }], cb);
  else throw new ERR_METHOD_NOT_IMPLEMENTED("_write()");
};
Writable.prototype._writev = null;

Writable.prototype.end = function (chunk, encoding, cb) {
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
  if (chunk != null) {
    const ret = _write(this, chunk, encoding);
    if (ret instanceof Error) err = ret;
  }
  if (state.corked) {
    state.corked = 1;
    this.uncork();
  }
  if (err) {
    // The write already failed.
  } else if (!state.ending && !state.errored) {
    state.ending = true;
    finishMaybe(this, state, true);
    state.ended = true;
  } else if (state.finished) {
    err = new ERR_STREAM_ALREADY_FINISHED("end");
  } else if (state.destroyed) {
    err = new ERR_STREAM_DESTROYED("end");
  }
  if (typeof cb === "function") {
    if (err) process.nextTick(cb, err);
    else if (state.errored) process.nextTick(cb, state.errored);
    else if (state.finished) process.nextTick(cb, null);
    else state.onFinished.push(cb);
  }
  return this;
};

function needFinish(state) {
  return state.ending && !state.destroyed && state.constructed && !state.finished &&
    !state.writing && !state.errorEmitted && !state.closeEmitted && !state.errored &&
    state.buffered.length === state.bufferedIndex && state.length === 0;
}

function onFinish(stream, state, err) {
  if (state.prefinished) {
    errorOrDestroy(stream, err ?? new ERR_MULTIPLE_CALLBACK());
    return;
  }
  state.pendingcb--;
  if (err) {
    callFinishedCallbacks(state, err);
    errorOrDestroy(stream, err, state.sync);
  } else if (needFinish(state)) {
    state.prefinished = true;
    stream.emit("prefinish");
    state.pendingcb++;
    process.nextTick(finish, stream, state);
  }
}

function prefinish(stream, state) {
  if (state.prefinished || state.finalCalled) return;
  if (typeof stream._final === "function" && !state.destroyed) {
    state.finalCalled = true;
    state.sync = true;
    state.pendingcb++;
    try {
      stream._final((err) => onFinish(stream, state, err));
    } catch (err) {
      onFinish(stream, state, err);
    }
    state.sync = false;
  } else {
    state.finalCalled = true;
    state.prefinished = true;
    stream.emit("prefinish");
  }
}

function finishMaybe(stream, state, sync) {
  if (needFinish(state)) {
    prefinish(stream, state);
    if (state.pendingcb === 0) {
      if (sync) {
        state.pendingcb++;
        process.nextTick((stream, state) => {
          if (needFinish(state)) finish(stream, state);
          else state.pendingcb--;
        }, stream, state);
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
  callFinishedCallbacks(state, null);
  stream.emit("finish");
  if (state.autoDestroy) {
    const rState = stream._readableState;
    const autoDestroy = !rState || (rState.autoDestroy && (rState.endEmitted || rState.readable === false));
    if (autoDestroy) stream.destroy();
  }
}

function callFinishedCallbacks(state, err) {
  if (state.onFinished.length === 0) return;
  const callbacks = state.onFinished;
  state.onFinished = [];
  for (let i = 0; i < callbacks.length; i++) callbacks[i](err);
}

Object.defineProperties(Writable.prototype, {
  closed: {
    get() { return this._writableState ? this._writableState.closed : false; },
  },
  destroyed: {
    get() { return this._writableState ? this._writableState.destroyed : false; },
    set(value) {
      if (this._writableState) this._writableState.destroyed = value;
    },
  },
  writable: {
    get() {
      const w = this._writableState;
      return !!w && w.writable !== false && !w.ending && !w.ended && !w.destroyed && !w.errored;
    },
    set(val) {
      if (this._writableState) this._writableState.writable = !!val;
    },
  },
  writableFinished: {
    get() { return this._writableState ? this._writableState.finished : false; },
  },
  writableObjectMode: {
    get() { return this._writableState ? this._writableState.objectMode : false; },
  },
  writableBuffer: {
    get() { return this._writableState && this._writableState.getBuffer(); },
  },
  writableEnded: {
    get() { return this._writableState ? this._writableState.ending : false; },
  },
  writableNeedDrain: {
    get() {
      const w = this._writableState;
      return w ? !w.destroyed && !w.ending && w.needDrain : false;
    },
  },
  writableHighWaterMark: {
    get() { return this._writableState && this._writableState.highWaterMark; },
  },
  writableCorked: {
    get() { return this._writableState ? this._writableState.corked : 0; },
  },
  writableLength: {
    get() { return this._writableState && this._writableState.length; },
  },
  errored: {
    get() { return this._writableState ? this._writableState.errored : null; },
  },
  writableAborted: {
    get() {
      const w = this._writableState;
      return !!(w.writable !== false && (w.destroyed || w.errored) && !w.finished);
    },
  },
});

Writable.prototype.destroy = function (err, cb) {
  const state = this._writableState;
  if (!state.destroyed && (state.buffered.length > state.bufferedIndex || state.onFinished.length)) {
    process.nextTick(errorBuffer, state);
  }
  destroyStream.call(this, err, cb);
  return this;
};
Writable.prototype._undestroy = undestroy;
Writable.prototype._destroy = function (err, cb) {
  cb(err);
};
Writable.prototype[EventEmitter.captureRejectionSymbol] = function (err) {
  this.destroy(err);
};
Writable.prototype[Symbol.asyncDispose] = async function () {
  let error;
  if (!this.destroyed) {
    error = this.writableFinished ? null : new AbortError();
    this.destroy(error);
  }
  await new Promise((resolve, reject) =>
    eos(this, (err) => (err && err.name !== "AbortError" ? reject(err) : resolve(null))));
};

Writable.fromWeb = function (writableStream, options) {
  return newStreamWritableFromWritableStream(writableStream, options);
};
Writable.toWeb = function (streamWritable) {
  return newWritableStreamFromStreamWritable(streamWritable);
};
`
