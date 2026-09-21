package host

import (
	"os"
	"path/filepath"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// fsStatsToVM manually builds the same object shape a typed `m.Function`
// return gets for free via paserati's struct-reflection (see statSync's
// `*fsStats` return) -- needed here because a classic Node callback-style
// function hands its result to a JS callback directly, not through a Go
// function return, so there's no reflection step to piggyback on. Mirrors
// dirent.go's newDirent, which manually builds a vm object for exactly the
// same reason.
func fsStatsToVM(vmInst *vm.VM, s *fsStats) vm.Value {
	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	obj.SetOwn("size", vm.NumberValue(float64(s.Size)))
	obj.SetOwn("mtimeMs", vm.NumberValue(s.MtimeMs))
	obj.SetOwn("mtime", s.Mtime)
	boolFn := func(v bool) vm.Value {
		return vm.NewNativeFunction(0, false, "", func(_ []vm.Value) (vm.Value, error) {
			return vm.BooleanValue(v), nil
		})
	}
	obj.SetOwn("isFile", boolFn(s.IsFile()))
	obj.SetOwn("isDirectory", boolFn(s.IsDirectory()))
	obj.SetOwn("isSymbolicLink", boolFn(s.IsSymbolicLink()))
	obj.SetOwn("isBlockDevice", boolFn(s.IsBlockDevice()))
	obj.SetOwn("isCharacterDevice", boolFn(s.IsCharacterDevice()))
	obj.SetOwn("isFIFO", boolFn(s.IsFIFO()))
	obj.SetOwn("isSocket", boolFn(s.IsSocket()))
	return vm.NewValueFromPlainObject(obj)
}

// fsErrToVM extracts the real JS Error value wrapFsErr built (.code,
// .errno, .syscall, .path and all), for handing to a Node-style
// `(err, ...)` callback directly. Real Node callbacks get `null` on
// success, never `undefined`.
func fsErrToVM(err error) vm.Value {
	if err == nil {
		return vm.Null
	}
	if fe, ok := err.(*fsSystemError); ok {
		return fe.exception
	}
	return vm.Undefined
}

// scheduleCallback defers a Node-style callback invocation to the VM's own
// next-tick queue. Real Node's fs callback API guarantees the callback
// never fires synchronously within the same tick as the call that
// requested it (real Node dispatches the actual I/O to a libuv thread
// pool); doing the I/O synchronously here but only deferring the callback
// preserves that ordering contract, which real code depends on --
// including proper-lockfile's own retry logic, the real package this was
// added for.
func scheduleCallback(vmInst *vm.VM, cb vm.Value, cbArgs []vm.Value) {
	rt := vmInst.GetAsyncRuntime()
	rt.ScheduleNextTick(func() {
		// The returned error (a throwing callback) is deliberately
		// discarded here, matching the exact same house pattern
		// immediate_object.go's setImmediate dispatch already documents:
		// paserati's own setTimeout/nextTick (host_timers.go) silently
		// discard a throwing callback's error too, with no
		// uncaughtException, no nonzero exit, nothing printed at all --
		// confirmed as a real, dependency-free engine-level gap and filed
		// upstream as paserati#484 (this round, docs/real-node-plan.md),
		// after it was the actual reason a real webpack compile hung
		// forever with zero diagnostics (an exception thrown deep inside
		// webpack's own `AsyncQueue`'s `setImmediate` callback vanished
		// the same way). Not papered over here with a one-off fix that
		// would leave setTimeout/setImmediate still silently broken the
		// same way -- the real fix belongs in paserati's own event-loop
		// dispatch, which every one of these call sites shares.
		_, _ = vmInst.Call(cb, vm.Undefined, cbArgs)
	})
}

// fsAsyncEncodingFromValue extracts a real Node fs.readFile `options`
// argument's encoding, straight from the raw vm.Value (a string
// shorthand, or an `{encoding}` object) - the async-callback-signature
// equivalent of fsReadEncoding (fs.go), which takes a Go
// `[]interface{}` instead since the sync/promise variants never need to
// tell an options object apart from a callback function in the first
// place.
func fsAsyncEncodingFromValue(v vm.Value) (string, bool) {
	if v.IsString() {
		return v.AsString(), true
	}
	// Real callers (graceful-fs's own fs.readFile wrapper included) pass
	// non-object non-string values here too -- `false`/`null`/`undefined`
	// placeholders among the trailing args -- so this must reject anything
	// that isn't a plain object rather than assume the caller already
	// filtered it out; AsPlainObject() panics on any value whose type isn't
	// exactly TypeObject (IsObject() is too broad -- it also covers arrays
	// and functions, which AsPlainObject() would still panic on).
	if v.Type() != vm.TypeObject {
		return "", false
	}
	obj := v.AsPlainObject()
	if obj == nil {
		return "", false
	}
	enc, ok := obj.GetOwn("encoding")
	if !ok || !enc.IsString() {
		return "", false
	}
	return enc.AsString(), true
}

// findAsyncCallback picks the callable value out of a classic Node fs
// function's trailing variadic arguments -- shared by stat/lstat/access,
// which each accept an optional options-or-mode argument before the
// callback (mirrors the inline loop readFile/writeFile above already use
// for the same reason: a fixed-position `cb vm.Value` parameter silently
// receives the options value instead whenever a real caller passes one,
// and never gets called).
func findAsyncCallback(opts []vm.Value) vm.Value {
	for _, v := range opts {
		if v.IsCallable() {
			return v
		}
	}
	return vm.Undefined
}

// declareFSAsync adds real Node's classic callback-style fs functions --
// fs.mkdir(path, cb), fs.stat(path, cb), fs.rmdir(path, cb),
// fs.utimes(path, atime, mtime, cb), fs.realpath(path, cb),
// fs.readFile(path, [options], cb), fs.writeFile(path, data, [options], cb)
// -- alongside the *Sync (fs.go) and Promise-based (fspromises.go)
// variants already implemented. Real Node's fs module has three parallel
// styles (sync, callback, promise); noderati previously had only two --
// entirely missing, not just incomplete. `graceful-fs` (proper-lockfile's
// real, direct dependency, and used directly by many other real npm
// packages besides) patches every one of these onto its own exported fs
// object; without them, graceful-fs's own `.mkdir`/`.stat`/etc. are simply
// undefined, since there's nothing on noderati's real `fs` module for it
// to find and wrap (confirmed directly: `require('graceful-fs').mkdir`
// was `undefined`, which is what made `proper-lockfile`'s async `lock()`
// throw `TypeError: undefined is not a function` inside its own
// `options.fs.mkdir(...)` call).
//
// Scoped to exactly what real, evidenced usage needs so far --
// proper-lockfile's real async lock/unlock path, as actually called by
// pi-coding-agent's `auth-storage.js`, `settings-manager.js`, and
// `trust-manager.js`. `settings-manager.js`/`trust-manager.js` only use
// the *Sync API with `realpath: false`; `auth-storage.js`'s async
// `lockfile.lock()` call passes no `realpath` option at all, which
// defaults to `true` in real proper-lockfile -- so `fs.realpath` genuinely
// is reachable from real, evidenced usage (initially assumed unreachable
// here, corrected after testing against the *exact* real options object
// pi-coding-agent passes, not a hand-simplified stand-in). `fs.mkdir`/
// `fs.stat`/`fs.rmdir`/`fs.utimes`/`fs.realpath` are the full set
// proper-lockfile's async core (`lib/lockfile.js`) ever calls.
//
// `readFile`/`writeFile` added separately (docs/real-node-plan.md,
// Round 122): real, unmodified `eslint@9.36.0`'s own
// `lib/eslint/legacy-eslint.js` does `const writeFile =
// util.promisify(fs.writeFile);` at module top level - a real,
// unconditional call, not behind any guard - so a missing callback-style
// `fs.writeFile` threw `TypeError: "original" argument must be of type
// function` (util.promisify's own argument check) before any of that
// module's real logic ran, the exact same "one real callsite reaches an
// entirely-missing style of an otherwise-real module" shape this file's
// own mkdir/stat/etc. were added for. `readFile` added alongside it since
// it's the other half of the same pair and equally likely to be the next
// thing a real package's own top-level `promisify(fs.readFile)` needs.
//
// Extend further only when another real package's real call site demands
// more of the callback surface -- don't build ahead of evidence.
func declareFSAsync(m *driver.ModuleBuilder, vmInst *vm.VM) {
	m.Function("readFile", func(path string, opts ...vm.Value) (vm.Value, error) {
		// Real Node's fs.readFile(path, [options], callback) - the
		// callback is always the *last* argument, whether or not an
		// options argument precedes it. `opts` is declared `...vm.Value`
		// specifically (not `...interface{}`) so paserati's own
		// reflection-based arg binding (native_module.go's
		// vmValueToReflectValue) hands back the real, untouched value
		// for each trailing argument instead of converting it to a
		// concrete Go type first - `interface{}` conversion has no
		// representation for "a callable function", so a real callback
		// argument would silently become a Go `nil` and get lost before
		// this function ever saw it. Finding the callback among the
		// trailing args by checking IsCallable() (rather than assuming a
		// fixed position) is the same real-Node-overload-resolution
		// shape parseWriteEncodingAndCallback (net.go) already uses.
		cb := vm.Undefined
		encoding, hasEncoding := "", false
		for _, v := range opts {
			if v.IsCallable() {
				cb = v
				continue
			}
			if e, ok := fsAsyncEncodingFromValue(v); ok {
				encoding, hasEncoding = e, true
			}
		}
		fsTouch("read", path)
		b, err := os.ReadFile(path)
		if err != nil {
			scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "open", path, err))})
			return vm.Undefined, nil
		}
		// Same default as readFileSync/fs.promises.readFile (rounds 88-89,
		// docs/real-node-plan.md): a Buffer unless an explicit encoding
		// was given.
		var dataVal vm.Value
		if hasEncoding {
			dataVal = vm.NewString(encodeBufferBytes(b, encoding))
		} else {
			dataVal = wrapBuffer(vmInst, b)
		}
		scheduleCallback(vmInst, cb, []vm.Value{vm.Null, dataVal})
		return vm.Undefined, nil
	})
	m.Function("writeFile", func(path string, data vm.Value, opts ...vm.Value) (vm.Value, error) {
		// Real Node's fs.writeFile(file, data, [options], callback) -
		// same trailing-callback shape, and the same `...vm.Value` (not
		// `...interface{}`) reasoning, as readFile above.
		cb := vm.Undefined
		for _, v := range opts {
			if v.IsCallable() {
				cb = v
				break
			}
		}
		// Same real string-or-Buffer/TypedArray `data` handling as
		// writeFileSync/fs.promises.writeFile (net.go's valueToBytes).
		err := os.WriteFile(path, valueToBytes(vmInst, data), 0644)
		scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "open", path, err))})
		return vm.Undefined, nil
	})
	// os.Mkdir (non-recursive), deliberately -- matches real Node's own
	// fs.mkdir default (no {recursive:true} means parent dirs must
	// already exist) and is load-bearing here: proper-lockfile's whole
	// mutual-exclusion protocol depends on this call failing with EEXIST
	// when the lock dir already exists. fs.mkdirSync (fs.go) uses
	// os.MkdirAll instead -- a pre-existing divergence from real Node's
	// own default, predating this file, not mirrored here on purpose.
	m.Function("mkdir", func(path string, cb vm.Value) (vm.Value, error) {
		err := os.Mkdir(path, 0755)
		scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "mkdir", path, err))})
		return vm.Undefined, nil
	})
	m.Function("stat", func(path string, opts ...vm.Value) (vm.Value, error) {
		// Real Node's fs.stat(path[, options], callback) -- same trailing-
		// callback shape as readFile/writeFile above. Fixed-arity `cb
		// vm.Value` (as this had before) silently never calls back at all
		// when a real caller passes an options argument too, since the
		// options value lands in the `cb` parameter instead and is never
		// callable -- confirmed as a real noderati bug this round: real,
		// unmodified enhanced-resolve (webpack's own resolver) calls
		// exactly this 3-argument form, and the missing callback left a
		// real webpack compile hanging forever with no error at all.
		cb := findAsyncCallback(opts)
		info, err := os.Stat(path)
		if err != nil {
			scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "stat", path, err))})
			return vm.Undefined, nil
		}
		scheduleCallback(vmInst, cb, []vm.Value{vm.Null, fsStatsToVM(vmInst, newFsStats(vmInst, info))})
		return vm.Undefined, nil
	})
	// lstat added alongside stat (docs/real-node-plan.md, Round 122): real
	// `locate-path` (a real, transitive eslint dependency via
	// `find-up`/`eslint/lib/config/config-loader.js`) does
	// `const fsLStat = promisify(fs.lstat);` at its own module top level,
	// right next to the identical `promisify(fs.stat)` call this file's
	// own `stat` was presumably added for - `lstat`'s own callback-style
	// variant was simply never added alongside it. Mirrors os.Stat's own
	// `stat` above exactly, swapped for os.Lstat (matching lstatSync's
	// real Go call in fs.go and fs/promises's own lstat).
	m.Function("lstat", func(path string, opts ...vm.Value) (vm.Value, error) {
		// Same trailing-options-then-callback shape as stat above.
		cb := findAsyncCallback(opts)
		info, err := os.Lstat(path)
		if err != nil {
			scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "lstat", path, err))})
			return vm.Undefined, nil
		}
		scheduleCallback(vmInst, cb, []vm.Value{vm.Null, fsStatsToVM(vmInst, newFsStats(vmInst, info))})
		return vm.Undefined, nil
	})
	m.Function("rmdir", func(path string, cb vm.Value) (vm.Value, error) {
		err := os.Remove(path)
		scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "rmdir", path, err))})
		return vm.Undefined, nil
	})
	m.Function("realpath", func(path string, cb vm.Value) (vm.Value, error) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "realpath", path, err))})
			return vm.Undefined, nil
		}
		scheduleCallback(vmInst, cb, []vm.Value{vm.Null, vm.NewString(resolved)})
		return vm.Undefined, nil
	})
	// access added alongside the others (docs/real-node-plan.md,
	// Round 122): real `path-exists` (a real, transitive eslint
	// dependency via `find-up`) does `promisify(fs.access)` at its own
	// module top level - same "the callback-style variant was simply
	// never added for this one function" shape as lstat above. Mirrors
	// fs/promises's own access (fspromises.go) - real Node's fs.access
	// only ever reports whether the path is reachable/permitted via the
	// callback's error argument, never a success payload.
	m.Function("access", func(path string, opts ...vm.Value) (vm.Value, error) {
		// Real Node's fs.access(path[, mode], callback) -- same trailing-
		// options-then-callback shape as stat/lstat above (the optional
		// arg here is a numeric mode instead of an options object, but the
		// callback-finding problem is identical).
		cb := findAsyncCallback(opts)
		fsTouch("stat", path)
		_, err := os.Stat(path)
		scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "access", path, err))})
		return vm.Undefined, nil
	})
	// readlink added (docs/real-node-plan.md, this round): real, unmodified
	// enhanced-resolve (webpack's own resolver, via `SymlinkPlugin.js`)
	// calls `fs.readlink(path, callback)` on every path segment of every
	// resolve attempt, unconditionally -- not just when a symlink is
	// suspected, since that's the only way it can find out. Missing
	// entirely (as `stat`/`lstat`/`access` all were before Round 122)
	// wasn't a plain "undefined is not a function" here: enhanced-resolve's
	// own `CachedInputFileSystem` wraps a missing async provider as a
	// literal `null` (`this.provide = provider ? ... : null` in its
	// `CacheBackend`), so the real, observed failure was a real webpack
	// compile hanging forever on a `null is not a function` exception
	// thrown deep inside a scheduled callback -- silently, since the
	// scheduler swallowed the error rather than surfacing it (a related,
	// separate gap; see docs/real-node-plan.md for this round's paserati
	// issue about that). Same real-Node-signature shape as stat/lstat:
	// `fs.readlink(path[, options], callback)`.
	m.Function("readlink", func(path string, opts ...vm.Value) (vm.Value, error) {
		cb := findAsyncCallback(opts)
		target, err := os.Readlink(path)
		if err != nil {
			scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "readlink", path, err))})
			return vm.Undefined, nil
		}
		scheduleCallback(vmInst, cb, []vm.Value{vm.Null, vm.NewString(target)})
		return vm.Undefined, nil
	})
	m.Function("utimes", func(path string, atime, mtime vm.Value, cb vm.Value) (vm.Value, error) {
		// atime/mtime arrive as JS Date instances (proper-lockfile's own
		// mtime-precision.js always passes `new Date(...)`) or, per real
		// Node's own accepted forms, plain numbers -- ToFloat() covers
		// both, since a Date's numeric coercion (ToNumber -> valueOf ->
		// getTime()) already yields the same millisecond value a bare
		// number would.
		at := time.UnixMilli(int64(atime.ToFloat()))
		mt := time.UnixMilli(int64(mtime.ToFloat()))
		err := os.Chtimes(path, at, mt)
		scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "utimes", path, err))})
		return vm.Undefined, nil
	})
}
