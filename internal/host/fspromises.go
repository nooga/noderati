package host

import (
	"os"
	"path/filepath"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

func declareFSPromises(p *driver.Paserati) {
	vmInst := p.GetVM()
	p.DeclareModule("fs/promises", func(m *driver.ModuleBuilder) {
		m.AsyncFunction("readFile", func(path string, _ ...interface{}) (string, error) {
			fsTouch("read", path)
			b, err := os.ReadFile(path)
			if err != nil {
				return "", wrapFsErr(vmInst, "open", path, err)
			}
			return string(b), nil
		})
		m.AsyncFunction("writeFile", func(path string, data string, _ ...interface{}) (interface{}, error) {
			return nil, wrapFsErr(vmInst, "open", path, os.WriteFile(path, []byte(data), 0644))
		})
		m.AsyncFunction("mkdir", func(path string, opts map[string]interface{}) (interface{}, error) {
			mkdirFn := os.Mkdir
			if mkdirRecursiveRequested(opts) {
				mkdirFn = os.MkdirAll
			}
			return nil, wrapFsErr(vmInst, "mkdir", path, mkdirFn(path, 0755))
		})
		m.AsyncFunction("readdir", func(path string, opts map[string]interface{}) ([]vm.Value, error) {
			fsTouch("readdir", path)
			entries, err := readdirEntries(vmInst, path, opts)
			if err != nil {
				return nil, wrapFsErr(vmInst, "scandir", path, err)
			}
			return entries, nil
		})
		m.AsyncFunction("stat", func(path string, _ ...interface{}) (*fsStats, error) {
			fsTouch("stat", path)
			info, err := os.Stat(path)
			if err != nil {
				return nil, wrapFsErr(vmInst, "stat", path, err)
			}
			return newFsStats(vmInst, info), nil
		})
		m.AsyncFunction("lstat", func(path string, _ ...interface{}) (*fsStats, error) {
			fsTouch("stat", path)
			info, err := os.Lstat(path)
			if err != nil {
				return nil, wrapFsErr(vmInst, "lstat", path, err)
			}
			return newFsStats(vmInst, info), nil
		})
		m.AsyncFunction("access", func(path string, _ ...interface{}) (interface{}, error) {
			fsTouch("stat", path)
			_, err := os.Stat(path)
			return nil, wrapFsErr(vmInst, "access", path, err)
		})
		m.AsyncFunction("unlink", func(path string) (interface{}, error) {
			return nil, wrapFsErr(vmInst, "unlink", path, os.Remove(path))
		})
		m.AsyncFunction("rm", func(path string, _ ...interface{}) (interface{}, error) {
			if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
				return nil, wrapFsErr(vmInst, "rm", path, err)
			}
			return nil, nil
		})
		m.AsyncFunction("realpath", func(path string, _ ...interface{}) (string, error) {
			resolved, err := filepath.EvalSymlinks(path)
			return resolved, wrapFsErr(vmInst, "realpath", path, err)
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:fs/promises", "fs/promises")
}
