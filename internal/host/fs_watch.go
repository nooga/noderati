package host

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// fs_watch.go implements fs.watch (native change notification via
// fsnotify: inotify on Linux, kqueue on macOS) and fs.watchFile/
// fs.unwatchFile (stat polling, mirroring libuv's uv_fs_poll exactly).
// First needed by real chokidar, which builds every watcher on these.

// keepAlive is a ref/unref-able hold on the event loop, the same
// ref-count-collapsed-to-a-bool model spawnHandle uses for child processes.
type keepAlive struct {
	mu sync.Mutex
	rt interface {
		BeginExternalOp()
		EndExternalOp()
	}
	refed  bool
	closed bool
}

func newKeepAlive(vmInst *vm.VM, persistent bool) *keepAlive {
	k := &keepAlive{rt: vmInst.GetAsyncRuntime()}
	if persistent {
		k.refed = true
		k.rt.BeginExternalOp()
	}
	return k
}

func (k *keepAlive) ref() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.refed && !k.closed {
		k.refed = true
		k.rt.BeginExternalOp()
	}
}

func (k *keepAlive) unref() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.refed && !k.closed {
		k.refed = false
		k.rt.EndExternalOp()
	}
}

func (k *keepAlive) isClosed() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.closed
}

// release drops the hold for good; reports whether it was the first call.
func (k *keepAlive) release() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return false
	}
	k.closed = true
	if k.refed {
		k.refed = false
		k.rt.EndExternalOp()
	}
	return true
}

func installRefUnref(obj *vm.PlainObject, k *keepAlive) {
	self := vm.NewValueFromPlainObject(obj)
	obj.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
		k.ref()
		return self, nil
	}))
	obj.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
		k.unref()
		return self, nil
	}))
}

// fsWatchEventType maps an fsnotify op to real Node's two fs.watch event
// types: "rename" when an entry appears, disappears or moves, "change"
// when its contents or attributes change.
func fsWatchEventType(op fsnotify.Op) string {
	if op.Has(fsnotify.Create) || op.Has(fsnotify.Remove) || op.Has(fsnotify.Rename) {
		return "rename"
	}
	return "change"
}

func addWatchTree(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return w.Add(p)
		}
		return nil
	})
}

func fsWatch(vmInst *vm.VM, args []vm.Value) (vm.Value, error) {
	path, err := pathArg(vmInst, argAt(args, 0))
	if err != nil {
		return vm.Undefined, err
	}
	persistent, recursive := true, false
	listener := vm.Undefined
	for _, a := range args[1:] {
		switch {
		case a.IsCallable():
			listener = a
		case a.Type() == vm.TypeObject:
			if v, ok := objOption(a, "persistent"); ok && !isNullish(v) {
				persistent = v.IsTruthy()
			}
			if v, ok := objOption(a, "recursive"); ok {
				recursive = v.IsTruthy()
			}
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return vm.Undefined, wrapFsErr(vmInst, "watch", path, err)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return vm.Undefined, wrapFsErr(vmInst, "watch", path, err)
	}
	isDir := info.IsDir()
	if isDir && recursive {
		err = addWatchTree(w, path)
	} else {
		err = w.Add(path)
	}
	if err != nil {
		_ = w.Close()
		return vm.Undefined, wrapFsErr(vmInst, "watch", path, err)
	}

	obj := newEventEmitterObject(vmInst)
	if listener.IsCallable() {
		addListener(vmInst, obj, "change", listener, false, false)
	}
	hold := newKeepAlive(vmInst, persistent)
	installRefUnref(obj, hold)
	rt := vmInst.GetAsyncRuntime()
	obj.SetOwn("close", vm.NewNativeFunction(0, false, "close", func(_ []vm.Value) (vm.Value, error) {
		if hold.release() {
			_ = w.Close()
			scheduleEmit(vmInst, obj, "close")
		}
		return vm.Undefined, nil
	}))

	go func() {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				name := filepath.Base(path)
				if isDir {
					rel, err := filepath.Rel(path, ev.Name)
					if err != nil || rel == "." {
						continue
					}
					name = rel
				}
				if recursive && ev.Op.Has(fsnotify.Create) {
					if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
						_ = addWatchTree(w, ev.Name)
					}
				}
				eventType := fsWatchEventType(ev.Op)
				rt.ScheduleNextTick(func() {
					if !hold.isClosed() {
						emitOnObject(vmInst, obj, "change", vm.NewString(eventType), vm.NewString(name))
					}
				})
			case werr, ok := <-w.Errors:
				if !ok {
					return
				}
				msg := werr.Error()
				rt.ScheduleNextTick(func() {
					if !hold.isClosed() {
						emitOnObject(vmInst, obj, "error", newJSError(vmInst, msg))
					}
				})
			}
		}
	}()
	return vm.NewValueFromPlainObject(obj), nil
}

