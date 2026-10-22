package host

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/runtime"
	"github.com/nooga/paserati/pkg/vm"
)

// net.go is the native half of node:net, shaped like Node's own split
// between lib/net.js (JS: Socket/Server as real Duplex/EventEmitter
// classes, argument normalization, DNS lookup, autoSelectFamily, timeouts,
// half-close bookkeeping) and libuv's TCPWrap/PipeWrap (native: a "handle"
// that can connect, read, write, shutdown and close, and nothing else).
// netShim (net_shim.go) is the JS half and owns every observable
// semantic; this file only moves bytes between a Go net.Conn and the
// handle's JS callbacks:
//
//	handle.onread(buf)       - a chunk arrived (Buffer)
//	handle.onread(null)      - EOF
//	handle.onread(undefined, err) - read failed (err is {code, errno, syscall})
//
// Every callback into JS is scheduled as a macrotask (the I/O phase) from
// the goroutine that observed the event, never called directly off the VM
// thread. A handle keeps the process alive (one external op) while it is
// open and ref'd, exactly like a libuv handle; unref() drops that.
//
// tls.go reuses this handle wholesale: startTLS swaps the live conn for a
// *tls.Conn in place (client or server side), which is how both
// tls.connect() and tls.Server's accepted sockets are built.

const defaultNetHighWaterMark = 64 * 1024

type netWriteReq struct {
	data     []byte
	shutdown bool
	cb       vm.Value
}

type netHandle struct {
	vmInst *vm.VM
	rt     runtime.AsyncRuntime
	obj    *vm.PlainObject

	mu   sync.Mutex
	cond *sync.Cond

	conn     net.Conn
	ready    bool // conn usable (false while dialing or during a TLS handshake)
	reading  bool
	inRead   bool
	closed   bool
	started  bool // reader/writer goroutines launched
	writeQ   []netWriteReq
	prefix   []byte // bytes read off the raw conn just before a TLS upgrade
	tlsState *tlsHandleState

	refed  bool
	active bool
	opHeld bool

	bytesRead    atomic.Int64
	bytesWritten atomic.Int64
}

var netHandleRegistry sync.Map // *vm.PlainObject -> *netHandle

func newNetHandle(vmInst *vm.VM) *netHandle {
	h := &netHandle{vmInst: vmInst, rt: vmInst.GetAsyncRuntime(), refed: true}
	h.cond = sync.NewCond(&h.mu)
	h.obj = buildNetHandleObject(h)
	netHandleRegistry.Store(h.obj, h)
	return h
}

func netHandleFromValue(v vm.Value) *netHandle {
	if v.Type() != vm.TypeObject {
		return nil
	}
	if h, ok := netHandleRegistry.Load(v.AsPlainObject()); ok {
		return h.(*netHandle)
	}
	return nil
}

