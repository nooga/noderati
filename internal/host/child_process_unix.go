//go:build !windows

package host

import (
	"os/exec"
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