func newJSError(vmInst *vm.VM, message string) vm.Value {
	if ctor, ok := vmInst.GetGlobal("Error"); ok {
		if v, err := vmInst.Construct(ctor, []vm.Value{vm.NewString(message)}); err == nil {
			return v
		}
	}
	return vm.NewString(message)
}

// pollStat is one uv_fs_poll sample: either a FileInfo or an errno.
type pollStat struct {
	info  os.FileInfo
	errno syscall.Errno
}

// statbufEqual mirrors libuv's statbuf_eq: the fields whose change makes
// uv_fs_poll report the file as changed.
func statbufEqual(a, b os.FileInfo) bool {
	if a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime()) {
		return false
	}
	sa, oka := statSysFields(a)
	sb, okb := statSysFields(b)
	if !oka || !okb {
		return a.Mode() == b.Mode()
	}
	return sa.mode == sb.mode && sa.uid == sb.uid && sa.gid == sb.gid && sa.ino == sb.ino &&
		sa.dev == sb.dev && sa.ctime.Equal(sb.ctime) && sa.birthtime.Equal(sb.birthtime)
}

type statWatcher struct {
	obj  *vm.PlainObject
	hold *keepAlive
	stop chan struct{}
}

var statWatchers sync.Map // *vm.VM -> *sync.Map (abs path -> *statWatcher)

func statWatchersFor(vmInst *vm.VM) *sync.Map {
	m, _ := statWatchers.LoadOrStore(vmInst, &sync.Map{})
	return m.(*sync.Map)
}

// zeroStatsValue is the all-zero Stats real Node reports for a missing file.
func zeroStatsValue(vmInst *vm.VM) vm.Value {
	obj := vm.NewObject(vm.NewValueFromPlainObject(statsPrototype(vmInst))).AsPlainObject()
	for _, name := range []string{"dev", "mode", "nlink", "uid", "gid", "rdev", "blksize", "ino", "size", "blocks", "atimeMs", "mtimeMs", "ctimeMs", "birthtimeMs"} {
		obj.SetOwn(name, vm.NumberValue(0))
	}
	return vm.NewValueFromPlainObject(obj)
}

func statsOrZero(vmInst *vm.VM, info os.FileInfo) vm.Value {
	if info == nil {
		return zeroStatsValue(vmInst)
	}
	return fsStatsValue(vmInst, info)
}

func sampleStat(path string) pollStat {
	info, err := os.Stat(path)
	if err != nil {
		var errno syscall.Errno
		if !errors.As(err, &errno) {
			errno = syscall.EIO
		}
		return pollStat{errno: errno}
	}
	return pollStat{info: info}
}

