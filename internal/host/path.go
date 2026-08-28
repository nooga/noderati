package host

import (
	"os"
	"path/filepath"

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
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:path", "path")
}
