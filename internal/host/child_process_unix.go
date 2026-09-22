//go:build !windows

package host

import (
	"os/exec"
	"runtime"
	"syscall"
)

// setDetached puts cmd's future child into its own new process group (POSIX
// setpgid(0, 0) - the new group's id equals the child's own pid) instead of
// inheriting noderati's. This is what makes process.kill(-pid, sig) (see
// signals.go's installProcessKill, which already forwards whatever pid JS
// passes - including negative ones - to syscall.Kill unmodified) actually
// reach the whole subprocess tree instead of just the one process: real
// Node's pi-agent-core relies on exactly this pairing for its own
// killProcessTree helper, used both for a tool call's timeout and for
// Escape-key cancellation.
func setDetached(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// exitSignalName reports the Node signal name that terminated a child
// ("SIGTERM"), or "" if it exited normally.
func exitSignalName(ee *exec.ExitError) string {
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return ""
	}
	sig := ws.Signal()
	for name, s := range nodeSignals {
		if s == sig {
			return name
		}
	}
	return ""
}

// processRSS is the process's peak resident set size in bytes
// (ru_maxrss is bytes on macOS, KiB on Linux).
func processRSS() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		return ru.Maxrss
	}
	return ru.Maxrss * 1024
}
