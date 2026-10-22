package host

// streamOpsJS: the Readable iterator helpers (map/filter/forEach/toArray/
// some/every/find/reduce/flatMap/drop/take, lib/internal/streams/
// operators.js) and the Node<->web stream adapters behind
// Readable/Writable/Duplex .fromWeb/.toWeb (lib/internal/webstreams/
// adapters.js), built on the engine's own ReadableStream/WritableStream.
const streamOpsJS = `
// ---- operators -----------------------------------------------------------

const kEmpty = Symbol("kEmpty");
const kEof = Symbol("kEof");

function validateOperatorOptions(options) {
  if (options != null) validateObject(options, "options");
  if (options && options.signal != null) validateAbortSignal(options.signal, "options.signal");
}

function map(fn, options) {
  validateFunction(fn, "fn");
  validateOperatorOptions(options);
  let concurrency = 1;
  if (options && options.concurrency != null) concurrency = Math.floor(options.concurrency);
  let highWaterMark = concurrency - 1;
  if (options && options.highWaterMark != null) highWaterMark = Math.floor(options.highWaterMark);
  validateInteger(concurrency, "options.concurrency", 1);
  validateInteger(highWaterMark, "options.highWaterMark", 0);
  highWaterMark += concurrency;

  return async function* map() {
    const signal = AbortSignal.any([options && options.signal].filter(Boolean));
    const stream = this;
    const queue = [];
    const signalOpt = { signal };
    let next;
    let resume;
    let done = false;
    let cnt = 0;

    function onCatch() {
      done = true;
      afterItemProcessed();
    }
    function afterItemProcessed() {
      cnt -= 1;
      maybeResume();
    }
    function maybeResume() {
      if (resume && !done && cnt < concurrency && queue.length < highWaterMark) {
        resume();
        resume = null;
      }
    }

    async function pump() {
      try {
        for await (let val of stream) {
          if (done) return;
          if (signal.aborted) throw new AbortError();
          try {
            val = fn(val, signalOpt);
            if (val === kEmpty) continue;
            val = Promise.resolve(val);
          } catch (err) {
            val = Promise.reject(err);
          }
          cnt += 1;
          val.then(afterItemProcessed, onCatch);
          queue.push(val);
          if (next) {
            next();
            next = null;
          }
          if (!done && (queue.length >= highWaterMark || cnt >= concurrency)) {
            await new Promise((resolve) => {
              resume = resolve;
            });
          }
        }
        queue.push(kEof);
      } catch (err) {
        const val = Promise.reject(err);
        val.then(afterItemProcessed, onCatch);
        queue.push(val);
      } finally {
        done = true;
        if (next) {
          next();
          next = null;
        }
      }
    }

    pump();

    try {
      while (true) {
        while (queue.length > 0) {
          const val = await queue[0];
          if (val === kEof) return;
          if (signal.aborted) throw new AbortError();
          if (val !== kEmpty) yield val;
          queue.shift();
          maybeResume();
        }
        await new Promise((resolve) => {
          next = resolve;
        });
      }
    } finally {
      done = true;
      if (resume) {
        resume();
        resume = null;
      }
    }
  }.call(this);
}

async function some(fn, options) {
  for await (const unused of filter.call(this, fn, options)) {
    return true;
  }
  return false;
}

async function every(fn, options) {
  validateFunction(fn, "fn");
  return !(await some.call(this, async (...args) => {
    return !(await fn(...args));
  }, options));
}

async function find(fn, options) {
  for await (const result of filter.call(this, fn, options)) {
    return result;
  }
  return undefined;
}

async function forEach(fn, options) {
  validateFunction(fn, "fn");
  async function forEachFn(value, options) {
    await fn(value, options);
    return kEmpty;
  }
  for await (const unused of map.call(this, forEachFn, options));
}

function filter(fn, options) {
  validateFunction(fn, "fn");
  async function filterFn(value, options) {
    if (await fn(value, options)) return value;
    return kEmpty;
  }
  return map.call(this, filterFn, options);
}

async function reduce(reducer, initialValue, options) {
  validateFunction(reducer, "reducer");
  validateOperatorOptions(options);
  let hasInitialValue = arguments.length > 1;
  if (options && options.signal && options.signal.aborted) {
    const err = new AbortError(undefined, { cause: options.signal.reason });
    this.once("error", () => {});
    await finished(this.destroy(err));
    throw err;
  }
  const ac = new AbortController();
  const signal = ac.signal;
  if (options && options.signal) {
    options.signal.addEventListener("abort", () => ac.abort(), { once: true });
  }
  let gotAnyItemFromStream = false;
  try {
    for await (const value of this) {
      gotAnyItemFromStream = true;
      if (options && options.signal && options.signal.aborted) throw new AbortError();
      if (!hasInitialValue) {
        initialValue = value;
        hasInitialValue = true;
      } else {
        initialValue = await reducer(initialValue, value, { signal });
      }
    }
    if (!gotAnyItemFromStream && !hasInitialValue) {
      const err = new ERR_MISSING_ARGS("reduce");
      err.message = "Reduce of an empty stream requires an initial value";
      throw err;
    }
  } finally {
    ac.abort();
  }
  return initialValue;
}

async function toArray(options) {
  validateOperatorOptions(options);
  const result = [];
  for await (const val of this) {
    if (options && options.signal && options.signal.aborted) {
      throw new AbortError(undefined, { cause: options.signal.reason });
    }
    result.push(val);
  }
  return result;
}

function flatMap(fn, options) {
  const values = map.call(this, fn, options);
  return async function* flatMap() {
    for await (const val of values) yield* val;
  }.call(this);
}

function toIntegerOrInfinity(number) {
  number = Number(number);
  if (Number.isNaN(number)) return 0;
  if (number < 0) throw new ERR_OUT_OF_RANGE("number", ">= 0", number);
  return number;
}

function drop(number, options) {
  validateOperatorOptions(options);
  number = toIntegerOrInfinity(number);
  return async function* drop() {
    if (options && options.signal && options.signal.aborted) throw new AbortError();
    for await (const val of this) {
      if (options && options.signal && options.signal.aborted) throw new AbortError();
      if (number-- <= 0) yield val;
    }
  }.call(this);
}

function take(number, options) {
  validateOperatorOptions(options);
  number = toIntegerOrInfinity(number);
  return async function* take() {
    if (options && options.signal && options.signal.aborted) throw new AbortError();
    for await (const val of this) {
      if (options && options.signal && options.signal.aborted) throw new AbortError();
      if (number-- > 0) yield val;
      if (number <= 0) return;
    }
  }.call(this);
}

const streamReturningOperators = { drop, filter, flatMap, map, take };
const promiseReturningOperators = { every, forEach, reduce, toArray, some, find };

for (const key of Object.keys(streamReturningOperators)) {
  const op = streamReturningOperators[key];
  const fn = {
    [op.name](...args) {
      if (new.target) throw new ERR_ILLEGAL_CONSTRUCTOR();
      return Readable.from(Reflect.apply(op, this, args));
    },
  }[op.name];
  Object.defineProperty(fn, "length", { value: op.length });
  Object.defineProperty(Readable.prototype, key, { value: fn, enumerable: false, configurable: true, writable: true });
}
for (const key of Object.keys(promiseReturningOperators)) {
  const op = promiseReturningOperators[key];
  const fn = {
    [op.name](...args) {
      if (new.target) throw new ERR_ILLEGAL_CONSTRUCTOR();
      return Reflect.apply(op, this, args);
    },
  }[op.name];
  Object.defineProperty(fn, "length", { value: op.length });
  Object.defineProperty(Readable.prototype, key, { value: fn, enumerable: false, configurable: true, writable: true });
}

// ---- web stream adapters ------------------------------------------------

function streamCountStrategy(highWaterMark) {
  return { highWaterMark, size() { return 1; } };
}
function streamByteStrategy(highWaterMark) {
  return { highWaterMark, size(chunk) { return chunk.byteLength; } };
}

function newWritableStreamFromStreamWritable(streamWritable) {
  if (!(streamWritable && typeof streamWritable.write === "function" && typeof streamWritable.on === "function")) {
    throw new ERR_INVALID_ARG_TYPE("streamWritable", "stream.Writable", streamWritable);
  }
  if (isDestroyed(streamWritable) || !isWritable(streamWritable)) {
    const writable = new WritableStream();
    writable.close();
    return writable;
  }
  const highWaterMark = streamWritable.writableHighWaterMark;
  const strategy = streamWritable.writableObjectMode ? streamCountStrategy(highWaterMark) : { highWaterMark };
  let controller;
  let backpressurePromise;
  let closed;
  function onDrain() {
    if (backpressurePromise) backpressurePromise.resolve();
  }
  const cleanup = eos(streamWritable, (error) => {
    cleanup();
    streamWritable.on("error", nop);
    if (error != null) {
      if (backpressurePromise) backpressurePromise.reject(error);
      if (closed !== undefined) {
        closed.reject(error);
        closed = undefined;
      }
      if (controller) controller.error(error);
      controller = undefined;
      return;
    }
    if (closed !== undefined) {
      closed.resolve();
      closed = undefined;
      return;
    }
    if (controller) controller.error(new AbortError());
    controller = undefined;
  });
  streamWritable.on("drain", onDrain);
  return new WritableStream({
    start(c) { controller = c; },
    write(chunk) {
      if (!streamWritable.writableObjectMode && chunk instanceof ArrayBuffer) chunk = new Uint8Array(chunk);
      if (streamWritable.writableNeedDrain || !streamWritable.write(chunk)) {
        backpressurePromise = Promise.withResolvers();
        if (!streamWritable.writableNeedDrain) backpressurePromise.resolve();
        return backpressurePromise.promise.finally(() => {
          backpressurePromise = undefined;
        });
      }
    },
    abort(reason) {
      destroyer(streamWritable, reason);
    },
    close() {
      if (closed === undefined && !isWritableEnded(streamWritable)) {
        closed = Promise.withResolvers();
        streamWritable.end();
        return closed.promise;
      }
      controller = undefined;
      return Promise.resolve();
    },
  }, strategy);
}

function encodeWebChunk(chunk, encoding, decodeStrings, objectMode) {
  if (typeof chunk === "string" && decodeStrings && !objectMode) {
    const b = Buffer.from(chunk, encoding);
    return new Uint8Array(b.buffer, b.byteOffset, b.byteLength);
  }
  return chunk;
}

function newStreamWritableFromWritableStream(writableStream, options) {
  if (options === undefined) options = kEmptyObject;
  if (!isWritableStream(writableStream)) {
    throw new ERR_INVALID_ARG_TYPE("writableStream", "WritableStream", writableStream);
  }
  validateObject(options, "options");
  const { highWaterMark, decodeStrings = true, objectMode = false, signal } = options;
  validateBoolean(objectMode, "options.objectMode");
  validateBoolean(decodeStrings, "options.decodeStrings");
  const writer = writableStream.getWriter();
  let closed = false;
  const writable = new Writable({
    highWaterMark,
    objectMode,
    decodeStrings,
    signal,
    writev(chunks, callback) {
      function done(error) {
        try {
          callback(error);
        } catch (err) {
          process.nextTick(() => destroyer(writable, err));
        }
      }
      writer.ready.then(() => Promise.all(chunks.map((data) => writer.write(data.chunk))).then(() => done(), done), done);
    },
    write(chunk, encoding, callback) {
      chunk = encodeWebChunk(chunk, encoding, decodeStrings, objectMode);
      function done(error) {
        try {
          callback(error);
        } catch (err) {
          destroyer(writable, err);
        }
      }
      writer.ready.then(() => writer.write(chunk).then(() => done(), done), done);
    },
    destroy(error, callback) {
      function done() {
        try {
          callback(error);
        } catch (err) {
          process.nextTick(() => { throw err; });
        }
      }
      if (!closed) {
        if (error != null) writer.abort(error).then(done, done);
        else writer.close().then(done, done);
        return;
      }
      done();
    },
    final(callback) {
      function done(error) {
        try {
          callback(error);
        } catch (err) {
          process.nextTick(() => destroyer(writable, err));
        }
      }
      if (!closed) writer.close().then(() => done(), done);
    },
  });
  writer.closed.then(() => {
    closed = true;
    if (!isWritableEnded(writable)) destroyer(writable, new ERR_STREAM_PREMATURE_CLOSE());
  }, (error) => {
    closed = true;
    destroyer(writable, error);
  });
  return writable;
}

const kErrorSentinelAttached = Symbol("kErrorSentinelAttached");

function newReadableStreamFromStreamReadable(streamReadable, options) {
  if (options === undefined) options = kEmptyObject;
  if (!streamReadable || typeof streamReadable._readableState !== "object") {
    throw new ERR_INVALID_ARG_TYPE("streamReadable", "stream.Readable", streamReadable);
  }
  validateObject(options, "options");
  const isBYOB = options.type === "bytes";
  let controller;
  let wasCanceled = false;
  let strategy;
  const underlyingSource = {
    start(c) { controller = c; },
    cancel(reason) {
      wasCanceled = true;
      destroyer(streamReadable, reason);
    },
  };
  if (isBYOB) underlyingSource.type = "bytes";
  const readable = isReadable(streamReadable);
  const objectMode = streamReadable.readableObjectMode;
  if (readable) {
    underlyingSource.pull = function pull() {
      streamReadable.resume();
    };
    const highWaterMark = streamReadable.readableHighWaterMark;
    strategy = isBYOB ? { highWaterMark } :
      (options.strategy ?? (objectMode ? streamCountStrategy(highWaterMark) : streamByteStrategy(highWaterMark)));
  }
  const readableStream = new ReadableStream(underlyingSource, strategy);
  let settle;
  try { readableStream[kWebClosed] = new Promise((resolve, reject) => { settle = { resolve, reject }; }); } catch {}
  if (readableStream[kWebClosed]) readableStream[kWebClosed].catch(nop);
  let cleanup = nop;
  cleanup = eos(streamReadable, { writable: false }, (error) => {
    cleanup();
    if (!(kErrorSentinelAttached in streamReadable)) {
      streamReadable.on("error", nop);
      streamReadable[kErrorSentinelAttached] = true;
    }
    if (settle) {
      if (error) settle.reject(error);
      else settle.resolve();
    }
    if (wasCanceled) return;
    wasCanceled = true;
    if (error) return controller.error(error);
    controller.close();
  });
  if (wasCanceled) {
    cleanup();
  } else if (readable) {
    streamReadable.pause();
    streamReadable.on("data", function onData(chunk) {
      if (Buffer.isBuffer(chunk) && !objectMode) chunk = new Uint8Array(chunk);
      controller.enqueue(chunk);
      if (controller.desiredSize <= 0) streamReadable.pause();
    });
  }
  return readableStream;
}

function newStreamReadableFromReadableStream(readableStream, options) {
  if (options === undefined) options = kEmptyObject;
  if (!isReadableStream(readableStream)) {
    throw new ERR_INVALID_ARG_TYPE("readableStream", "ReadableStream", readableStream);
  }
  validateObject(options, "options");
  const { highWaterMark, encoding, objectMode = false, signal } = options;
  if (encoding !== undefined && !Buffer.isEncoding(encoding)) {
    throw new ERR_INVALID_ARG_VALUE("options.encoding", encoding);
  }
  validateBoolean(objectMode, "options.objectMode");
  const reader = readableStream.getReader();
  let closed = false;
  const readable = new Readable({
    objectMode,
    highWaterMark,
    encoding,
    signal,
    read() {
      reader.read().then((chunk) => {
        if (chunk.done) readable.push(null);
        else readable.push(chunk.value);
      }, (error) => destroyer(readable, error));
    },
    destroy(error, callback) {
      function done() {
        try {
          callback(error);
        } catch (err) {
          process.nextTick(() => { throw err; });
        }
      }
      if (!closed) {
        reader.cancel(error).then(done, done);
        return;
      }
      done();
    },
  });
  if (reader.closed && typeof reader.closed.then === "function") {
    reader.closed.then(() => {
      closed = true;
    }, (error) => {
      closed = true;
      destroyer(readable, error);
    });
  }
  return readable;
}

function newReadableWritablePairFromDuplex(duplex, options) {
  if (options === undefined) options = kEmptyObject;
  if (!duplex || typeof duplex._writableState !== "object" || typeof duplex._readableState !== "object") {
    throw new ERR_INVALID_ARG_TYPE("duplex", "stream.Duplex", duplex);
  }
  validateObject(options, "options");
  const readableOptions = { type: options.readableType ?? options.type };
  if (isDestroyed(duplex)) {
    const writable = new WritableStream();
    const readable = new ReadableStream(readableOptions.type ? { type: readableOptions.type } : undefined);
    writable.close();
    readable.cancel();
    return { readable, writable };
  }
  const writable = isWritable(duplex) ? newWritableStreamFromStreamWritable(duplex) : new WritableStream();
  if (!isWritable(duplex)) writable.close();
  const readable = isReadable(duplex) ?
    newReadableStreamFromStreamReadable(duplex, readableOptions) :
    new ReadableStream(readableOptions.type ? { type: readableOptions.type } : undefined);
  if (!isReadable(duplex)) readable.cancel();
  return { writable, readable };
}

function newStreamDuplexFromReadableWritablePair(pair, options) {
  if (pair === undefined) pair = kEmptyObject;
  if (options === undefined) options = kEmptyObject;
  validateObject(pair, "pair");
  const { readable: readableStream, writable: writableStream } = pair;
  if (!isReadableStream(readableStream)) {
    throw new ERR_INVALID_ARG_TYPE("pair.readable", "ReadableStream", readableStream);
  }
  if (!isWritableStream(writableStream)) {
    throw new ERR_INVALID_ARG_TYPE("pair.writable", "WritableStream", writableStream);
  }
  validateObject(options, "options");
  const { allowHalfOpen = false, objectMode = false, encoding, decodeStrings = true, highWaterMark, signal } = options;
  validateBoolean(objectMode, "options.objectMode");
  if (encoding !== undefined && !Buffer.isEncoding(encoding)) {
    throw new ERR_INVALID_ARG_VALUE("options.encoding", encoding);
  }
  const writer = writableStream.getWriter();
  const reader = readableStream.getReader();
  let writableClosed = false;
  let readableClosed = false;
  const duplex = new Duplex({
    allowHalfOpen,
    highWaterMark,
    objectMode,
    encoding,
    decodeStrings,
    signal,
    writev(chunks, callback) {
      function done(error) {
        try {
          callback(error);
        } catch (err) {
          process.nextTick(() => destroyer(duplex, err));
        }
      }
      writer.ready.then(() => Promise.all(chunks.map((data) => writer.write(data.chunk))).then(() => done(), done), done);
    },
    write(chunk, encoding, callback) {
      chunk = encodeWebChunk(chunk, encoding, decodeStrings, objectMode);
      function done(error) {
        try {
          callback(error);
        } catch (err) {
          destroyer(duplex, err);
        }
      }
      writer.ready.then(() => writer.write(chunk).then(() => done(), done), done);
    },
    final(callback) {
      function done(error) {
        try {
          callback(error);
        } catch (err) {
          process.nextTick(() => destroyer(duplex, err));
        }
      }
      if (!writableClosed) writer.close().then(() => done(), done);
    },
    read() {
      reader.read().then((chunk) => {
        if (chunk.done) duplex.push(null);
        else duplex.push(chunk.value);
      }, (error) => destroyer(duplex, error));
    },
    destroy(error, callback) {
      function done() {
        try {
          callback(error);
        } catch (err) {
          process.nextTick(() => { throw err; });
        }
      }
      async function closeWriter() {
        if (!writableClosed) await writer.abort(error);
      }
      async function closeReader() {
        if (!readableClosed) await reader.cancel(error);
      }
      if (!writableClosed || !readableClosed) {
        Promise.all([closeWriter(), closeReader()]).then(done, done);
        return;
      }
      done();
    },
  });
  writer.closed.then(() => {
    writableClosed = true;
    if (!isWritableEnded(duplex)) destroyer(duplex, new ERR_STREAM_PREMATURE_CLOSE());
  }, (error) => {
    writableClosed = true;
    readableClosed = true;
    destroyer(duplex, error);
  });
  if (reader.closed && typeof reader.closed.then === "function") {
    reader.closed.then(() => {
      readableClosed = true;
    }, (error) => {
      writableClosed = true;
      readableClosed = true;
      destroyer(duplex, error);
    });
  }
  return duplex;
}
`
