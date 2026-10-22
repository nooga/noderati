package host

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"syscall"

	"github.com/nooga/paserati/pkg/runtime"
	"github.com/nooga/paserati/pkg/vm"
)

// dgram.go is node:dgram's native half, shaped like libuv's UDPWrap: a
// handle that can bind, connect, send, receive and set socket options,
// returning libuv-style error descriptions instead of throwing. Every
// observable semantic (argument parsing, the bind/send queue, lookups,
// events) lives in dgramShim (dgram_shim.go), a port of lib/dgram.js.
//
//	handle.onmessage(buf, rinfo)  - a datagram arrived
//	handle.onerror(err)           - a receive failed (err is {code, errno})
//
// While it is receiving, open and ref'd, a handle holds one external op
// so the process stays alive, exactly like a libuv handle.

const (
	dgramFlagIPv6Only  = 1
	dgramFlagReuseAddr = 4
	dgramFlagReusePort = 8
)

type dgramHandle struct {
	vmInst *vm.VM
	rt     runtime.AsyncRuntime
	obj    *vm.PlainObject
	ipv6   bool

	mu        sync.Mutex
	cond      *sync.Cond
	conn      *net.UDPConn
	connected bool
	reading   bool
	closed    bool
	started   bool
	refed     bool
	opHeld    bool
}

func errnoInfo(e syscall.Errno, syscallName string) *netErrorInfo {
	name := errnoNames[e]
	if name == "" {
		name = "UNKNOWN"
	}
	return &netErrorInfo{code: name, errno: -int(e), syscall: syscallName}
}

func (h *dgramHandle) updateOpLocked() {
	want := h.refed && h.reading && !h.closed && h.conn != nil
	if want == h.opHeld {
		return
	}
	h.opHeld = want
	if want {
		h.rt.BeginExternalOp()
	} else {
		h.rt.EndExternalOp()
	}
}

func (h *dgramHandle) family() string {
	if h.ipv6 {
		return "IPv6"
	}
	return "IPv4"
}

// parseIP accepts the address forms libuv's uv_ip4_addr/uv_ip6_addr do,
// for the handle's family only (anything else is EINVAL).
func (h *dgramHandle) parseIP(s string) (net.IP, string, bool) {
	zone := ""
	if h.ipv6 {
		for i := 0; i < len(s); i++ {
			if s[i] == '%' {
				zone = s[i+1:]
				s = s[:i]
				break
			}
		}
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, "", false
	}
	if h.ipv6 {
		if ip.To4() != nil && !containsColon(s) {
			return nil, "", false
		}
		return ip, zone, true
	}
	if ip.To4() == nil {
		return nil, "", false
	}
	return ip.To4(), "", true
}

func containsColon(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return true
		}
	}
	return false
}

func (h *dgramHandle) bind(ipStr string, port int, flags int) *netErrorInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errnoInfo(syscall.EBADF, "bind")
	}
	if h.conn != nil {
		return errnoInfo(syscall.EINVAL, "bind")
	}
	ip, zone, ok := h.parseIP(ipStr)
	if !ok {
		return errnoInfo(syscall.EINVAL, "bind")
	}
	network := "udp4"
	if h.ipv6 {
		network = "udp6"
		if ip.IsUnspecified() && flags&dgramFlagIPv6Only == 0 {
			// Go only builds a dual-stack socket for "udp" + wildcard.
			network = "udp"
		}
	}
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		return dgramControl(c, flags)
	}}
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	if zone != "" {
		addr = net.JoinHostPort(ip.String()+"%"+zone, strconv.Itoa(port))
	}
	pc, err := lc.ListenPacket(context.Background(), network, addr)
	if err != nil {
		return netErrInfo(err, "bind")
	}
	h.conn = pc.(*net.UDPConn)
	h.updateOpLocked()
	h.startLocked()
	return nil
}

func (h *dgramHandle) rawControl(fn func(fd int) error) error {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		return syscall.EBADF
	}
	rc, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

func (h *dgramHandle) sockaddr(ipStr string, port int) (net.IP, string, *netErrorInfo) {
	ip, zone, ok := h.parseIP(ipStr)
	if !ok {
		return nil, "", errnoInfo(syscall.EINVAL, "")
	}
	return ip, zone, nil
}

