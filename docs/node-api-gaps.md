# Node API gap inventory

What real Node v26.3.0 exposes that noderati does not, measured on 2026-10-21
against `main` at `bff791f` (paserati at `be4b7c4d` plus uncommitted checker
changes).

## How this was measured

One probe script ran under both `node` and `./noderati`. For every entry in
Node's `module.builtinModules` it called `require("node:<name>")` (falling back
to `import()` when `require` failed) and recorded every own and inherited key
of the returned object. It also recorded `globalThis`, `process`, and the
methods on real instances of the main classes (a `Buffer`, a `Duplex`, a
`net.Socket`, an `http.Server`, and so on). The lists below are Node's keys
minus noderati's.

This measures the **real** surface. ESM named imports of a builtin always
link, because `augmentShimExports` (`internal/host/node_exports.go`) fills
every missing name with a stub that throws `ERR_NODERATI_NOT_IMPLEMENTED`.
The default export, `require()`, and feature detection (`if (fs.glob)`) see
only what is actually implemented, and that is what this inventory counts.

The probe only checks that a name exists. A name that exists can still be
wrong or partial; the APIs that exist but throw are listed separately in
section 2.

Constants (`ALL_CAPS` names and `*.constants.*`) are counted apart from
callable API so they don't swamp the numbers.

---

## 1. Builtin modules that don't exist at all

| Module | Node exports | Notes |
|---|---|---|
| `cluster` | 28 | |
| `dgram` | 2 | `Socket`, `createSocket` (UDP) |
| `domain` | 4 | deprecated, still `require`d by old packages |
| `inspector`, `inspector/promises` | 9 each | |
| `punycode` | 6 | deprecated, but still pulled in by `tr46`/`whatwg-url` chains |
| `repl` | 8 | |
| `sqlite` | 50 | `DatabaseSync`, `StatementSync`, `Session`, `backup` |
| `stream/consumers` | 6 | `text`, `json`, `buffer`, `arrayBuffer`, `blob`, `bytes` |
| `stream/web` | 17 | the Web Streams classes are globals already, but not importable from here |
| `sys` | 31 | alias of `util` |
| `test`, `test/reporters` | 19 / 5 | the `node:test` runner |
| `trace_events` | 2 | |
| `wasi` | 1 | `WASI` |
| `sea` | 5 | single-executable assets |

Importing any of these fails with `ERR_UNKNOWN_BUILTIN_MODULE`
("No such built-in module").

### Subpath modules that work only from ESM

`require()` fails with "Cannot find module" for these; `import` works:

- `assert/strict`
- `dns/promises`
- `path/posix`, `path/win32`
- `readline/promises`

---

## 2. Present but throws or does nothing

These names exist, so feature detection passes, but calling them fails.

| API | Behaviour |
|---|---|
| `worker_threads.Worker` | throws: no second concurrent JS execution context |
| `zlib` Brotli and Zstd (`createBrotli*`, `createZstd*`) | throws: Go stdlib has no codec |
| `http2.connect`, `http2.createServer`, `createSecureServer`, … | throws "not implemented in noderati yet" (stub module) |
| `crypto.createPublicKey`, `crypto.createPrivateKey` | throws "not implemented" |
| `WebAssembly.Memory`, `WebAssembly.Global` constructors | throw; only instances from `instance.exports` work. Table imports are also unsupported |
| `diagnostics_channel` `AsyncLocalStorage` binding (`bindStore`/`runStores`) | no-op |
| `new net.Socket()` with no arguments | dials `[::1]:0` immediately and emits `error`; in Node an unconnected socket does nothing until `.connect()` |
| `require()` of an ES module | the file is compiled as CommonJS, so `import`/`import.meta` in it is a compile error. Node 22+ supports `require(esm)` |

---

## 3. Gaps in modules that exist

Counts are callable/class exports present / Node total. Constants are listed
apart.

### High-impact (commonly used by npm packages)

**`buffer` / `Buffer` instances.** The module exports are almost complete
(missing `File`, `resolveObjectURL`), but a `Buffer` instance has only the
`Uint8Array` surface plus `toString`/`write`/`slice` and similar. It is
missing:

- every numeric accessor: `read{Int,UInt,Uint}{8,16,32}{LE,BE}`,
  `read{Int,UInt,Uint}{LE,BE}`, `readBig{Int,UInt,Uint}64{LE,BE}`,
  `read{Float,Double}{LE,BE}`, and the matching `write*` methods
- `equals`, `compare`, `swap16`, `swap32`, `swap64`, `inspect`
- the per-encoding fast paths `{utf8,ascii,latin1,hex,base64,base64url,ucs2}{Slice,Write}`
- `parent`, `offset`

**`crypto`: 11 of ~93 present.** Present: `createHash`, `createHmac`,
`randomBytes`, `randomUUID`, `getRandomValues`, `getHashes`,
`timingSafeEqual`, `createSecretKey`, `KeyObject` (missing `equals`, `type`),
and `createPublicKey`/`createPrivateKey`, which throw. Missing:

- ciphers: `createCipheriv`, `createDecipheriv`, `Cipheriv`, `Decipheriv`,
  `getCiphers`, `getCipherInfo`
- KDFs: `pbkdf2`, `pbkdf2Sync`, `scrypt`, `scryptSync`, `hkdf`, `hkdfSync`,
  `argon2`, `argon2Sync`
- signing and asymmetric: `createSign`, `createVerify`, `Sign`, `Verify`,
  `sign`, `verify`, `publicEncrypt`, `publicDecrypt`, `privateEncrypt`,
  `privateDecrypt`, `encapsulate`, `decapsulate`
- key generation: `generateKey`, `generateKeySync`, `generateKeyPair`,
  `generateKeyPairSync`, `generatePrime`, `generatePrimeSync`, `checkPrime`,
  `checkPrimeSync`
- DH/ECDH: `createDiffieHellman`, `createDiffieHellmanGroup`,
  `getDiffieHellman`, `diffieHellman`, `createECDH`, `DiffieHellman`,
  `DiffieHellmanGroup`, `ECDH`, `getCurves`
- random: `randomFill`, `randomFillSync`, `randomInt`, `randomUUIDv7`,
  `pseudoRandomBytes`, `prng`, `rng`
- other: `hash` (one-shot), `X509Certificate`, `Certificate`, `Hash`, `Hmac`
  (class exports), `getFips`, `setFips`, `fips`, `setEngine`,
  `secureHeapUsed`
- `webcrypto` and `subtle` are missing entirely, so there is no
  `crypto.subtle.digest`/`sign`/`importKey` and so on, either here or on the
  global (see section 4)
- all 57 `crypto.constants`
- the `Hash`/`Hmac` objects that `createHash`/`createHmac` return are not
  streams: only `update` and `digest` exist (no `copy`, `write`, `pipe`, or
  `on`)

**`fs`: 79/136 (constants: 12/55).** Present: most sync and
callback primitives (`open`/`read`/`write`/`stat`/`readdir`/`mkdir`/`rename`/
`unlink`/`watch`, and so on). Missing:

- streams: `createReadStream`, `createWriteStream`, `ReadStream`,
  `WriteStream`, `FileReadStream`, `FileWriteStream`, `Utf8Stream`
- callback forms: `appendFile`, `copyFile`, `cp`, `mkdtemp`, `rm`,
  `truncate`, `exists`, `opendir`, `readv`, `statfs`, `lutimes`, `lchmod`,
  `glob`
- sync forms: `cpSync`, `mkdtempSync`, `mkdtempDisposableSync`,
  `opendirSync`, `truncateSync`, `readvSync`, `statfsSync`, `lutimesSync`,
  `lchmodSync`, `globSync`
- classes: `Dir`, `Dirent`, `Stats` (instances exist; the constructors aren't
  exported)
- `openAsBlob`
- most of `fs.constants` (43 of 55 missing)
- `Stats` instances lack `atimeInstant`/`mtimeInstant`/`ctimeInstant`/`birthtimeInstant`; `Dirent` lacks `parentPath`

**`fs/promises`: 10/33.** Present: `access`, `lstat`, `mkdir`, `readFile`,
`readdir`, `realpath`, `rm`, `stat`, `unlink`, `writeFile`. Missing:
`open` (so there is no `FileHandle` at all), `appendFile`, `chmod`, `chown`,
`copyFile`, `cp`, `glob`, `lchmod`, `lchown`, `link`, `lutimes`, `mkdtemp`,
`mkdtempDisposable`, `opendir`, `readlink`, `rename`, `rmdir`, `statfs`,
`symlink`, `truncate`, `utimes`, `watch`, `constants`.

