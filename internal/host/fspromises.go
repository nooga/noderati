package host

import (
	"os"
	"path/filepath"

	"github.com/nooga/paserati/pkg/driver"
)

func declareFSPromises(p *driver.Paserati) {
	p.DeclareModule("fs/promises", func(m *driver.ModuleBuilder) {
		m.AsyncFunction("readFile", func(path string, _ ...interface{}) (string, error) {
			fsTouch("read", path)
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			return string(b), nil
		})
		m.AsyncFunction("writeFile", func(path string, data string, _ ...interface{}) (interface{}, error) {
			return nil, os.WriteFile(path, []byte(data), 0644)
		})
		m.AsyncFunction("mkdir", func(path string, _ ...interface{}) (interface{}, error) {
			return nil, os.MkdirAll(path, 0755)
		})
		m.AsyncFunction("readdir", func(path string, _ ...interface{}) ([]string, error) {
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
		m.AsyncFunction("stat", func(path string, _ ...interface{}) (*fsStats, error) {
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
		m.AsyncFunction("access", func(path string, _ ...interface{}) (interface{}, error) {
			fsTouch("stat", path)
			_, err := os.Stat(path)
			return nil, err
		})
		m.AsyncFunction("unlink", func(path string) (interface{}, error) {
			return nil, os.Remove(path)
		})
		m.AsyncFunction("rm", func(path string, _ ...interface{}) (interface{}, error) {
			if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			return nil, nil
		})
		m.AsyncFunction("realpath", func(path string, _ ...interface{}) (string, error) {
			return filepath.EvalSymlinks(path)
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:fs/promises", "fs/promises")
}
