package host

import (
	"encoding/base64"
	"encoding/hex"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/runtime"
	"github.com/nooga/paserati/pkg/vm"
)

// net.go implements real node:net (net.connect/net.createConnection/
// net.Socket) on top of Go's own net.Dial - a genuine TCP socket, not a
// synthetic stand-in. This exists specifically to back real undici's
// connector (lib/core/connect.js in the real npm package, read directly
// before writing this - see docs/real-node-plan.md's round 69 entry),
// which builds its own HTTP/1.1 client straight on raw net.Socket/
// tls.Socket rather than on node:http. tls.go is the TLS half of the same
// pair and reuses everything here (socketState, buildSocketObject,
// writerLoop/readerLoop) - a *tls.Conn satisfies net.Conn identically to
// a *net.TCPConn, so the byte-pump plumbing doesn't need to know which
// one it's holding.
//
// Backpressure is real, not faked: write() enqueues onto an unbounded
// Go-side queue (matching Node's own internal buffering - Node's write()
// doesn't block the event loop either, and returns a `false` hint while
// still accepting the data) and a dedicated goroutine drains it onto the
// actual conn; write() returns false once the queued byte count exceeds
// highWaterMark, and 'drain' fires for real once the queue empties back
// out - not on a timer or a fixed delay.

const defaultNetHighWaterMark = 64 * 1024

var (
	socketRegistry sync.Map // id(uint64) -> *socketState
	socketRegSeq   atomic.Uint64
)

const socketHandleMarker = "__noderatiSocketHandle"

type socketWriteItem struct {
	data  []byte
	isEnd bool
	cb    vm.Value
}

// socketState is the Go-side half of a Socket - the JS-visible object
// built by buildSocketObject only ever reaches into this through the
// native closures it installs, never the other way around, so nothing
// here touches VM values off the VM's own goroutine.
type socketState struct {
	mu   sync.Mutex
	cond *sync.Cond

	conn     net.Conn
	detached bool // true once ownership of conn was handed to a TLS upgrade; loops exit without closing it

	writeQueue []socketWriteItem
	closed     bool
	destroyed  bool
	closeOnce  sync.Once

	bytesQueued   int
	needDrain     bool
	paused        bool
	highWaterMark int

	bytesRead    int64
	bytesWritten int64

	encoding string // set via setEncoding(); "" means emit Buffers, matching real Node's default

	pendingNoDelay    *bool
	pendingKeepAlive  *bool
	keepAliveInitDlay time.Duration

	timeoutMu    sync.Mutex
	timeoutTimer *time.Timer
}

