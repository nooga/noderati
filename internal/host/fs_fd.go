package host

import (
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// fs_fd.go holds the fd-based fs surface (open/close/read/write/writev/
// fstat/futimes/fchmod/fchown/fsync/ftruncate) plus the path-based calls
// whose real Node signatures take optional middle arguments (mkdir, rmdir,
// realpath, chmod, chown, utimes, rename, unlink, link, symlink), in all
// the forms real Node offers: sync, callback, and (for mkdir) promise.
// First reached as a whole via real tar, whose fs-minipass streams and
// Unpack drive every one of these.

func argAt(args []vm.Value, i int) vm.Value {
	if i < len(args) {
		return args[i]
	}
	return vm.Undefined
}

func isNullish(v vm.Value) bool {
	return v.IsUndefined() || v.Type() == vm.TypeNull
}

// splitCallback peels a trailing callback off a callback-style call's args.
func splitCallback(vmInst *vm.VM, args []vm.Value) ([]vm.Value, vm.Value, error) {
	if len(args) > 0 && args[len(args)-1].IsCallable() {
		return args[:len(args)-1], args[len(args)-1], nil
	}
	return args, vm.Undefined, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE", `The "cb" argument must be of type function`)
}

func newNodeTypeError(vmInst *vm.VM, code, message string) error {
	exception := vm.Undefined
	if ctor, ok := vmInst.GetGlobal("TypeError"); ok {
		if v, err := vmInst.Construct(ctor, []vm.Value{vm.NewString(message)}); err == nil {
			exception = v
		}
	}
	if obj := exception.AsPlainObject(); obj != nil {
		obj.SetOwn("code", vm.NewString(code))
	}
	return &fsSystemError{exception: exception, message: message}
}

func fdArg(vmInst *vm.VM, v vm.Value) (int64, error) {
	if !v.IsNumber() {
		return 0, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE", `The "fd" argument must be of type number`)
	}
	return int64(v.ToFloat()), nil
}

func fdFile(fd int64) (*os.File, error) {
	if f, ok := fsFile(fd); ok {
		return f, nil
	}
	return nil, syscall.EBADF
}

// posixToGoMode converts a POSIX permission number (what JS passes) into
// Go's os.FileMode, whose setuid/setgid/sticky bits live elsewhere.
func posixToGoMode(m uint32) os.FileMode {
	mode := os.FileMode(m & 0o777)
	if m&0o4000 != 0 {
		mode |= os.ModeSetuid
	}
	if m&0o2000 != 0 {
		mode |= os.ModeSetgid
	}
	if m&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode
}

// modeArg reads a mode argument: a number, or an octal string like "755".
func modeArg(v vm.Value, def uint32) uint32 {
	switch {
	case v.IsNumber():
		return uint32(v.ToFloat())
	case v.IsString():
		if n, err := strconv.ParseUint(v.ToString(), 8, 32); err == nil {
			return uint32(n)
		}
	}
	return def
}

// openFlagsArg accepts real Node's string flags or a numeric O_* bitmask.
func openFlagsArg(v vm.Value) (int, error) {
	switch {
	case isNullish(v):
		return os.O_RDONLY, nil
	case v.IsNumber():
		return int(v.ToFloat()), nil
	default:
		return fsOpenFlags(v.ToString())
	}
}

// fsTimeArg converts a utimes/futimes time argument the way real Node's
// toUnixTimestamp does: a Date is milliseconds, a number or numeric string
// is *seconds*, and a non-finite or negative number means "now".
func fsTimeArg(vmInst *vm.VM, v vm.Value) (time.Time, error) {
	if v.Type() == vm.TypeObject {
		if getTime, ok := v.AsPlainObject().Get("getTime"); ok && getTime.IsCallable() {
			ms, err := vmInst.Call(getTime, v, nil)
			if err != nil {
				return time.Time{}, err
			}
			return time.UnixMilli(0).Add(time.Duration(ms.ToFloat() * float64(time.Millisecond))), nil
		}
	}
	var secs float64
	switch {
	case v.IsNumber():
		secs = v.ToFloat()
	case v.IsString():
		n, err := strconv.ParseFloat(strings.TrimSpace(v.ToString()), 64)
		if err != nil {
			return time.Time{}, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE", `The "time" argument must be of type number or an instance of Date`)
		}
		secs = n
	default:
		return time.Time{}, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE", `The "time" argument must be of type number or an instance of Date`)
	}
	if math.IsNaN(secs) || math.IsInf(secs, 0) || secs < 0 {
		return time.Now(), nil
	}
	return time.Unix(0, int64(secs*1e9)), nil
}

func statThrowIfNoEntryFalse(opts []vm.Value) bool {
	for _, v := range opts {
		if v.Type() != vm.TypeObject {
			continue
		}
		if obj := v.AsPlainObject(); obj != nil {
			if t, ok := obj.GetOwn("throwIfNoEntry"); ok && t.Type() == vm.TypeBoolean && !t.IsTruthy() {
				return true
			}
		}
	}
	return false
}

func objOption(v vm.Value, name string) (vm.Value, bool) {
	if v.Type() != vm.TypeObject {
		return vm.Undefined, false
	}
	obj := v.AsPlainObject()
	if obj == nil {
		return vm.Undefined, false
	}
	return obj.GetOwn(name)
}

// fsMkdir implements mkdir's (path, [options|mode]) semantics. With
// recursive it returns the first directory actually created ("" if none),
// which real Node hands back as the result.
func fsMkdir(path string, opt vm.Value) (string, error) {
	mode := uint32(0o777)
	recursive := false
	if opt.IsNumber() || opt.IsString() {
		mode = modeArg(opt, mode)
	} else if opt.Type() == vm.TypeObject {
		if r, ok := objOption(opt, "recursive"); ok {
			recursive = r.IsTruthy()
		}
		if m, ok := objOption(opt, "mode"); ok {
			mode = modeArg(m, mode)
		}
	}
	perm := posixToGoMode(mode)
	if !recursive {
		return "", os.Mkdir(path, perm)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	first := ""
	for p := abs; ; p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			break
		}
		first = p
		if filepath.Dir(p) == p {
			break
		}
	}
	if err := os.MkdirAll(abs, perm); err != nil {
		return "", err
	}
	return first, nil
}

