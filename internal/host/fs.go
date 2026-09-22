package host

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// statSys is the raw stat(2) data behind a real fs.Stats, filled per
// platform by statSysFields (fs_stat_*.go).
type statSys struct {
	dev, ino, nlink, rdev   uint64
	mode, uid, gid          uint32
	blksize, blocks         int64
	atime, ctime, birthtime time.Time
}

// POSIX S_IFMT file-type bits, which real Node's stats.mode carries.
const (
	sIFMT   = 0o170000
	sIFSOCK = 0o140000
	sIFLNK  = 0o120000
	sIFREG  = 0o100000
	sIFBLK  = 0o060000
	sIFDIR  = 0o040000
	sIFCHR  = 0o020000
	sIFIFO  = 0o010000
)

// modeFromFileInfo reconstructs a POSIX st_mode for platforms without a
// syscall.Stat_t.
func modeFromFileInfo(info os.FileInfo) uint32 {
	m := info.Mode()
	mode := uint32(m.Perm())
	switch {
	case m&os.ModeSymlink != 0:
		mode |= sIFLNK
	case m.IsDir():
		mode |= sIFDIR
	case m&os.ModeNamedPipe != 0:
		mode |= sIFIFO
	case m&os.ModeSocket != 0:
		mode |= sIFSOCK
	case m&os.ModeCharDevice != 0:
		mode |= sIFCHR
	case m&os.ModeDevice != 0:
		mode |= sIFBLK
	default:
		mode |= sIFREG
	}
	if m&os.ModeSetuid != 0 {
		mode |= 0o4000
	}
	if m&os.ModeSetgid != 0 {
		mode |= 0o2000
	}
	if m&os.ModeSticky != 0 {
		mode |= 0o1000
	}
	return mode
}

var statsProtos sync.Map // *vm.VM -> *vm.PlainObject

// statsPrototype is the shared prototype every Stats object gets: the
// is*() predicates read this.mode, like real Node's own StatsBase.
func statsPrototype(vmInst *vm.VM) *vm.PlainObject {
	if p, ok := statsProtos.Load(vmInst); ok {
		return p.(*vm.PlainObject)
	}
	proto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	for name, bits := range map[string]uint32{
		"isFile": sIFREG, "isDirectory": sIFDIR, "isSymbolicLink": sIFLNK,
		"isBlockDevice": sIFBLK, "isCharacterDevice": sIFCHR, "isFIFO": sIFIFO, "isSocket": sIFSOCK,
	} {
		want := bits
		proto.SetOwn(name, vm.NewNativeFunction(0, false, name, func(_ []vm.Value) (vm.Value, error) {
			obj := vmInst.GetThis().AsPlainObject()
			if obj == nil {
				return vm.False, nil
			}
			modeVal, _ := obj.Get("mode")
			return vm.BooleanValue(uint32(modeVal.ToFloat())&sIFMT == want), nil
		}))
	}
	// Real Node (22+) exposes the Date fields as lazy prototype accessors
	// over the *Ms numbers (so they're absent from Object.keys until used);
	// the first read, or any write, replaces the accessor with a plain own
	// enumerable data property.
	yes := true
	for _, name := range []string{"atime", "mtime", "ctime", "birthtime"} {
		field := name
		getter := vm.NewNativeFunction(0, false, "get "+field, func(_ []vm.Value) (vm.Value, error) {
			obj := vmInst.GetThis().AsPlainObject()
			if obj == nil {
				return vm.Undefined, nil
			}
			ms, _ := obj.Get(field + "Ms")
			date := newJSDate(vmInst, math.Trunc(ms.ToFloat()))
			obj.DefineOwnProperty(field, date, &yes, &yes, &yes)
			return date, nil
		})
		setter := vm.NewNativeFunction(1, false, "set "+field, func(args []vm.Value) (vm.Value, error) {
			if obj := vmInst.GetThis().AsPlainObject(); obj != nil {
				obj.DefineOwnProperty(field, argAt(args, 0), &yes, &yes, &yes)
			}
			return vm.Undefined, nil
		})
		proto.DefineAccessorProperty(field, getter, true, setter, true, &yes, &yes)
	}
	actual, _ := statsProtos.LoadOrStore(vmInst, proto)
	return actual.(*vm.PlainObject)
}

