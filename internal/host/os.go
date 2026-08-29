package host

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

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
		m.Function("release", func() string {
			return releaseVersion()
		})
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

func releaseVersion() string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("uname", "-r").Output()
		if err != nil {
			return "0.0.0"
		}
		return strings.TrimSpace(string(out))
	case "windows":
		return "10.0.0"
	default:
		out, err := exec.Command("uname", "-r").Output()
		if err != nil {
			return "0.0.0"
		}
		return strings.TrimSpace(string(out))
	}
}
