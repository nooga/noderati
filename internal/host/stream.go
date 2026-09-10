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
    this._disturbed = false;
    this.destroyed = false;
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
    return true;
  }
  destroy(err) {
    // Missing re-entrancy guard - found the hard way while stress-
    // testing real undici's fetch() past its first success (round 81,
    // docs/real-node-plan.md): real undici's own onError(error) handler
    // does "this.body?.destroy(error)" AND is itself registered as an
    // 'error' listener on that same body ("this.body.on('error',
    // onError)") - a real, unavoidable pairing, not a hypothetical one.
    // Without this guard, every destroy(err) call unconditionally
    // re-emitted 'error', which re-invoked onError, which called
    // destroy(err) again - genuine infinite recursion, a VM stack
    // overflow that (unlike a caught JS exception) never actually
    // stopped script execution on its own. Real Node's own
    // Readable.destroy() has exactly this guard (a destroyed flag
    // that makes every call after the first a no-op for emission
    // purposes) for precisely this reason - a stream's own error/close
    // handling calling destroy() again on an already-destroyed stream
    // is a normal, expected pattern, not misuse.
    //
    // Only this class (Readable) has a destroy() at all - Writable and
    // Transform don't define one here. Real undici's own onError only
    // ever calls it on a response body, which is always a Readable, so
    // that's not a gap this round hit - noted for whoever adds one to
    // either of those classes next: match this same guard.
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

export { Readable, Writable, Transform, pipeline, isDisturbed, isErrored, pipelineStreams as _pipelineStreams };
export default { Readable, Writable, Transform, pipeline, isDisturbed, isErrored };
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