**`stream`: 11/40.** Present: `Readable`, `Writable`, `Duplex`, `Transform`,
`PassThrough`, `pipeline`, `isDisturbed`, `isErrored`. Missing module
exports: `Stream` (the legacy base class), `finished`, `promises`
(`stream/promises` has only `pipeline`, not `finished`), `compose`,
`addAbortSignal`, `destroy`, `duplexPair`, `isReadable`, `isWritable`,
`isDestroyed`, `getDefaultHighWaterMark`, `setDefaultHighWaterMark`, and the
re-exported `EventEmitter` statics.

Instance gaps:

- `Readable`: the iterator helpers `map`, `filter`, `forEach`, `toArray`,
  `some`, `every`, `find`, `reduce`, `flatMap`, `drop`, `take`, `compose`;
  also `wrap`, `readableAborted`, `readableBuffer`
- `Writable`: `pipe` (it should throw `ERR_STREAM_CANNOT_PIPE`), `_writev`
- `Duplex`/`Transform`/`PassThrough`: none of the readable side is present on
  the instance (`read`, `pause`, `resume`, `isPaused`, `setEncoding`,
  `unshift`, `unpipe`, `readable*` getters, async iterator), and neither is
  `cork`/`uncork`/`setDefaultEncoding`/`writable*` getters/`allowHalfOpen`
  (Transform also lacks `destroy`/`destroyed`). They behave like a
  `Writable` plus a few methods.

**`http`: 8/18.** Present: `Agent`, `IncomingMessage`, `Server`,
`ServerResponse`, `createServer`, `request`, `get`, `maxHeaderSize`.
Missing: `ClientRequest`, `OutgoingMessage` (class exports),
`globalAgent`, `validateHeaderName`, `validateHeaderValue`,
`setMaxIdleHTTPParsers`, `setGlobalProxyFromEnv`, `WebSocket`,
`CloseEvent`, `MessageEvent`, `METHODS`, `STATUS_CODES`. Instance gaps:

- `http.Server`: `closeAllConnections`, `closeIdleConnections`,
  `getConnections`, and the config fields `timeout`, `keepAliveTimeout`,
  `headersTimeout`, `requestTimeout`, `maxRequestsPerSocket`,
  `maxHeaderSize`
- `ClientRequest`: very thin. Missing `setHeader`, `getHeader`,
  `getHeaders`, `getHeaderNames`, `hasHeader`, `removeHeader`, `flushHeaders`,
  `headersSent`, `abort`/`aborted`, `setNoDelay`, `setSocketKeepAlive`,
  `onSocket`, `method`, `path`, `host`, `protocol`, `agent`, `res`, `cork`,
  `uncork`, `writable*` state, `reusedSocket`, `maxHeadersCount`

**`https`: 3/6.** Missing `createServer`, `Server`, `globalAgent`. There is
no HTTPS server.

**`net`: 6/15.** Present: `Socket`, `connect`, `createConnection`, `isIP`,
`isIPv4`, `isIPv6`. Missing: `createServer`, `Server` (no TCP server),
`BlockList`, `SocketAddress`, `Stream`, `get`/`setDefaultAutoSelectFamily`,
and `get`/`setDefaultAutoSelectFamilyAttemptTimeout`. `Socket` instances
lack `connect` as a method, `localAddress`, `localPort`, `localFamily`,
`remoteAddress`, `remotePort`, `remoteFamily`, `bytesRead`, `bytesWritten`,
`readyState`, `pipe`, `push`, `unshift`, `destroySoon`, `resetAndDestroy`,
the iterator helpers, and the `readable*`/`writable*` getters.

**`tls`: 2/12.** Present: `TLSSocket`, `connect`. Missing: `createServer`,
`Server`, `createSecureContext`, `SecureContext`, `checkServerIdentity`,
`getCiphers`, `rootCertificates`, `getCACertificates`,
`setDefaultCACertificates`, `convertALPNProtocols`, `DEFAULT_*` constants.

