package host

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

type fsStats struct {
	Size    int64    `json:"size"`
	MtimeMs float64  `json:"mtimeMs"`
	Mtime   vm.Value `json:"mtime"`
	file    bool
	dir     bool
	symlink bool
}

func (s *fsStats) IsFile() bool            { return s.file }
func (s *fsStats) IsDirectory() bool       { return s.dir }
func (s *fsStats) IsSymbolicLink() bool    { return s.symlink }
func (s *fsStats) IsBlockDevice() bool     { return false }
func (s *fsStats) IsCharacterDevice() bool { return false }
func (s *fsStats) IsFIFO() bool            { return false }
func (s *fsStats) IsSocket() bool          { return false }

// newFsStats builds an fsStats from a Go os.FileInfo, including a real JS
// Date for .mtime — real Node's fs.Stats has both .mtimeMs (a number) and
// .mtime (a Date); real packages call .mtime.getTime() directly
// (proper-lockfile's mtime-precision.js is what surfaced this gap).
// info.IsDir() is false for a symlink even when it points at a
// directory — correct for lstat's own result (which must describe the
// link itself, not its target), which is the whole reason lstat exists
// as distinct from stat.
func newFsStats(vmInst *vm.VM, info os.FileInfo) *fsStats {
	mtimeMs := float64(info.ModTime().UnixMilli())
	mtime := vm.Undefined
	if dateCtor, ok := vmInst.GetGlobal("Date"); ok {
		if v, err := vmInst.Construct(dateCtor, []vm.Value{vm.NumberValue(mtimeMs)}); err == nil {
			mtime = v
		}
	}
	isSymlink := info.Mode()&os.ModeSymlink != 0
	return &fsStats{
		Size:    info.Size(),
		MtimeMs: mtimeMs,
		Mtime:   mtime,
		file:    info.Mode().IsRegular(),
		dir:     info.IsDir(),
		symlink: isSymlink,
	}
}

var (
	fsFDs  sync.Map
	fsFDID atomic.Int64

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

func fsOpen(path string, flags string) (int64, error) {
	goFlags, err := fsOpenFlags(flags)
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, goFlags, 0644)
	if err != nil {
		return 0, err
	}
	id := fsFDID.Add(1)
	fsFDs.Store(id, f)
	return id, nil
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
		return nil
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

func fsWrite(fd int64, data string) (int64, error) {
	v, ok := fsFDs.Load(fd)
	if !ok {
		return 0, os.ErrInvalid
	}
	n, err := v.(*os.File).WriteString(data)
	return int64(n), err
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
		m.Function("mkdirSync", func(pathVal vm.Value, opts map[string]interface{}) (interface{}, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			mkdirFn := os.Mkdir
			if mkdirRecursiveRequested(opts) {
				mkdirFn = os.MkdirAll
			}
			return nil, wrapFsErr(vmInst, "mkdir", path, mkdirFn(path, 0755))
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
		m.Function("rmdirSync", func(pathVal vm.Value) (interface{}, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			return nil, wrapFsErr(vmInst, "rmdir", path, os.Remove(path))
		})
		m.Function("statSync", func(pathVal vm.Value, _ ...interface{}) (*fsStats, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			fsTouch("stat", path)
			info, err := os.Stat(path)
			if err != nil {
				return nil, wrapFsErr(vmInst, "stat", path, err)
			}
			return newFsStats(vmInst, info), nil
		})
		m.Function("lstatSync", func(pathVal vm.Value, _ ...interface{}) (*fsStats, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return nil, perr
			}
			fsTouch("stat", path)
			info, err := os.Lstat(path)
			if err != nil {
				return nil, wrapFsErr(vmInst, "lstat", path, err)
			}
			return newFsStats(vmInst, info), nil
		})
		m.Function("realpathSync", func(pathVal vm.Value, _ ...interface{}) (string, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return "", perr
			}
			resolved, err := filepath.EvalSymlinks(path)
			return resolved, wrapFsErr(vmInst, "realpath", path, err)
		})
		m.Function("openSync", func(pathVal vm.Value, opts ...interface{}) (int64, error) {
			path, perr := pathArg(vmInst, pathVal)
			if perr != nil {
				return 0, perr
			}
			flags := fsOpenFlagsArg(opts)
			fd, err := fsOpen(path, flags)
			return fd, wrapFsErr(vmInst, "open", path, err)
		})
		m.Function("closeSync", func(fd int64, _ ...interface{}) (interface{}, error) {
			return nil, wrapFsErr(vmInst, "close", "", fsClose(fd))
		})
		m.Function("writeSync", func(fd int64, data string, _ ...interface{}) (int64, error) {
			n, err := fsWrite(fd, data)
			return n, wrapFsErr(vmInst, "write", "", err)
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
