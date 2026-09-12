package host

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// http.go implements node:http/node:https's request-level API
// (request()/get()/Agent/ClientRequest/IncomingMessage) directly on top of
// Go's own net/http.Client, not on raw net/tls sockets. That's a deliberate
// deviation from Node's own architecture (real Node builds http on top of
// net/tls sockets it manages itself) rather than an incomplete stand-in for
// one: the only real consumer found in the whole pi dependency tree
// (@smithy/node-http-handler, AWS Bedrock's HTTP transport - round 68,
// docs/real-node-plan.md) imports only `{ Agent, request } from "node:https"`,
// never a raw socket constructor, and hand-rolling HTTP/1.1 framing on top
// of our own sockets would just be reinventing the protocol-parsing bugs
// Go's stdlib already gets right. node:net/node:tls (real Socket/TLSSocket)
// stay unimplemented - nothing reachable needs them, confirmed by survey,
// not assumed.
//
// request.socket's real shape (checked directly against @smithy/node-
// http-handler's own set-connection-timeout.js/set-socket-timeout.js/
// set-socket-keep-alive.js before writing this, not inferred) only needs:
// .connecting, .on("connect"), .setTimeout(ms, cb), .setKeepAlive(on, ms).
// Built as a small object rather than a real net.Socket, wired to Go's own
// httptrace.ClientTrace.GotConn hook for a real (not synthetic) "connect"
// moment.

const httpAgentMarker = "__noderatiHTTPAgent"

var httpAgentTransports sync.Map // id(uint64) -> *http.Transport
var httpAgentSeq atomic.Uint64

func declareHTTP() {
	registerJSShim("http", httpShim)
	registerJSShim("https", httpsShim)
}

func installHTTPNatives(p *driver.Paserati) {
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

	obj.SetOwn("__noderatiHTTPAgentCtor", buildHTTPAgentConstructor(vmInst))
	obj.SetOwn("__noderatiHTTPRequest", vm.NewNativeFunction(2, false, "__noderatiHTTPRequest", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 2 {
			return vm.Undefined, nil
		}
		return doHTTPRequest(vmInst, args[0].ToString(), args[1]), nil
	}))
}

// buildHTTPAgentConstructor: a real Agent, not a stub that only looks
// constructible - it owns a real *http.Transport (found via
// httpAgentTransports, keyed by a numeric id stored on the JS object, the
// same handle-registry pattern child_process.go uses for *exec.Cmd) built
// from the options actually passed, and every request made with `agent:
// thisAgent` reuses that same Transport - real Go-level connection
// pooling/keep-alive, not a per-request throwaway. Defaults to
// http.ProxyFromEnvironment unconditionally: unlike paserati's own fetch
// (see paserati#290 - no hook to configure this from a host), this
// Transport is entirely ours to build, so there's no reason to leave the
// same real-world proxy-env-var gap unfixed here too.
func buildHTTPAgentConstructor(vmInst *vm.VM) vm.Value {
	return vm.NewNativeConstructor(0, false, "Agent", func(args []vm.Value) (vm.Value, error) {
		keepAlive := false
		var opts *vm.PlainObject
		if len(args) > 0 {
			opts = args[0].AsPlainObject()
		}
		if opts != nil {
			if v, ok := opts.GetOwn("keepAlive"); ok {
				keepAlive = v.IsTruthy()
			}
		}
		transport := &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DisableKeepAlives: !keepAlive,
		}
		id := httpAgentSeq.Add(1)
		httpAgentTransports.Store(id, transport)

		obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		obj.SetOwn(httpAgentMarker, vm.NumberValue(float64(id)))
		obj.SetOwn("destroy", vm.NewNativeFunction(0, false, "destroy", func(_ []vm.Value) (vm.Value, error) {
			transport.CloseIdleConnections()
			return vm.Undefined, nil
		}))
		return vm.NewValueFromPlainObject(obj), nil
	})
}

// defaultHTTPTransport backs every request() call that doesn't pass an
// explicit `agent:` option - still a real, shared, proxy-aware Transport
// (not a fresh one per call, and not the bare zero-value gap paserati#290
// describes for fetch), just not one JS code can reach and reconfigure.
var defaultHTTPTransport = &http.Transport{Proxy: http.ProxyFromEnvironment}