// updateOpLocked keeps exactly one external op registered while the
// handle is active (dialing or open), not closed, and ref'd.
func (h *netHandle) updateOpLocked() {
	want := h.refed && h.active && !h.closed
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

// schedule runs fn on the VM thread in the I/O (macrotask) phase.
func (h *netHandle) schedule(fn func()) {
	scheduleIO(h.rt, fn)
}

// scheduleIO queues fn as a macrotask from any goroutine.
//
// Works around a lost wakeup in paserati's DefaultAsyncRuntime:
// DrainUntilIdle checks the macrotask queue, then calls
// WaitForExternalOp, which waits on the idle cond without re-checking the
// queues. A macrotask scheduled from another goroutine in between those
// two steps broadcasts before anyone waits, and the loop then sleeps
// until some unrelated event - a live socket can stall indefinitely. A
// per-runtime waker goroutine re-signals the cond (a Begin/EndExternalOp
// pulse, which broadcasts) shortly after each burst of scheduling.
func scheduleIO(rt runtime.AsyncRuntime, fn func()) {
	rt.ScheduleMacrotask(fn)
	kickWaker(rt)
}

var ioWakers sync.Map // runtime.AsyncRuntime -> chan struct{}

func kickWaker(rt runtime.AsyncRuntime) {
	chv, ok := ioWakers.Load(rt)
	if !ok {
		ch := make(chan struct{}, 1)
		var loaded bool
		chv, loaded = ioWakers.LoadOrStore(rt, ch)
		if !loaded {
			go func() {
				for range ch {
					time.Sleep(time.Millisecond)
					rt.BeginExternalOp()
					rt.EndExternalOp()
				}
			}()
		}
	}
	select {
	case chv.(chan struct{}) <- struct{}{}:
	default:
	}
}

func (h *netHandle) callJS(name string, args ...vm.Value) {
	fn, ok := h.obj.Get(name)
	if !ok || !fn.IsCallable() {
		return
	}
	if _, err := h.vmInst.Call(fn, vm.NewValueFromPlainObject(h.obj), args); err != nil {
		reportUncaughtCallbackException(h.vmInst, err)
	}
}

func callJSCallback(vmInst *vm.VM, cb vm.Value, args ...vm.Value) {
	if !cb.IsCallable() {
		return
	}
	if _, err := vmInst.Call(cb, vm.Undefined, args); err != nil {
		reportUncaughtCallbackException(vmInst, err)
	}
}

// attach installs an already-connected conn (accept, or a finished dial).
func (h *netHandle) attach(conn net.Conn) {
	h.mu.Lock()
	h.conn = conn
	h.ready = true
	h.active = true
	h.updateOpLocked()
	h.startLoopsLocked()
	h.mu.Unlock()
	h.cond.Broadcast()
}

func (h *netHandle) startLoopsLocked() {
	if h.started {
		return
	}
	h.started = true
	go h.readLoop()
	go h.writeLoop()
}

func (h *netHandle) readLoop() {
	buf := make([]byte, 64*1024)
	for {
		h.mu.Lock()
		for !h.closed && (!h.reading || !h.ready) {
			h.cond.Wait()
		}
		if h.closed {
			h.mu.Unlock()
			return
		}
		conn := h.conn
		h.inRead = true
		h.mu.Unlock()

		n, err := conn.Read(buf)

		h.mu.Lock()
		h.inRead = false
		closed := h.closed
		upgrading := !h.ready
		if upgrading && n > 0 {
			h.prefix = append(h.prefix, buf[:n]...)
		}
		h.mu.Unlock()
		h.cond.Broadcast()
		if closed {
			return
		}
		if upgrading {
			// startTLS interrupted this Read (via a past read deadline)
			// to take the raw conn over; go back to waiting.
			continue
		}
		if n > 0 {
			h.bytesRead.Add(int64(n))
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			h.schedule(func() {
				if h.isClosed() {
					return
				}
				h.callJS("onread", wrapBuffer(h.vmInst, chunk))
			})
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				h.schedule(func() {
					if !h.isClosed() {
						h.callJS("onread", vm.Null)
					}
				})
			} else {
				info := netErrInfo(err, "read")
				h.schedule(func() {
					if !h.isClosed() {
						h.callJS("onread", vm.Undefined, info.toValue(h.vmInst))
					}
				})
			}
			return
		}
	}
}

func (h *netHandle) isClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

func (h *netHandle) writeLoop() {
	for {
		h.mu.Lock()
		for !h.closed && (len(h.writeQ) == 0 || !h.ready) {
			h.cond.Wait()
		}
		if h.closed {
			pending := h.writeQ
			h.writeQ = nil
			h.mu.Unlock()
			for _, req := range pending {
				if req.cb.IsCallable() {
					cb := req.cb
					h.schedule(func() { callJSCallback(h.vmInst, cb, vm.Null) })
				}
			}
			return
		}
		req := h.writeQ[0]
		h.writeQ = h.writeQ[1:]
		conn := h.conn
		h.mu.Unlock()

		var err error
		if len(req.data) > 0 {
			var n int
			n, err = conn.Write(req.data)
			h.bytesWritten.Add(int64(n))
		}
		if err == nil && req.shutdown {
			if cw, ok := conn.(interface{ CloseWrite() error }); ok {
				err = cw.CloseWrite()
			}
		}
		if req.cb.IsCallable() {
			cb := req.cb
			var info *netErrorInfo
			if err != nil && !h.isClosed() {
				syscallName := "write"
				if req.shutdown && len(req.data) == 0 {
					syscallName = "shutdown"
				}
				info = netErrInfo(err, syscallName)
			}
			h.schedule(func() {
				if info != nil {
					callJSCallback(h.vmInst, cb, info.toValue(h.vmInst))
				} else {
					callJSCallback(h.vmInst, cb, vm.Null)
				}
			})
		}
	}
}