**`dns`: 1 function of ~45.** Present: only `lookup` (and
`promises.lookup`). Missing: the whole resolver API (`resolve`,
`resolve4`, `resolve6`, `resolveAny`, `resolveCaa`, `resolveCname`,
`resolveMx`, `resolveNaptr`, `resolveNs`, `resolvePtr`, `resolveSoa`,
`resolveSrv`, `resolveTlsa`, `resolveTxt`, `reverse`), `lookupService`,
`Resolver`, `getServers`, `setServers`, `get`/`setDefaultResultOrder`, all
the error-code constants, and the same set under `dns.promises` /
`dns/promises`.

**`process`: 43/83.** Missing:

- `chdir`, `uptime`, `ppid`, `argv0`, `release`, `config`, `features`
- `abort`, `reallyExit`, `execve`
- `resourceUsage`, `threadCpuUsage`, `availableMemory`, `constrainedMemory`
- `getBuiltinModule`, `loadEnvFile`, `mainModule`, `moduleLoadList`,
  `binding`, `dlopen`
- `setUncaughtExceptionCaptureCallback`,
  `hasUncaughtExceptionCaptureCallback`,
  `addUncaughtExceptionCaptureCallback`
- `setuid`, `setgid`, `seteuid`, `setegid`, `setgroups`, `initgroups`
- `ref`, `unref`, `getActiveResourcesInfo`, `finalization`, `report`,
  `allowedNodeEnvironmentFlags`, `debugPort`, `openStdin`, `domain`
- `sourceMapsEnabled`, `setSourceMapsEnabled`

`process.stdout`/`stderr` lack the full `Writable` state
(`writableEnded`, `writableFinished`, `writableNeedDrain`, `cork`,
`destroy`, `pipe`, and so on). `process.stdin` is a bare object, not a
`net.Socket`/`tty.ReadStream`-shaped stream: 22 of 118 members.

**`child_process`: 7/8.** All the entry points exist; the `ChildProcess`
class isn't exported. Instances lack `connected`, `spawnargs`, `spawn`.

**`module`: 6/25.** Present: `Module`, `builtinModules`, `createRequire`.
Missing: `isBuiltin`, `register`, `registerHooks`, `findPackageJSON`,
`syncBuiltinESMExports`, `stripTypeScriptTypes`, `SourceMap`,
`findSourceMap`, `get`/`setSourceMapsSupport`, `enableCompileCache`,
`flushCompileCache`, `getCompileCacheDir`, `runMain`, `wrap`, `wrapper`,
`globalPaths`, `constants`. Also `require.main`, `module.paths`, and
`module.require` are undefined in CJS. `import.meta` has only `url`: no
`dirname`, `filename`, `resolve`, or `main`.

### Medium-impact

**`console`: 15/25.** The global and the module both lack `table`,
`dir`, `dirxml`, `assert`, `timeLog`, `timeStamp`, `profile`, `profileEnd`,
`context`, and `createTask`. A `new Console(...)` instance additionally lacks
`time`, `timeEnd`, `count`, `countReset`, `clear`, and `groupCollapsed`.

**`util`: 22/31.** Missing: `MIMEType`, `MIMEParams`, `parseEnv`,
`getCallSites`, `diff`, `transferableAbortController`,
`transferableAbortSignal`, `setTraceSigInt`,
`convertProcessSignalToExitCode`. `TextEncoder` lacks `encodeInto`.
`util/types` is complete.

**`url`: 8/14.** Missing: `format`, `resolveObject`, `urlToHttpOptions`,
`fileURLToPathBuffer`, `Url` (legacy class), `URLPattern`. **`URL`
instances have no `searchParams`** (undefined).

**`zlib`: 33/107 (constants: 47/217).** Deflate/inflate/gzip/
unzip work. Missing: `brotliCompress`, `brotliCompressSync`,
`brotliDecompress`, `brotliDecompressSync`, `zstd*` one-shots, `crc32`,
`codes`, the `BrotliCompress`/`BrotliDecompress`/`ZstdCompress`/
`ZstdDecompress` classes, and all `ZSTD_*`/most `BROTLI_*` constants.
Stream instances lack `params` and the readable-side `Duplex` surface.

