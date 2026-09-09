package host

import (
	"context"
	"crypto/tls"
	"net"
	"sync"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// tls.go implements real node:tls (tls.connect/tls.Socket) on top of Go's
// crypto/tls.Client - a genuine TLS handshake against a real
// net.Dial'd connection, not a stand-in. It shares socketState/
// buildSocketObject/writerLoop/readerLoop wholesale with net.go: a
// *tls.Conn satisfies net.Conn exactly like a *net.TCPConn does, so once
// the handshake completes the byte-pump plumbing underneath is identical
// - only the setup (dial, then wrap, then handshake, then read the
// negotiated ALPN protocol) differs from plain net.connect.
//
// One deliberate, documented gap (per advisor's review before writing
// this): real undici's connector listens for a 'session' event to
// populate its own JS-level session cache (lib/core/connect.js's
// SessionCache). That event is never emitted here - synthesizing a fake
// session object just to satisfy the listener would be exactly the
// lying-no-op this project rejects elsewhere. Session resumption still
// happens for real, just invisibly to JS: clientSessionCache below is a
// genuine tls.ClientSessionCache shared across every tls.connect() call,
// so Go's own crypto/tls resumes sessions internally when the server
// supports it. The observable difference is that undici's own
// process-lifetime session cache stays empty (a missed optimization,
// not a correctness gap - every connection still negotiates a real,
// valid TLS session either way).
var clientSessionCache = tls.NewLRUClientSessionCache(256)

// doTLSConnect implements tls.connect(). Mirrors doNetConnect's shape
// (dial asynchronously, return the Socket immediately) but adds the
// handshake as a second async step after the TCP dial succeeds - both
// steps run inside the same background goroutine so there's exactly one
// place that can fail and emit 'error'.
func doTLSConnect(vmInst *vm.VM, optsVal vm.Value, connectCb vm.Value) vm.Value {
	opts := optsVal.AsPlainObject()
	hwm := getIntOpt(opts, "highWaterMark", 16384) // TLS records cap out around 16KB either way - matches real Node's own comment in undici's connect.js.
	s := newSocketState(hwm)
	obj, self := buildSocketObject(vmInst, s)
	obj.SetOwn("connecting", vm.True)

	if connectCb.IsCallable() {
		addListener(vmInst, obj, "secureConnect", connectCb, true, false)
	}

	servername := getStrOpt(opts, "servername", getStrOpt(opts, "host", getStrOpt(opts, "hostname", "")))
	rejectUnauthorized := getBoolOpt(opts, "rejectUnauthorized", true)
	alpn := stringArrayOpt(opts, "ALPNProtocols")

	tlsConf := &tls.Config{
		ServerName:         servername,
		InsecureSkipVerify: !rejectUnauthorized,
		ClientSessionCache: clientSessionCache,
	}
	if len(alpn) > 0 {
		tlsConf.NextProtos = alpn
	}

	rt := vmInst.GetAsyncRuntime()
	rt.BeginExternalOp()
	s.beginTrackedExternalOp(rt)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() { defer wg.Done(); s.writerLoop(vmInst, rt, obj, self) }()

	go func() {
		var rawConn net.Conn
		var err error

		// Upgrade an already-connected plain socket, when one was handed
		// in via `socket:` (undici's httpSocket upgrade path) - steal its
		// live conn rather than dialing a fresh one. Marked detached so
		// the old socket's own loops stop touching the conn without
		// closing it out from under the new TLS wrapper.
		if upgradeVal, ok := getObjOpt(opts, "socket"); ok {
			if old := socketStateFromValue(upgradeVal); old != nil {
				old.mu.Lock()
				rawConn = old.conn
				old.detached = true
				old.closed = true
				old.mu.Unlock()
				old.cond.Broadcast()
			}
		}
		if rawConn == nil {
			rawConn, err = dialTCP(opts)
		}
		if err != nil {
			failTLSConnect(vmInst, rt, obj, s, err)
			wg.Done()
			return
		}

		tlsConn := tls.Client(rawConn, tlsConf)
		if err := tlsConn.HandshakeContext(context.Background()); err != nil {
			_ = rawConn.Close()
			failTLSConnect(vmInst, rt, obj, s, err)
			wg.Done()
			return
		}

		s.mu.Lock()
		s.conn = tlsConn
		noDelay := s.pendingNoDelay
		keepAlive := s.pendingKeepAlive
		keepAliveDelay := s.keepAliveInitDlay
		s.mu.Unlock()
		if noDelay != nil {
			applyNoDelay(tcpConnOf(tlsConn), *noDelay)
		}
		if keepAlive != nil {
			applyKeepAlive(tcpConnOf(tlsConn), *keepAlive, keepAliveDelay)
		}
		s.cond.Broadcast()

		state := tlsConn.ConnectionState()
		rt.ScheduleNextTick(func() {
			obj.SetOwn("connecting", vm.False)
			obj.SetOwn("pending", vm.False)
			obj.SetOwn("authorized", vm.BooleanValue(rejectUnauthorized))
			if state.NegotiatedProtocol != "" {
				obj.SetOwn("alpnProtocol", vm.NewString(state.NegotiatedProtocol))
			} else {
				obj.SetOwn("alpnProtocol", vm.False)
			}
			setConnAddrFields(vmInst, obj, tlsConn)
			emitOnObject(vmInst, obj, "secureConnect")
			emitOnObject(vmInst, obj, "connect")
			emitOnObject(vmInst, obj, "ready")
		})

		go func() { defer wg.Done(); s.readerLoop(vmInst, rt, obj) }()
	}()

	go func() {
		wg.Wait()
		s.endTrackedExternalOp()
	}()

	return self
}

func failTLSConnect(vmInst *vm.VM, rt interface{ ScheduleNextTick(func()) }, obj *vm.PlainObject, s *socketState, err error) {
	s.mu.Lock()
	s.closed = true
	s.destroyed = true
	s.mu.Unlock()
	s.cond.Broadcast()
	rt.ScheduleNextTick(func() {
		obj.SetOwn("connecting", vm.False)
		emitOnObject(vmInst, obj, "error", errorValueFromGo(vmInst, err))
	})
}

func stringArrayOpt(opts *vm.PlainObject, key string) []string {
	if opts == nil {
		return nil
	}
	v, ok := opts.GetOwn(key)
	if !ok {
		return nil
	}
	arr := v.AsArray()
	if arr == nil {
		return nil
	}
	out := make([]string, arr.Length())
	for i := 0; i < arr.Length(); i++ {
		out[i] = arr.Get(i).ToString()
	}
	return out
}

func getObjOpt(opts *vm.PlainObject, key string) (vm.Value, bool) {
	if opts == nil {
		return vm.Undefined, false
	}
	v, ok := opts.GetOwn(key)
	if !ok || v.IsUndefined() || v.Type() == vm.TypeNull || !v.IsObject() {
		return vm.Undefined, false
	}
	return v, true
}

func declareTLS() {
	registerJSShim("tls", tlsShim)
}

func installTLSNatives(p *driver.Paserati) {
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
	obj.SetOwn("__noderatiTLSConnect", vm.NewNativeFunction(2, true, "__noderatiTLSConnect", func(args []vm.Value) (vm.Value, error) {
		var optsVal vm.Value = vm.Undefined
		var cb vm.Value = vm.Undefined
		for _, a := range args {
			if a.IsCallable() && cb.IsUndefined() {
				cb = a
			} else if a.IsObject() && optsVal.IsUndefined() {
				optsVal = a
			}
		}
		return doTLSConnect(vmInst, optsVal, cb), nil
	}))
}

const tlsShim = `const __connect = globalThis.__noderatiTLSConnect;

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

class TLSSocket {
  constructor(socket, options) {
    return connect({ ...(options || {}), socket });
  }
}

export { connect, connect as createConnection, TLSSocket };
export default { connect, createConnection: connect, TLSSocket };
`