func (h *netHandle) enqueue(req netWriteReq) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		if req.cb.IsCallable() {
			cb := req.cb
			h.schedule(func() { callJSCallback(h.vmInst, cb, vm.Null) })
		}
		return
	}
	h.writeQ = append(h.writeQ, req)
	h.mu.Unlock()
	h.cond.Broadcast()
}

func (h *netHandle) close(cb vm.Value) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		if cb.IsCallable() {
			h.schedule(func() { callJSCallback(h.vmInst, cb) })
		}
		return
	}
	h.closed = true
	conn := h.conn
	h.mu.Unlock()
	h.cond.Broadcast()
	if conn != nil {
		_ = conn.Close()
	}
	if cb.IsCallable() {
		h.schedule(func() { callJSCallback(h.vmInst, cb) })
	}
	h.mu.Lock()
	h.updateOpLocked()
	h.mu.Unlock()
	netHandleRegistry.Delete(h.obj)
}

// connect dials asynchronously; cb(err|null) runs on the VM thread.
func (h *netHandle) connect(network, address, localAddr string, localPort int, timeout time.Duration, cb vm.Value) {
	h.mu.Lock()
	h.active = true
	h.updateOpLocked()
	h.mu.Unlock()
	go func() {
		d := net.Dialer{Timeout: timeout}
		if network == "tcp" && (localAddr != "" || localPort != 0) {
			d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(localAddr), Port: localPort}
		}
		conn, err := d.Dial(network, address)
		if err != nil {
			info := netErrInfo(err, "connect")
			h.schedule(func() {
				if h.isClosed() {
					return
				}
				callJSCallback(h.vmInst, cb, info.toValue(h.vmInst))
			})
			return
		}
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			_ = conn.Close()
			return
		}
		h.mu.Unlock()
		h.attach(conn)
		h.schedule(func() {
			if h.isClosed() {
				return
			}
			callJSCallback(h.vmInst, cb, vm.Null)
		})
	}()
}

// netErrorInfo is a libuv-shaped error description; the JS side turns it
// into a real Error via errnoException/exceptionWithHostPort.
type netErrorInfo struct {
	code    string
	errno   int
	syscall string
	message string
}

func (e *netErrorInfo) toValue(vmInst *vm.VM) vm.Value {
	o := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	o.SetOwn("code", vm.NewString(e.code))
	o.SetOwn("errno", vm.NumberValue(float64(e.errno)))
	o.SetOwn("syscall", vm.NewString(e.syscall))
	if e.message != "" {
		o.SetOwn("message", vm.NewString(e.message))
	}
	return vm.NewValueFromPlainObject(o)
}

var errnoNames = func() map[syscall.Errno]string {
	m := map[syscall.Errno]string{}
	for name, e := range sysErrno {
		if name == "EWOULDBLOCK" || name == "ENOTSUP" {
			if _, dup := m[e]; dup {
				continue
			}
		}
		m[e] = name
	}
	return m
}()

func netErrInfo(err error, syscallName string) *netErrorInfo {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		name := errnoNames[errno]
		if name == "" {
			name = "UNKNOWN"
		}
		return &netErrorInfo{code: name, errno: -int(errno), syscall: syscallName}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &netErrorInfo{code: "ENOTFOUND", errno: -3008, syscall: "getaddrinfo"}
	}
	if errors.Is(err, os.ErrDeadlineExceeded) || isTimeoutErr(err) {
		return &netErrorInfo{code: "ETIMEDOUT", errno: -int(syscall.ETIMEDOUT), syscall: syscallName}
	}
	if errors.Is(err, net.ErrClosed) {
		return &netErrorInfo{code: "EBADF", errno: -int(syscall.EBADF), syscall: syscallName}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return &netErrorInfo{code: "ECONNRESET", errno: -int(syscall.ECONNRESET), syscall: syscallName}
	}
	return &netErrorInfo{code: "UNKNOWN", errno: -4094, syscall: syscallName, message: err.Error()}
}

func isTimeoutErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func sockAddrValue(vmInst *vm.VM, addr net.Addr) vm.Value {
	if addr == nil {
		return vm.Undefined
	}
	if ua, ok := addr.(*net.UnixAddr); ok {
		o := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		o.SetOwn("path", vm.NewString(ua.Name))
		return vm.NewValueFromPlainObject(o)
	}
	return addrObject(vmInst, addr)
}

func buildNetHandleObject(h *netHandle) *vm.PlainObject {
	vmInst := h.vmInst
	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	fn := func(name string, arity int, f func(args []vm.Value) (vm.Value, error)) {
		obj.SetOwn(name, vm.NewNativeFunction(arity, true, name, f))
	}
	arg := func(args []vm.Value, i int) vm.Value {
		if i < len(args) {
			return args[i]
		}
		return vm.Undefined
	}
	fn("readStart", 0, func(_ []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		h.reading = true
		h.mu.Unlock()
		h.cond.Broadcast()
		return vm.Undefined, nil
	})
	fn("readStop", 0, func(_ []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		h.reading = false
		h.mu.Unlock()
		return vm.Undefined, nil
	})
	fn("write", 2, func(args []vm.Value) (vm.Value, error) {
		data := append([]byte(nil), valueToBytes(vmInst, arg(args, 0))...)
		h.enqueue(netWriteReq{data: data, cb: arg(args, 1)})
		return vm.Undefined, nil
	})
	fn("shutdown", 1, func(args []vm.Value) (vm.Value, error) {
		h.enqueue(netWriteReq{shutdown: true, cb: arg(args, 0)})
		return vm.Undefined, nil
	})
	fn("close", 1, func(args []vm.Value) (vm.Value, error) {
		h.close(arg(args, 0))
		return vm.Undefined, nil
	})
	// reset closes with SO_LINGER 0 so the peer sees ECONNRESET.
	fn("reset", 1, func(args []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		conn := h.conn
		h.mu.Unlock()
		if tc, ok := rawConnOf(conn).(*net.TCPConn); ok {
			_ = tc.SetLinger(0)
		}
		h.close(arg(args, 0))
		return vm.Undefined, nil
	})
	fn("connect", 5, func(args []vm.Value) (vm.Value, error) {
		opts := arg(args, 0)
		cb := arg(args, 1)
		var o *vm.PlainObject
		if opts.Type() == vm.TypeObject {
			o = opts.AsPlainObject()
		}
		timeout := time.Duration(getIntOpt(o, "timeout", 0)) * time.Millisecond
		if path := getStrOpt(o, "path", ""); path != "" {
			h.connect("unix", path, "", 0, timeout, cb)
			return vm.Undefined, nil
		}
		address := getStrOpt(o, "address", "127.0.0.1")
		port := getIntOpt(o, "port", 0)
		h.connect("tcp", net.JoinHostPort(address, strconv.Itoa(port)), getStrOpt(o, "localAddress", ""), getIntOpt(o, "localPort", 0), timeout, cb)
		return vm.Undefined, nil
	})
	fn("setNoDelay", 1, func(args []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		conn := h.conn
		h.mu.Unlock()
		if tc, ok := rawConnOf(conn).(*net.TCPConn); ok {
			_ = tc.SetNoDelay(arg(args, 0).IsTruthy())
		}
		return vm.Undefined, nil
	})
	fn("setKeepAlive", 2, func(args []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		conn := h.conn
		h.mu.Unlock()
		if tc, ok := rawConnOf(conn).(*net.TCPConn); ok {
			on := arg(args, 0).IsTruthy()
			_ = tc.SetKeepAlive(on)
			if d := arg(args, 1); on && d.IsNumber() && d.ToFloat() > 0 {
				_ = tc.SetKeepAlivePeriod(time.Duration(d.ToFloat()) * time.Second)
			}
		}
		return vm.Undefined, nil
	})
	fn("getsockname", 0, func(_ []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		conn := h.conn
		h.mu.Unlock()
		if conn == nil {
			return vm.Undefined, nil
		}
		return sockAddrValue(vmInst, conn.LocalAddr()), nil
	})
	fn("getpeername", 0, func(_ []vm.Value) (vm.Value, error) {
		h.mu.Lock()
		conn := h.conn
		h.mu.Unlock()
		if conn == nil {
			return vm.Undefined, nil
		}
		return sockAddrValue(vmInst, conn.RemoteAddr()), nil
	})
	fn("bytesRead", 0, func(_ []vm.Value) (vm.Value, error) {
		return vm.NumberValue(float64(h.bytesRead.Load())), nil
	})
	fn("bytesWritten", 0, func(_ []vm.Value) (vm.Value, error) {
		return vm.NumberValue(float64(h.bytesWritten.Load())), nil
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
	installTLSHandleMethods(h, obj, fn, arg)
	return obj
}

// rawConnOf unwraps a *tls.Conn to the TCP/Unix conn underneath it, so
// socket options still reach the real OS socket.
func rawConnOf(conn net.Conn) net.Conn {
	if tc, ok := conn.(*tls.Conn); ok {
		return tc.NetConn()
	}
	if pc, ok := conn.(*prefixConn); ok {
		return rawConnOf(pc.Conn)
	}
	return conn
}

// prefixConn replays bytes that were already read off the raw conn before
// a TLS upgrade took it over.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

func (c *prefixConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// ---- server handle ----

type netServerHandle struct {
	vmInst *vm.VM
	rt     runtime.AsyncRuntime
	obj    *vm.PlainObject

	mu      sync.Mutex
	ln      net.Listener
	closed  bool
	refed   bool
	opHeld  bool
	started bool
}

func (s *netServerHandle) updateOpLocked() {
	want := s.refed && !s.closed
	if want == s.opHeld {
		return
	}
	s.opHeld = want
	if want {
		s.rt.BeginExternalOp()
	} else {
		s.rt.EndExternalOp()
	}
}

func (s *netServerHandle) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return
			}
			if isTimeoutErr(err) {
				continue
			}
			info := netErrInfo(err, "accept")
			scheduleIO(s.rt, func() {
				if fn, ok := s.obj.Get("onerror"); ok && fn.IsCallable() {
					callJSCallback(s.vmInst, fn, info.toValue(s.vmInst))
				}
			})
			time.Sleep(10 * time.Millisecond)
			continue
		}
		scheduleIO(s.rt, func() {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				_ = conn.Close()
				return
			}
			h := newNetHandle(s.vmInst)
			h.attach(conn)
			fn, ok := s.obj.Get("onconnection")
			if !ok || !fn.IsCallable() {
				h.close(vm.Undefined)
				return
			}
			if _, err := s.vmInst.Call(fn, vm.NewValueFromPlainObject(s.obj), []vm.Value{vm.NewValueFromPlainObject(h.obj)}); err != nil {
				reportUncaughtCallbackException(s.vmInst, err)
			}
		})
	}
}

