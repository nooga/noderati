package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// zlib.go is node:zlib: every class (Gzip/Gunzip/Deflate/Inflate/
// DeflateRaw/InflateRaw/Unzip, BrotliCompress/BrotliDecompress,
// ZstdCompress/ZstdDecompress) is a stream.Transform driving one of
// zlib_sync.go's synchronous Go handles, mirroring lib/zlib.js's own
// ZlibBase/Zlib/Brotli/Zstd hierarchy. Brotli and zstd come from pure-Go
// codecs (andybalholm/brotli, klauspost/compress/zstd), so undici's
// fetch() decoder chain works for every Content-Encoding it supports.
//
// The synchronous `_processChunk(chunk, flushFlag)` entry point is what
// real tar's minizlib drives, so it has to answer in the same call.
func declareZlib() {
	registerJSShim("zlib", zlibShim)
}

func installZlibNatives(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	obj := gt.AsPlainObject()
	if obj == nil {
		return
	}

	obj.SetOwn("__noderatiZlibHandle", vm.NewNativeFunction(2, false, "__noderatiZlibHandle", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 1 {
			return vm.Undefined, nil
		}
		opts := vm.Undefined
		if len(args) > 1 {
			opts = args[1]
		}
		return buildZlibSyncHandle(vmInst, args[0].ToString(), opts)
	}))
	obj.SetOwn("__noderatiZlibCRC32", vm.NewNativeFunction(2, false, "__noderatiZlibCRC32", func(args []vm.Value) (vm.Value, error) {
		var data []byte
		if len(args) > 0 {
			data = valueToBytes(vmInst, args[0])
		}
		value := uint32(0)
		if len(args) > 1 && args[1].IsNumber() {
			value = uint32(int64(args[1].ToFloat()))
		}
		return vm.NumberValue(float64(zlibCRC32(data, value))), nil
	}))
}

