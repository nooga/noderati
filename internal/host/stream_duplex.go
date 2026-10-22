package host

// streamDuplexJS: Duplex (a genuine Readable that also carries every
// Writable.prototype method and a WritableState, the way Node builds it),
// Transform, PassThrough and duplexPair.
//
// Real subclasses this has to keep working: @smithy/core's
// ChecksumStream (extends Duplex; upstream .pipe()s into it, something
// else reads from it), undici's EventSourceStream (extends Transform),
// fast-glob's ReaderStream (assigns "stream._write = ..." directly on a
// PassThrough instance - Transform's writable side must consult the
// instance's _write, not call _transform directly), zlib's classes.
const streamDuplexJS = `
function Duplex(options) {
  if (!(this instanceof Duplex)) return new Duplex(options);
  this._readableState = new ReadableState(options, this, true);
  this._writableState = new WritableState(options, this, true);
  if (options) {
    this.allowHalfOpen = options.allowHalfOpen !== false;
    if (options.readable === false) {
      this._readableState.readable = false;
      this._readableState.ended = true;
      this._readableState.endEmitted = true;
    }
    if (options.writable === false) {
      this._writableState.writable = false;
      this._writableState.ending = true;
      this._writableState.ended = true;
      this._writableState.finished = true;
    }
    if (typeof options.read === "function") this._read = options.read;
    if (typeof options.write === "function") this._write = options.write;
    if (typeof options.writev === "function") this._writev = options.writev;
    if (typeof options.destroy === "function") this._destroy = options.destroy;
    if (typeof options.final === "function") this._final = options.final;
    if (typeof options.construct === "function") this._construct = options.construct;
    if (options.signal) addAbortSignal(options.signal, this);
  } else {
    this.allowHalfOpen = true;
  }
  Stream.call(this, options);
  if (this._construct != null) {
    construct(this, () => {
      this._readableState[kOnConstructed](this);
      this._writableState[kOnConstructed](this);
    });
  }
}
Object.setPrototypeOf(Duplex.prototype, Readable.prototype);
Object.setPrototypeOf(Duplex, Readable);
{
  const keys = Object.keys(Writable.prototype);
  for (let i = 0; i < keys.length; i++) {
    const method = keys[i];
    if (!Duplex.prototype[method]) Duplex.prototype[method] = Writable.prototype[method];
  }
}
Duplex.prototype.destroy = Writable.prototype.destroy;

{
  const desc = {};
  for (const name of ["writable", "writableHighWaterMark", "writableObjectMode", "writableBuffer",
                      "writableLength", "writableFinished", "writableCorked", "writableEnded",
                      "writableNeedDrain"]) {
    desc[name] = { ...Object.getOwnPropertyDescriptor(Writable.prototype, name) };
  }
  desc.destroyed = {
    get() {
      if (this._readableState === undefined || this._writableState === undefined) return false;
      return this._readableState.destroyed && this._writableState.destroyed;
    },
    set(value) {
      if (this._readableState && this._writableState) {
        this._readableState.destroyed = value;
        this._writableState.destroyed = value;
      }
    },
  };
  Object.defineProperties(Duplex.prototype, desc);
}

Duplex.fromWeb = function (pair, options) {
  return newStreamDuplexFromReadableWritablePair(pair, options);
};
Duplex.toWeb = function (duplex, options) {
  return newReadableWritablePairFromDuplex(duplex, options);
};
Duplex.from = function (body) {
  return duplexify(body, "body");
};

// ---- Transform -------------------------------------------------------

const kCallback = Symbol("kCallback");

function Transform(options) {
  if (!(this instanceof Transform)) return new Transform(options);
  const readableHighWaterMark = options ? getHighWaterMark(this, options, "readableHighWaterMark", true) : null;
  if (readableHighWaterMark === 0) {
    options = {
      ...options,
      highWaterMark: null,
      readableHighWaterMark,
      writableHighWaterMark: options.writableHighWaterMark || 0,
    };
  }
  Duplex.call(this, options);
  // Transform pushes from inside _write; that must not look "sync" to the
  // readable side or 'data' would never be emitted directly.
  this._readableState.sync = false;
  this[kCallback] = null;
  if (options) {
    if (typeof options.transform === "function") this._transform = options.transform;
    if (typeof options.flush === "function") this._flush = options.flush;
  }
  this.on("prefinish", transformPrefinish);
}
Object.setPrototypeOf(Transform.prototype, Duplex.prototype);
Object.setPrototypeOf(Transform, Duplex);

function transformFinal(cb) {
  if (typeof this._flush === "function" && !this.destroyed) {
    this._flush((er, data) => {
      if (er) {
        if (cb) cb(er);
        else this.destroy(er);
        return;
      }
      if (data != null) this.push(data);
      this.push(null);
      if (cb) cb();
    });
  } else {
    this.push(null);
    if (cb) cb();
  }
}

function transformPrefinish() {
  if (this._final !== transformFinal) transformFinal.call(this);
}

Transform.prototype._final = transformFinal;

Transform.prototype._transform = function (chunk, encoding, callback) {
  throw new ERR_METHOD_NOT_IMPLEMENTED("_transform()");
};

Transform.prototype._write = function (chunk, encoding, callback) {
  const rState = this._readableState;
  const wState = this._writableState;
  const length = rState.length;
  this._transform(chunk, encoding, (err, val) => {
    if (err) {
      callback(err);
      return;
    }
    if (val != null) this.push(val);
    if (rState.ended) {
      process.nextTick(callback);
    } else if (wState.ended || length === rState.length || rState.length < rState.highWaterMark) {
      callback();
    } else {
      this[kCallback] = callback;
    }
  });
};

Transform.prototype._read = function () {
  if (this[kCallback]) {
    const callback = this[kCallback];
    this[kCallback] = null;
    callback();
  }
};

function PassThrough(options) {
  if (!(this instanceof PassThrough)) return new PassThrough(options);
  Transform.call(this, options);
}
Object.setPrototypeOf(PassThrough.prototype, Transform.prototype);
Object.setPrototypeOf(PassThrough, Transform);
PassThrough.prototype._transform = function (chunk, encoding, cb) {
  cb(null, chunk);
};

// ---- duplexPair --------------------------------------------------------

const kOtherSide = Symbol("kOtherSide");

class DuplexSide extends Duplex {
  constructor(options) {
    super(options);
    this[kCallback] = null;
    this[kOtherSide] = null;
  }
  _read() {
    const callback = this[kCallback];
    if (callback) {
      this[kCallback] = null;
      callback();
    }
  }
  _write(chunk, encoding, callback) {
    const other = this[kOtherSide];
    if (chunk.length === 0) {
      process.nextTick(callback);
    } else {
      other.push(chunk);
      other[kCallback] = callback;
    }
  }
  _final(callback) {
    this[kOtherSide].on("end", callback);
    this[kOtherSide].push(null);
  }
  _destroy(err, callback) {
    const otherSide = this[kOtherSide];
    if (otherSide !== null && !otherSide.destroyed) {
      process.nextTick(() => {
        if (otherSide.destroyed) return;
        if (err) otherSide.destroy();
        else otherSide.push(null);
      });
    }
    callback(err);
  }
}

function duplexPair(options) {
  const side0 = new DuplexSide(options);
  const side1 = new DuplexSide(options);
  side0[kOtherSide] = side1;
  side1[kOtherSide] = side0;
  return [side0, side1];
}
`