func (h *dgramHandle) connect(ipStr string, port int) *netErrorInfo {
	ip, zone, info := h.sockaddr(ipStr, port)
	if info != nil {
		return info
	}
	err := h.rawControl(func(fd int) error { return dgramConnect(fd, ip, zone, port, h.ipv6) })
	if err != nil {
		return netErrInfo(err, "connect")
	}
	h.mu.Lock()
	h.connected = true
	h.mu.Unlock()
	return nil
}

func (h *dgramHandle) disconnect() *netErrorInfo {
	err := h.rawControl(dgramDisconnect)
	if err != nil {
		return netErrInfo(err, "connect")
	}
	h.mu.Lock()
	h.connected = false
	h.mu.Unlock()
	return nil
}

func (h *dgramHandle) send(data []byte, port int, ipStr string, hasAddr bool) (int, *netErrorInfo) {
	h.mu.Lock()
	conn := h.conn
	connected := h.connected
	h.mu.Unlock()
	if conn == nil {
		return 0, errnoInfo(syscall.EBADF, "send")
	}
	var n int
	var err error
	if connected || !hasAddr {
		n, err = conn.Write(data)
	} else {
		ip, zone, info := h.sockaddr(ipStr, port)
		if info != nil {
			return 0, info
		}
		n, err = conn.WriteToUDP(data, &net.UDPAddr{IP: ip, Port: port, Zone: zone})
	}
	if err != nil {
		return 0, netErrInfo(err, "send")
	}
	return n, nil
}

func (h *dgramHandle) startLocked() {
	if h.started {
		return
	}
	h.started = true
	go h.recvLoop()
}

func (h *dgramHandle) recvLoop() {
	buf := make([]byte, 64*1024)
	for {
		h.mu.Lock()
		for !h.closed && !h.reading {
			h.cond.Wait()
		}
		if h.closed {
			h.mu.Unlock()
			return
		}
		conn := h.conn
		h.mu.Unlock()

		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || h.isClosed() {
				return
			}
			info := netErrInfo(err, "recvmsg")
			h.schedule(func() {
				if !h.isClosed() {
					h.callJS("onerror", info.toValue(h.vmInst))
				}
			})
			continue
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		// Like libuv after recvStop, hold the datagram until reading resumes.
		h.mu.Lock()
		for !h.closed && !h.reading {
			h.cond.Wait()
		}
		h.mu.Unlock()
		h.schedule(func() {
			if h.isClosed() {
				return
			}
			h.callJS("onmessage", wrapBuffer(h.vmInst, data), h.addrValue(addr))
		})
	}
}

// addrValue is {address, family, port}; an IPv6 socket reports IPv4 peers
// as v4-mapped IPv6 addresses, like libuv.
func (h *dgramHandle) addrValue(a net.Addr) vm.Value {
	ua, ok := a.(*net.UDPAddr)
	if !ok || !h.ipv6 || ua.IP.To4() == nil {
		return addrObject(h.vmInst, a)
	}
	o := vm.NewObject(h.vmInst.ObjectPrototype).AsPlainObject()
	addr := "::ffff:" + ua.IP.To4().String()
	if ua.IP.IsUnspecified() {
		addr = "::"
	}
	o.SetOwn("address", vm.NewString(addr))
	o.SetOwn("family", vm.NewString("IPv6"))
	o.SetOwn("port", vm.NumberValue(float64(ua.Port)))
	return vm.NewValueFromPlainObject(o)
}

func (h *dgramHandle) isClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

func (h *dgramHandle) schedule(fn func()) {
	scheduleIO(h.rt, fn)
}

func (h *dgramHandle) callJS(name string, args ...vm.Value) {
	fn, ok := h.obj.Get(name)
	if !ok || !fn.IsCallable() {
		return
	}
	if _, err := h.vmInst.Call(fn, vm.NewValueFromPlainObject(h.obj), args); err != nil {
		reportUncaughtCallbackException(h.vmInst, err)
	}
}

func (h *dgramHandle) close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	conn := h.conn
	h.updateOpLocked()
	h.mu.Unlock()
	h.cond.Broadcast()
	if conn != nil {
		_ = conn.Close()
	}
}