// runStatPoll follows uv_fs_poll's callback rules: the first successful
// sample is only a baseline; an error is reported once per distinct
// errno (with an all-zero current stat); after that, any sample differing
// from the last good one is reported.
func runStatPoll(vmInst *vm.VM, sw *statWatcher, path string, interval time.Duration) {
	rt := vmInst.GetAsyncRuntime()
	var last os.FileInfo
	busy := 0 // 0: no sample yet, 1: last sample ok, <0: last sample's errno
	emit := func(curr, prev os.FileInfo) {
		rt.ScheduleNextTick(func() {
			if !sw.hold.isClosed() {
				emitOnObject(vmInst, sw.obj, "change", statsOrZero(vmInst, curr), statsOrZero(vmInst, prev))
			}
		})
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		s := sampleStat(path)
		if s.info == nil {
			if busy != -int(s.errno) {
				emit(nil, last)
				busy = -int(s.errno)
			}
		} else {
			if busy < 0 || (busy != 0 && !statbufEqual(last, s.info)) {
				emit(s.info, last)
			}
			last = s.info
			busy = 1
		}
		select {
		case <-sw.stop:
			return
		case <-ticker.C:
		}
	}
}

func fsWatchFile(vmInst *vm.VM, args []vm.Value) (vm.Value, error) {
	path, err := pathArg(vmInst, argAt(args, 0))
	if err != nil {
		return vm.Undefined, err
	}
	persistent, interval := true, 5007*time.Millisecond
	listener := vm.Undefined
	for _, a := range args[1:] {
		switch {
		case a.IsCallable():
			listener = a
		case a.Type() == vm.TypeObject:
			if v, ok := objOption(a, "persistent"); ok && !isNullish(v) {
				persistent = v.IsTruthy()
			}
			if v, ok := objOption(a, "interval"); ok && v.IsNumber() {
				interval = time.Duration(v.ToFloat() * float64(time.Millisecond))
			}
		}
	}
	if !listener.IsCallable() {
		return vm.Undefined, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE", `The "listener" argument must be of type function`)
	}
	if interval <= 0 {
		interval = time.Millisecond
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return vm.Undefined, err
	}
	watchers := statWatchersFor(vmInst)
	if existing, ok := watchers.Load(abs); ok {
		sw := existing.(*statWatcher)
		addListener(vmInst, sw.obj, "change", listener, false, false)
		return vm.NewValueFromPlainObject(sw.obj), nil
	}
	sw := &statWatcher{obj: newEventEmitterObject(vmInst), hold: newKeepAlive(vmInst, persistent), stop: make(chan struct{})}
	installRefUnref(sw.obj, sw.hold)
	addListener(vmInst, sw.obj, "change", listener, false, false)
	watchers.Store(abs, sw)
	go runStatPoll(vmInst, sw, abs, interval)
	return vm.NewValueFromPlainObject(sw.obj), nil
}

func fsUnwatchFile(vmInst *vm.VM, args []vm.Value) (vm.Value, error) {
	path, err := pathArg(vmInst, argAt(args, 0))
	if err != nil {
		return vm.Undefined, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return vm.Undefined, err
	}
	watchers := statWatchersFor(vmInst)
	existing, ok := watchers.Load(abs)
	if !ok {
		return vm.Undefined, nil
	}
	sw := existing.(*statWatcher)
	if l := argAt(args, 1); l.IsCallable() {
		removeListener(sw.obj, "change", l)
	} else {
		removeAllListeners(sw.obj, []vm.Value{vm.NewString("change")})
	}
	if listenerCount(sw.obj, "change") == 0 {
		watchers.Delete(abs)
		close(sw.stop)
		if sw.hold.release() {
			scheduleEmit(vmInst, sw.obj, "stop")
		}
	}
	return vm.Undefined, nil
}

func declareFSWatch(m *driver.ModuleBuilder, vmInst *vm.VM) {
	m.Function("watch", func(args ...vm.Value) (vm.Value, error) { return fsWatch(vmInst, args) })
	m.Function("watchFile", func(args ...vm.Value) (vm.Value, error) { return fsWatchFile(vmInst, args) })
	m.Function("unwatchFile", func(args ...vm.Value) (vm.Value, error) { return fsUnwatchFile(vmInst, args) })
}
