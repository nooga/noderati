package host

import (
	"os"

	"github.com/nooga/paserati/pkg/driver"
)

type fsStats struct {
	Size        int64   `json:"size"`
	IsFile      bool    `json:"isFile"`
	IsDirectory bool    `json:"isDirectory"`
	MtimeMs     float64 `json:"mtimeMs"`
}

func declareFS(p *driver.Paserati) {
	p.DeclareModule("fs", func(m *driver.ModuleBuilder) {
		m.Function("readFileSync", func(path string) (string, error) {
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			return string(b), nil
		})
		m.Function("writeFileSync", func(path string, data string) (interface{}, error) {
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
			_, err := os.Stat(path)
			return err == nil
		})
		m.Function("mkdirSync", func(path string) (interface{}, error) {
			return nil, os.MkdirAll(path, 0755)
		})
		m.Function("readdirSync", func(path string) ([]string, error) {
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
		m.Function("statSync", func(path string) (*fsStats, error) {
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			return &fsStats{
				Size:        info.Size(),
				IsFile:      !info.IsDir(),
				IsDirectory: info.IsDir(),
				MtimeMs:     float64(info.ModTime().UnixMilli()),
			}, nil
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
