package host

import (
	"strings"

	"github.com/nooga/paserati/pkg/driver"
)

func declareUtil(p *driver.Paserati) {
	p.DeclareModule("util", func(m *driver.ModuleBuilder) {
		m.Function("format", func(parts ...string) string {
			if len(parts) == 0 {
				return ""
			}
			return strings.Join(parts, " ")
		})
		m.Function("inspect", func(v string) string {
			return v
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:util", "util")
}
