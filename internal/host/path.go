package host

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
)

func declarePath(p *driver.Paserati) {
	p.DeclareModule("path", func(m *driver.ModuleBuilder) {
		m.Const("sep", string(os.PathSeparator))
		m.Const("delimiter", string(os.PathListSeparator))
		m.Function("join", func(parts ...string) string {
			return filepath.Join(parts...)
		})
		m.Function("dirname", filepath.Dir)
		m.Function("basename", func(p string) string {
			return filepath.Base(p)
		})
		m.Function("extname", filepath.Ext)
		m.Function("isAbsolute", filepath.IsAbs)
		m.Function("normalize", filepath.Clean)
		m.Function("resolve", func(parts ...string) string {
			if len(parts) == 0 {
				cwd, err := os.Getwd()
				if err != nil {
					return ""
				}
				return cwd
			}
			p := filepath.Join(parts...)
			if filepath.IsAbs(p) {
				return filepath.Clean(p)
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				return filepath.Clean(p)
			}
			return abs
		})
		m.Function("relative", func(from, to string) string {
			rel, err := filepath.Rel(from, to)
			if err != nil {
				return to
			}
			return rel
		})
		m.Function("toNamespacedPath", func(p string) string { return p })
		m.Namespace("win32", func(ns *driver.NamespaceBuilder) {
			ns.Const("sep", `\`)
			ns.Function("basename", win32Basename)
			ns.Function("dirname", win32Dirname)
		})
		m.Namespace("posix", func(ns *driver.NamespaceBuilder) {
			ns.Const("sep", "/")
			ns.Function("basename", func(p string) string { return filepath.Base(p) })
			ns.Function("dirname", filepath.Dir)
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:path", "path")
}

func win32Basename(p string) string {
	p = strings.ReplaceAll(p, `/`, `\`)
	i := strings.LastIndex(p, `\`)
	if i < 0 {
		return p
	}
	return p[i+1:]
}

func win32Dirname(p string) string {
	p = strings.ReplaceAll(p, `/`, `\`)
	i := strings.LastIndex(p, `\`)
	if i <= 0 {
		if i == 0 {
			return `\`
		}
		return "."
	}
	return p[:i]
}