func newSocketState(hwm int) *socketState {
	s := &socketState{highWaterMark: hwm}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func registerSocketState(s *socketState) uint64 {
	id := socketRegSeq.Add(1)
	socketRegistry.Store(id, s)
	return id
}

func socketStateFromValue(v vm.Value) *socketState {
	obj := v.AsPlainObject()
	if obj == nil {
		return nil
	}
	idVal, ok := obj.GetOwn(socketHandleMarker)
	if !ok || !idVal.IsNumber() {
		return nil
	}
	if s, ok := socketRegistry.Load(uint64(idVal.ToFloat())); ok {
		return s.(*socketState)
	}
	return nil
}

// valueToBytes accepts either a JS string or a real Buffer/TypedArray
// (buffer.go's Buffer is a real Uint8Array subclass as of the Buffer
// realness pass done alongside paserati#375/WebAssembly - see buffer.go)
// and returns the underlying bytes. A TypedArray's own real backing
// bytes are read directly via typedArrayBytes rather than through any
// string round trip, so this is safe for arbitrary binary data too.
func valueToBytes(vmInst *vm.VM, v vm.Value) []byte {
	if ta := v.AsTypedArray(); ta != nil {
		if b := typedArrayBytes(ta); b != nil {
			return b
		}
		return nil
	}
	return []byte(v.ToString())
}

// emitDataChunk mirrors real Node's default: a socket's 'data' event
// carries a Buffer unless setEncoding() was called, in which case it
// carries a decoded string instead. The encoding is only ever read here,
// on the VM thread (via scheduleEmit's ScheduleNextTick), never from the
// background read goroutine that captured the raw bytes.
func emitDataChunk(vmInst *vm.VM, obj *vm.PlainObject, chunk []byte, encoding string) {
	switch encoding {
	case "":
		scheduleEmit(vmInst, obj, "data", wrapBuffer(vmInst, chunk))
	case "hex":
		scheduleEmit(vmInst, obj, "data", vm.NewString(hex.EncodeToString(chunk)))
	case "base64":
		scheduleEmit(vmInst, obj, "data", vm.NewString(base64.StdEncoding.EncodeToString(chunk)))
	default:
		scheduleEmit(vmInst, obj, "data", vm.NewString(string(chunk)))
	}
}

// writerLoop is the only goroutine that ever calls conn.Write - write()/
// end() just enqueue, exactly like http.go's bodyCh pattern, so a
// synchronous native call never blocks the VM's own execution thread on
// unbuffered (or slow) network I/O. It parks in cond.Wait() while there's
// nothing to send and no conn to send it on yet (pre-connect writes are
// legal in real Node and get flushed once the socket connects), and exits
// for good once the socket is closed and its queue has fully drained.
func (s *socketState) writerLoop(vmInst *vm.VM, rt runtime.AsyncRuntime, obj *vm.PlainObject, self vm.Value) {
	for {
		s.mu.Lock()
		for {
			if s.closed && (len(s.writeQueue) == 0 || s.conn == nil) {
				break
			}
			if len(s.writeQueue) > 0 && s.conn != nil {
				break
			}
			s.cond.Wait()
		}
		// A pending write/end() queued before destroy() can lose its data
		// here - closed became true (destroyInternal already closed conn)
		// while the item was still sitting in the queue, so it's drained
		// with an error instead of ever reaching conn.Write. This matches
		// real Node's own documented destroy() semantics, not a gap:
		// destroy() is specified to abort the socket immediately, with
		// "any data currently in flight" dropped, not flushed first (that
		// flush-before-close behavior is what end() alone is for). A
		// caller wanting queued data to actually go out has to await
		// end()/'finish' before calling destroy(), same as in real Node.
		if s.closed && (len(s.writeQueue) == 0 || s.conn == nil) {
			pending := s.writeQueue
			s.writeQueue = nil
			s.mu.Unlock()
			for _, item := range pending {
				item := item
				if item.cb.IsCallable() {
					rt.ScheduleNextTick(func() {
						_, _ = vmInst.Call(item.cb, self, []vm.Value{errorValueFromGo(vmInst, io.ErrClosedPipe)})
					})
				}
			}
			return
		}

		item := s.writeQueue[0]
		s.writeQueue = s.writeQueue[1:]
		conn := s.conn
		s.mu.Unlock()

		var writeErr error
		if len(item.data) > 0 {
			_, writeErr = conn.Write(item.data)
		}
		if writeErr == nil && item.isEnd {
			if cw, ok := conn.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			} else {
				writeErr = conn.Close()
			}
		}

		s.mu.Lock()
		if writeErr == nil {
			s.bytesWritten += int64(len(item.data))
		}
		s.bytesQueued -= len(item.data)
		if s.bytesQueued < 0 {
			s.bytesQueued = 0
		}
		drain := false
		if s.bytesQueued == 0 && s.needDrain {
			s.needDrain = false
			drain = true
		}
		wasIntentional := s.closed
		s.mu.Unlock()

		if item.cb.IsCallable() {
			cb := item.cb
			goErr := writeErr
			rt.ScheduleNextTick(func() {
				errArg := vm.Undefined
				if goErr != nil {
					errArg = errorValueFromGo(vmInst, goErr)
				}
				_, _ = vmInst.Call(cb, self, []vm.Value{errArg})
			})
		}

		if writeErr != nil {
			if !wasIntentional {
				scheduleErrorEmit(vmInst, rt, obj, writeErr)
			}
			s.destroyInternal(vmInst, obj, writeErr != nil && !wasIntentional)
			return
		}
		if drain {
			scheduleEmit(vmInst, obj, "drain")
		}
	}
}