// netListen binds synchronously (like uv_tcp_bind+uv_listen) and returns
// either a server handle or an error description.
func netListen(vmInst *vm.VM, o *vm.PlainObject) (vm.Value, *netErrorInfo) {
	var ln net.Listener
	var err error
	if path := getStrOpt(o, "path", ""); path != "" {
		ln, err = net.Listen("unix", path)
	} else {
		host := getStrOpt(o, "address", "")
		network := "tcp"
		if getBoolOpt(o, "ipv6Only", false) {
			network = "tcp6"
		} else if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
			network = "tcp4"
		}
		ln, err = listenTCP(network, net.JoinHostPort(host, strconv.Itoa(getIntOpt(o, "port", 0))), getBoolOpt(o, "reusePort", false))
	}
	if err != nil {
		info := netErrInfo(err, "listen")
		if info.code == "UNKNOWN" && strings.Contains(err.Error(), "address already in use") {
			info = &netErrorInfo{code: "EADDRINUSE", errno: -int(syscall.EADDRINUSE), syscall: "listen"}
		}
		return vm.Undefined, info
	}
	s := &netServerHandle{vmInst: vmInst, rt: vmInst.GetAsyncRuntime(), ln: ln, refed: true}
	s.mu.Lock()
	s.updateOpLocked()
	s.mu.Unlock()
	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	s.obj = obj
	fn := func(name string, arity int, f func(args []vm.Value) (vm.Value, error)) {
		obj.SetOwn(name, vm.NewNativeFunction(arity, true, name, f))
	}
	fn("start", 0, func(_ []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		if !s.started && !s.closed {
			s.started = true
			go s.acceptLoop()
		}
		s.mu.Unlock()
		return vm.Undefined, nil
	})
	fn("close", 1, func(args []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		was := s.closed
		s.closed = true
		s.mu.Unlock()
		if !was {
			_ = s.ln.Close()
		}
		if len(args) > 0 && args[0].IsCallable() {
			cb := args[0]
			scheduleIO(s.rt, func() { callJSCallback(vmInst, cb) })
		}
		s.mu.Lock()
		s.updateOpLocked()
		s.mu.Unlock()
		return vm.Undefined, nil
	})
	fn("getsockname", 0, func(_ []vm.Value) (vm.Value, error) {
		return sockAddrValue(vmInst, s.ln.Addr()), nil
	})
	fn("ref", 0, func(_ []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		s.refed = true
		s.updateOpLocked()
		s.mu.Unlock()
		return vm.Undefined, nil
	})
	fn("unref", 0, func(_ []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		s.refed = false
		s.updateOpLocked()
		s.mu.Unlock()
		return vm.Undefined, nil
	})
	fn("hasRef", 0, func(_ []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		return vm.BooleanValue(s.refed), nil
	})
	return vm.NewValueFromPlainObject(obj), nil
}