func optionalString(s string) vm.Value {
	if s == "" {
		return vm.Undefined
	}
	return vm.NewString(s)
}

// ioParams is the (offset, length, position) triple read/write accept
// either positionally or as an options object.
type ioParams struct {
	offset, length int
	position       int64 // -1: use and advance the current file position
}

func parseIOParams(args []vm.Value, bufLen int) (ioParams, error) {
	p := ioParams{offset: 0, length: -1, position: -1}
	pos := vm.Undefined
	if first := argAt(args, 0); first.Type() == vm.TypeObject && first.AsTypedArray() == nil {
		if v, ok := objOption(first, "offset"); ok && v.IsNumber() {
			p.offset = int(v.ToFloat())
		}
		if v, ok := objOption(first, "length"); ok && v.IsNumber() {
			p.length = int(v.ToFloat())
		}
		pos, _ = objOption(first, "position")
	} else {
		if v := argAt(args, 0); v.IsNumber() {
			p.offset = int(v.ToFloat())
		}
		if v := argAt(args, 1); v.IsNumber() {
			p.length = int(v.ToFloat())
		}
		pos = argAt(args, 2)
	}
	if pos.IsNumber() || pos.Type() == vm.TypeBigInt {
		if n := int64(pos.ToFloat()); n >= 0 {
			p.position = n
		}
	}
	if p.length < 0 {
		p.length = bufLen - p.offset
	}
	if p.offset < 0 || p.offset > bufLen {
		return p, errors.New(`The value of "offset" is out of range.`)
	}
	if p.length < 0 || p.offset+p.length > bufLen {
		return p, errors.New(`The value of "length" is out of range.`)
	}
	return p, nil
}