// readerLoop pulls bytes off conn as they arrive and schedules a 'data'
// event per Read() call - never buffering the whole response before
// emitting anything, so a slow/large response streams incrementally the
// same way http.go's pumpHTTPResponseBody already had to get right.
// pause()/resume() gate this loop directly (not just the JS-visible
// event delivery) so a paused socket genuinely stops pulling bytes off
// the OS socket, which is what makes TCP-level backpressure apply
// upstream instead of us just buffering in Go instead of buffering in JS.
func (s *socketState) readerLoop(vmInst *vm.VM, rt runtime.AsyncRuntime, obj *vm.PlainObject) {
	buf := make([]byte, 64*1024)
	for {
		s.mu.Lock()
		for s.paused && !s.closed {
			s.cond.Wait()
		}
		closed := s.closed
		conn := s.conn
		s.mu.Unlock()
		if closed || conn == nil {
			return
		}

		n, err := conn.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.bytesRead += int64(n)
			encoding := s.encoding
			s.mu.Unlock()
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			emitDataChunk(vmInst, obj, chunk, encoding)
		}
		if err != nil {
			s.mu.Lock()
			wasIntentional := s.closed
			s.mu.Unlock()
			if wasIntentional {
				return
			}
			if err == io.EOF {
				scheduleEmit(vmInst, obj, "end")
				s.destroyInternal(vmInst, obj, false)
			} else {
				scheduleErrorEmit(vmInst, rt, obj, err)
				s.destroyInternal(vmInst, obj, true)
			}
			return
		}
	}
}

// destroyInternal is the single real teardown path - called whether the
// peer closed first (readerLoop's EOF), a write failed, or JS called
// .destroy() directly - guarded by closeOnce so 'close' fires exactly
// once regardless of which of those raced to get there first.
func (s *socketState) destroyInternal(vmInst *vm.VM, obj *vm.PlainObject, hadError bool) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.destroyed = true
		conn := s.conn
		detached := s.detached
		s.mu.Unlock()
		s.timeoutMu.Lock()
		if s.timeoutTimer != nil {
			s.timeoutTimer.Stop()
		}
		s.timeoutMu.Unlock()
		if conn != nil && !detached {
			_ = conn.Close()
		}
		s.cond.Broadcast()
		obj.SetOwn("destroyed", vm.True)
		scheduleEmit(vmInst, obj, "close", vm.BooleanValue(hadError))
	})
}

func (s *socketState) queueWrite(vmInst *vm.VM, obj *vm.PlainObject, data []byte, isEnd bool, cb vm.Value) bool {
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		if cb.IsCallable() {
			scheduleEmit0(vmInst, func() { _, _ = vmInst.Call(cb, vm.NewValueFromPlainObject(obj), []vm.Value{errorValueFromGo(vmInst, io.ErrClosedPipe)}) })
		}
		return false
	}
	s.writeQueue = append(s.writeQueue, socketWriteItem{data: data, isEnd: isEnd, cb: cb})
	s.bytesQueued += len(data)
	underHWM := s.bytesQueued <= s.highWaterMark
	if !underHWM {
		s.needDrain = true
	}
	s.mu.Unlock()
	s.cond.Broadcast()
	return underHWM
}

// scheduleEmit0 schedules a plain closure on the VM's next tick - used
// where there's no PlainObject/event name pair handy (a callback
// invocation rather than an emit).
func scheduleEmit0(vmInst *vm.VM, fn func()) {
	vmInst.GetAsyncRuntime().ScheduleNextTick(fn)
}