func transportFromAgentOption(opts *vm.PlainObject) *http.Transport {
	if opts == nil {
		return defaultHTTPTransport
	}
	agentVal, ok := opts.GetOwn("agent")
	if !ok {
		return defaultHTTPTransport
	}
	agentObj := agentVal.AsPlainObject()
	if agentObj == nil {
		return defaultHTTPTransport
	}
	idVal, ok := agentObj.GetOwn(httpAgentMarker)
	if !ok || !idVal.IsNumber() {
		return defaultHTTPTransport
	}
	if t, ok := httpAgentTransports.Load(uint64(idVal.ToFloat())); ok {
		return t.(*http.Transport)
	}
	return defaultHTTPTransport
}

// doHTTPRequest builds the real ClientRequest object returned by
// http.request()/https.request(). scheme is "http" or "https" - which
// module-level function was actually called - since real Node's http and
// https each only ever produce a request of their own protocol, regardless
// of what a caller's options object happens to say.
func doHTTPRequest(vmInst *vm.VM, scheme string, optsVal vm.Value) vm.Value {
	opts := optsVal.AsPlainObject()

	getStr := func(key, def string) string {
		if opts == nil {
			return def
		}
		if v, ok := opts.GetOwn(key); ok && !v.IsUndefined() && v.Type() != vm.TypeNull {
			return v.ToString()
		}
		return def
	}
	host := getStr("hostname", getStr("host", "localhost"))
	defaultPort := "80"
	if scheme == "https" {
		defaultPort = "443"
	}
	port := defaultPort
	if opts != nil {
		if v, ok := opts.GetOwn("port"); ok && !v.IsUndefined() && v.Type() != vm.TypeNull {
			if v.IsNumber() {
				port = strconv.Itoa(int(v.ToFloat()))
			} else {
				port = v.ToString()
			}
		}
	}
	path := getStr("path", "/")
	method := strings.ToUpper(getStr("method", "GET"))
	urlStr := scheme + "://" + net.JoinHostPort(host, port) + path

	pr, pw := io.Pipe()
	bodyCh := make(chan []byte, 64)
	// A native call (.write()/.end()) runs synchronously on the VM's own
	// single execution thread - it must never block on an unbuffered
	// io.Pipe write directly (client.Do's reader goroutine may not have
	// started reading yet), or the whole VM stalls waiting for itself.
	// This goroutine is the only thing that actually touches pw, decoupling
	// JS-visible write()/end() calls (which just enqueue) from the blocking
	// io.Pipe protocol underneath.
	go func() {
		for chunk := range bodyCh {
			if _, err := pw.Write(chunk); err != nil {
				break
			}
		}
		_ = pw.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	httpReq, reqErr := http.NewRequestWithContext(ctx, method, urlStr, pr)

	reqObj := newEventEmitterObject(vmInst)
	reqSelf := vm.NewValueFromPlainObject(reqObj)
	rt := vmInst.GetAsyncRuntime()

	// Built on newEventEmitterObject, not a bespoke object with a hand-
	// rolled .on() - a first version of this hand-rolled its own "connect"
	// handling (fire immediately if already connected, otherwise silently
	// drop the listener) and lost every "connect" event whose listener was
	// registered before GotConn actually fired, which is the *common* case,
	// not an edge one (confirmed by testing against a real endpoint before
	// trusting it - see docs/real-node-plan.md's round 68 entry). Reusing
	// the real event-emitter base means "connect" (and anything else
	// real code listens for on a socket) is delivered correctly regardless
	// of registration order, the same guarantee every other EventEmitter
	// in this codebase already provides.
	socketObj := newEventEmitterObject(vmInst)
	socketObj.SetOwn("connecting", vm.True)
	socketSelf := vm.NewValueFromPlainObject(socketObj)
	var socketTimeoutMu sync.Mutex
	var socketTimeoutTimer *time.Timer
	socketObj.SetOwn("setTimeout", vm.NewNativeFunction(2, false, "setTimeout", func(a []vm.Value) (vm.Value, error) {
		if len(a) == 0 || !a[0].IsNumber() {
			return socketSelf, nil
		}
		ms := a[0].ToFloat()
		var cb vm.Value
		if len(a) > 1 && a[1].IsCallable() {
			cb = a[1]
		}
		socketTimeoutMu.Lock()
		if socketTimeoutTimer != nil {
			socketTimeoutTimer.Stop()
		}
		if ms > 0 {
			socketTimeoutTimer = time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
				rt.ScheduleNextTick(func() {
					cancel()
					if cb.IsCallable() {
						_, _ = vmInst.Call(cb, vm.Undefined, nil)
					}
					emitOnObject(vmInst, reqObj, "timeout")
				})
			})
		}
		socketTimeoutMu.Unlock()
		return socketSelf, nil
	}))
	socketObj.SetOwn("setKeepAlive", vm.NewNativeFunction(2, true, "setKeepAlive", func(_ []vm.Value) (vm.Value, error) {
		// Real Go connection reuse is already governed by the Transport
		// itself (Agent's keepAlive option, see buildHTTPAgentConstructor) -
		// per-socket keep-alive tuning has no equivalent lever to pull once
		// a request already has a Transport, so this is an honest no-op on
		// an otherwise real object, not a stand-in for missing behavior.
		return socketSelf, nil
	}))

	if reqErr != nil {
		cancel()
		// The body-pipe goroutine above is already running and blocked on
		// `range bodyCh` - write()/end() are about to be replaced with
		// no-ops that never touch bodyCh, so nothing would ever close it
		// without this, leaking that goroutine forever on every malformed-
		// URL request.
		close(bodyCh)
		rt.ScheduleNextTick(func() {
			emitOnObject(vmInst, reqObj, "error", errorValueFromGo(vmInst, reqErr))
		})
		reqObj.SetOwn("write", noopTrueFn())
		reqObj.SetOwn("end", noopSelfFn(reqSelf))
		reqObj.SetOwn("destroy", noopSelfFn(reqSelf))
		reqObj.SetOwn("setTimeout", noopSelfFn(reqSelf))
		reqObj.SetOwn("socket", vm.Undefined)
		return reqSelf
	}

	if opts != nil {
		if hv, ok := opts.GetOwn("headers"); ok {
			if hobj := hv.AsPlainObject(); hobj != nil {
				for _, k := range hobj.OwnKeys() {
					if val, ok := hobj.GetOwn(k); ok {
						httpReq.Header.Set(k, val.ToString())
					}
				}
			}
		}
	}

	transport := transportFromAgentOption(opts)
	if scheme == "https" && transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{ServerName: host}
	}

	trace := &httptrace.ClientTrace{
		GotConn: func(_ httptrace.GotConnInfo) {
			rt.ScheduleNextTick(func() {
				connecting, _ := socketObj.GetOwn("connecting")
				if connecting.IsTruthy() {
					socketObj.SetOwn("connecting", vm.False)
					emitOnObject(vmInst, socketObj, "connect")
				}
			})
		},
	}
	httpReq = httpReq.WithContext(httptrace.WithClientTrace(httpReq.Context(), trace))

	reqObj.SetOwn("socket", socketSelf)
	rt.ScheduleNextTick(func() {
		emitOnObject(vmInst, reqObj, "socket", socketSelf)
	})

	reqObj.SetOwn("write", vm.NewNativeFunction(1, true, "write", func(a []vm.Value) (vm.Value, error) {
		if len(a) > 0 && !a[0].IsUndefined() {
			bodyCh <- []byte(a[0].ToString())
		}
		if len(a) > 1 && a[1].IsCallable() {
			cb := a[1]
			rt.ScheduleNextTick(func() { _, _ = vmInst.Call(cb, vm.Undefined, nil) })
		}
		return vm.True, nil
	}))
	reqObj.SetOwn("end", vm.NewNativeFunction(0, true, "end", func(a []vm.Value) (vm.Value, error) {
		if len(a) > 0 && a[0].IsCallable() {
			// end(callback) - no body chunk, just the finish callback.
			close(bodyCh)
			cb := a[0]
			rt.ScheduleNextTick(func() { _, _ = vmInst.Call(cb, vm.Undefined, nil) })
			return reqSelf, nil
		}
		if len(a) > 0 && !a[0].IsUndefined() {
			bodyCh <- []byte(a[0].ToString())
		}
		close(bodyCh)
		if len(a) > 1 && a[1].IsCallable() {
			cb := a[1]
			rt.ScheduleNextTick(func() { _, _ = vmInst.Call(cb, vm.Undefined, nil) })
		}
		return reqSelf, nil
	}))
	reqObj.SetOwn("destroy", vm.NewNativeFunction(0, true, "destroy", func(a []vm.Value) (vm.Value, error) {
		cancel()
		return reqSelf, nil
	}))
	reqObj.SetOwn("setTimeout", vm.NewNativeFunction(2, false, "setTimeout", func(a []vm.Value) (vm.Value, error) {
		if fn, ok := socketObj.GetOwn("setTimeout"); ok {
			return vmInst.Call(fn, socketSelf, a)
		}
		return reqSelf, nil
	}))

	rt.BeginExternalOp()
	go func() {
		defer rt.EndExternalOp()
		defer cancel()
		client := &http.Client{Transport: transport}
		resp, err := client.Do(httpReq)
		if err != nil {
			rt.ScheduleNextTick(func() {
				emitOnObject(vmInst, reqObj, "error", errorValueFromGo(vmInst, err))
			})
			return
		}
		incoming := buildIncomingMessage(vmInst, resp)
		rt.ScheduleNextTick(func() {
			emitOnObject(vmInst, reqObj, "response", vm.NewValueFromPlainObject(incoming))
		})
		pumpHTTPResponseBody(vmInst, resp, incoming)
	}()

	return reqSelf
}