func fdReadInto(f *os.File, buf []byte, position int64) (int, error) {
	var n int
	var err error
	if position >= 0 {
		n, err = f.ReadAt(buf, position)
	} else {
		n, err = f.Read(buf)
	}
	if err == io.EOF {
		err = nil
	}
	return n, err
}

func fdWriteFrom(f *os.File, data []byte, position int64) (int, error) {
	if position >= 0 {
		n, err := f.WriteAt(data, position)
		// Go refuses WriteAt on an O_APPEND file; POSIX pwrite there just
		// appends, which is what real Node does too.
		if err != nil && strings.Contains(err.Error(), "O_APPEND") {
			return f.Write(data)
		}
		return n, err
	}
	return f.Write(data)
}

// fsReadArgs resolves every real fs.read/readSync form into the target
// buffer's live bytes plus io parameters.
func fsReadArgs(vmInst *vm.VM, args []vm.Value) (vm.Value, []byte, ioParams, error) {
	bufVal := argAt(args, 0)
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	if ta := bufVal.AsTypedArray(); ta == nil {
		// read(fd, [options]) - options may carry its own buffer.
		opts := bufVal
		bufVal = vm.Undefined
		if b, ok := objOption(opts, "buffer"); ok && b.AsTypedArray() != nil {
			bufVal = b
		} else {
			bufVal = wrapBuffer(vmInst, make([]byte, 16384))
		}
		rest = []vm.Value{opts}
	}
	live := typedArrayLiveBytes(bufVal.AsTypedArray())
	p, err := parseIOParams(rest, len(live))
	return bufVal, live, p, err
}

// fsWriteArgs resolves every real fs.write/writeSync form into bytes to
// write plus a position. isString reports the string form, whose
// callback gets the string back rather than a buffer.
func fsWriteArgs(vmInst *vm.VM, args []vm.Value) ([]byte, int64, bool, error) {
	data := argAt(args, 0)
	if ta := data.AsTypedArray(); ta != nil {
		live := typedArrayLiveBytes(ta)
		p, err := parseIOParams(args[1:], len(live))
		if err != nil {
			return nil, 0, false, err
		}
		return live[p.offset : p.offset+p.length], p.position, false, nil
	}
	// write(fd, string, [position, [encoding]])
	position := int64(-1)
	if pos := argAt(args, 1); pos.IsNumber() && pos.ToFloat() >= 0 {
		position = int64(pos.ToFloat())
	}
	encoding := "utf8"
	if enc := argAt(args, 2); enc.IsString() {
		encoding = enc.ToString()
	}
	return valueToBytesWithEncoding(vmInst, vm.NewString(data.ToString()), encoding), position, true, nil
}

func fsWritev(f *os.File, bufs vm.Value, position int64) (int, error) {
	arr := bufs.AsArray()
	if arr == nil {
		return 0, errors.New(`The "buffers" argument must be an instance of ArrayBufferView`)
	}
	var all []byte
	for i := 0; i < arr.Length(); i++ {
		all = append(all, typedArrayLiveBytes(arr.Get(i).AsTypedArray())...)
	}
	return fdWriteFrom(f, all, position)
}

func fsFutimes(f *os.File, at, mt time.Time) error {
	tv := []syscall.Timeval{syscall.NsecToTimeval(at.UnixNano()), syscall.NsecToTimeval(mt.UnixNano())}
	return syscall.Futimes(int(f.Fd()), tv)
}