// scheduleErrorEmit schedules an 'error' emit whose Error *value* is
// built from goErr only once the tick actually runs on the VM's own
// thread - never before. A real, race-detector-caught bug (not a
// hypothetical one - see docs/real-node-plan.md's round 73 entry) is
// exactly the mistake this guards against: errorValueFromGo() ultimately
// calls vmInst.Construct() on the Error constructor, which mutates
// shared VM fields (currentThis/currentNewTarget/inConstructorCall) -
// safe when it runs on the VM's own goroutine via this tick, a genuine
// data race with the VM's main execution loop when called eagerly on a
// background goroutine before scheduling (which is what
// `scheduleEmit(vmInst, obj, "error", errorValueFromGo(vmInst, err))`
// actually does: Go evaluates errorValueFromGo's call *before* handing
// its result to scheduleEmit, on whichever goroutine made the call).
// Passing the plain Go `error` across the goroutine boundary and
// constructing the vm.Value only inside this closure is what makes it
// safe - callers on a background goroutine must use this, never
// scheduleEmit(..., errorValueFromGo(...)) directly.
func scheduleErrorEmit(vmInst *vm.VM, rt runtime.AsyncRuntime, obj *vm.PlainObject, goErr error) {
	rt.ScheduleNextTick(func() {
		emitOnObject(vmInst, obj, "error", errorValueFromGo(vmInst, goErr))
	})
}