**`perf_hooks`: 1 of 21 functions.** Only `performance` is present, with
`now`, `mark`, `measure`, `getEntriesByName`, and `clearMarks`. Missing: `PerformanceObserver`,
`PerformanceEntry`, `PerformanceMark`, `PerformanceMeasure`,
`PerformanceObserverEntryList`, `PerformanceResourceTiming`, `Performance`,
`createHistogram`, `monitorEventLoopDelay`, `eventLoopUtilization`,
`timerify`, `constants`. On `performance`: `timeOrigin`, `toJSON`,
`getEntries`, `clearResourceTimings`, `setResourceTimingBufferSize`, and the
`EventTarget` methods are missing.

**`async_hooks`: 2/7.** Present: `AsyncLocalStorage` (missing `withScope`,
`name`), `AsyncResource` (missing `bind`). Missing: `createHook`,
`executionAsyncId`, `triggerAsyncId`, `executionAsyncResource`,
`asyncWrapProviders`.

**`worker_threads`: 10/21.** `Worker` throws (see section 2). Missing:
`BroadcastChannel`, `SHARE_ENV`, `getEnvironmentData`, `setEnvironmentData`,
`markAsUntransferable`, `isMarkedAsUntransferable`,
`moveMessagePortToContext`, `postMessageToThread`, `isInternalThread`,
`threadName`, `locks`.

**`vm`: 6/10.** Missing: `compileFunction`, `createScript`,
`measureMemory`, `constants`. `Script` lacks `createCachedData`.

**`v8`: 9/24.** Missing: `cachedDataVersionTag`,
`getHeapSpaceStatistics`, `getHeapCodeStatistics`,
`getCppHeapStatistics`, `getHeapSnapshot`, `writeHeapSnapshot`,
`setHeapSnapshotNearHeapLimit`, `takeCoverage`, `stopCoverage`,
`queryObjects`, `startCpuProfile`, `startHeapProfile`,
`isStringOneByteRepresentation`, `promiseHooks`, `GCProfiler`.

**`querystring`: 4/7.** Missing the aliases `decode`/`encode` and
`unescapeBuffer`.

**`path`: 41/48.** Missing `matchesGlob`, and `path.posix.posix`,
`path.posix.win32`, `path.win32.posix`, `path.win32.win32` (the
self-references). `path/posix` and `path/win32` don't expose `.posix`/`.win32`
either.

**`readline`**: complete at module level. `Interface` lacks `clearLine`,
`setupHistoryManager`, `historyIndex`, `tabSize`, `isCompletionEnabled`.

### Small

- `assert`: missing `Assert` class, `partialDeepStrictEqual`
- `events`: missing `kMaxEventTargetListeners`,
  `kMaxEventTargetListenersWarned`; `EventEmitterAsyncResource` lacks
  `asyncResource`
- `diagnostics_channel`: missing `BoundedChannel`, `boundedChannel`;
  `Channel` lacks `withStoreScope`
- `http2`: only `Http2ServerRequest`/`Http2ServerResponse` are missing as
  names, but the module is a stub (section 2)
- `string_decoder`: complete; `StringDecoder` lacks the legacy
  `lastChar`/`lastNeed`/`lastTotal` fields
- `constants` (the deprecated module): has only 12 of 230 names; errno,
  signal, priority, `O_*`, `RTLD_*`, crypto and `SSL_OP_*` values are all
  missing. `os.constants` is complete.

### Complete

`os`, `readline` (module level), `readline/promises`, `string_decoder`,
`timers`, `timers/promises`, `tty`, `util/types`.

---

## 4. Missing globals

| Area | Missing |
|---|---|
| Web Crypto | `crypto`, `Crypto`, `CryptoKey`, `SubtleCrypto` |
| Web Streams (partial) | `ReadableStreamDefaultReader`, `ReadableStreamBYOBReader`, `ReadableStreamBYOBRequest`, `ReadableByteStreamController`, `ReadableStreamDefaultController`, `WritableStreamDefaultWriter`, `WritableStreamDefaultController`, `TransformStreamDefaultController`, `ByteLengthQueuingStrategy`, `CountQueuingStrategy`, `TextEncoderStream`, `TextDecoderStream`, `CompressionStream`, `DecompressionStream`. (`ReadableStream`, `WritableStream`, `TransformStream` exist.) |
| Performance | `PerformanceObserver`, `PerformanceEntry`, `PerformanceMark`, `PerformanceMeasure`, `PerformanceObserverEntryList`, `PerformanceResourceTiming`, `Performance` |
| Messaging and network | `BroadcastChannel`, `WebSocket`, `CloseEvent`, `ErrorEvent` |
| Navigator and storage | `navigator`, `Navigator`, `localStorage`, `sessionStorage`, `Storage` |
| Misc | `URLPattern`, `QuotaExceededError` |