func newDgramHandle(vmInst *vm.VM, ipv6 bool) *dgramHandle {
	h := &dgramHandle{vmInst: vmInst, rt: vmInst.GetAsyncRuntime(), ipv6: ipv6, refed: true}
	h.cond = sync.NewCond(&h.mu)
	h.obj = buildDgramHandleObject(h)
	return h
}

func buildDgramHandleObject(h *dgramHandle) *vm.PlainObject {
	vmInst := h.vmInst
	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	fn := func(name string, arity int, f func(args []vm.Value) (vm.Value, error)) {
		obj.SetOwn(name, vm.NewNativeFunction(arity, false, name, f))
	}
	arg := func(args []vm.Value, i int) vm.Value {
		if i < len(args) {
			return args[i]
		}
		return vm.Undefined
	}
	errOrUndef := func(info *netErrorInfo, syscallName string) vm.Value {
		if info == nil {
			return vm.Undefined
		}
		if info.syscall == "" {
			info.syscall = syscallName
		}
		return info.toValue(vmInst)
	}
	sockErr := func(err error, syscallName string) vm.Value {
		if err == nil {
			return vm.Undefined
		}
		return netErrInfo(err, syscallName).toValue(vmInst)
	}
	fn("bind", 3, func(args []vm.Value) (vm.Value, error) {
		return errOrUndef(h.bind(arg(args, 0).ToString(), int(arg(args, 1).ToFloat()), int(arg(args, 2).ToFloat())), "bind"), nil
	})
	fn("connect", 2, func(args []vm.Value) (vm.Value, error) {
		return errOrUndef(h.connect(arg(args, 0).ToString(), int(arg(args, 1).ToFloat())), "connect"), nil
	})
	fn("disconnect", 0, func(_ []vm.Value) (vm.Value, error) {
		return errOrUndef(h.disconnect(), "connect"), nil
	})
	// send(buf, port?, ip?) -> bytes+1 on (synchronous) success, else an error info.
	fn("send", 3, func(args []vm.Value) (vm.Value, error) {
		data := valueToBytes(vmInst, arg(args, 0))
		hasAddr := len(args) > 2 && !args[2].IsUndefined() && args[2].Type() != vm.TypeNull
		n, info := h.send(data, int(arg(args, 1).ToFloat()), arg(args, 2).ToString(), hasAddr)
		if info != nil {
			return errOrUndef(info, "send"), nil
		}
		return vm.NumberValue(float64(n + 1)), nil
	})
	fn("recvStart", 0, func(_ []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		h.reading = true
		h.updateOpLocked()
		h.mu.Unlock()
		h.cond.Broadcast()
		return vm.Undefined, nil
	})
	fn("recvStop", 0, func(_ []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		h.reading = false
		h.updateOpLocked()
		h.mu.Unlock()
		return vm.Undefined, nil
	})
	addrInto := func(args []vm.Value, peer bool) (vm.Value, error) {
		h.mu.Lock()
		conn := h.conn
		h.mu.Unlock()
		name := "getsockname"
		if peer {
			name = "getpeername"
		}
		if conn == nil {
			return errnoInfo(syscall.EBADF, name).toValue(vmInst), nil
		}
		var a net.Addr
		if peer {
			var err error
			a, err = dgramPeerName(conn)
			if err != nil {
				return netErrInfo(err, name).toValue(vmInst), nil
			}
		} else {
			a = conn.LocalAddr()
		}
		src := h.addrValue(a).AsPlainObject()
		if out := arg(args, 0); out.Type() == vm.TypeObject {
			o := out.AsPlainObject()
			for _, k := range []string{"address", "family", "port"} {
				if v, ok := src.GetOwn(k); ok {
					o.SetOwn(k, v)
				}
			}
		}
		return vm.Undefined, nil
	}
	fn("getsockname", 1, func(args []vm.Value) (vm.Value, error) { return addrInto(args, false) })
	fn("getpeername", 1, func(args []vm.Value) (vm.Value, error) { return addrInto(args, true) })
	intOpt := func(name string, f func(fd int, v int, ipv6 bool) error) {
		fn(name, 1, func(args []vm.Value) (vm.Value, error) {
			v := int(arg(args, 0).ToFloat())
			return sockErr(h.rawControl(func(fd int) error { return f(fd, v, h.ipv6) }), name), nil
		})
	}
	intOpt("setBroadcast", dgramSetBroadcast)
	intOpt("setTTL", func(fd, v int, ipv6 bool) error {
		if v < 1 || v > 255 {
			return syscall.EINVAL
		}
		return dgramSetTTL(fd, v, ipv6)
	})
	intOpt("setMulticastTTL", func(fd, v int, ipv6 bool) error {
		if v < 0 || v > 255 {
			return syscall.EINVAL
		}
		return dgramSetMulticastTTL(fd, v, ipv6)
	})
	intOpt("setMulticastLoopback", dgramSetMulticastLoop)
	fn("setMulticastInterface", 1, func(args []vm.Value) (vm.Value, error) {
		s := arg(args, 0).ToString()
		return sockErr(h.rawControl(func(fd int) error { return dgramSetMulticastIF(fd, s, h.ipv6) }), "setMulticastInterface"), nil
	})
	member := func(name string, join bool) {
		fn(name, 2, func(args []vm.Value) (vm.Value, error) {
			group := arg(args, 0).ToString()
			iface := ""
			if v := arg(args, 1); !v.IsUndefined() && v.Type() != vm.TypeNull {
				iface = v.ToString()
			}
			return sockErr(h.rawControl(func(fd int) error { return dgramMembership(fd, group, iface, h.ipv6, join) }), name), nil
		})
	}
	member("addMembership", true)
	member("dropMembership", false)
	ssm := func(name string, join bool) {
		fn(name, 3, func(args []vm.Value) (vm.Value, error) {
			src, group := arg(args, 0).ToString(), arg(args, 1).ToString()
			iface := ""
			if v := arg(args, 2); !v.IsUndefined() && v.Type() != vm.TypeNull {
				iface = v.ToString()
			}
			return sockErr(h.rawControl(func(fd int) error { return dgramSourceMembership(fd, src, group, iface, h.ipv6, join) }), name), nil
		})
	}
	ssm("addSourceSpecificMembership", true)
	ssm("dropSourceSpecificMembership", false)
	// bufferSize(size, recv) -> the (new) size, or an error info.
	fn("bufferSize", 2, func(args []vm.Value) (vm.Value, error) {
		size := int(arg(args, 0).ToFloat())
		recv := arg(args, 1).IsTruthy()
		var out int
		err := h.rawControl(func(fd int) error {
			var e error
			out, e = dgramBufferSize(fd, size, recv)
			return e
		})
		if err != nil {
			return netErrInfo(err, "").toValue(vmInst), nil
		}
		return vm.NumberValue(float64(out)), nil
	})
	fn("close", 0, func(_ []vm.Value) (vm.Value, error) {
		h.close()
		return vm.Undefined, nil
	})
	fn("ref", 0, func(_ []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		h.refed = true
		h.updateOpLocked()
		h.mu.Unlock()
		return vm.Undefined, nil
	})
	fn("unref", 0, func(_ []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		h.refed = false
		h.updateOpLocked()
		h.mu.Unlock()
		return vm.Undefined, nil
	})
	fn("hasRef", 0, func(_ []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		return vm.BooleanValue(h.refed), nil
	})
	return obj
}

func dgramPeerName(conn *net.UDPConn) (net.Addr, error) {
	if ra := conn.RemoteAddr(); ra != nil {
		return ra, nil
	}
	rc, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	var a net.Addr
	var ferr error
	if err := rc.Control(func(fd uintptr) { a, ferr = dgramGetpeername(int(fd)) }); err != nil {
		return nil, err
	}
	return a, ferr
}

func declareDgram() {
	registerJSShim("dgram", dgramShim)
}

func installDgramBinding(vmInst *vm.VM, g *vm.PlainObject) {
	b := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	b.SetOwn("newHandle", vm.NewNativeFunction(1, false, "newHandle", func(args []vm.Value) (vm.Value, error) {
		ipv6 := len(args) > 0 && args[0].ToString() == "udp6"
		return vm.NewValueFromPlainObject(newDgramHandle(vmInst, ipv6).obj), nil
	}))
	g.SetOwn("__noderatiDgramBinding", vm.NewValueFromPlainObject(b))
}
