package host

import (
	"fmt"
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
		m.Function("cpus", cpuInfos)
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:os", "os")
}

// osCPUTimes mirrors real Node's os.cpus()[n].times shape. Noderati has no
// way to read real per-core tick counts, so every field is zero - callers
// that only care about the shape (model/speed/times with the five numeric
// fields) get what they expect; callers that sum real ticks to compute load
// won't get a meaningful answer, but that's true of the "fake but
// shaped-correctly" builtins elsewhere in this package too.
type osCPUTimes struct {
	User int64 `json:"user"`
	Nice int64 `json:"nice"`
	Sys  int64 `json:"sys"`
	Idle int64 `json:"idle"`
	Irq  int64 `json:"irq"`
}

// osCPUInfo mirrors one entry of real Node's os.cpus() array.
type osCPUInfo struct {
	Model string      `json:"model"`
	Speed float64     `json:"speed"`
	Times *osCPUTimes `json:"times"`
}

// cpuInfos synthesizes an os.cpus()-shaped array from runtime.NumCPU() -
// there's no portable way to read real model/speed/tick-count info without a
// platform-specific syscall, and nothing in noderati needs the numbers to be
// real, just correctly shaped (found bisecting against the Node-builtin
// shim - see docs/real-node-plan.md's Sixtieth round entry).
func cpuInfos() []*osCPUInfo {
	n := max(runtime.NumCPU(), 1)
	infos := make([]*osCPUInfo, n)
	for i := range infos {
		infos[i] = &osCPUInfo{
			Model: fmt.Sprintf("%s (%s)", runtime.GOARCH, runtime.GOOS),
			Speed: 2400,
			Times: &osCPUTimes{},
		}
	}
	return infos
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