// buildSocketObject builds the JS-visible Socket - readable+writable,
// wired directly onto socketState rather than via newReadableStream's
// destroy()/pipe() defaults, since a real Socket's destroy() must
// actually close a live conn (not just emit events) and its data flow is
// byte-level, not the string-only shape newReadableStream assumes.
func buildSocketObject(vmInst *vm.VM, s *socketState) (*vm.PlainObject, vm.Value) {
	obj := newEventEmitterObject(vmInst)
	self := vm.NewValueFromPlainObject(obj)
	id := registerSocketState(s)
	obj.SetOwn(socketHandleMarker, vm.NumberValue(float64(id)))
	obj.SetOwn("readable", vm.True)
	obj.SetOwn("writable", vm.True)
	obj.SetOwn("connecting", vm.False)
	obj.SetOwn("pending", vm.True)
	obj.SetOwn("destroyed", vm.False)
	obj.SetOwn("bufferSize", vm.NumberValue(0))

	rt := vmInst.GetAsyncRuntime()

	obj.SetOwn("write", vm.NewNativeFunction(3, true, "write", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.True, nil
		}
		data := valueToBytes(vmInst, args[0])
		var cb vm.Value
		for _, a := range args[1:] {
			if a.IsCallable() {
				cb = a
				break
			}
		}
		ok := s.queueWrite(vmInst, obj, data, false, cb)
		return vm.BooleanValue(ok), nil
	}))

	obj.SetOwn("end", vm.NewNativeFunction(2, true, "end", func(args []vm.Value) (vm.Value, error) {
		var data []byte
		var cb vm.Value
		for _, a := range args {
			if a.IsCallable() {
				cb = a
			} else if !a.IsUndefined() && a.Type() != vm.TypeNull {
				data = valueToBytes(vmInst, a)
			}
		}
		s.queueWrite(vmInst, obj, data, true, cb)
		return self, nil
	}))

	obj.SetOwn("destroy", vm.NewNativeFunction(1, true, "destroy", func(args []vm.Value) (vm.Value, error) {
		hadError := false
		if len(args) > 0 && !args[0].IsUndefined() && args[0].Type() != vm.TypeNull {
			hadError = true
			scheduleEmit(vmInst, obj, "error", args[0])
		}
		s.destroyInternal(vmInst, obj, hadError)
		return self, nil
	}))

	obj.SetOwn("pause", vm.NewNativeFunction(0, false, "pause", func(_ []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		s.paused = true
		s.mu.Unlock()
		return self, nil
	}))
	obj.SetOwn("resume", vm.NewNativeFunction(0, false, "resume", func(_ []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		s.paused = false
		s.mu.Unlock()
		s.cond.Broadcast()
		return self, nil
	}))

	obj.SetOwn("setEncoding", vm.NewNativeFunction(1, false, "setEncoding", func(args []vm.Value) (vm.Value, error) {
		enc := ""
		if len(args) > 0 && !args[0].IsUndefined() {
			enc = args[0].ToString()
			if enc == "utf8" || enc == "utf-8" {
				enc = ""
			}
		}
		s.mu.Lock()
		s.encoding = enc
		s.mu.Unlock()
		return self, nil
	}))

	obj.SetOwn("setNoDelay", vm.NewNativeFunction(1, false, "setNoDelay", func(args []vm.Value) (vm.Value, error) {
		v := true
		if len(args) > 0 {
			v = args[0].IsTruthy()
		}
		s.mu.Lock()
		conn := s.conn
		s.pendingNoDelay = &v
		s.mu.Unlock()
		applyNoDelay(conn, v)
		return self, nil
	}))

	obj.SetOwn("setKeepAlive", vm.NewNativeFunction(2, false, "setKeepAlive", func(args []vm.Value) (vm.Value, error) {
		v := false
		if len(args) > 0 {
			v = args[0].IsTruthy()
		}
		delay := 0 * time.Millisecond
		if len(args) > 1 && args[1].IsNumber() {
			delay = time.Duration(args[1].ToFloat()) * time.Millisecond
		}
		s.mu.Lock()
		conn := s.conn
		s.pendingKeepAlive = &v
		s.keepAliveInitDlay = delay
		s.mu.Unlock()
		applyKeepAlive(conn, v, delay)
		return self, nil
	}))

	obj.SetOwn("setTimeout", vm.NewNativeFunction(2, false, "setTimeout", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsNumber() {
			return self, nil
		}
		ms := args[0].ToFloat()
		var cb vm.Value
		if len(args) > 1 && args[1].IsCallable() {
			cb = args[1]
		}
		s.timeoutMu.Lock()
		if s.timeoutTimer != nil {
			s.timeoutTimer.Stop()
		}
		if ms > 0 {
			s.timeoutTimer = time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
				rt.ScheduleNextTick(func() {
					if cb.IsCallable() {
						_, _ = vmInst.Call(cb, self, nil)
					}
					emitOnObject(vmInst, obj, "timeout")
				})
			})
		}
		s.timeoutMu.Unlock()
		return self, nil
	}))

	obj.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) { return self, nil }))
	obj.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) { return self, nil }))
	// cork/uncork: real Node batches writes between these into a single
	// underlying write; our queue already coalesces at the OS-write
	// granularity of one item per write()/end() call, and undici's h1
	// path (allowH2:false, the only path this implementation targets)
	// never calls either - honest no-ops rather than unbuilt batching.
	obj.SetOwn("cork", vm.NewNativeFunction(0, false, "cork", func(_ []vm.Value) (vm.Value, error) { return vm.Undefined, nil }))
	obj.SetOwn("uncork", vm.NewNativeFunction(0, false, "uncork", func(_ []vm.Value) (vm.Value, error) { return vm.Undefined, nil }))

	obj.SetOwn("address", vm.NewNativeFunction(0, false, "address", func(_ []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		conn := s.conn
		s.mu.Unlock()
		if conn == nil {
			return vm.NewValueFromPlainObject(vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()), nil
		}
		return addrObject(vmInst, conn.LocalAddr()), nil
	}))

	return obj, self
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
	out.SetOwn("address", vm.NewString(host))
	out.SetOwn("family", vm.NewString(family))
	if port, perr := strconv.Atoi(portStr); perr == nil {
		out.SetOwn("port", vm.NumberValue(float64(port)))
	}
	return vm.NewValueFromPlainObject(out)
}