const zlibShim = `import Stream from "stream";

const Transform = Stream.Transform;
const __zlibHandle = globalThis.__noderatiZlibHandle;
const __crc32 = globalThis.__noderatiZlibCRC32;
const kMaxLength = 2 ** 53 - 1;

const constants = Object.assign(Object.create(null), {
  Z_NO_FLUSH: 0, Z_PARTIAL_FLUSH: 1, Z_SYNC_FLUSH: 2, Z_FULL_FLUSH: 3, Z_FINISH: 4, Z_BLOCK: 5,
  Z_OK: 0, Z_STREAM_END: 1, Z_NEED_DICT: 2, Z_ERRNO: -1, Z_STREAM_ERROR: -2, Z_DATA_ERROR: -3,
  Z_MEM_ERROR: -4, Z_BUF_ERROR: -5, Z_VERSION_ERROR: -6,
  Z_NO_COMPRESSION: 0, Z_BEST_SPEED: 1, Z_BEST_COMPRESSION: 9, Z_DEFAULT_COMPRESSION: -1,
  Z_FILTERED: 1, Z_HUFFMAN_ONLY: 2, Z_RLE: 3, Z_FIXED: 4, Z_DEFAULT_STRATEGY: 0, ZLIB_VERNUM: 4800,
  DEFLATE: 1, INFLATE: 2, GZIP: 3, GUNZIP: 4, DEFLATERAW: 5, INFLATERAW: 6, UNZIP: 7,
  BROTLI_DECODE: 8, BROTLI_ENCODE: 9, ZSTD_DECOMPRESS: 11, ZSTD_COMPRESS: 10,
  Z_MIN_WINDOWBITS: 8, Z_MAX_WINDOWBITS: 15, Z_DEFAULT_WINDOWBITS: 15,
  Z_MIN_CHUNK: 64, Z_MAX_CHUNK: Infinity, Z_DEFAULT_CHUNK: 16384,
  Z_MIN_MEMLEVEL: 1, Z_MAX_MEMLEVEL: 9, Z_DEFAULT_MEMLEVEL: 8,
  Z_MIN_LEVEL: -1, Z_MAX_LEVEL: 9, Z_DEFAULT_LEVEL: -1,
  BROTLI_OPERATION_PROCESS: 0, BROTLI_OPERATION_FLUSH: 1, BROTLI_OPERATION_FINISH: 2, BROTLI_OPERATION_EMIT_METADATA: 3,
  BROTLI_PARAM_MODE: 0, BROTLI_MODE_GENERIC: 0, BROTLI_MODE_TEXT: 1, BROTLI_MODE_FONT: 2, BROTLI_DEFAULT_MODE: 0,
  BROTLI_PARAM_QUALITY: 1, BROTLI_MIN_QUALITY: 0, BROTLI_MAX_QUALITY: 11, BROTLI_DEFAULT_QUALITY: 11,
  BROTLI_PARAM_LGWIN: 2, BROTLI_MIN_WINDOW_BITS: 10, BROTLI_MAX_WINDOW_BITS: 24, BROTLI_LARGE_MAX_WINDOW_BITS: 30,
  BROTLI_DEFAULT_WINDOW: 22, BROTLI_PARAM_LGBLOCK: 3, BROTLI_MIN_INPUT_BLOCK_BITS: 16, BROTLI_MAX_INPUT_BLOCK_BITS: 24,
  BROTLI_PARAM_DISABLE_LITERAL_CONTEXT_MODELING: 4, BROTLI_PARAM_SIZE_HINT: 5, BROTLI_PARAM_LARGE_WINDOW: 6,
  BROTLI_PARAM_NPOSTFIX: 7, BROTLI_PARAM_NDIRECT: 8,
  BROTLI_DECODER_RESULT_ERROR: 0, BROTLI_DECODER_RESULT_SUCCESS: 1, BROTLI_DECODER_RESULT_NEEDS_MORE_INPUT: 2,
  BROTLI_DECODER_RESULT_NEEDS_MORE_OUTPUT: 3, BROTLI_DECODER_PARAM_DISABLE_RING_BUFFER_REALLOCATION: 0,
  BROTLI_DECODER_PARAM_LARGE_WINDOW: 1, BROTLI_DECODER_NO_ERROR: 0, BROTLI_DECODER_SUCCESS: 1,
  BROTLI_DECODER_NEEDS_MORE_INPUT: 2, BROTLI_DECODER_NEEDS_MORE_OUTPUT: 3,
  BROTLI_DECODER_ERROR_FORMAT_EXUBERANT_NIBBLE: -1, BROTLI_DECODER_ERROR_FORMAT_RESERVED: -2,
  BROTLI_DECODER_ERROR_FORMAT_EXUBERANT_META_NIBBLE: -3, BROTLI_DECODER_ERROR_FORMAT_SIMPLE_HUFFMAN_ALPHABET: -4,
  BROTLI_DECODER_ERROR_FORMAT_SIMPLE_HUFFMAN_SAME: -5, BROTLI_DECODER_ERROR_FORMAT_CL_SPACE: -6,
  BROTLI_DECODER_ERROR_FORMAT_HUFFMAN_SPACE: -7, BROTLI_DECODER_ERROR_FORMAT_CONTEXT_MAP_REPEAT: -8,
  BROTLI_DECODER_ERROR_FORMAT_BLOCK_LENGTH_1: -9, BROTLI_DECODER_ERROR_FORMAT_BLOCK_LENGTH_2: -10,
  BROTLI_DECODER_ERROR_FORMAT_TRANSFORM: -11, BROTLI_DECODER_ERROR_FORMAT_DICTIONARY: -12,
  BROTLI_DECODER_ERROR_FORMAT_WINDOW_BITS: -13, BROTLI_DECODER_ERROR_FORMAT_PADDING_1: -14,
  BROTLI_DECODER_ERROR_FORMAT_PADDING_2: -15, BROTLI_DECODER_ERROR_FORMAT_DISTANCE: -16,
  BROTLI_DECODER_ERROR_DICTIONARY_NOT_SET: -19, BROTLI_DECODER_ERROR_INVALID_ARGUMENTS: -20,
  BROTLI_DECODER_ERROR_ALLOC_CONTEXT_MODES: -21, BROTLI_DECODER_ERROR_ALLOC_TREE_GROUPS: -22,
  BROTLI_DECODER_ERROR_ALLOC_CONTEXT_MAP: -25, BROTLI_DECODER_ERROR_ALLOC_RING_BUFFER_1: -26,
  BROTLI_DECODER_ERROR_ALLOC_RING_BUFFER_2: -27, BROTLI_DECODER_ERROR_ALLOC_BLOCK_TYPE_TREES: -30,
  BROTLI_DECODER_ERROR_UNREACHABLE: -31,
  ZSTD_e_continue: 0, ZSTD_e_flush: 1, ZSTD_e_end: 2,
  ZSTD_fast: 1, ZSTD_dfast: 2, ZSTD_greedy: 3, ZSTD_lazy: 4, ZSTD_lazy2: 5, ZSTD_btlazy2: 6, ZSTD_btopt: 7,
  ZSTD_btultra: 8, ZSTD_btultra2: 9,
  ZSTD_c_compressionLevel: 100, ZSTD_c_windowLog: 101, ZSTD_c_hashLog: 102, ZSTD_c_chainLog: 103,
  ZSTD_c_searchLog: 104, ZSTD_c_minMatch: 105, ZSTD_c_targetLength: 106, ZSTD_c_strategy: 107,
  ZSTD_c_enableLongDistanceMatching: 160, ZSTD_c_ldmHashLog: 161, ZSTD_c_ldmMinMatch: 162,
  ZSTD_c_ldmBucketSizeLog: 163, ZSTD_c_ldmHashRateLog: 164, ZSTD_c_contentSizeFlag: 200,
  ZSTD_c_checksumFlag: 201, ZSTD_c_dictIDFlag: 202, ZSTD_c_nbWorkers: 400, ZSTD_c_jobSize: 401,
  ZSTD_c_overlapLog: 402, ZSTD_d_windowLogMax: 100, ZSTD_CLEVEL_DEFAULT: 3,
  ZSTD_error_no_error: 0, ZSTD_error_GENERIC: 1, ZSTD_error_prefix_unknown: 10, ZSTD_error_version_unsupported: 12,
  ZSTD_error_frameParameter_unsupported: 14, ZSTD_error_frameParameter_windowTooLarge: 16,
  ZSTD_error_corruption_detected: 20, ZSTD_error_checksum_wrong: 22, ZSTD_error_literals_headerWrong: 24,
  ZSTD_error_dictionary_corrupted: 30, ZSTD_error_dictionary_wrong: 32, ZSTD_error_dictionaryCreation_failed: 34,
  ZSTD_error_parameter_unsupported: 40, ZSTD_error_parameter_combination_unsupported: 41,
  ZSTD_error_parameter_outOfBound: 42, ZSTD_error_tableLog_tooLarge: 44, ZSTD_error_maxSymbolValue_tooLarge: 46,
  ZSTD_error_maxSymbolValue_tooSmall: 48, ZSTD_error_stabilityCondition_notRespected: 50, ZSTD_error_stage_wrong: 60,
  ZSTD_error_init_missing: 62, ZSTD_error_memory_allocation: 64, ZSTD_error_workSpace_tooSmall: 66,
  ZSTD_error_dstSize_tooSmall: 70, ZSTD_error_srcSize_wrong: 72, ZSTD_error_dstBuffer_null: 74,
  ZSTD_error_noForwardProgress_destFull: 80, ZSTD_error_noForwardProgress_inputEmpty: 82,
});

const codes = {
  Z_OK: 0, Z_STREAM_END: 1, Z_NEED_DICT: 2, Z_ERRNO: -1, Z_STREAM_ERROR: -2,
  Z_DATA_ERROR: -3, Z_MEM_ERROR: -4, Z_BUF_ERROR: -5, Z_VERSION_ERROR: -6,
};
for (const k of Object.keys(codes)) codes[codes[k]] = k;
Object.freeze(codes);

// --- errors / validation, worded like lib/internal/errors.js ---
function received(v) {
  if (v == null) return " Received " + v;
  if (typeof v === "function") return " Received function " + (v.name || "<anonymous>");
  if (typeof v === "object") return " Received an instance of " + ((v.constructor && v.constructor.name) || "Object");
  let s = typeof v === "string" ? "'" + v + "'" : String(v);
  if (typeof v === "string" && v.length > 28) s = "'" + v.slice(0, 25) + "'...";
  return " Received type " + typeof v + " (" + s + ")";
}
function invalidArgType(name, expected, v) {
  const what = '"' + name + '" ' + (name.includes(".") ? "property" : "argument");
  const err = new TypeError("The " + what + " must be " + expected + "." + received(v));
  err.code = "ERR_INVALID_ARG_TYPE";
  return err;
}
function outOfRange(name, range, v) {
  const err = new RangeError('The value of "' + name + '" is out of range. It must be ' + range + ". Received " + String(v));
  err.code = "ERR_OUT_OF_RANGE";
  return err;
}
function codedError(Ctor, code, message) {
  const err = new Ctor(message);
  err.code = code;
  return err;
}
function bufferTooLarge(max) {
  return codedError(RangeError, "ERR_BUFFER_TOO_LARGE", "Cannot create a Buffer larger than " + max + " bytes");
}
function checkFiniteNumber(number, name) {
  if (number === undefined) return false;
  if (Number.isFinite(number)) return true;
  if (Number.isNaN(number)) return false;
  if (typeof number !== "number") throw invalidArgType(name, "of type number", number);
  throw outOfRange(name, "a finite number", number);
}
function checkRangesOrGetDefault(number, name, lower, upper, def) {
  if (!checkFiniteNumber(number, name)) return def;
  if (number < lower || number > upper) throw outOfRange(name, ">= " + lower + " and <= " + upper, number);
  return number;
}

function toBuffer(chunk, encoding) {
  if (typeof chunk === "string") return Buffer.from(chunk, encoding || "utf8");
  if (Buffer.isBuffer(chunk)) return chunk;
  if (ArrayBuffer.isView(chunk)) return Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
  if (chunk instanceof ArrayBuffer) return Buffer.from(chunk);
  throw invalidArgType("buffer", "of type string or an instance of Buffer, TypedArray, DataView, or ArrayBuffer", chunk);
}

function crc32(data, value = 0) {
  if (typeof data !== "string" && !ArrayBuffer.isView(data)) {
    throw invalidArgType("data", "of type string or an instance of Buffer, TypedArray, or DataView", data);
  }
  if (typeof value !== "number") throw invalidArgType("value", "of type number", value);
  if (!Number.isInteger(value)) throw outOfRange("value", "an integer", value);
  if (value < 0 || value > 4294967295) throw outOfRange("value", ">= 0 and <= 4294967295", value);
  const buf = typeof data === "string" ? Buffer.from(data, "utf8") : toBuffer(data);
  return __crc32(buf, value);
}

const kFlushFlag = Symbol("kFlushFlag");
const kFlushBuffers = [];
for (let i = 0; i <= 5; i++) {
  const b = Buffer.alloc(0);
  b[kFlushFlag] = i;
  kFlushBuffers.push(b);
}

function closeHandle(engine) {
  if (!engine._handle) return;
  engine._handle.close();
  engine._handle = null;
}

// processChunkSync is lib/zlib.js's own: whole input in, every byte of
// output back, with maxOutputLength enforced against the running total.
function processChunkSync(self, chunk, flushFlag, streaming) {
  if (!self._handle) throw codedError(Error, "ERR_ZLIB_BINDING_CLOSED", "zlib binding closed");
  const buf = toBuffer(chunk);
  let out;
  try {
    out = self._handle.process(buf, flushFlag);
  } catch (err) {
    closeHandle(self);
    throw err;
  }
  self.bytesWritten += buf.length;
  if (!streaming && out.length > self._maxOutputLength) {
    closeHandle(self);
    throw bufferTooLarge(self._maxOutputLength);
  }
  return out;
}

function processChunk(self, chunk, flushFlag, cb) {
  if (!self._handle) {
    process.nextTick(cb);
    return;
  }
  let out;
  try {
    out = processChunkSync(self, chunk, flushFlag, true);
  } catch (err) {
    closeHandle(self);
    cb(err);
    return;
  }
  const size = self._chunkSize;
  for (let off = 0; off < out.length; off += size) {
    self.push(off === 0 && out.length <= size ? out : out.subarray(off, Math.min(off + size, out.length)));
  }
  cb();
}

class ZlibBase extends Transform {
  constructor(opts, mode, handleOpts, { flush, finishFlush, fullFlush }) {
    let chunkSize = constants.Z_DEFAULT_CHUNK;
    let maxOutputLength = kMaxLength;
    if (opts) {
      chunkSize = opts.chunkSize;
      if (!checkFiniteNumber(chunkSize, "options.chunkSize")) {
        chunkSize = constants.Z_DEFAULT_CHUNK;
      } else if (chunkSize < constants.Z_MIN_CHUNK) {
        throw outOfRange("options.chunkSize", ">= " + constants.Z_MIN_CHUNK, chunkSize);
      }
      flush = checkRangesOrGetDefault(opts.flush, "options.flush", constants.Z_NO_FLUSH, constants.Z_BLOCK, flush);
      finishFlush = checkRangesOrGetDefault(opts.finishFlush, "options.finishFlush", constants.Z_NO_FLUSH, constants.Z_BLOCK, finishFlush);
      maxOutputLength = checkRangesOrGetDefault(opts.maxOutputLength, "options.maxOutputLength", 1, kMaxLength, kMaxLength);
      if (opts.encoding || opts.objectMode || opts.writableObjectMode) {
        opts = { ...opts };
        opts.encoding = null;
        opts.objectMode = false;
        opts.writableObjectMode = false;
      }
    }
    super({ autoDestroy: true, ...opts });
    this._handle = __zlibHandle(mode, handleOpts);
    this.bytesWritten = 0;
    this._chunkSize = chunkSize;
    this._defaultFlushFlag = flush;
    this._finishFlushFlag = finishFlush;
    this._defaultFullFlushFlag = fullFlush;
    this._info = opts && opts.info;
    this._maxOutputLength = maxOutputLength;
  }
  get _closed() { return !this._handle; }
  reset() {
    if (!this._handle) throw codedError(Error, "ERR_ZLIB_BINDING_CLOSED", "zlib binding closed");
    return this._handle.reset();
  }
  _flush(callback) {
    this._transform(Buffer.alloc(0), "", callback);
  }
  _final(callback) {
    callback();
  }
  flush(kind, callback) {
    if (typeof kind === "function" || (kind === undefined && !callback)) {
      callback = kind;
      kind = this._defaultFullFlushFlag;
    }
    if (this.writableFinished) {
      if (callback) process.nextTick(callback);
    } else if (this.writableEnded) {
      if (callback) this.once("end", callback);
    } else {
      this.write(kFlushBuffers[kind], "", callback);
    }
  }
  close(callback) {
    if (callback) {
      if (typeof Stream.finished === "function") Stream.finished(this, callback);
      else this.once("close", callback);
    }
    this.destroy();
  }
  _destroy(err, callback) {
    closeHandle(this);
    callback(err);
  }
  _transform(chunk, encoding, cb) {
    let flushFlag = this._defaultFlushFlag;
    if (chunk && typeof chunk[kFlushFlag] === "number") {
      flushFlag = chunk[kFlushFlag];
    } else if (chunk && chunk.length === 0 && this._ending) {
      flushFlag = this._finishFlushFlag;
    }
    processChunk(this, toBuffer(chunk, encoding), flushFlag, cb);
  }
  _processChunk(chunk, flushFlag, cb) {
    if (typeof cb === "function") processChunk(this, chunk, flushFlag, cb);
    else return processChunkSync(this, chunk, flushFlag);
  }
  end(chunk, encoding, cb) {
    this._ending = true;
    return super.end(chunk, encoding, cb);
  }
}

// The stream module's Transform may not (yet) provide destroy(); give
// zlib streams the same close/destroy lifecycle either way.
if (typeof Transform.prototype.destroy !== "function") {
  ZlibBase.prototype.destroy = function destroy(err) {
    if (this.destroyed) return this;
    this.destroyed = true;
    this._destroy(err || null, (e) => {
      process.nextTick(() => {
        if (e) this.emit("error", e);
        this.emit("close");
      });
    });
    return this;
  };
}

const zlibDefaultOpts = {
  flush: constants.Z_NO_FLUSH,
  finishFlush: constants.Z_FINISH,
  fullFlush: constants.Z_FULL_FLUSH,
};
const modeNames = { 1: "Deflate", 2: "Inflate", 3: "Gzip", 4: "Gunzip", 5: "DeflateRaw", 6: "InflateRaw", 7: "Unzip" };

class Zlib extends ZlibBase {
  constructor(opts, mode) {
    let windowBits = constants.Z_DEFAULT_WINDOWBITS;
    let level = constants.Z_DEFAULT_COMPRESSION;
    let memLevel = constants.Z_DEFAULT_MEMLEVEL;
    let strategy = constants.Z_DEFAULT_STRATEGY;
    let dictionary;
    if (opts) {
      if ((opts.windowBits == null || opts.windowBits === 0) &&
          (mode === constants.INFLATE || mode === constants.GUNZIP || mode === constants.UNZIP)) {
        windowBits = 0;
      } else {
        windowBits = checkRangesOrGetDefault(opts.windowBits, "options.windowBits",
          constants.Z_MIN_WINDOWBITS, constants.Z_MAX_WINDOWBITS, constants.Z_DEFAULT_WINDOWBITS);
      }
      level = checkRangesOrGetDefault(opts.level, "options.level",
        constants.Z_MIN_LEVEL, constants.Z_MAX_LEVEL, constants.Z_DEFAULT_COMPRESSION);
      memLevel = checkRangesOrGetDefault(opts.memLevel, "options.memLevel",
        constants.Z_MIN_MEMLEVEL, constants.Z_MAX_MEMLEVEL, constants.Z_DEFAULT_MEMLEVEL);
      strategy = checkRangesOrGetDefault(opts.strategy, "options.strategy",
        constants.Z_DEFAULT_STRATEGY, constants.Z_FIXED, constants.Z_DEFAULT_STRATEGY);
      dictionary = opts.dictionary;
      if (dictionary !== undefined && !ArrayBuffer.isView(dictionary)) {
        if (dictionary instanceof ArrayBuffer) {
          dictionary = Buffer.from(dictionary);
        } else {
          throw invalidArgType("options.dictionary", "an instance of Buffer, TypedArray, DataView, or ArrayBuffer", dictionary);
        }
      }
    }
    super(opts, modeNames[mode], { level, strategy, dictionary }, zlibDefaultOpts);
    this._level = level;
    this._strategy = strategy;
  }
  params(level, strategy, callback) {
    level = checkRangesOrGetDefault(level, "level", constants.Z_MIN_LEVEL, constants.Z_MAX_LEVEL, this._level);
    strategy = checkRangesOrGetDefault(strategy, "strategy", constants.Z_DEFAULT_STRATEGY, constants.Z_FIXED, this._strategy);
    if (this._level !== level || this._strategy !== strategy) {
      this.flush(constants.Z_SYNC_FLUSH, () => {
        if (this._handle) {
          const out = this._handle.params(level, strategy);
          if (out && out.length) this.push(out);
          if (!this.destroyed) {
            this._level = level;
            this._strategy = strategy;
            if (callback) callback();
          }
        }
      });
    } else if (callback) {
      process.nextTick(callback);
    }
  }
}

class Deflate extends Zlib { constructor(opts) { super(opts, constants.DEFLATE); } }
class Inflate extends Zlib { constructor(opts) { super(opts, constants.INFLATE); } }
class Gzip extends Zlib { constructor(opts) { super(opts, constants.GZIP); } }
class Gunzip extends Zlib { constructor(opts) { super(opts, constants.GUNZIP); } }
class DeflateRaw extends Zlib {
  constructor(opts) {
    if (opts && opts.windowBits === 8) opts = { ...opts, windowBits: 9 };
    super(opts, constants.DEFLATERAW);
  }
}
class InflateRaw extends Zlib { constructor(opts) { super(opts, constants.INFLATERAW); } }
class Unzip extends Zlib { constructor(opts) { super(opts, constants.UNZIP); } }

// --- Brotli ---
const kMaxBrotliParam = 8;
const brotliDefaultOpts = {
  flush: constants.BROTLI_OPERATION_PROCESS,
  finishFlush: constants.BROTLI_OPERATION_FINISH,
  fullFlush: constants.BROTLI_OPERATION_FLUSH,
};
function collectParams(opts, isValid, errCode, label) {
  const params = new Map();
  if (opts && opts.params) {
    for (const origKey of Object.keys(opts.params)) {
      const key = +origKey;
      if (Number.isNaN(key) || !isValid(key) || params.has(key)) {
        throw codedError(RangeError, errCode, origKey + " is not a valid " + label + " parameter");
      }
      const value = opts.params[origKey];
      if (typeof value !== "number" && typeof value !== "boolean") {
        throw invalidArgType("options.params[key]", "of type number", value);
      }
      params.set(key, +value);
    }
  }
  return params;
}

class Brotli extends ZlibBase {
  constructor(opts, mode) {
    const params = collectParams(opts, (k) => k >= 0 && k <= kMaxBrotliParam, "ERR_BROTLI_INVALID_PARAM", "Brotli");
    const handleOpts = {};
    if (mode === constants.BROTLI_ENCODE) {
      if (params.has(constants.BROTLI_PARAM_QUALITY)) handleOpts.quality = params.get(constants.BROTLI_PARAM_QUALITY);
      if (params.has(constants.BROTLI_PARAM_LGWIN)) handleOpts.lgwin = params.get(constants.BROTLI_PARAM_LGWIN);
    }
    super(opts, mode === constants.BROTLI_ENCODE ? "BrotliEncode" : "BrotliDecode", handleOpts, brotliDefaultOpts);
  }
}
class BrotliCompress extends Brotli { constructor(opts) { super(opts, constants.BROTLI_ENCODE); } }
class BrotliDecompress extends Brotli { constructor(opts) { super(opts, constants.BROTLI_DECODE); } }

// --- Zstd ---
const zstdDefaultOpts = {
  flush: constants.ZSTD_e_continue,
  finishFlush: constants.ZSTD_e_end,
  fullFlush: constants.ZSTD_e_flush,
};
const zstdCParams = new Set(Object.keys(constants).filter((k) => k.startsWith("ZSTD_c_")).map((k) => constants[k]));
const zstdDParams = new Set([constants.ZSTD_d_windowLogMax]);

class Zstd extends ZlibBase {
  constructor(opts, mode) {
    const compress = mode === constants.ZSTD_COMPRESS;
    const valid = compress ? zstdCParams : zstdDParams;
    const params = collectParams(opts, (k) => valid.has(k), "ERR_ZSTD_INVALID_PARAM", "zstd");
    const handleOpts = {};
    if (compress) {
      if (params.has(constants.ZSTD_c_compressionLevel)) handleOpts.level = params.get(constants.ZSTD_c_compressionLevel);
      if (params.has(constants.ZSTD_c_checksumFlag)) handleOpts.checksum = params.get(constants.ZSTD_c_checksumFlag) ? 1 : 0;
      if (params.has(constants.ZSTD_c_windowLog)) handleOpts.windowLog = params.get(constants.ZSTD_c_windowLog);
      const pledged = opts && opts.pledgedSrcSize;
      if (pledged !== undefined) {
        if (typeof pledged !== "number") throw invalidArgType("options.pledgedSrcSize", "of type number", pledged);
        handleOpts.pledgedSrcSize = pledged;
      }
    } else if (params.has(constants.ZSTD_d_windowLogMax)) {
      handleOpts.windowLogMax = params.get(constants.ZSTD_d_windowLogMax);
    }
    super(opts, compress ? "ZstdCompress" : "ZstdDecompress", handleOpts, zstdDefaultOpts);
  }
}
class ZstdCompress extends Zstd { constructor(opts) { super(opts, constants.ZSTD_COMPRESS); } }
class ZstdDecompress extends Zstd { constructor(opts) { super(opts, constants.ZSTD_DECOMPRESS); } }

// --- convenience functions ---
function zlibBufferSync(engine, buffer) {
  buffer = toBuffer(buffer);
  try {
    buffer = processChunkSync(engine, buffer, engine._finishFlushFlag);
  } finally {
    closeHandle(engine);
  }
  if (engine._info) return { buffer, engine };
  return buffer;
}
function zlibBuffer(engine, buffer, callback) {
  if (typeof callback !== "function") throw invalidArgType("callback", "of type function", callback);
  buffer = toBuffer(buffer);
  let result, error = null;
  try {
    result = zlibBufferSync(engine, buffer);
  } catch (err) {
    error = err;
  }
  process.nextTick(() => (error ? callback(error) : callback(null, result)));
}
function syncFn(Ctor) {
  return function (buffer, opts) {
    return zlibBufferSync(new Ctor(opts), buffer);
  };
}
function asyncFn(Ctor) {
  return function (buffer, opts, callback) {
    if (typeof opts === "function") {
      callback = opts;
      opts = {};
    }
    return zlibBuffer(new Ctor(opts), buffer, callback);
  };
}

const deflate = asyncFn(Deflate), deflateSync = syncFn(Deflate);
const gzip = asyncFn(Gzip), gzipSync = syncFn(Gzip);
const deflateRaw = asyncFn(DeflateRaw), deflateRawSync = syncFn(DeflateRaw);
const unzip = asyncFn(Unzip), unzipSync = syncFn(Unzip);
const inflate = asyncFn(Inflate), inflateSync = syncFn(Inflate);
const gunzip = asyncFn(Gunzip), gunzipSync = syncFn(Gunzip);
const inflateRaw = asyncFn(InflateRaw), inflateRawSync = syncFn(InflateRaw);
const brotliCompress = asyncFn(BrotliCompress), brotliCompressSync = syncFn(BrotliCompress);
const brotliDecompress = asyncFn(BrotliDecompress), brotliDecompressSync = syncFn(BrotliDecompress);
const zstdCompress = asyncFn(ZstdCompress), zstdCompressSync = syncFn(ZstdCompress);
const zstdDecompress = asyncFn(ZstdDecompress), zstdDecompressSync = syncFn(ZstdDecompress);

const createDeflate = (o) => new Deflate(o);
const createInflate = (o) => new Inflate(o);
const createDeflateRaw = (o) => new DeflateRaw(o);
const createInflateRaw = (o) => new InflateRaw(o);
const createGzip = (o) => new Gzip(o);
const createGunzip = (o) => new Gunzip(o);
const createUnzip = (o) => new Unzip(o);
const createBrotliCompress = (o) => new BrotliCompress(o);
const createBrotliDecompress = (o) => new BrotliDecompress(o);
const createZstdCompress = (o) => new ZstdCompress(o);
const createZstdDecompress = (o) => new ZstdDecompress(o);

const api = {
  crc32,
  Deflate, Inflate, Gzip, Gunzip, DeflateRaw, InflateRaw, Unzip,
  BrotliCompress, BrotliDecompress, ZstdCompress, ZstdDecompress,
  deflate, deflateSync, gzip, gzipSync, deflateRaw, deflateRawSync, unzip, unzipSync,
  inflate, inflateSync, gunzip, gunzipSync, inflateRaw, inflateRawSync,
  brotliCompress, brotliCompressSync, brotliDecompress, brotliDecompressSync,
  zstdCompress, zstdCompressSync, zstdDecompress, zstdDecompressSync,
  createDeflate, createInflate, createDeflateRaw, createInflateRaw, createGzip, createGunzip, createUnzip,
  createBrotliCompress, createBrotliDecompress, createZstdCompress, createZstdDecompress,
};
Object.defineProperty(api, "constants", { value: constants, writable: false, enumerable: true, configurable: false });
Object.defineProperty(api, "codes", { value: codes, writable: false, enumerable: true, configurable: false });
// Every constant is also a (non-enumerable, read-only) top-level export.
for (const k of Object.keys(constants)) {
  Object.defineProperty(api, k, { value: constants[k], writable: false, enumerable: false, configurable: false });
}

export {
  crc32,
  Deflate, Inflate, Gzip, Gunzip, DeflateRaw, InflateRaw, Unzip,
  BrotliCompress, BrotliDecompress, ZstdCompress, ZstdDecompress,
  deflate, deflateSync, gzip, gzipSync, deflateRaw, deflateRawSync, unzip, unzipSync,
  inflate, inflateSync, gunzip, gunzipSync, inflateRaw, inflateRawSync,
  brotliCompress, brotliCompressSync, brotliDecompress, brotliDecompressSync,
  zstdCompress, zstdCompressSync, zstdDecompress, zstdDecompressSync,
  createDeflate, createInflate, createDeflateRaw, createInflateRaw, createGzip, createGunzip, createUnzip,
  createBrotliCompress, createBrotliDecompress, createZstdCompress, createZstdDecompress,
  constants, codes,
};
export default api;
`