func newJSDate(vmInst *vm.VM, ms float64) vm.Value {
	if dateCtor, ok := vmInst.GetGlobal("Date"); ok {
		if v, err := vmInst.Construct(dateCtor, []vm.Value{vm.NumberValue(ms)}); err == nil {
			return v
		}
	}
	return vm.Undefined
}

func timeToMs(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e6
}

// fsStatsValue builds a real Node-shaped fs.Stats object (every numeric
// field plus the atime/mtime/ctime/birthtime Dates) from a Go FileInfo.
// Real packages read far more than size/mtime: tar writes mode/uid/gid/
// atime/ctime into every header and keys hardlink detection on dev:ino
// when nlink > 1.
func fsStatsValue(vmInst *vm.VM, info os.FileInfo) vm.Value {
	sys, ok := statSysFields(info)
	if !ok {
		mt := info.ModTime()
		sys = statSys{mode: modeFromFileInfo(info), nlink: 1, blksize: 4096, atime: mt, ctime: mt, birthtime: mt}
		sys.blocks = (info.Size() + 511) / 512
	}
	obj := vm.NewObject(vm.NewValueFromPlainObject(statsPrototype(vmInst))).AsPlainObject()
	num := func(name string, v float64) { obj.SetOwn(name, vm.NumberValue(v)) }
	num("dev", float64(sys.dev))
	num("mode", float64(sys.mode))
	num("nlink", float64(sys.nlink))
	num("uid", float64(sys.uid))
	num("gid", float64(sys.gid))
	num("rdev", float64(sys.rdev))
	num("blksize", float64(sys.blksize))
	num("ino", float64(sys.ino))
	num("size", float64(info.Size()))
	num("blocks", float64(sys.blocks))
	for _, t := range []struct {
		name string
		at   time.Time
	}{{"atime", sys.atime}, {"mtime", info.ModTime()}, {"ctime", sys.ctime}, {"birthtime", sys.birthtime}} {
		num(t.name+"Ms", timeToMs(t.at))
	}
	return vm.NewValueFromPlainObject(obj)
}

var (
	fsFDs sync.Map // real OS fd (int64) -> *os.File

	fsStatReads atomic.Int64
	fsStatStats atomic.Int64
	fsStatDirs  atomic.Int64
	fsLastPath  atomic.Value // string
	fsStatsOnce sync.Once
)

func fsTouch(kind string, path string) {
	switch kind {
	case "read":
		fsStatReads.Add(1)
	case "stat":
		fsStatStats.Add(1)
	case "readdir":
		fsStatDirs.Add(1)
	}
	fsLastPath.Store(path)
	if os.Getenv("NODERATI_FS_STATS") == "" {
		return
	}
	fsStatsOnce.Do(func() {
		t0 := time.Now()
		go func() {
			for {
				time.Sleep(time.Second)
				last, _ := fsLastPath.Load().(string)
				fmt.Fprintf(os.Stderr, "[noderati fs] t=%.1fs reads=%d stats=%d readdirs=%d last=%s\n",
					time.Since(t0).Seconds(), fsStatReads.Load(), fsStatStats.Load(), fsStatDirs.Load(), last)
			}
		}()
	})
}