func setConnAddrFields(vmInst *vm.VM, obj *vm.PlainObject, conn net.Conn) {
	if local, ok := addrParts(conn.LocalAddr()); ok {
		obj.SetOwn("localAddress", vm.NewString(local.host))
		obj.SetOwn("localPort", vm.NumberValue(float64(local.port)))
		obj.SetOwn("localFamily", vm.NewString(local.family))
	}
	if remote, ok := addrParts(conn.RemoteAddr()); ok {
		obj.SetOwn("remoteAddress", vm.NewString(remote.host))
		obj.SetOwn("remotePort", vm.NumberValue(float64(remote.port)))
		obj.SetOwn("remoteFamily", vm.NewString(remote.family))
	}
}

type addrParsed struct {
	host   string
	port   int
	family string
}

func addrParts(addr net.Addr) (addrParsed, bool) {
	if addr == nil {
		return addrParsed{}, false
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addrParsed{}, false
	}
	family := "IPv4"
	if strings.Contains(host, ":") {
		family = "IPv6"
	}
	port, _ := strconv.Atoi(portStr)
	return addrParsed{host: host, port: port, family: family}, true
}

func applyNoDelay(conn net.Conn, v bool) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(v)
	} else if nd, ok := conn.(interface{ SetNoDelay(bool) error }); ok {
		_ = nd.SetNoDelay(v)
	}
}

func applyKeepAlive(conn net.Conn, v bool, delay time.Duration) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(v)
		if v && delay > 0 {
			_ = tc.SetKeepAlivePeriod(delay)
		}
	}
}