// buildIncomingMessage is built on newReadableStream (emitter.go), not a
// bespoke object - a real Node IncomingMessage already is a Readable, and
// reusing it means setEncoding/destroy/pipe (round 66/67's own fixes) come
// along for free instead of needing a second copy of the same gaps.
func buildIncomingMessage(vmInst *vm.VM, resp *http.Response) *vm.PlainObject {
	obj := newReadableStream(vmInst)
	obj.SetOwn("statusCode", vm.NumberValue(float64(resp.StatusCode)))
	obj.SetOwn("statusMessage", vm.NewString(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)+" ")))
	obj.SetOwn("httpVersion", vm.NewString(strings.TrimPrefix(resp.Proto, "HTTP/")))
	headersObj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	for k, vs := range resp.Header {
		headersObj.SetOwn(strings.ToLower(k), vm.NewString(strings.Join(vs, ", ")))
	}
	obj.SetOwn("headers", vm.NewValueFromPlainObject(headersObj))
	return obj
}

// pumpHTTPResponseBody mirrors pumpSpawnStream's exact shape (child_process.go)
// deliberately - same real gap that round already fixed (a response that
// looks done before its data actually arrived) would recur here otherwise.
// Runs to completion inside the same goroutine that called client.Do(),
// not a separate one, so there is no ordering race to reason about at all.
func pumpHTTPResponseBody(vmInst *vm.VM, resp *http.Response, stream *vm.PlainObject) {
	defer resp.Body.Close()
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			// Real Node's IncomingMessage emits real Buffers by
			// default (only a string once setEncoding() has been
			// called) - this used to always emit a plain JS string,
			// found wrong the hard way chasing the real Bedrock
			// investigation (docs/real-node-plan.md, round 101): real
			// @smithy/node-http-handler's own streamCollector collects
			// chunks into an array and does Buffer.concat(chunks) on
			// it - concatenating a string produces zero bytes, not a
			// thrown error, so the response body silently came back
			// empty rather than failing loudly. wrapBuffer defensively
			// copies buf[:n] into a fresh ArrayBuffer, so reusing buf
			// across loop iterations is safe.
			scheduleEmit(vmInst, stream, "data", wrapBuffer(vmInst, buf[:n]))
		}
		if err != nil {
			if err != io.EOF {
				scheduleEmit(vmInst, stream, "error", vm.NewString(err.Error()))
			}
			scheduleEmit(vmInst, stream, "end")
			return
		}
	}
}

func errorValueFromGo(vmInst *vm.VM, err error) vm.Value {
	exception, built := vm.Undefined, false
	if errCtor, ok := vmInst.GetGlobal("Error"); ok {
		if v, cerr := vmInst.Construct(errCtor, []vm.Value{vm.NewString(err.Error())}); cerr == nil {
			exception, built = v, true
		}
	}
	if !built {
		return vm.NewString(err.Error())
	}
	return exception
}

func noopTrueFn() vm.Value {
	return vm.NewNativeFunction(0, true, "write", func(_ []vm.Value) (vm.Value, error) {
		return vm.True, nil
	})
}

func noopSelfFn(self vm.Value) vm.Value {
	return vm.NewNativeFunction(0, true, "noop", func(_ []vm.Value) (vm.Value, error) {
		return self, nil
	})
}
