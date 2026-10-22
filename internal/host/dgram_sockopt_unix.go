//go:build unix

package host

import (
	"net"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// dgramControl runs before bind(2): node's reuseAddr/reusePort flags, and
// SO_BROADCAST back off (Go turns it on for every datagram socket, libuv
// leaves it to setBroadcast()).
func dgramControl(c syscall.RawConn, flags int) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		f := int(fd)
		if serr = unix.SetsockoptInt(f, unix.SOL_SOCKET, unix.SO_BROADCAST, 0); serr != nil {
			return
		}
		if flags&dgramFlagReuseAddr != 0 {
			if serr = unix.SetsockoptInt(f, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); serr != nil {
				return
			}
			// libuv: on the BSDs SO_REUSEADDR alone doesn't let two UDP
			// sockets share a port, SO_REUSEPORT does.
			if runtime.GOOS != "linux" {
				if serr = unix.SetsockoptInt(f, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); serr != nil {
					return
				}
			}
		}
		if flags&dgramFlagReusePort != 0 {
			serr = unix.SetsockoptInt(f, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
	})
	if err != nil {
		return err
	}
	return serr
}

func zoneIndex(zone string) uint32 {
	if zone == "" {
		return 0
	}
	if n, err := strconv.Atoi(zone); err == nil {
		return uint32(n)
	}
	if ifi, err := net.InterfaceByName(zone); err == nil {
		return uint32(ifi.Index)
	}
	return 0
}

func dgramConnect(fd int, ip net.IP, zone string, port int, ipv6 bool) error {
	if ipv6 {
		sa := &unix.SockaddrInet6{Port: port, ZoneId: zoneIndex(zone)}
		copy(sa.Addr[:], ip.To16())
		return unix.Connect(fd, sa)
	}
	sa := &unix.SockaddrInet4{Port: port}
	copy(sa.Addr[:], ip.To4())
	return unix.Connect(fd, sa)
}

// dgramDisconnect connects to an AF_UNSPEC address, which dissolves a UDP
// association (libuv's uv__udp_disconnect).
func dgramDisconnect(fd int) error {
	var sa [28]byte // sizeof(struct sockaddr_in6), all zero = AF_UNSPEC
	if runtime.GOOS != "linux" {
		sa[0] = byte(len(sa)) // sa_len on the BSDs
	}
	_, _, errno := syscall.Syscall(syscall.SYS_CONNECT, uintptr(fd), uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa)))
	if errno != 0 && errno != syscall.EAFNOSUPPORT && !(runtime.GOOS != "linux" && errno == syscall.EINVAL) {
		return errno
	}
	return nil
}

func dgramGetpeername(fd int) (net.Addr, error) {
	sa, err := unix.Getpeername(fd)
	if err != nil {
		return nil, err
	}
	switch a := sa.(type) {
	case *unix.SockaddrInet4:
		return &net.UDPAddr{IP: net.IP(a.Addr[:]).To16(), Port: a.Port}, nil
	case *unix.SockaddrInet6:
		return &net.UDPAddr{IP: net.IP(a.Addr[:]), Port: a.Port}, nil
	}
	return nil, syscall.EAFNOSUPPORT
}

func boolInt(v int) int {
	if v != 0 {
		return 1
	}
	return 0
}

func dgramSetBroadcast(fd, v int, _ bool) error {
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BROADCAST, boolInt(v))
}

func dgramSetTTL(fd, v int, ipv6 bool) error {
	if ipv6 {
		return unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, v)
	}
	return unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_TTL, v)
}

func dgramSetMulticastTTL(fd, v int, ipv6 bool) error {
	if ipv6 {
		return unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_HOPS, v)
	}
	return unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_MULTICAST_TTL, v)
}

func dgramSetMulticastLoop(fd, v int, ipv6 bool) error {
	if ipv6 {
		return unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_LOOP, boolInt(v))
	}
	return unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_MULTICAST_LOOP, boolInt(v))
}