func listenTCP(network, address string, reusePort bool) (net.Listener, error) {
	lc := net.ListenConfig{}
	if reusePort {
		lc.Control = setReusePort
	}
	return lc.Listen(context.Background(), network, address)
}

// ---- shared helpers (also used by http/fs/zlib/child_process) ----

// valueToBytes accepts either a JS string or a Buffer/TypedArray and
// returns its bytes (a TypedArray's real backing bytes, no string round
// trip).
func valueToBytes(vmInst *vm.VM, v vm.Value) []byte {
	if ta := v.AsTypedArray(); ta != nil {
		if b := typedArrayBytes(ta); b != nil {
			return b
		}
		return nil
	}
	return []byte(v.ToString())
}

// valueToBytesWithEncoding is valueToBytes plus an explicit string
// encoding for the string branch (Writable.write(chunk, encoding)).
func valueToBytesWithEncoding(vmInst *vm.VM, v vm.Value, encoding string) []byte {
	if ta := v.AsTypedArray(); ta != nil {
		if b := typedArrayBytes(ta); b != nil {
			return b
		}
		return nil
	}
	if encoding == "" || normalizeBufferEncoding(encoding) == "utf8" {
		return []byte(v.ToString())
	}
	if b, err := decodeBufferString(v.ToString(), encoding); err == nil {
		return b
	}
	return []byte(v.ToString())
}

// parseWriteEncodingAndCallback pulls the optional encoding/callback pair
// out of a `write(chunk, [encoding], [callback])` argument list, starting
// at index `from`.
func parseWriteEncodingAndCallback(args []vm.Value, from int) (encoding string, cb vm.Value) {
	cb = vm.Undefined
	if len(args) > from {
		if args[from].IsCallable() {
			cb = args[from]
		} else if args[from].IsString() {
			encoding = args[from].ToString()
		}
	}
	if len(args) > from+1 && args[from+1].IsCallable() {
		cb = args[from+1]
	}
	return encoding, cb
}