// fsOpenFlags maps real Node's fs.open(Sync) flags string (default "r"
// when omitted) to the Go os.OpenFile bits it actually means. Found the
// hard way: the previous fsOpen(path) ignored flags entirely and always
// opened O_WRONLY|O_CREATE|O_TRUNC - so `fs.openSync(path, "r")` (real
// Node: open existing file read-only, ENOENT if missing) instead
// silently created the file if it didn't exist, and silently truncated
// it to empty if it did. A real, dangerous, pre-existing gap - not a
// hypothetical, chasing real chokidar's own read-only file probing
// under noderati is exactly what surfaced it. Every real flag Node
// documents is mapped, not just the read-only case that happened to
// matter here.
func fsOpenFlags(flags string) (int, error) {
	switch flags {
	case "", "r":
		return os.O_RDONLY, nil
	case "rs", "sr":
		return os.O_RDONLY, nil
	case "r+":
		return os.O_RDWR, nil
	case "rs+", "sr+":
		return os.O_RDWR, nil
	case "w":
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC, nil
	case "wx", "xw":
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC | os.O_EXCL, nil
	case "w+":
		return os.O_RDWR | os.O_CREATE | os.O_TRUNC, nil
	case "wx+", "xw+":
		return os.O_RDWR | os.O_CREATE | os.O_TRUNC | os.O_EXCL, nil
	case "a":
		return os.O_WRONLY | os.O_CREATE | os.O_APPEND, nil
	case "ax", "xa":
		return os.O_WRONLY | os.O_CREATE | os.O_APPEND | os.O_EXCL, nil
	case "a+":
		return os.O_RDWR | os.O_CREATE | os.O_APPEND, nil
	case "ax+", "xa+":
		return os.O_RDWR | os.O_CREATE | os.O_APPEND | os.O_EXCL, nil
	default:
		return 0, fmt.Errorf("Unknown file open flag: %s", flags)
	}
}

// fsOpen opens path and registers it under its real OS file descriptor,
// so fd numbers mean what they do in real Node (never colliding with
// stdio's 0/1/2) and fd-based calls reach the right file.
func fsOpen(path string, flags string, mode os.FileMode) (int64, error) {
	goFlags, err := fsOpenFlags(flags)
	if err != nil {
		return 0, err
	}
	return fsOpenRaw(path, goFlags, mode)
}

func fsOpenRaw(path string, goFlags int, mode os.FileMode) (int64, error) {
	f, err := os.OpenFile(path, goFlags, mode)
	if err != nil {
		return 0, err
	}
	fd := int64(f.Fd())
	fsFDs.Store(fd, f)
	return fd, nil
}

// fsFile resolves a JS fd: one this process opened, or stdio.
func fsFile(fd int64) (*os.File, bool) {
	if v, ok := fsFDs.Load(fd); ok {
		return v.(*os.File), true
	}
	switch fd {
	case 0:
		return os.Stdin, true
	case 1:
		return os.Stdout, true
	case 2:
		return os.Stderr, true
	}
	return nil, false
}

// fsOpenFlagsArg picks the real Node flags-string argument out of
// fs.open(Sync)'s optional `[flags[, mode]]` tail, defaulting to "r"
// (real Node's own default) for anything else - omitted entirely, a
// numeric fs.constants.O_* bitmask (not modeled at this layer yet), or
// a callback (the async variant's own trailing argument, never a
// flags string).
func fsOpenFlagsArg(opts []interface{}) string {
	if len(opts) > 0 {
		if s, ok := opts[0].(string); ok {
			return s
		}
	}
	return "r"
}

func fsClose(fd int64) error {
	v, ok := fsFDs.LoadAndDelete(fd)
	if !ok {
		return syscall.EBADF
	}
	return v.(*os.File).Close()
}

