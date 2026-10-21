package host

// streamPipelineJS: finished()/eos, addAbortSignal, pipeline (callback
// and promise forms, including generator-function stages and web
// streams), compose and Duplex.from (duplexify), following
// lib/internal/streams/{end-of-stream,add-abort-signal,pipeline,compose,
// duplexify}.js.
const streamPipelineJS = `
// ---- finished / eos ----------------------------------------------------

function getEosErrored(stream) {
  const errored = isWritableErrored(stream) || isReadableErrored(stream);
  return (typeof errored !== "boolean" && errored) || null;
}

function getEosOnCloseError(stream, readable, readableFinished, writable, writableFinished) {
  const errored = getEosErrored(stream);
  if (errored) return errored;
  if (readable && !readableFinished && isReadableNodeStream(stream, true)) {
    if (!isReadableFinished(stream, false)) return new ERR_STREAM_PREMATURE_CLOSE();
  }
  if (writable && !writableFinished) {
    if (!isWritableFinished(stream, false)) return new ERR_STREAM_PREMATURE_CLOSE();
  }
  return null;
}

function eos(stream, options, callback) {
  if (arguments.length === 2) {
    callback = options;
    options = kEmptyObject;
  } else if (options == null) {
    options = kEmptyObject;
  } else {
    validateObject(options, "options");
  }
  validateFunction(callback, "callback");
  validateAbortSignal(options.signal, "options.signal");

  if (isReadableStream(stream) || isWritableStream(stream)) return eosWeb(stream, options, callback);
  if (!isNodeStream(stream)) {
    throw new ERR_INVALID_ARG_TYPE("stream", ["ReadableStream", "WritableStream", "Stream"], stream);
  }

  const readable = options.readable ?? isReadableNodeStream(stream);
  const writable = options.writable ?? isWritableNodeStream(stream);
  const wState = stream._writableState;
  const rState = stream._readableState;

  let willClose = willEmitClose(stream) &&
    isReadableNodeStream(stream) === readable &&
    isWritableNodeStream(stream) === writable;
  let writableFinished = isWritableFinished(stream, false);
  let readableFinished = isReadableFinished(stream, false);

  // undefined: to be determined; null: no error; Error: errored.
  let immediateResult;
  if (isClosed(stream)) {
    immediateResult = getEosOnCloseError(stream, readable, readableFinished, writable, writableFinished);
  } else if ((wState && wState.errorEmitted) || (rState && rState.errorEmitted)) {
    if (!willClose) immediateResult = getEosErrored(stream);
  } else if (!readable &&
             (!willClose || isReadable(stream)) &&
             (writableFinished || isWritable(stream) === false) &&
             (wState == null || wState.pendingcb === undefined || wState.pendingcb === 0)) {
    immediateResult = getEosErrored(stream);
  } else if (!writable &&
             (!willClose || isWritable(stream)) &&
             (readableFinished || isReadable(stream) === false)) {
    immediateResult = getEosErrored(stream);
  } else if (rState && stream.req && stream.aborted) {
    immediateResult = getEosErrored(stream);
  }

  let cleanup = () => {
    callback = nop;
  };
  if (immediateResult !== undefined) {
    if (options.error !== false) {
      stream.on("error", nop);
      cleanup = () => {
        callback = nop;
        stream.removeListener("error", nop);
      };
    }
  } else if (options.signal && options.signal.aborted) {
    immediateResult = new AbortError(undefined, { cause: options.signal.reason });
  }
  if (immediateResult !== undefined) {
    process.nextTick(() => Reflect.apply(callback, stream, immediateResult === null ? [] : [immediateResult]));
    return cleanup;
  }

  callback = once(callback);

  const onlegacyfinish = () => {
    if (!stream.writable) onfinish();
  };
  const onfinish = () => {
    writableFinished = true;
    if (stream.destroyed) willClose = false;
    if (willClose && (!stream.readable || readable)) return;
    if (!readable || readableFinished) callback.call(stream);
  };
  const onend = () => {
    readableFinished = true;
    if (stream.destroyed) willClose = false;
    if (willClose && (!stream.writable || writable)) return;
    if (!writable || writableFinished) callback.call(stream);
  };
  const onerror = (err) => {
    callback.call(stream, err);
  };
  const onclose = () => {
    const error = getEosOnCloseError(stream, readable, readableFinished, writable, writableFinished);
    if (error === null) callback.call(stream);
    else callback.call(stream, error);
  };
  const onrequest = () => {
    stream.req.on("finish", onfinish);
  };

  if (isRequest(stream)) {
    stream.on("complete", onfinish);
    if (!willClose) stream.on("abort", onclose);
    if (stream.req) onrequest();
    else stream.on("request", onrequest);
  } else if (writable && !wState) {
    stream.on("end", onlegacyfinish);
    stream.on("close", onlegacyfinish);
  }
  if (!willClose && typeof stream.aborted === "boolean") stream.on("aborted", onclose);
  stream.on("end", onend);
  stream.on("finish", onfinish);
  if (options.error !== false) stream.on("error", onerror);
  stream.on("close", onclose);

  cleanup = () => {
    callback = nop;
    stream.removeListener("aborted", onclose);
    stream.removeListener("complete", onfinish);
    stream.removeListener("abort", onclose);
    stream.removeListener("request", onrequest);
    if (stream.req) stream.req.removeListener("finish", onfinish);
    stream.removeListener("end", onlegacyfinish);
    stream.removeListener("close", onlegacyfinish);
    stream.removeListener("finish", onfinish);
    stream.removeListener("end", onend);
    stream.removeListener("error", onerror);
    stream.removeListener("close", onclose);
  };

  if (options.signal) {
    const signal = options.signal;
    const abort = () => {
      const endCallback = callback;
      cleanup();
      endCallback.call(stream, new AbortError(undefined, { cause: signal.reason }));
    };
    const disposable = addAbortListener(signal, abort);
    const originalCallback = callback;
    callback = once((...args) => {
      disposable[Symbol.dispose]();
      Reflect.apply(originalCallback, stream, args);
    });
  }
  return cleanup;
}

// Web streams expose no observable "closed" without locking them, so a
// web stream's completion is observed through the hooks that
// Readable.toWeb/fromWeb and pipeline install (kWebClosed), and is
// otherwise reported on the next tick of an explicit cancel/abort.
const kWebClosed = Symbol("kWebClosed");
function eosWeb(stream, options, callback) {
  callback = once(callback);
  let isAborted = false;
  if (options.signal) {
    const signal = options.signal;
    const abort = () => {
      isAborted = true;
      callback.call(stream, new AbortError(undefined, { cause: signal.reason }));
    };
    if (signal.aborted) {
      process.nextTick(abort);
    } else {
      const disposable = addAbortListener(signal, abort);
      const originalCallback = callback;
      callback = once((...args) => {
        disposable[Symbol.dispose]();
        Reflect.apply(originalCallback, stream, args);
      });
    }
  }
  const resolverFn = (...args) => {
    if (!isAborted) process.nextTick(() => Reflect.apply(callback, stream, args));
  };
  const closed = stream[kWebClosed];
  if (closed && typeof closed.then === "function") closed.then(() => resolverFn(), resolverFn);
  return nop;
}

function finished(stream, opts) {
  let autoCleanup = false;
  if (opts === null) opts = kEmptyObject;
  if (opts && opts.cleanup) {
    validateBoolean(opts.cleanup, "cleanup");
    autoCleanup = opts.cleanup;
  }
  return new Promise((resolve, reject) => {
    const cleanup = eos(stream, opts, (err) => {
      if (autoCleanup) cleanup();
      if (err) reject(err);
      else resolve();
    });
  });
}

// ---- addAbortSignal ------------------------------------------------------

function addAbortSignal(signal, stream) {
  if (typeof signal !== "object" || signal === null || !("aborted" in signal)) {
    throw new ERR_INVALID_ARG_TYPE("signal", "AbortSignal", signal);
  }
  if (!isNodeStream(stream) && !isWebStream(stream)) {
    throw new ERR_INVALID_ARG_TYPE("stream", ["ReadableStream", "WritableStream", "Stream"], stream);
  }
  return addAbortSignalNoValidate(signal, stream);
}

function addAbortSignalNoValidate(signal, stream) {
  if (typeof signal !== "object" || signal === null || !("aborted" in signal)) return stream;
  const onAbort = isNodeStream(stream) ?
    () => {
      stream.destroy(new AbortError(undefined, { cause: signal.reason }));
    } :
    () => {
      const err = new AbortError(undefined, { cause: signal.reason });
      if (isReadableStream(stream)) stream.cancel(err).catch(nop);
      else if (isWritableStream(stream)) stream.abort(err).catch(nop);
    };
  if (signal.aborted) {
    onAbort();
  } else {
    const disposable = addAbortListener(signal, onAbort);
    eos(stream, disposable[Symbol.dispose]);
  }
  return stream;
}

// ---- pipeline ------------------------------------------------------------

function pipelineDestroyer(stream, reading, writing) {
  let isFinished = false;
  stream.on("close", () => {
    isFinished = true;
  });
  const cleanup = eos(stream, { readable: reading, writable: writing }, (err) => {
    isFinished = !err;
  });
  return {
    destroy: (err) => {
      if (isFinished) return;
      isFinished = true;
      destroyer(stream, err || new ERR_STREAM_DESTROYED("pipe"));
    },
    cleanup,
  };
}

function popCallback(streams) {
  validateFunction(streams[streams.length - 1], "streams[stream.length - 1]");
  return streams.pop();
}

function makeAsyncIterable(val) {
  if (isIterable(val)) return val;
  if (isReadableNodeStream(val)) return fromReadable(val);
  throw new ERR_INVALID_ARG_TYPE("val", ["Readable", "Iterable", "AsyncIterable"], val);
}

async function* fromReadable(val) {
  yield* Readable.prototype[Symbol.asyncIterator].call(val);
}

async function pumpToNode(iterable, writable, finish, { end }) {
  let error;
  let onresolve = null;
  const resume = (err) => {
    if (err) error = err;
    if (onresolve) {
      const callback = onresolve;
      onresolve = null;
      callback();
    }
  };
  const wait = () => new Promise((resolve, reject) => {
    if (error) {
      reject(error);
    } else {
      onresolve = () => {
        if (error) reject(error);
        else resolve();
      };
    }
  });
  writable.on("drain", resume);
  const cleanup = eos(writable, { readable: false }, resume);
  try {
    if (writable.writableNeedDrain) await wait();
    for await (const chunk of iterable) {
      if (!writable.write(chunk)) await wait();
    }
    if (end) {
      writable.end();
      await wait();
    }
    finish();
  } catch (err) {
    finish(error !== err ? aggregateTwoErrors(error, err) : err);
  } finally {
    cleanup();
    writable.off("drain", resume);
  }
}

async function pumpToWeb(readable, writable, finish, { end }) {
  if (isTransformStream(writable)) writable = writable.writable;
  const writer = writable.getWriter();
  try {
    for await (const chunk of readable) {
      await writer.ready;
      writer.write(chunk).catch(nop);
    }
    await writer.ready;
    if (end) await writer.close();
    finish();
  } catch (err) {
    try {
      await writer.abort(err);
      finish(err);
    } catch (err2) {
      finish(err2);
    }
  }
}

// web ReadableStreams are async-iterable in Node; paserati's may not be.
function webReadableIterable(rs) {
  if (typeof rs[Symbol.asyncIterator] === "function") return rs;
  return {
    async *[Symbol.asyncIterator]() {
      const reader = rs.getReader();
      try {
        for (;;) {
          const { value, done } = await reader.read();
          if (done) return;
          yield value;
        }
      } finally {
        reader.releaseLock();
      }
    },
  };
}

function pipeline(...streams) {
  return pipelineImpl(streams, once(popCallback(streams)));
}

function pipelineImpl(streams, callback, opts) {
  if (streams.length === 1 && Array.isArray(streams[0])) streams = streams[0];
  if (streams.length < 2) throw new ERR_MISSING_ARGS("streams");

  const ac = new AbortController();
  const signal = ac.signal;
  const outerSignal = opts && opts.signal;
  const lastStreamCleanup = [];
  validateAbortSignal(outerSignal, "options.signal");

  function abort() {
    finishImpl(new AbortError(undefined, { cause: outerSignal && outerSignal.reason }));
  }
  let disposable;
  if (outerSignal) disposable = addAbortListener(outerSignal, abort);

  let error;
  let value;
  const destroys = [];
  let finishCount = 0;

  function finish(err) {
    finishImpl(err, --finishCount === 0);
  }
  function finishOnlyHandleError(err) {
    finishImpl(err, false);
  }
  function finishImpl(err, final) {
    if (err && (!error || error.code === "ERR_STREAM_PREMATURE_CLOSE" || error.name === "AbortError")) error = err;
    if (!error && !final) return;
    while (destroys.length) destroys.shift()(error);
    if (disposable) disposable[Symbol.dispose]();
    ac.abort();
    if (final) {
      if (!error) lastStreamCleanup.forEach((fn) => fn());
      process.nextTick(callback, error, value);
    }
  }

  let ret;
  for (let i = 0; i < streams.length; i++) {
    const stream = streams[i];
    const reading = i < streams.length - 1;
    const writing = i > 0;
    const next = i + 1 < streams.length ? streams[i + 1] : null;
    const end = reading || !opts || opts.end !== false;
    const isLastStream = i === streams.length - 1;

    if (isNodeStream(stream)) {
      if (next !== null && next && (next.closed || next.destroyed)) throw new ERR_STREAM_UNABLE_TO_PIPE();
      if (end) {
        const { destroy, cleanup } = pipelineDestroyer(stream, reading, writing);
        destroys.push(destroy);
        if (isReadable(stream) && isLastStream) lastStreamCleanup.push(cleanup);
      }
      function onError(err) {
        if (err && err.name !== "AbortError" && err.code !== "ERR_STREAM_PREMATURE_CLOSE") {
          finishOnlyHandleError(err);
        }
      }
      stream.on("error", onError);
      if (isReadable(stream) && isLastStream) {
        lastStreamCleanup.push(() => {
          stream.removeListener("error", onError);
        });
      }
    }

    if (i === 0) {
      if (typeof stream === "function") {
        ret = stream({ signal });
        if (!isIterable(ret)) throw new ERR_INVALID_RETURN_VALUE("Iterable, AsyncIterable or Stream", "source", ret);
      } else if (isIterable(stream) || isReadableNodeStream(stream) || isTransformStream(stream)) {
        ret = stream;
      } else if (isReadableStream(stream)) {
        ret = webReadableIterable(stream);
      } else {
        ret = Duplex.from(stream);
      }
    } else if (typeof stream === "function") {
      if (isTransformStream(ret)) ret = makeAsyncIterable(webReadableIterable(ret.readable));
      else ret = makeAsyncIterable(ret);
      ret = stream(ret, { signal });
      if (reading) {
        if (!isIterable(ret, true)) throw new ERR_INVALID_RETURN_VALUE("AsyncIterable", "transform[" + (i - 1) + "]", ret);
      } else {
        const pt = new PassThrough({ objectMode: true });
        const then = ret && ret.then;
        if (typeof then === "function") {
          finishCount++;
          then.call(ret, (val) => {
            value = val;
            if (val != null) pt.write(val);
            if (end) pt.end();
            process.nextTick(finish);
          }, (err) => {
            pt.destroy(err);
            process.nextTick(finish, err);
          });
        } else if (isIterable(ret, true)) {
          finishCount++;
          pumpToNode(ret, pt, finish, { end });
        } else if (isReadableStream(ret) || isTransformStream(ret)) {
          const toRead = ret.readable || ret;
          finishCount++;
          pumpToNode(webReadableIterable(toRead), pt, finish, { end });
        } else {
          throw new ERR_INVALID_RETURN_VALUE("AsyncIterable or Promise", "destination", ret);
        }
        ret = pt;
        const { destroy, cleanup } = pipelineDestroyer(ret, false, true);
        destroys.push(destroy);
        if (isLastStream) lastStreamCleanup.push(cleanup);
      }
    } else if (isNodeStream(stream)) {
      if (isReadableNodeStream(ret)) {
        finishCount += 2;
        const cleanup = pipe(ret, stream, finish, finishOnlyHandleError, { end });
        if (isReadable(stream) && isLastStream) lastStreamCleanup.push(cleanup);
      } else if (isTransformStream(ret) || isReadableStream(ret)) {
        const toRead = ret.readable || ret;
        finishCount++;
        pumpToNode(webReadableIterable(toRead), stream, finish, { end });
      } else if (isIterable(ret)) {
        finishCount++;
        pumpToNode(ret, stream, finish, { end });
      } else {
        throw new ERR_INVALID_ARG_TYPE("val", ["Readable", "Iterable", "AsyncIterable", "ReadableStream", "TransformStream"], ret);
      }
      ret = stream;
    } else if (isWebStream(stream)) {
      if (isReadableNodeStream(ret)) {
        finishCount++;
        pumpToWeb(makeAsyncIterable(ret), stream, finish, { end });
      } else if (isReadableStream(ret)) {
        finishCount++;
        pumpToWeb(webReadableIterable(ret), stream, finish, { end });
      } else if (isIterable(ret)) {
        finishCount++;
        pumpToWeb(ret, stream, finish, { end });
      } else if (isTransformStream(ret)) {
        finishCount++;
        pumpToWeb(webReadableIterable(ret.readable), stream, finish, { end });
      } else {
        throw new ERR_INVALID_ARG_TYPE("val", ["Readable", "Iterable", "AsyncIterable", "ReadableStream", "TransformStream"], ret);
      }
      ret = stream;
    } else {
      ret = Duplex.from(stream);
    }
  }

  if ((signal && signal.aborted) || (outerSignal && outerSignal.aborted)) process.nextTick(abort);
  return ret;
}

function pipe(src, dst, finish, finishOnlyHandleError, { end }) {
  let ended = false;
  dst.on("close", () => {
    if (!ended) finishOnlyHandleError(new ERR_STREAM_PREMATURE_CLOSE());
  });
  src.pipe(dst, { end: false });
  if (end) {
    function endFn() {
      ended = true;
      dst.end();
    }
    if (isReadableFinished(src)) process.nextTick(endFn);
    else src.once("end", endFn);
  } else {
    finish();
  }
  eos(src, { readable: true, writable: false }, (err) => {
    const rState = src._readableState;
    if (err && err.code === "ERR_STREAM_PREMATURE_CLOSE" &&
        rState && rState.ended && !rState.errored && !rState.errorEmitted) {
      src.once("end", finish).once("error", finish);
    } else {
      finish(err);
    }
  });
  return eos(dst, { readable: false, writable: true }, finish);
}

function pipelinePromise(...streams) {
  return new Promise((resolve, reject) => {
    let signal;
    let end;
    const lastArg = streams[streams.length - 1];
    if (lastArg && typeof lastArg === "object" && !isNodeStream(lastArg) && !isIterable(lastArg) && !isWebStream(lastArg)) {
      const options = streams.pop();
      signal = options.signal;
      end = options.end;
    }
    pipelineImpl(streams, (err, value) => {
      if (err) reject(err);
      else resolve(value);
    }, { signal, end });
  });
}

// ---- compose ---------------------------------------------------------------

function composeStreams(...streams) {
  if (streams.length === 0) throw new ERR_MISSING_ARGS("streams");
  if (streams.length === 1) return Duplex.from(streams[0]);
  const orgStreams = streams.slice();
  if (typeof streams[0] === "function") streams[0] = Duplex.from(streams[0]);
  if (typeof streams[streams.length - 1] === "function") {
    const idx = streams.length - 1;
    streams[idx] = Duplex.from(streams[idx]);
  }
  for (let n = 0; n < streams.length; ++n) {
    if (!isNodeStream(streams[n]) && !isWebStream(streams[n])) continue;
    if (n < streams.length - 1 &&
        !(isReadable(streams[n]) || isReadableStream(streams[n]) || isTransformStream(streams[n]))) {
      throw new ERR_INVALID_ARG_VALUE("streams[" + n + "]", orgStreams[n], "must be readable");
    }
    if (n > 0 &&
        !(isWritable(streams[n]) || isWritableStream(streams[n]) || isTransformStream(streams[n]))) {
      throw new ERR_INVALID_ARG_VALUE("streams[" + n + "]", orgStreams[n], "must be writable");
    }
  }

  let ondrain;
  let onfinish;
  let onclose;
  let d;

  function onfinished(err) {
    const cb = onclose;
    onclose = null;
    if (cb) cb(err);
    else if (err) d.destroy(err);
    else if (!readable && !writable) d.destroy();
  }

  const head = streams[0];
  const tail = pipeline(streams, onfinished);
  const writable = !!(isWritable(head) || isWritableStream(head) || isTransformStream(head));
  const readable = !!(isReadable(tail) || isReadableStream(tail) || isTransformStream(tail));

  d = new Duplex({
    writableObjectMode: !!(head && head.writableObjectMode),
    readableObjectMode: !!(tail && tail.readableObjectMode),
    writable,
    readable,
  });

  if (writable) {
    if (isNodeStream(head)) {
      d._write = function (chunk, encoding, callback) {
        if (head.write(chunk, encoding)) callback();
        else ondrain = callback;
      };
      d._final = function (callback) {
        head.end();
        onfinish = callback;
      };
      head.on("drain", function () {
        if (ondrain) {
          const cb = ondrain;
          ondrain = null;
          cb();
        }
      });
    } else if (isWebStream(head)) {
      const w = isTransformStream(head) ? head.writable : head;
      const writer = w.getWriter();
      d._write = async function (chunk, encoding, callback) {
        try {
          await writer.ready;
          writer.write(chunk).catch(nop);
          callback();
        } catch (err) {
          callback(err);
        }
      };
      d._final = async function (callback) {
        try {
          await writer.ready;
          writer.close().catch(nop);
          onfinish = callback;
        } catch (err) {
          callback(err);
        }
      };
    }
    const toRead = isTransformStream(tail) ? tail.readable : tail;
    if (isNodeStream(toRead)) {
      eos(toRead, () => {
        if (onfinish) {
          const cb = onfinish;
          onfinish = null;
          cb();
        }
      });
    }
  }

  if (readable) {
    if (isNodeStream(tail)) {
      d._read = function () {
        tail.resume();
      };
      tail.on("data", function (chunk) {
        if (!d.push(chunk)) tail.pause();
      });
      tail.on("end", function () {
        d.push(null);
      });
    } else if (isWebStream(tail)) {
      const r = isTransformStream(tail) ? tail.readable : tail;
      const reader = r.getReader();
      d._read = async function () {
        while (true) {
          try {
            const { value, done } = await reader.read();
            if (done) {
              d.push(null);
              if (onfinish) {
                const cb = onfinish;
                onfinish = null;
                cb();
              }
              return;
            }
            if (!d.push(value)) return;
          } catch {
            return;
          }
        }
      };
    }
  }

  d._destroy = function (err, callback) {
    if (!err && onclose !== null) err = new AbortError();
    ondrain = null;
    onfinish = null;
    if (isNodeStream(tail)) destroyer(tail, err);
    if (onclose === null) callback(err);
    else onclose = callback;
  };
  return d;
}

// ---- Duplex.from (duplexify) --------------------------------------------

function Duplexify(options) {
  Duplex.call(this, options);
  if (options && options.readable === false) {
    this._readableState.readable = false;
    this._readableState.ended = true;
    this._readableState.endEmitted = true;
  }
  if (options && options.writable === false) {
    this._writableState.writable = false;
    this._writableState.ending = true;
    this._writableState.ended = true;
    this._writableState.finished = true;
  }
}
Object.setPrototypeOf(Duplexify.prototype, Duplex.prototype);
Object.setPrototypeOf(Duplexify, Duplex);

function duplexify(body, name) {
  if (isDuplexNodeStream(body)) return body;
  if (isReadableNodeStream(body)) return _duplexify({ readable: body });
  if (isWritableNodeStream(body)) return _duplexify({ writable: body });
  if (isNodeStream(body)) return _duplexify({ writable: false, readable: false });
  if (isReadableStream(body)) return _duplexify({ readable: Readable.fromWeb(body) });
  if (isWritableStream(body)) return _duplexify({ writable: Writable.fromWeb(body) });
  if (typeof body === "function") {
    const { value, write, final, destroy } = fromAsyncGen(body);
    if (isDuplexNodeStream(value)) return value;
    if (isIterable(value)) {
      return readableFrom(Duplexify, value, { objectMode: true, write, final, destroy });
    }
    const then = value && value.then;
    if (typeof then === "function") {
      let d;
      const promise = then.call(value, (val) => {
        if (val != null) throw new ERR_INVALID_RETURN_VALUE("nully", "body", val);
      }, (err) => {
        destroyer(d, err);
      });
      return d = new Duplexify({
        objectMode: true,
        readable: false,
        write,
        final(cb) {
          final(async () => {
            try {
              await promise;
              process.nextTick(cb, null);
            } catch (err) {
              process.nextTick(cb, err);
            }
          });
        },
        destroy,
      });
    }
    throw new ERR_INVALID_RETURN_VALUE("Iterable, AsyncIterable or AsyncFunction", name, value);
  }
  if (isBlob(body)) return duplexify(body.arrayBuffer());
  if (isIterable(body)) return readableFrom(Duplexify, body, { objectMode: true, writable: false });
  if (body && isReadableStream(body.readable) && isWritableStream(body.writable)) {
    return Duplex.fromWeb(body);
  }
  if (body && (typeof body.writable === "object" || typeof body.readable === "object")) {
    const readable = body.readable ?
      (isReadableNodeStream(body.readable) ? body.readable : duplexify(body.readable)) : undefined;
    const writable = body.writable ?
      (isWritableNodeStream(body.writable) ? body.writable : duplexify(body.writable)) : undefined;
    return _duplexify({ readable, writable });
  }
  const then = body && body.then;
  if (typeof then === "function") {
    let d;
    then.call(body, (val) => {
      if (val != null) d.push(val);
      d.push(null);
    }, (err) => {
      destroyer(d, err);
    });
    return d = new Duplexify({ objectMode: true, writable: false, read() {} });
  }
  throw new ERR_INVALID_ARG_TYPE(name,
    ["Blob", "ReadableStream", "WritableStream", "Stream", "Iterable", "AsyncIterable", "Function", "{ readable, writable } pair", "Promise"],
    body);
}

function fromAsyncGen(fn) {
  let { promise, resolve } = Promise.withResolvers();
  const ac = new AbortController();
  const signal = ac.signal;
  const value = fn(async function* () {
    while (true) {
      const _promise = promise;
      promise = null;
      const { chunk, done, cb } = await _promise;
      process.nextTick(cb);
      if (done) return;
      if (signal.aborted) throw new AbortError(undefined, { cause: signal.reason });
      ({ promise, resolve } = Promise.withResolvers());
      yield chunk;
    }
  }(), { signal });
  return {
    value,
    write(chunk, encoding, cb) {
      const _resolve = resolve;
      resolve = null;
      _resolve({ chunk, done: false, cb });
    },
    final(cb) {
      const _resolve = resolve;
      resolve = null;
      _resolve({ done: true, cb });
    },
    destroy(err, cb) {
      ac.abort(err);
      if (resolve !== null) {
        const _resolve = resolve;
        resolve = null;
        _resolve({ done: true, cb() {} });
      }
      cb(err);
    },
  };
}

function _duplexify(pair) {
  const r = pair.readable && typeof pair.readable.read !== "function" ?
    Readable.wrap(pair.readable) : pair.readable;
  const w = pair.writable;
  let readable = !!isReadable(r);
  let writable = !!isWritable(w);
  let ondrain;
  let onfinish;
  let onreadable;
  let onclose;
  let d;

  function onfinished(err) {
    const cb = onclose;
    onclose = null;
    if (cb) cb(err);
    else if (err) d.destroy(err);
  }

  d = new Duplexify({
    readableObjectMode: !!(r && r.readableObjectMode),
    writableObjectMode: !!(w && w.writableObjectMode),
    readable,
    writable,
  });

  if (writable) {
    eos(w, (err) => {
      writable = false;
      if (err) destroyer(r, err);
      onfinished(err);
    });
    d._write = function (chunk, encoding, callback) {
      if (w.write(chunk, encoding)) callback();
      else ondrain = callback;
    };
    d._final = function (callback) {
      w.end();
      onfinish = callback;
    };
    w.on("drain", function () {
      if (ondrain) {
        const cb = ondrain;
        ondrain = null;
        cb();
      }
    });
    w.on("finish", function () {
      if (onfinish) {
        const cb = onfinish;
        onfinish = null;
        cb();
      }
    });
  }

  if (readable) {
    eos(r, (err) => {
      readable = false;
      if (err) destroyer(w, err);
      onfinished(err);
    });
    r.on("readable", function () {
      if (onreadable) {
        const cb = onreadable;
        onreadable = null;
        cb();
      }
    });
    r.on("end", function () {
      d.push(null);
    });
    d._read = function () {
      while (true) {
        const buf = r.read();
        if (buf === null) {
          onreadable = d._read;
          return;
        }
        if (!d.push(buf)) return;
      }
    };
  }

  d._destroy = function (err, callback) {
    if (!err && onclose !== null) err = new AbortError();
    onreadable = null;
    ondrain = null;
    onfinish = null;
    if (onclose === null) {
      callback(err);
    } else {
      onclose = callback;
      destroyer(w, err);
      destroyer(r, err);
    }
  };
  return d;
}
`