func declareFSFd(m *driver.ModuleBuilder, vmInst *vm.VM) {
	cbErr := func(cb vm.Value, syscallName, path string, err error, results ...vm.Value) {
		if err != nil {
			scheduleCallback(vmInst, cb, []vm.Value{fsErrToVM(wrapFsErr(vmInst, syscallName, path, err))})
			return
		}
		scheduleCallback(vmInst, cb, append([]vm.Value{vm.Null}, results...))
	}
	// asyncFn registers a callback-style function whose body is the same
	// Go code as its Sync twin: do the work, then hand (err, ...results)
	// to the callback on the next tick.
	asyncFn := func(name string, body func(args []vm.Value) (string, string, []vm.Value, error)) {
		m.Function(name, func(all ...vm.Value) (vm.Value, error) {
			args, cb, err := splitCallback(vmInst, all)
			if err != nil {
				return vm.Undefined, err
			}
			syscallName, path, results, err := body(args)
			// Already-built JS errors are argument-validation failures,
			// which real Node throws synchronously instead of calling back.
			if _, ok := err.(*fsSystemError); ok {
				return vm.Undefined, err
			}
			cbErr(cb, syscallName, path, err, results...)
			return vm.Undefined, nil
		})
	}
	syncFn := func(name string, body func(args []vm.Value) (string, string, []vm.Value, error)) {
		m.Function(name, func(args ...vm.Value) (vm.Value, error) {
			syscallName, path, results, err := body(args)
			if err != nil {
				if _, ok := err.(*fsSystemError); ok {
					return vm.Undefined, err
				}
				return vm.Undefined, wrapFsErr(vmInst, syscallName, path, err)
			}
			if len(results) > 0 {
				return results[0], nil
			}
			return vm.Undefined, nil
		})
	}
	both := func(name string, body func(args []vm.Value) (string, string, []vm.Value, error)) {
		asyncFn(name, body)
		syncFn(name+"Sync", body)
	}
	pathOf := func(args []vm.Value, i int) (string, error) {
		return pathArg(vmInst, argAt(args, i))
	}

	both("open", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "open", "", nil, err
		}
		flags, err := openFlagsArg(argAt(args, 1))
		if err != nil {
			return "open", path, nil, err
		}
		mode := posixToGoMode(modeArg(argAt(args, 2), 0o666))
		fd, err := fsOpenRaw(path, flags, mode)
		return "open", path, []vm.Value{vm.NumberValue(float64(fd))}, err
	})
	both("close", func(args []vm.Value) (string, string, []vm.Value, error) {
		fd, err := fdArg(vmInst, argAt(args, 0))
		if err != nil {
			return "close", "", nil, err
		}
		return "close", "", nil, fsClose(fd)
	})
	both("fstat", func(args []vm.Value) (string, string, []vm.Value, error) {
		fd, err := fdArg(vmInst, argAt(args, 0))
		if err != nil {
			return "fstat", "", nil, err
		}
		f, err := fdFile(fd)
		if err != nil {
			return "fstat", "", nil, err
		}
		info, err := f.Stat()
		if err != nil {
			return "fstat", "", nil, err
		}
		return "fstat", "", []vm.Value{fsStatsValue(vmInst, info)}, nil
	})
	asyncFn("read", func(args []vm.Value) (string, string, []vm.Value, error) {
		fd, err := fdArg(vmInst, argAt(args, 0))
		if err != nil {
			return "read", "", nil, err
		}
		f, err := fdFile(fd)
		if err != nil {
			return "read", "", nil, err
		}
		bufVal, live, p, err := fsReadArgs(vmInst, args[1:])
		if err != nil {
			return "read", "", nil, newNodeRangeError(vmInst, err.Error())
		}
		n, err := fdReadInto(f, live[p.offset:p.offset+p.length], p.position)
		return "read", "", []vm.Value{vm.NumberValue(float64(n)), bufVal}, err
	})
	syncFn("readSync", func(args []vm.Value) (string, string, []vm.Value, error) {
		fd, err := fdArg(vmInst, argAt(args, 0))
		if err != nil {
			return "read", "", nil, err
		}
		f, err := fdFile(fd)
		if err != nil {
			return "read", "", nil, err
		}
		_, live, p, err := fsReadArgs(vmInst, args[1:])
		if err != nil {
			return "read", "", nil, newNodeRangeError(vmInst, err.Error())
		}
		n, err := fdReadInto(f, live[p.offset:p.offset+p.length], p.position)
		return "read", "", []vm.Value{vm.NumberValue(float64(n))}, err
	})
	writeBody := func(returnData bool) func(args []vm.Value) (string, string, []vm.Value, error) {
		return func(args []vm.Value) (string, string, []vm.Value, error) {
			fd, err := fdArg(vmInst, argAt(args, 0))
			if err != nil {
				return "write", "", nil, err
			}
			f, err := fdFile(fd)
			if err != nil {
				return "write", "", nil, err
			}
			data, position, _, err := fsWriteArgs(vmInst, args[1:])
			if err != nil {
				return "write", "", nil, newNodeRangeError(vmInst, err.Error())
			}
			n, err := fdWriteFrom(f, data, position)
			results := []vm.Value{vm.NumberValue(float64(n))}
			if returnData {
				results = append(results, argAt(args, 1))
			}
			return "write", "", results, err
		}
	}
	asyncFn("write", writeBody(true))
	syncFn("writeSync", writeBody(false))
	writevBody := func(returnData bool) func(args []vm.Value) (string, string, []vm.Value, error) {
		return func(args []vm.Value) (string, string, []vm.Value, error) {
			fd, err := fdArg(vmInst, argAt(args, 0))
			if err != nil {
				return "write", "", nil, err
			}
			f, err := fdFile(fd)
			if err != nil {
				return "write", "", nil, err
			}
			position := int64(-1)
			if pos := argAt(args, 2); pos.IsNumber() && pos.ToFloat() >= 0 {
				position = int64(pos.ToFloat())
			}
			n, err := fsWritev(f, argAt(args, 1), position)
			results := []vm.Value{vm.NumberValue(float64(n))}
			if returnData {
				results = append(results, argAt(args, 1))
			}
			return "write", "", results, err
		}
	}
	asyncFn("writev", writevBody(true))
	syncFn("writevSync", writevBody(false))
	both("futimes", func(args []vm.Value) (string, string, []vm.Value, error) {
		fd, err := fdArg(vmInst, argAt(args, 0))
		if err != nil {
			return "futime", "", nil, err
		}
		f, err := fdFile(fd)
		if err != nil {
			return "futime", "", nil, err
		}
		at, err := fsTimeArg(vmInst, argAt(args, 1))
		if err != nil {
			return "futime", "", nil, err
		}
		mt, err := fsTimeArg(vmInst, argAt(args, 2))
		if err != nil {
			return "futime", "", nil, err
		}
		return "futime", "", nil, fsFutimes(f, at, mt)
	})
	both("fchmod", func(args []vm.Value) (string, string, []vm.Value, error) {
		fd, err := fdArg(vmInst, argAt(args, 0))
		if err != nil {
			return "fchmod", "", nil, err
		}
		f, err := fdFile(fd)
		if err != nil {
			return "fchmod", "", nil, err
		}
		return "fchmod", "", nil, f.Chmod(posixToGoMode(modeArg(argAt(args, 1), 0)))
	})
	both("fchown", func(args []vm.Value) (string, string, []vm.Value, error) {
		fd, err := fdArg(vmInst, argAt(args, 0))
		if err != nil {
			return "fchown", "", nil, err
		}
		f, err := fdFile(fd)
		if err != nil {
			return "fchown", "", nil, err
		}
		return "fchown", "", nil, f.Chown(int(argAt(args, 1).ToFloat()), int(argAt(args, 2).ToFloat()))
	})
	for _, name := range []string{"fsync", "fdatasync"} {
		both(name, func(args []vm.Value) (string, string, []vm.Value, error) {
			fd, err := fdArg(vmInst, argAt(args, 0))
			if err != nil {
				return name, "", nil, err
			}
			f, err := fdFile(fd)
			if err != nil {
				return name, "", nil, err
			}
			return name, "", nil, f.Sync()
		})
	}
	both("ftruncate", func(args []vm.Value) (string, string, []vm.Value, error) {
		fd, err := fdArg(vmInst, argAt(args, 0))
		if err != nil {
			return "ftruncate", "", nil, err
		}
		f, err := fdFile(fd)
		if err != nil {
			return "ftruncate", "", nil, err
		}
		n := int64(0)
		if v := argAt(args, 1); v.IsNumber() {
			n = int64(v.ToFloat())
		}
		return "ftruncate", "", nil, f.Truncate(n)
	})

	both("mkdir", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "mkdir", "", nil, err
		}
		first, err := fsMkdir(path, argAt(args, 1))
		return "mkdir", path, []vm.Value{optionalString(first)}, err
	})
	both("rmdir", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "rmdir", "", nil, err
		}
		if r, ok := objOption(argAt(args, 1), "recursive"); ok && r.IsTruthy() {
			return "rmdir", path, nil, os.RemoveAll(path)
		}
		return "rmdir", path, nil, syscall.Rmdir(path)
	})
	asyncFn("realpath", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "realpath", "", nil, err
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			resolved, err = filepath.Abs(resolved)
		}
		return "realpath", path, []vm.Value{vm.NewString(resolved)}, err
	})
	asyncFn("chmod", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "chmod", "", nil, err
		}
		return "chmod", path, nil, os.Chmod(path, posixToGoMode(modeArg(argAt(args, 1), 0)))
	})
	both("chown", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "chown", "", nil, err
		}
		return "chown", path, nil, os.Chown(path, int(argAt(args, 1).ToFloat()), int(argAt(args, 2).ToFloat()))
	})
	both("lchown", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "lchown", "", nil, err
		}
		return "lchown", path, nil, os.Lchown(path, int(argAt(args, 1).ToFloat()), int(argAt(args, 2).ToFloat()))
	})
	both("utimes", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "utime", "", nil, err
		}
		at, err := fsTimeArg(vmInst, argAt(args, 1))
		if err != nil {
			return "utime", path, nil, err
		}
		mt, err := fsTimeArg(vmInst, argAt(args, 2))
		if err != nil {
			return "utime", path, nil, err
		}
		return "utime", path, nil, os.Chtimes(path, at, mt)
	})
	asyncFn("rename", func(args []vm.Value) (string, string, []vm.Value, error) {
		from, err := pathOf(args, 0)
		if err != nil {
			return "rename", "", nil, err
		}
		to, err := pathOf(args, 1)
		if err != nil {
			return "rename", from, nil, err
		}
		return "rename", from, nil, os.Rename(from, to)
	})
	asyncFn("unlink", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "unlink", "", nil, err
		}
		return "unlink", path, nil, syscall.Unlink(path)
	})
	both("symlink", func(args []vm.Value) (string, string, []vm.Value, error) {
		target, err := pathOf(args, 0)
		if err != nil {
			return "symlink", "", nil, err
		}
		path, err := pathOf(args, 1)
		if err != nil {
			return "symlink", target, nil, err
		}
		return "symlink", target, nil, os.Symlink(target, path)
	})
	both("link", func(args []vm.Value) (string, string, []vm.Value, error) {
		existing, err := pathOf(args, 0)
		if err != nil {
			return "link", "", nil, err
		}
		newPath, err := pathOf(args, 1)
		if err != nil {
			return "link", existing, nil, err
		}
		return "link", existing, nil, os.Link(existing, newPath)
	})
	syncFn("readlinkSync", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "readlink", "", nil, err
		}
		target, err := os.Readlink(path)
		return "readlink", path, []vm.Value{vm.NewString(target)}, err
	})
}

func newNodeRangeError(vmInst *vm.VM, message string) error {
	exception := vm.Undefined
	if ctor, ok := vmInst.GetGlobal("RangeError"); ok {
		if v, err := vmInst.Construct(ctor, []vm.Value{vm.NewString(message)}); err == nil {
			exception = v
		}
	}
	if obj := exception.AsPlainObject(); obj != nil {
		obj.SetOwn("code", vm.NewString("ERR_OUT_OF_RANGE"))
	}
	return &fsSystemError{exception: exception, message: message}
}
