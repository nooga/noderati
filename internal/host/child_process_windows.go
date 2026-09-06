//go:build windows

package host

import "os/exec"

// setDetached is a no-op on Windows: there's no POSIX process-group concept
// for negative-pid signaling to target, and Node's own "detached" option
// means something different there too (CREATE_NEW_PROCESS_GROUP, used for
// Ctrl+Break rather than SIGTERM-style signals) - out of scope for the
// gap this file's unix counterpart closes.
func setDetached(cmd *exec.Cmd) {}
