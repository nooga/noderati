package host

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// fs_extra.go: the remaining Go-backed fs calls - truncate, lutimes,
// lchmod, mkdtemp, statfs, readv, copyFile (with mode), rm, glob, plus
// the Stats/Dirent constructors. Everything built purely on top of these
// (streams, Dir, cp, fs/promises, FileHandle) lives in the JS layer
// (fs_shim.go).

func declareFSExtra(m *driver.ModuleBuilder, vmInst *vm.VM) {
	r := fsRegistrar{m: m, vmInst: vmInst}
	pathOf := func(args []vm.Value, i int) (string, error) {
		return pathArg(vmInst, argAt(args, i))
	}
	classes := fsClasses(vmInst)
	m.Const("Stats", classes.statsCtor)
	m.Const("Dirent", classes.direntCtor)

	r.both("truncate", func(args []vm.Value) (string, string, []vm.Value, error) {
		n := int64(0)
		if v := argAt(args, 1); v.IsNumber() {
			n = int64(v.ToFloat())
		}
		if v := argAt(args, 0); v.IsNumber() { // deprecated fd form
			f, err := fdFile(int64(v.ToFloat()))
			if err != nil {
				return "ftruncate", "", nil, err
			}
			return "ftruncate", "", nil, f.Truncate(n)
		}
		path, err := pathOf(args, 0)
		if err != nil {
			return "open", "", nil, err
		}
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return "open", path, nil, err
		}
		defer f.Close()
		return "ftruncate", "", nil, f.Truncate(n)
	})
	r.both("lutimes", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "lutime", "", nil, err
		}
		at, err := fsTimeArg(vmInst, argAt(args, 1))
		if err != nil {
			return "lutime", path, nil, err
		}
		mt, err := fsTimeArg(vmInst, argAt(args, 2))
		if err != nil {
			return "lutime", path, nil, err
		}
		return "lutime", path, nil, fsLutimes(path, at.UnixNano(), mt.UnixNano())
	})
	if fsHasLchmod {
		r.both("lchmod", func(args []vm.Value) (string, string, []vm.Value, error) {
			path, err := pathOf(args, 0)
			if err != nil {
				return "open", "", nil, err
			}
			sc, err := fsLchmod(path, modeArg(argAt(args, 1), 0))
			if sc != "open" {
				path = ""
			}
			return sc, path, nil, err
		})
	}
	r.both("mkdtemp", func(args []vm.Value) (string, string, []vm.Value, error) {
		prefix, err := pathOf(args, 0)
		if err != nil {
			return "mkdtemp", "", nil, err
		}
		const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		for tries := 0; ; tries++ {
			var rnd [6]byte
			if _, err := io.ReadFull(rand.Reader, rnd[:]); err != nil {
				return "mkdtemp", prefix + "XXXXXX", nil, err
			}
			for i := range rnd {
				rnd[i] = chars[int(rnd[i])%len(chars)]
			}
			dir := prefix + string(rnd[:])
			err := syscall.Mkdir(dir, 0o700)
			if err == nil {
				return "mkdtemp", "", []vm.Value{vm.NewString(dir)}, nil
			}
			if !errors.Is(err, syscall.EEXIST) || tries > 100 {
				return "mkdtemp", prefix + "XXXXXX", nil, err
			}
		}
	})
	r.both("statfs", func(args []vm.Value) (string, string, []vm.Value, error) {
		path, err := pathOf(args, 0)
		if err != nil {
			return "statfs", "", nil, err
		}
		raw, err := fsStatfsRaw(path)
		if err != nil {
			return "statfs", path, nil, err
		}
		return "statfs", path, []vm.Value{fsStatfsValue(vmInst, raw, bigintRequested(args[1:]))}, nil
	})
	readvBody := func(returnBufs bool) fsBody {
		return func(args []vm.Value) (string, string, []vm.Value, error) {
			fd, err := fdArg(vmInst, argAt(args, 0))
			if err != nil {
				return "read", "", nil, err
			}
			f, err := fdFile(fd)
			if err != nil {
				return "read", "", nil, err
			}
			if argAt(args, 1).Type() != vm.TypeArray {
				return "read", "", nil, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE", `The "buffers" argument must be an instance of ArrayBufferView[]`)
			}
			arr := argAt(args, 1).AsArray()
			position := int64(-1)
			if pos := argAt(args, 2); pos.IsNumber() && pos.ToFloat() >= 0 {
				position = int64(pos.ToFloat())
			}
			total := 0
			for i := 0; i < arr.Length(); i++ {
				ta := arr.Get(i).AsTypedArray()
				if ta == nil {
					continue
				}
				live := typedArrayLiveBytes(ta)
				if len(live) == 0 {
					continue
				}
				pos := int64(-1)
				if position >= 0 {
					pos = position + int64(total)
				}
				n, err := fdReadInto(f, live, pos)
				total += n
				if err != nil {
					return "read", "", nil, err
				}
				if n < len(live) {
					break
				}
			}
			results := []vm.Value{vm.NumberValue(float64(total))}
			if returnBufs {
				results = append(results, argAt(args, 1))
			}
			return "read", "", results, nil
		}
	}
	r.asyncFn("readv", readvBody(true))
	r.syncFn("readvSync", readvBody(false))

	copyBody := func(args []vm.Value) (string, string, []vm.Value, error) {
		src, err := pathOf(args, 0)
		if err != nil {
			return "copyfile", "", nil, err
		}
		dst, err := pathOf(args, 1)
		if err != nil {
			return "copyfile", src, nil, err
		}
		mode := 0
		if v := argAt(args, 2); v.IsNumber() {
			mode = int(v.ToFloat())
		}
		return "copyfile", src, nil, withDest(fsCopyFile(src, dst, mode&1 != 0), dst)
	}
	r.both("copyFile", copyBody)

	r.both("rm", func(args []vm.Value) (string, string, []vm.Value, error) { return fsRmBody(vmInst, args) })

	m.Function("globSync", func(args ...vm.Value) (vm.Value, error) {
		return fsGlobValue(vmInst, args)
	})
	m.Function("glob", func(all ...vm.Value) (vm.Value, error) {
		args, cb, err := splitCallback(vmInst, all)
		if err != nil {
			return vm.Undefined, err
		}
		res, err := fsGlobValue(vmInst, args)
		if err != nil {
			if fe, ok := err.(*fsSystemError); ok {
				scheduleCallback(vmInst, cb, []vm.Value{fe.exception})
				return vm.Undefined, nil
			}
			return vm.Undefined, err
		}
		scheduleCallback(vmInst, cb, []vm.Value{vm.Null, res})
		return vm.Undefined, nil
	})
}