Present and working at name level: `fetch`/`Request`/`Response`/`Headers`,
`structuredClone`, `atob`/`btoa`, `setImmediate`, `queueMicrotask`,
`AbortController`/`AbortSignal`, `Blob`/`File`, `TextEncoder`/`TextDecoder`,
`URL`/`URLSearchParams`, `Event`/`EventTarget`/`CustomEvent`,
`MessageChannel`/`MessagePort`, `WebAssembly`, `DOMException`, `performance`.

---

## 5. Suggested order

Ranked by how often real packages hit each gap:

1. **`Buffer` numeric read/write, `equals`, `compare`.** Binary parsers,
   protocol clients, and `safe-buffer`-era code all use them.
2. **`URL.prototype.searchParams`.** Ubiquitous, and the failure is silent
   (undefined, not a throw).
3. **The full `stream.Duplex`/`Transform` surface, `stream.finished`, and
   `stream.Stream`.** Many packages call `require('stream').Stream` or
   `finished(...)` at load time.
4. **`fs.createReadStream`/`createWriteStream` and `fs.promises.open` with
   `FileHandle`,** plus the rest of `fs/promises`.
5. **`crypto`: `pbkdf2`, `scrypt`, ciphers, `sign`/`verify`,
   `generateKeyPair`, and `webcrypto`/global `crypto.subtle`.** Auth, JWT,
   and AWS SDK paths need them.
6. **`net.createServer`, `https.createServer`, `tls.createServer`.**
7. **`process.chdir`, `process.uptime`, `process.ppid`, `process.argv0`,
   `process.release`, and `process.getBuiltinModule`.**
8. **`require()` of subpath builtins (`assert/strict`, `path/posix`, …),
   `require(esm)`, and `import.meta.dirname`/`filename`/`resolve`.**
9. **`console.table`/`dir`/`timeLog`, and `util.MIMEType`/`parseEnv`.**
10. **The missing modules, by demand:** `stream/web`, `stream/consumers`,
    `punycode`, `dgram`, `sqlite`, `node:test`, and then the rest.

---

## Progress

Re-measure with `node scripts/apigap/report.mjs` (build `./noderati`
first). It prints per-module coverage and every missing name.

| Date | Commit | Module API (excl. constants) | Notes |
|---|---|---|---|
| 2026-10-21 | `09e7904` | — | inventory written |
| 2026-10-21 | `cb59254` | 559/1310 (42.7%) | Buffer instances complete; `URL` is paserati's (live `searchParams`) |
| 2026-10-22 | `78c7409` | 578/1310 (44.1%) | real `util.inspect`/`util.format`, console (incl. `table`), util extras |
| 2026-10-22 | `2e92146` | 658/1310 (50.2%) | fs and fs/promises complete: file streams, `FileHandle`, `Dir`, `glob`, `cp`, constants |
| 2026-10-22 | `c32cfaf` | 696/1310 (53.1%) | stream rebuilt: full Duplex/Transform, `finished`, `compose`, iterator helpers, `stream/web`, `stream/consumers` |
| 2026-10-22 | `0486f31` | 793/1310 (60.5%) | net/tls/https servers, Unix sockets, dns resolver API, `node:dgram`; `new net.Socket()` no longer dials |
| 2026-10-22 | `36f8228` | 922/1310 (70.4%) | real `worker_threads.Worker`; Brotli/Zstd; perf_hooks, async_hooks, v8, vm complete |
| 2026-10-22 | `261af01` | 1004/1310 (76.6%) | crypto complete (ciphers, KDFs, keys, sign/verify, X509) and Web Crypto |

The per-module gap lists in sections 1-4 above predate these merges; the
report script is the current source of truth.

### Not merged yet