// fsReadEncoding reads the encoding out of readFileSync's variadic options
// argument, matching real Node's own two accepted shapes: a bare string
// encoding shorthand (readFileSync(path, "utf8")), or an
// {encoding, flag} object (readFileSync(path, {encoding: "utf8"})). The
// bool return distinguishes "no encoding requested" (opts empty, or an
// options object with no/null encoding - real Node's own default) from
// "encoding requested" so the caller can return a real Buffer in the
// former case rather than guessing from an empty string.
func fsReadEncoding(opts []interface{}) (string, bool) {
	if len(opts) == 0 {
		return "", false
	}
	switch v := opts[0].(type) {
	case string:
		return v, true
	case map[string]interface{}:
		if raw, ok := v["encoding"]; ok {
			if s, ok := raw.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

// pathArg extracts a filesystem path from a real Node fs path argument,
// which can be a string, a Buffer/TypedArray (real Node decodes its
// bytes as the path text), or a URL (a real `file:` URL - the same
// conversion url.go's fileURLToPath exposes to JS, reused here as
// fileURLStringToPath). Found via a real, extremely common ESM idiom
// (real vite's own dist/node/constants.js: `readFileSync(new
// URL("../../package.json", import.meta.url))`) that a plain `path
// string` parameter type silently broke: it doesn't throw a helpful
// error, it turns the URL object into the literal string "[object
// Object]" - paserati's generic Go-string coercion for a non-string
// value (vm.Value.ToString(), used by the reflection-based Function
// wrapper for any `string`-typed parameter) doesn't run the real JS
// ToString abstract operation, so it never reaches the URL's own
// `href` - then reports a plausible-looking but wrong ENOENT for that
// literal string. Not the Buffer.concat-on-a-string shape (silently
// empty) this codebase has already fixed twice (round 101,
// child_process.go last round), but the same root idea: a host
// function typed to accept a plain string silently mis-stringifies a
// real, valid non-string argument instead of handling or rejecting it
// correctly.
func pathArg(vmInst *vm.VM, v vm.Value) (string, error) {
	if v.IsString() {
		return v.ToString(), nil
	}
	if href, ok := hrefFromURLLike(v); ok {
		return fileURLStringToPath(href)
	}
	return string(valueToBytes(vmInst, v)), nil
}

func declareFS(p *driver.Paserati) {
	vmInst := p.GetVM()
	p.DeclareModule("fs", func(m *driver.ModuleBuilder) {
		m.Function("readFileSync", func(pathVal vm.Value, opts ...interface{}) (vm.Value, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return vm.Undefined, perr
			}
			fsTouch("read", path)
			b, err := os.ReadFile(path)
			if err != nil {
				return vm.Undefined, wrapFsErr(vmInst, "open", path, err)
			}
			// Real Node's fs.readFileSync(path) returns a Buffer by
			// default and only decodes to a string when an explicit
			// encoding was given (a string shorthand, or an
			// {encoding} object) - matching readFile/readFileSync's
			// documented "no encoding is specified... Buffer object"
			// behavior. Previously this always ran raw bytes through
			// Go's string(b), which corrupts any binary read (e.g. a
			// .wasm file) the moment those bytes aren't valid UTF-8:
			// round-tripping through paserati's own JS string
			// representation and back out as a Uint8Array produced
			// truncated/garbled bytes, not the original file.
			encoding, hasEncoding := fsReadEncoding(opts)
			if !hasEncoding {
				return wrapBuffer(vmInst, b), nil
			}
			return vm.NewString(encodeBufferBytes(b, encoding)), nil
		})
		m.Function("writeFileSync", func(pathVal vm.Value, data vm.Value, _ ...interface{}) (interface{}, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			// Real Node's fs.writeFileSync accepts a string or a real
			// Buffer/TypedArray `data` argument and writes its raw
			// bytes either way. valueToBytes (net.go) already draws
			// that same string-or-real-bytes distinction for socket
			// writes, reused here rather than assuming (and silently
			// mis-stringifying) a JS string as this used to.
			return nil, wrapFsErr(vmInst, "open", path, os.WriteFile(path, valueToBytes(vmInst, data), 0644))
		})
		m.Function("appendFileSync", func(pathVal vm.Value, data vm.Value) (interface{}, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return nil, wrapFsErr(vmInst, "open", path, err)
			}
			defer f.Close()
			_, err = f.Write(valueToBytes(vmInst, data))
			return nil, wrapFsErr(vmInst, "write", path, err)
		})
		m.Function("existsSync", func(pathVal vm.Value) bool {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return false
			}
			fsTouch("stat", path)
			_, err := os.Stat(path)
			return err == nil
		})
		m.Function("accessSync", func(pathVal vm.Value, _ ...interface{}) (interface{}, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			fsTouch("stat", path)
			_, err := os.Stat(path)
			return nil, wrapFsErr(vmInst, "access", path, err)
		})
		m.Function("chmodSync", func(pathVal vm.Value, mode int64) (interface{}, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			return nil, wrapFsErr(vmInst, "chmod", path, os.Chmod(path, os.FileMode(mode)))
		})
		m.Namespace("constants", func(ns *driver.NamespaceBuilder) {
			for _, c := range fsConstantEntries() {
				ns.Const(c.Name, c.Value)
			}
		})
		m.Function("readdirSync", func(pathVal vm.Value, opts map[string]interface{}) ([]vm.Value, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			fsTouch("readdir", path)
			entries, err := readdirEntries(vmInst, path, opts)
			if err != nil {
				return nil, wrapFsErr(vmInst, "scandir", path, err)
			}
			return entries, nil
		})
		m.Function("unlinkSync", func(pathVal vm.Value) (interface{}, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			return nil, wrapFsErr(vmInst, "unlink", path, os.Remove(path))
		})
		m.Function("statSync", func(pathVal vm.Value, opts ...vm.Value) (vm.Value, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return vm.Undefined, perr
			}
			fsTouch("stat", path)
			info, err := os.Stat(path)
			if err != nil {
				if statThrowIfNoEntryFalse(opts) && os.IsNotExist(err) {
					return vm.Undefined, nil
				}
				return vm.Undefined, wrapFsErr(vmInst, "stat", path, err)
			}
			return fsStatsValue(vmInst, info), nil
		})
		m.Function("lstatSync", func(pathVal vm.Value, opts ...vm.Value) (vm.Value, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return vm.Undefined, perr
			}
			fsTouch("stat", path)
			info, err := os.Lstat(path)
			if err != nil {
				if statThrowIfNoEntryFalse(opts) && os.IsNotExist(err) {
					return vm.Undefined, nil
				}
				return vm.Undefined, wrapFsErr(vmInst, "lstat", path, err)
			}
			return fsStatsValue(vmInst, info), nil
		})
		m.Function("realpathSync", func(pathVal vm.Value, _ ...interface{}) (string, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return "", perr
			}
			resolved, err := filepath.EvalSymlinks(path)
			return resolved, wrapFsErr(vmInst, "realpath", path, err)
		})
		m.Function("copyFileSync", func(srcVal, dstVal vm.Value) (interface{}, error) {
			src, perr := pathArg(vmInst, srcVal)
			if perr != nil {
				return nil, perr
			}
			dst, perr := pathArg(vmInst, dstVal)
			if perr != nil {
				return nil, perr
			}
			return nil, wrapFsErr(vmInst, "copyfile", src, copyFile(src, dst))
		})
		m.Function("renameSync", func(oldPathVal, newPathVal vm.Value) (interface{}, error) {
			oldPath, perr := pathArg(vmInst, oldPathVal)
			if perr != nil {
				return nil, perr
			}
			newPath, perr := pathArg(vmInst, newPathVal)
			if perr != nil {
				return nil, perr
			}
			return nil, wrapFsErr(vmInst, "rename", oldPath, os.Rename(oldPath, newPath))
		})
		m.Function("rmSync", func(pathVal vm.Value) (interface{}, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			return nil, wrapFsErr(vmInst, "rm", path, os.RemoveAll(path))
		})
		declareFSAsync(m, vmInst)
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:fs", "fs")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = out.ReadFrom(in)
	return err
}
