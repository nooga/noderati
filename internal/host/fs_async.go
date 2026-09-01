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
		_, _ = vmInst.Call(cb, vm.Undefined, cbArgs)
	})
}

// declareFSAsync adds real Node's classic callback-style fs functions --
// fs.mkdir(path, cb), fs.stat(path, cb), fs.rmdir(path, cb),
// fs.utimes(path, atime, mtime, cb), fs.realpath(path, cb) -- alongside
// the *Sync (fs.go) and Promise-based (fspromises.go) variants already
// implemented. Real Node's fs module has three parallel styles (sync,
// callback, promise); noderati previously had only two -- entirely
// missing, not just incomplete. `graceful-fs` (proper-lockfile's real,
// direct dependency, and used directly by many other real npm packages
// besides) patches every one of these onto its own exported fs object;
// without them, graceful-fs's own `.mkdir`/`.stat`/etc. are simply
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
// proper-lockfile's async core (`lib/lockfile.js`) ever calls. Extend when
// another real package's real call site demands more of the callback
// surface -- don't build ahead of evidence.
func declareFSAsync(m *driver.ModuleBuilder, vmInst *vm.VM) {
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
	m.Function("stat", func(path string, cb vm.Value) (vm.Value, error) {
		info, err := os.Stat(path)
		if err != nil {
			scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, "stat", path, err))})
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