func splitZone(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '%' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

func dgramSetMulticastIF(fd int, s string, ipv6 bool) error {
	if ipv6 {
		host, zone := splitZone(s)
		if ip := net.ParseIP(host); ip == nil || ip.To4() != nil {
			return syscall.EINVAL
		}
		return unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_IF, int(zoneIndex(zone)))
	}
	ip := net.ParseIP(s).To4()
	if ip == nil {
		return syscall.EINVAL
	}
	var a [4]byte
	copy(a[:], ip)
	return unix.SetsockoptInet4Addr(fd, unix.IPPROTO_IP, unix.IP_MULTICAST_IF, a)
}

func dgramMembership(fd int, group, iface string, _ bool, join bool) error {
	if g4 := net.ParseIP(group).To4(); g4 != nil && !containsColon(group) {
		mreq := &unix.IPMreq{}
		copy(mreq.Multiaddr[:], g4)
		if iface != "" {
			i4 := net.ParseIP(iface).To4()
			if i4 == nil {
				return syscall.EINVAL
			}
			copy(mreq.Interface[:], i4)
		}
		opt := unix.IP_ADD_MEMBERSHIP
		if !join {
			opt = unix.IP_DROP_MEMBERSHIP
		}
		return unix.SetsockoptIPMreq(fd, unix.IPPROTO_IP, opt, mreq)
	}
	host, zone := splitZone(group)
	g6 := net.ParseIP(host)
	if g6 == nil {
		return syscall.EINVAL
	}
	mreq := &unix.IPv6Mreq{}
	copy(mreq.Multiaddr[:], g6.To16())
	if iface != "" {
		_, izone := splitZone(iface)
		mreq.Interface = zoneIndex(izone)
	} else {
		mreq.Interface = zoneIndex(zone)
	}
	opt := unix.IPV6_JOIN_GROUP
	if !join {
		opt = unix.IPV6_LEAVE_GROUP
	}
	return unix.SetsockoptIPv6Mreq(fd, unix.IPPROTO_IPV6, opt, mreq)
}

func dgramSourceMembership(fd int, src, group, iface string, _ bool, join bool) error {
	s4, g4 := net.ParseIP(src).To4(), net.ParseIP(group).To4()
	if s4 == nil || g4 == nil {
		// IPv6 source-specific multicast (MCAST_JOIN_SOURCE_GROUP) isn't wired up.
		if net.ParseIP(src) == nil || net.ParseIP(group) == nil {
			return syscall.EINVAL
		}
		return syscall.ENOSYS
	}
	var i4 net.IP = net.IPv4zero.To4()
	if iface != "" {
		if i4 = net.ParseIP(iface).To4(); i4 == nil {
			return syscall.EINVAL
		}
	}
	// struct ip_mreq_source: Linux orders it multiaddr, interface, source;
	// the BSDs multiaddr, source, interface.
	var mreq [12]byte
	copy(mreq[0:4], g4)
	if runtime.GOOS == "linux" {
		copy(mreq[4:8], i4)
		copy(mreq[8:12], s4)
	} else {
		copy(mreq[4:8], s4)
		copy(mreq[8:12], i4)
	}
	opt := unix.IP_ADD_SOURCE_MEMBERSHIP
	if !join {
		opt = unix.IP_DROP_SOURCE_MEMBERSHIP
	}
	return unix.SetsockoptString(fd, unix.IPPROTO_IP, opt, string(mreq[:]))
}

func dgramBufferSize(fd, size int, recv bool) (int, error) {
	opt := unix.SO_SNDBUF
	if recv {
		opt = unix.SO_RCVBUF
	}
	if size == 0 {
		return unix.GetsockoptInt(fd, unix.SOL_SOCKET, opt)
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, opt, size); err != nil {
		return 0, err
	}
	return size, nil
}
