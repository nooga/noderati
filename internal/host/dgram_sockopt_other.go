//go:build !unix

package host

import (
	"net"
	"syscall"
)

func dgramControl(_ syscall.RawConn, _ int) error { return nil }

func dgramConnect(int, net.IP, string, int, bool) error     { return syscall.ENOSYS }
func dgramDisconnect(int) error                             { return syscall.ENOSYS }
func dgramGetpeername(int) (net.Addr, error)                { return nil, syscall.ENOSYS }
func dgramSetBroadcast(int, int, bool) error                { return syscall.ENOSYS }
func dgramSetTTL(int, int, bool) error                      { return syscall.ENOSYS }
func dgramSetMulticastTTL(int, int, bool) error             { return syscall.ENOSYS }
func dgramSetMulticastLoop(int, int, bool) error            { return syscall.ENOSYS }
func dgramSetMulticastIF(int, string, bool) error           { return syscall.ENOSYS }
func dgramMembership(int, string, string, bool, bool) error { return syscall.ENOSYS }
func dgramSourceMembership(int, string, string, string, bool, bool) error {
	return syscall.ENOSYS
}
func dgramBufferSize(int, int, bool) (int, error) { return 0, syscall.ENOSYS }