Branch `worktree-agent-a77b21f1fc0efd6b9` (process, module system, small
modules) is unfinished. Its last commit is a WIP. It moves the CommonJS
loader into `node:module`, adds `require(esm)`, subpath-builtin `require`,
`import.meta.dirname`/`filename`/`resolve`, most missing `process`
members and exit/beforeExit events, and the missing modules (`sys`,
`punycode`, `trace_events`, `inspector`, `sea`, `domain`, `cluster`,
`node:test`, `wasi`, `repl`). Finish it, then merge; expect conflicts in
`cjs.go`/`lazy_jsmodule.go` with the stream merge.

### Known open issues

- `process.on("exit")` never fires on normal exit (fixed on the unmerged
  branch above).
- `process.nextTick` queued inside a promise callback runs before the rest
  of the microtask queue (noderati's scheduler, not paserati).
- Loading `node:stream` costs ~40-60 ms (was ~9 ms); `util.inspect` ~10 ms
  on first use.
- Web Crypto lacks ML-DSA, ML-KEM, ChaCha20-Poly1305, Ed448/X448, AES-OCB,
  KMAC.
- `worker_threads`: `SHARE_ENV` copies rather than shares; MessagePorts
  can't be transferred to a worker; worker stdout isn't piped through the
  parent.
- http server runs on Go's `net/http`: `maxRequestsPerSocket` isn't
  enforced, `_connectionListener` throws; `http.WebSocket` is undefined.
- Several files under `internal/host` aren't `gofmt`-clean.

Found since the inventory, not visible to a name-only probe:

- `util.inspect` coerces to string: objects print `[object Object]`,
  `Map` prints `<unknown type 22>`. `console.log` of objects and
  `util.format('%o')` depend on it.
- paserati: `JSON.stringify` never uses exponent notation
  (`JSON.stringify(1.5e-36)` prints 36 zeros, `JSON.stringify(1e21)`
  prints 22 digits).
- paserati `URL`: no IDNA (`例え.jp` isn't punycoded), no IPv4 number
  parsing (`0x7f.1`), `blob:` URLs get a `null` origin, invalid-URL errors
  lack `code: 'ERR_INVALID_URL'`, non-ASCII in the query isn't
  percent-encoded, `%7e` gets decoded to `~`, and `..` pops a `file:`
  URL's Windows drive letter.

### paserati bugs found while filling gaps

Each is a one-line repro; noderati works around most of them.

- `instanceof` ignores `Symbol.hasInstance` (hit by four areas; makes
  `duplex instanceof Writable` false).
- A subclass inherits a parent `name` set via `defineProperty`:
  `class P{}; Object.defineProperty(P,"name",{value:"Z"}); class R extends P{}; R.name`
  is `"Z"`.
- `Error.captureStackTrace`/`error.stack` lack the `Name: message` first
  line; `return f()` drops the caller's frame.
- `for (const {0:k,1:v} of new Map([[1,2]]))` binds `k=false`.
- `"a\nb".split(/(?<=\n)/)` crashes the VM (slice bounds out of range).
- `Number.parseInt('12.5')` is `NaN` (global `parseInt` is fine).
- `Object.defineProperties(o, {[sym]: {value: 1}})` ignores symbol keys.
- Accessors defined on native functions read back `undefined`;
  `Reflect.set` doesn't call a setter on a function.
- `[...{*[Symbol.iterator](){yield 1}}]` throws "Iterator is not an object".
- Methods get a `prototype` own property.
- `AggregateError#errors` is enumerable.
- A lone surrogate in a string literal doesn't match `/[\ud800-\udbff]/`.
- for-in on a function skips inherited properties.
- Throwing in a `for await` body reaches `catch` before the async
  generator's `finally` runs.
- `JSON.stringify(await rs.getReader().read())` is `{}`.
- `function f(){return o.m(...mk())}` returns the spread array.
- `ArrayBuffer.isView(new DataView(new ArrayBuffer(1)))` is `false`.
- `Object.setPrototypeOf(A, Transform)` changes `A.length`.
- `Uint8Array.from(new Uint8Array([1,2])).length` is `0`.
- `DefaultAsyncRuntime.WaitForExternalOp` can miss a wakeup from another
  goroutine.
- 32-bit builds fail: `0xFFFFFFFE` overflows `int` (`vm/object.go:1445`,
  `vm/vm.go:9479`).
- `JSON.stringify` never uses exponent notation (listed above), and the
  `URL` issues listed above.
