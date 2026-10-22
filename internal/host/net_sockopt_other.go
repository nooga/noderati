//go:build !unix

package host

import "syscall"

func setReusePort(_, _ string, _ syscall.RawConn) error { return nil }