// newFsCodeError builds a Node internal error (e.g. a SystemError with
// an ERR_FS_* code) carrying the given extra properties.
func newFsCodeError(vmInst *vm.VM, name, code, message string, props map[string]vm.Value) error {
	exception := newJSError(vmInst, message)
	if obj := exception.AsPlainObject(); obj != nil {
		obj.SetOwnNonEnumerable("name", vm.NewString(name))
		obj.SetOwn("code", vm.NewString(code))
		for k, v := range props {
			obj.SetOwn(k, v)
		}
	}
	return &fsSystemError{exception: exception, message: message}
}

func fsCopyFile(src, dst string, excl bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return syscall.EISDIR
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if excl {
		flags |= os.O_EXCL
	}
	out, err := os.OpenFile(dst, flags, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	_ = out.Chmod(info.Mode().Perm())
	return out.Close()
}

func fsGlobValue(vmInst *vm.VM, args []vm.Value) (vm.Value, error) {
	patVal := argAt(args, 0)
	var patterns []string
	if patVal.Type() == vm.TypeArray {
		arr := patVal.AsArray()
		for i := 0; i < arr.Length(); i++ {
			patterns = append(patterns, arr.Get(i).ToString())
		}
	} else if patVal.IsString() {
		patterns = []string{patVal.ToString()}
	} else {
		return vm.Undefined, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE", `The "pattern" argument must be of type string or an instance of Array.`)
	}
	opt := argAt(args, 1)
	cwd, _ := os.Getwd()
	if v, ok := objOption(opt, "cwd"); ok && !isNullish(v) {
		p, err := pathArg(vmInst, v)
		if err != nil {
			return vm.Undefined, err
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		cwd = p
	}
	withTypes := false
	if v, ok := objOption(opt, "withFileTypes"); ok {
		withTypes = v.IsTruthy()
	}
	direntFor := func(abs string, isDir bool) vm.Value {
		mode := os.FileMode(0)
		if info, err := os.Lstat(abs); err == nil {
			mode = info.Mode()
		} else if isDir {
			mode = os.ModeDir
		}
		return newDirent(vmInst, filepath.Base(abs), filepath.Dir(abs), mode)
	}
	gopts := globOptions{cwd: cwd}
	var callErr error
	if ex, ok := objOption(opt, "exclude"); ok && !isNullish(ex) {
		if ex.IsCallable() {
			gopts.exclude = func(rel, abs string, isDir bool) bool {
				arg := vm.NewString(rel)
				if withTypes {
					arg = direntFor(abs, isDir)
				}
				res, err := vmInst.Call(ex, vm.Undefined, []vm.Value{arg})
				if err != nil && callErr == nil {
					callErr = err
				}
				return res.IsTruthy()
			}
		} else if ex.Type() == vm.TypeArray {
			arr := ex.AsArray()
			var pats []string
			for i := 0; i < arr.Length(); i++ {
				pats = append(pats, arr.Get(i).ToString())
			}
			match := globExcludeMatcher(pats)
			gopts.exclude = func(rel, _ string, _ bool) bool { return match(rel) }
		}
	}
	matches, err := fsGlob(patterns, gopts)
	if callErr != nil {
		return vm.Undefined, callErr
	}
	if err != nil {
		return vm.Undefined, err
	}
	out := make([]vm.Value, len(matches))
	for i, mt := range matches {
		if withTypes {
			out[i] = direntFor(mt.abs, mt.isDir)
		} else {
			out[i] = vm.NewString(mt.rel)
		}
	}
	return vm.NewArrayWithArgs(out), nil
}

// fsRmBody is rm/rmSync/fs.promises.rm: recursive and force as in Node.
func fsRmBody(vmInst *vm.VM, args []vm.Value) (string, string, []vm.Value, error) {
	path, err := pathArg(vmInst, argAt(args, 0))
	if err != nil {
		return "rm", "", nil, err
	}
	opt := argAt(args, 1)
	recursive, force := false, false
	if v, ok := objOption(opt, "recursive"); ok {
		recursive = v.IsTruthy()
	}
	if v, ok := objOption(opt, "force"); ok {
		force = v.IsTruthy()
	}
	info, err := os.Lstat(path)
	if err != nil {
		if force && errors.Is(err, os.ErrNotExist) {
			return "rm", path, nil, nil
		}
		return "lstat", path, nil, err
	}
	if info.IsDir() {
		if !recursive {
			return "rm", path, nil, newFsCodeError(vmInst, "SystemError", "ERR_FS_EISDIR",
				"Path is a directory: rm returned EISDIR (is a directory) "+path,
				map[string]vm.Value{"errno": vm.NumberValue(float64(syscall.EISDIR)), "syscall": vm.NewString("rm"), "path": vm.NewString(path), "code": vm.NewString("ERR_FS_EISDIR")})
		}
		return "rm", path, nil, os.RemoveAll(path)
	}
	return "rm", path, nil, syscall.Unlink(path)
}
