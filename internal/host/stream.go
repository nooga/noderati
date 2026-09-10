package host

const streamShim = `class EventEmitter {
  constructor() {
    this._events = Object.create(null);
  }
  on(event, listener) {
    if (!this._events[event]) this._events[event] = [];
    this._events[event].push(listener);
    return this;
  }
  once(event, listener) {
    const wrapper = (...args) => {
      this.off(event, wrapper);
      listener(...args);
    };
    return this.on(event, wrapper);
  }
  off(event, listener) {
    return this.removeListener(event, listener);
  }
  removeListener(event, listener) {
    const list = this._events[event];
    if (!list) return this;
    const i = list.indexOf(listener);
    if (i >= 0) list.splice(i, 1);
    return this;
  }
  emit(event, ...args) {
    const list = this._events[event];
    if (!list || list.length === 0) return false;
    for (const fn of list.slice()) fn(...args);
    return true;
  }
}

// push()/destroy()/[Symbol.asyncIterator] were missing entirely - found
// the hard way while probing real undici's fetch() end to end (round 79,
// docs/real-node-plan.md): lib/web/fetch/index.js's own httpNetworkFetch
// builds the response body as "this.body = new Readable({ read: resume })",
// pushes bytes into it directly from the dispatch handler's onData/
// onComplete callbacks ("this.body.push(bytes)"/"this.body.push(null)"),
// and later consumes it as "body[Symbol.asyncIterator]()" - a real,
// unavoidable call site, not a hypothetical one. Without push(), the very
// first onComplete() call threw "undefined is not a function" immediately
// after a full response had already parsed correctly.
//
// The queue+waiters pattern below is the standard way to implement a
// real, correct async iterator over a push-driven source: push() either
// hands a chunk straight to a waiting next() call or buffers it if
// nobody's waiting yet; next() either returns a buffered chunk
// immediately or parks a Promise until push()/destroy() resolves it.
// 'data'/'end' still fire too (unchanged from before), so pipe() and any
// other 'data'-based consumer keep working exactly as they did - real
// undici's fetch() body only ever uses the async-iterator path for this
// class specifically, but nothing here assumes that's the only consumer.
//
// Deliberately not wired up: the constructor's 'read' option (Node's
// real mechanism for pulling more data from upstream once the internal
// buffer drains, e.g. backpressure on a large streamed response) is
// accepted but never invoked - every real call site exercised so far
// (small-to-moderate fetch() response bodies) pushes its entire content
// synchronously before anything ever awaits a chunk, so there's never
// been a real gap to pull against. An honest gap, not a silent one: a
// future response large/slow enough to need genuine pull-driven
// backpressure here would stall waiting for a next() that never
// resolves, rather than silently dropping data - flagged for whoever
// hits it next, not glossed over.
class Readable extends EventEmitter {
  constructor(_opts) {
    super();
    this.readable = true;
    this._queue = [];
    this._ended = false;
    this._error = null;
    this._waiters = [];
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
    this._queue.push(chunk);
    this.emit("data", chunk);
    this._settleWaiters();
    return true;
  }
  destroy(err) {
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

class Writable extends EventEmitter {
  constructor(_opts) {
    super();
    this.writable = true;
  }
  write(chunk) {
    this.emit("data", chunk);
    return true;
  }
  end(chunk) {
    if (chunk !== undefined) this.write(chunk);
    this.emit("end");
    this.emit("finish");
    return this;
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
  write(chunk, encoding, cb) {
    if (typeof encoding === "function") {
      cb = encoding;
      encoding = undefined;
    }
    this._transform(chunk, encoding, (err, data) => {
      if (err) {
        this.emit("error", err);
        return;
      }
      if (data !== undefined && data !== null) this.emit("data", data);
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
      this._transform(chunk, encoding, (err, data) => {
        if (err) {
          this.emit("error", err);
          return;
        }
        if (data !== undefined && data !== null) this.emit("data", data);
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

export { Readable, Writable, Transform, pipeline, pipelineStreams as _pipelineStreams };
export default { Readable, Writable, Transform, pipeline };
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