// tcpConnOf unwraps to the underlying *net.TCPConn for option-setting
// even when conn is a *tls.Conn (tls.Conn.NetConn() surfaces the raw
// conn it was built on) - so setNoDelay/setKeepAlive on a TLS socket
// still reach the real OS socket, not a no-op.
func tcpConnOf(conn net.Conn) net.Conn {
	if u, ok := conn.(interface{ NetConn() net.Conn }); ok {
		return u.NetConn()
	}
	return conn
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

// portOpt reads a `port` option that real Node (and undici's connector)
// may pass as either a number or a numeric string.
func portOpt(opts *vm.PlainObject, def string) string {
	if opts == nil {
		return def
	}
	if v, ok := opts.GetOwn("port"); ok && !v.IsUndefined() && v.Type() != vm.TypeNull {
		if v.IsNumber() {
			return strconv.Itoa(int(v.ToFloat()))
		}
		return v.ToString()
	}
	return def
}

func dialTCP(opts *vm.PlainObject) (net.Conn, error) {
	host := getStrOpt(opts, "host", getStrOpt(opts, "hostname", "localhost"))
	port := portOpt(opts, "0")
	dialer := &net.Dialer{}
	if la := getStrOpt(opts, "localAddress", ""); la != "" {
		ip := net.ParseIP(la)
		dialer.LocalAddr = &net.TCPAddr{IP: ip}
	}
	return dialer.Dial("tcp", net.JoinHostPort(host, port))
}

// doNetConnect implements net.connect()/net.createConnection() - dials
// asynchronously (never blocking the VM thread on the network) and
// returns the Socket object immediately, exactly like real Node: writes
// issued before 'connect' fires are legal and simply queue, flushed once
// the dial actually completes.
func doNetConnect(vmInst *vm.VM, optsVal vm.Value, connectCb vm.Value) vm.Value {
	opts := optsVal.AsPlainObject()
	hwm := getIntOpt(opts, "highWaterMark", defaultNetHighWaterMark)
	s := newSocketState(hwm)
	obj, self := buildSocketObject(vmInst, s)
	obj.SetOwn("connecting", vm.True)

	if connectCb.IsCallable() {
		addListener(vmInst, obj, "connect", connectCb, true, false)
	}

	rt := vmInst.GetAsyncRuntime()
	rt.BeginExternalOp()

	var wg sync.WaitGroup
	wg.Add(2) // writer slot + reader slot; reader's Done() fires even on dial failure (see below)

	go func() { defer wg.Done(); s.writerLoop(vmInst, rt, obj, self) }()

	go func() {
		conn, err := dialTCP(opts)
		if err != nil {
			s.mu.Lock()
			s.closed = true
			s.destroyed = true
			s.mu.Unlock()
			s.cond.Broadcast()
			rt.ScheduleNextTick(func() {
				obj.SetOwn("connecting", vm.False)
				obj.SetOwn("pending", vm.True)
				emitOnObject(vmInst, obj, "error", errorValueFromGo(vmInst, err))
			})
			wg.Done()
			return
		}

		s.mu.Lock()
		s.conn = conn
		noDelay := s.pendingNoDelay
		keepAlive := s.pendingKeepAlive
		keepAliveDelay := s.keepAliveInitDlay
		s.mu.Unlock()
		if noDelay != nil {
			applyNoDelay(conn, *noDelay)
		}
		if keepAlive != nil {
			applyKeepAlive(conn, *keepAlive, keepAliveDelay)
		}
		s.cond.Broadcast()

		rt.ScheduleNextTick(func() {
			obj.SetOwn("connecting", vm.False)
			obj.SetOwn("pending", vm.False)
			setConnAddrFields(vmInst, obj, conn)
			emitOnObject(vmInst, obj, "connect")
			emitOnObject(vmInst, obj, "ready")
		})

		go func() { defer wg.Done(); s.readerLoop(vmInst, rt, obj) }()
	}()

	go func() {
		wg.Wait()
		rt.EndExternalOp()
	}()

	return self
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
	obj := gt.AsPlainObject()
	if obj == nil {
		return
	}
	obj.SetOwn("__noderatiNetConnect", vm.NewNativeFunction(2, true, "__noderatiNetConnect", func(args []vm.Value) (vm.Value, error) {
		var optsVal vm.Value = vm.Undefined
		var cb vm.Value = vm.Undefined
		for _, a := range args {
			if a.IsCallable() && cb.IsUndefined() {
				cb = a
			} else if a.IsObject() && optsVal.IsUndefined() {
				optsVal = a
			}
		}
		return doNetConnect(vmInst, optsVal, cb), nil
	}))
	obj.SetOwn("__noderatiIsIP", vm.NewNativeFunction(1, false, "__noderatiIsIP", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.NumberValue(0), nil
		}
		s := args[0].ToString()
		if ip := net.ParseIP(s); ip != nil {
			if ip.To4() != nil {
				return vm.NumberValue(4), nil
			}
			return vm.NumberValue(6), nil
		}
		return vm.NumberValue(0), nil
	}))
}

const netShim = `const __connect = globalThis.__noderatiNetConnect;
const __isIP = globalThis.__noderatiIsIP;

function normalizeArgs(args) {
  let options = args[0];
  let cb = undefined;
  if (typeof options === "number") {
    options = { port: options, host: typeof args[1] === "string" ? args[1] : undefined };
    cb = typeof args[1] === "function" ? args[1] : args[2];
  } else if (typeof options === "object" && options !== null) {
    cb = args[1];
  }
  return [options, typeof cb === "function" ? cb : undefined];
}

function connect(...args) {
  const [options, cb] = normalizeArgs(args);
  return __connect(options || {}, cb);
}

class Socket {
  constructor(options) {
    return connect(options);
  }
}

function isIP(input) {
  return __isIP(String(input));
}
function isIPv4(input) {
  return isIP(input) === 4;
}
function isIPv6(input) {
  return isIP(input) === 6;
}

export { connect, connect as createConnection, Socket, isIP, isIPv4, isIPv6 };
export default { connect, createConnection: connect, Socket, isIP, isIPv4, isIPv6 };
`
