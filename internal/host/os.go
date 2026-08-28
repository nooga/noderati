package host

import (
	"os"
	"runtime"

	"github.com/nooga/paserati/pkg/driver"
)

func declareOS(p *driver.Paserati) {
	eol := "\n"
	if runtime.GOOS == "windows" {
		eol = "\r\n"
	}
	p.DeclareModule("os", func(m *driver.ModuleBuilder) {
		m.Const("EOL", eol)
		m.Function("platform", func() string { return runtime.GOOS })
		m.Function("arch", func() string { return runtime.GOARCH })
		m.Function("homedir", func() string {
			h, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			return h
		})
		m.Function("tmpdir", os.TempDir)
		m.Function("hostname", func() string {
			h, err := os.Hostname()
			if err != nil {
				return ""
			}
			return h
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:os", "os")
}
