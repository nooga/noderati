package host

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nooga/paserati/pkg/driver"
)

type fsStats struct {
	Size    int64   `json:"size"`
	MtimeMs float64 `json:"mtimeMs"`
	file    bool
	dir     bool
}

func (s *fsStats) IsFile() bool      { return s.file }
func (s *fsStats) IsDirectory() bool { return s.dir }

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

func fsOpen(path string) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return 0, err
	}
	id := fsFDID.Add(1)
	fsFDs.Store(id, f)
	return id, nil
}

func fsClose(fd int64) error {
	v, ok := fsFDs.LoadAndDelete(fd)
	if !ok {
		return nil
	}
	return v.(*os.File).Close()
}

func fsWrite(fd int64, data string) (int64, error) {
	v, ok := fsFDs.Load(fd)
	if !ok {
		return 0, os.ErrInvalid
	}
	n, err := v.(*os.File).WriteString(data)
	return int64(n), err
}

func declareFS(p *driver.Paserati) {
	p.DeclareModule("fs", func(m *driver.ModuleBuilder) {
		m.Function("readFileSync", func(path string, _ ...interface{}) (string, error) {
			fsTouch("read", path)
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			return string(b), nil
		})
		m.Function("writeFileSync", func(path string, data string, _ ...interface{}) (interface{}, error) {
			return nil, os.WriteFile(path, []byte(data), 0644)
		})
		m.Function("appendFileSync", func(path string, data string) (interface{}, error) {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			_, err = f.WriteString(data)
			return nil, err
		})
		m.Function("existsSync", func(path string) bool {
			fsTouch("stat", path)
			_, err := os.Stat(path)
			return err == nil
		})
		m.Function("accessSync", func(path string, _ ...interface{}) (interface{}, error) {
			fsTouch("stat", path)
			_, err := os.Stat(path)
			return nil, err
		})
		m.Function("chmodSync", func(path string, mode int64) (interface{}, error) {
			return nil, os.Chmod(path, os.FileMode(mode))
		})
		m.Namespace("constants", func(ns *driver.NamespaceBuilder) {
			ns.Const("F_OK", 0)
			ns.Const("X_OK", 1)
			ns.Const("W_OK", 2)
			ns.Const("R_OK", 4)
		})
		m.Function("mkdirSync", func(path string, _ ...interface{}) (interface{}, error) {
			return nil, os.MkdirAll(path, 0755)
		})
		m.Function("readdirSync", func(path string, _ ...interface{}) ([]string, error) {
			fsTouch("readdir", path)
			entries, err := os.ReadDir(path)
			if err != nil {
				return nil, err
			}
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
			}
			return names, nil
		})
		m.Function("unlinkSync", func(path string) (interface{}, error) {
			return nil, os.Remove(path)
		})
		m.Function("rmdirSync", func(path string) (interface{}, error) {
			return nil, os.Remove(path)
		})
		m.Function("statSync", func(path string, _ ...interface{}) (*fsStats, error) {
			fsTouch("stat", path)
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			return &fsStats{
				Size:    info.Size(),
				MtimeMs: float64(info.ModTime().UnixMilli()),
				file:    !info.IsDir(),
				dir:     info.IsDir(),
			}, nil
		})
		m.Function("realpathSync", func(path string, _ ...interface{}) (string, error) {
			return filepath.EvalSymlinks(path)
		})
		m.Function("openSync", func(path string, _ ...interface{}) (int64, error) {
			return fsOpen(path)
		})
		m.Function("closeSync", func(fd int64, _ ...interface{}) (interface{}, error) {
			return nil, fsClose(fd)
		})
		m.Function("writeSync", func(fd int64, data string, _ ...interface{}) (int64, error) {
			return fsWrite(fd, data)
		})
		m.Function("copyFileSync", func(src, dst string) (interface{}, error) {
			return nil, copyFile(src, dst)
		})
		m.Function("renameSync", func(oldPath, newPath string) (interface{}, error) {
			return nil, os.Rename(oldPath, newPath)
		})
		m.Function("rmSync", func(path string) (interface{}, error) {
			return nil, os.RemoveAll(path)
		})
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