// scheduleErrorEmit schedules an 'error' emit whose Error value is built
// only once the tick runs on the VM's own thread (constructing an Error
// off-thread races the VM).
func scheduleErrorEmit(vmInst *vm.VM, rt runtime.AsyncRuntime, obj *vm.PlainObject, goErr error) {
	rt.ScheduleNextTick(func() {
		emitOnObject(vmInst, obj, "error", errorValueFromGo(vmInst, goErr))
	})
}

func addrObject(vmInst *vm.VM, addr net.Addr) vm.Value {
	out := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	if addr == nil {
		return vm.NewValueFromPlainObject(out)
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	family := "IPv4"
	if err == nil && strings.Contains(host, ":") {
		family = "IPv6"
	}
	if i := strings.IndexByte(host, '%'); i >= 0 && family == "IPv6" {
		host = host[:i]
	}
	out.SetOwn("address", vm.NewString(host))
	out.SetOwn("family", vm.NewString(family))
	if port, perr := strconv.Atoi(portStr); perr == nil {
		out.SetOwn("port", vm.NumberValue(float64(port)))
	}
	return vm.NewValueFromPlainObject(out)
}

func getStrOpt(opts *vm.PlainObject, key, def string) string {
	if opts == nil {
		return def
	}
	if v, ok := opts.GetOwn(key); ok && !v.IsUndefined() && v.Type() != vm.TypeNull {
		if s := v.ToString(); s != "" {
			return s
		}
	}
	return def
}

func getIntOpt(opts *vm.PlainObject, key string, def int) int {
	if opts == nil {
		return def
	}
	if v, ok := opts.GetOwn(key); ok && v.IsNumber() {
		return int(v.ToFloat())
	}
	return def
}

func getBoolOpt(opts *vm.PlainObject, key string, def bool) bool {
	if opts == nil {
		return def
	}
	if v, ok := opts.GetOwn(key); ok && !v.IsUndefined() && v.Type() != vm.TypeNull {
		return v.IsTruthy()
	}
	return def
}

func declareNet() {
	registerJSShim("net", netShim)
}

func installNetNatives(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	g := gt.AsPlainObject()
	if g == nil {
		return
	}
	b := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	b.SetOwn("newHandle", vm.NewNativeFunction(0, false, "newHandle", func(_ []vm.Value) (vm.Value, error) {
		return vm.NewValueFromPlainObject(newNetHandle(vmInst).obj), nil
	}))
	b.SetOwn("listen", vm.NewNativeFunction(1, false, "listen", func(args []vm.Value) (vm.Value, error) {
		var o *vm.PlainObject
		if len(args) > 0 && args[0].Type() == vm.TypeObject {
			o = args[0].AsPlainObject()
		}
		h, info := netListen(vmInst, o)
		if info != nil {
			return info.toValue(vmInst), nil
		}
		return h, nil
	}))
	b.SetOwn("isIP", vm.NewNativeFunction(1, false, "isIP", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.NumberValue(0), nil
		}
		return vm.NumberValue(float64(isIPString(args[0].ToString()))), nil
	}))
	b.SetOwn("errname", vm.NewNativeFunction(1, false, "errname", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, nil
		}
		code := args[0].ToString()
		if e, ok := sysErrno[code]; ok {
			return vm.NewString(errnoMessage(e)), nil
		}
		return vm.Undefined, nil
	}))
	g.SetOwn("__noderatiNetBinding", vm.NewValueFromPlainObject(b))
	installDgramBinding(vmInst, g)
}

// isIPString mirrors node's isIP: 4, 6 or 0. Zone ids are allowed on v6.
func isIPString(s string) int {
	if strings.Contains(s, ":") {
		host := s
		if i := strings.IndexByte(s, '%'); i >= 0 {
			host = s[:i]
			if i == len(s)-1 {
				return 0
			}
		}
		if ip := net.ParseIP(host); ip != nil {
			return 6
		}
		return 0
	}
	if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
		// net.ParseIP accepts leading zeros on Go < 1.17 only; node rejects them.
		for _, part := range strings.Split(s, ".") {
			if len(part) > 1 && part[0] == '0' {
				return 0
			}
		}
		return 4
	}
	return 0
}
