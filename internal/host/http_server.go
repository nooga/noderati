package host

import (
	"context"
	"maps"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/runtime"
	"github.com/nooga/paserati/pkg/vm"
)

// http_server.go implements node:http's server-side API (createServer/
// Server/IncomingMessage[server-side]/ServerResponse) on top of Go's own
// net/http.Server, the same deliberate deviation from Node's own
// architecture that http.go's client half already documents at its top:
// Node builds http.Server on net.Server's raw sockets it manages itself;
// this builds it on Go's net/http, which already gets HTTP/1.1 framing
// right, rather than re-parsing that protocol by hand over our own
// net.Server (net.go), which stays client-only. The two server halves
// (this file and a future net.createServer) are independent
// implementations, not layered on each other - matching the client side's
// existing http-on-net/http.Client vs net-on-net.Dial split.
//
// Added to get real Connect/Koa apps (`app.listen(port)`) running - see
// docs/real-node-plan.md's real-node-plan entry for this round.

// headerValuesFromValue reads a header value passed to setHeader()/
// writeHead() - a plain string in the common case, or an array of strings
// for a multi-value header like Set-Cookie, real Node's own contract for
// both. v.IsArray() is checked before ever calling v.AsArray() - unlike
// AsTypedArray's own nil-safe check, AsArray() panics on anything but
// TypeArray exactly, and setHeader("X-Test", "yes")'s plain-string value
// is by far the more common call shape, not the exceptional one.
func headerValuesFromValue(v vm.Value) []string {
	if !v.IsArray() {
		return []string{v.ToString()}
	}
	arr := v.AsArray()
	vals := make([]string, arr.Length())
	for i := 0; i < arr.Length(); i++ {
		vals[i] = arr.Get(i).ToString()
	}
	return vals
}

// srvWriteItem is one entry in a ServerResponse's write queue: enqueued
// synchronously by write()/end()/flushHeaders() (native calls running on
// the VM's own thread) and drained by the one background goroutine
// net/http spawned to run this request's handler - mirroring http.go's
// doHTTPRequest bodyCh pattern in reverse, for the same reason: a native
// call must never block the VM thread on network I/O it doesn't control
// the pace of.
type srvWriteItem struct {
	data  []byte
	isEnd bool
	cb    vm.Value

	// headerSnapshot/status are set only on the one item that transitions
	// headersSent from false to true (see markHeadersSent) - a full copy
	// of whatever the Go-side header map held at that instant, so a caller
	// that (incorrectly, post-send) mutates headers afterwards can't race
	// with what the draining goroutine is about to write.
	headerSnapshot http.Header
	status         int
}

// httpServerState is the Go-side half of a Server - obj (the JS-visible
// EventEmitter) never reaches back into this except through the native
// closures buildHTTPServerObject installs, matching socketState's own
// contract in net.go.
type httpServerState struct {
	vmInst *vm.VM
	rt     runtime.AsyncRuntime
	obj    *vm.PlainObject
	self   vm.Value

	mu        sync.Mutex
	listener  net.Listener
	goSrv     *http.Server
	listening bool
}

func doHTTPCreateServer(vmInst *vm.VM, requestListener vm.Value) vm.Value {
	obj := newEventEmitterObject(vmInst)
	self := vm.NewValueFromPlainObject(obj)
	srv := &httpServerState{vmInst: vmInst, rt: vmInst.GetAsyncRuntime(), obj: obj, self: self}

	if requestListener.IsCallable() {
		addListener(vmInst, obj, "request", requestListener, false, false)
	}

	obj.SetOwn("listening", vm.False)
	obj.SetOwn("maxHeadersCount", vm.NumberValue(2000))

	obj.SetOwn("listen", vm.NewNativeFunction(0, true, "listen", func(args []vm.Value) (vm.Value, error) {
		return srv.doListen(args), nil
	}))
	obj.SetOwn("close", vm.NewNativeFunction(1, true, "close", func(args []vm.Value) (vm.Value, error) {
		var cb vm.Value = vm.Undefined
		for _, a := range args {
			if a.IsCallable() {
				cb = a
				break
			}
		}
		srv.doClose(cb)
		return self, nil
	}))
	obj.SetOwn("address", vm.NewNativeFunction(0, false, "address", func(_ []vm.Value) (vm.Value, error) {
		srv.mu.Lock()
		l := srv.listener
		srv.mu.Unlock()
		if l == nil {
			return vm.Null, nil
		}
		return addrObject(vmInst, l.Addr()), nil
	}))
	// setTimeout/ref/unref: real Node's Server exposes these too (undici's
	// own test harnesses and most app frameworks never call them), kept as
	// honest no-ops/self-returns rather than left missing entirely, same
	// spirit as socketObj's cork/uncork in net.go.
	obj.SetOwn("setTimeout", vm.NewNativeFunction(2, false, "setTimeout", func(_ []vm.Value) (vm.Value, error) { return self, nil }))
	obj.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) { return self, nil }))
	obj.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) { return self, nil }))

	return self
}

// doListen parses listen()'s several real-Node overloads - listen(port),
// listen(port, cb), listen(port, host, cb), listen({port, host}, cb),
// listen(cb) (random port) - deliberately not supporting a pipe/UDS path
// argument (nothing in Connect/Koa's own listen() calls ever passes one).
// Binds synchronously, before returning, matching a real Go net.Listen
// call's own synchronous failure mode - EADDRINUSE must surface as an
// 'error' event, not a thrown exception, since Connect/Koa's own
// `app.listen(port)` callers never wrap that call in try/catch.
func (s *httpServerState) doListen(args []vm.Value) vm.Value {
	port := 0
	host := ""
	var cb vm.Value = vm.Undefined
	for _, a := range args {
		switch {
		case a.IsNumber():
			port = int(a.ToFloat())
		case a.IsCallable():
			cb = a
		case a.IsString():
			str := a.ToString()
			if n, err := strconv.Atoi(str); err == nil {
				port = n
			} else {
				host = str
			}
		default:
			// AsPlainObject() panics on anything but TypeObject exactly
			// (unlike AsTypedArray's own nil-safe check) - a bare
			// Type()==vm.TypeObject guard is required before calling it on
			// a value of unknown, caller-supplied shape.
			if a.Type() == vm.TypeObject {
				o := a.AsPlainObject()
				if v, ok := o.GetOwn("port"); ok && v.IsNumber() {
					port = int(v.ToFloat())
				}
				if v, ok := o.GetOwn("host"); ok && v.IsString() {
					host = v.ToString()
				}
			}
		}
	}

	if cb.IsCallable() {
		addListener(s.vmInst, s.obj, "listening", cb, true, false)
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.rt.ScheduleNextTick(func() {
			emitOnObject(s.vmInst, s.obj, "error", errorValueFromGo(s.vmInst, err))
		})
		return s.self
	}

	goSrv := &http.Server{Handler: http.HandlerFunc(s.handleRequest)}
	s.mu.Lock()
	s.listener = ln
	s.goSrv = goSrv
	s.listening = true
	s.mu.Unlock()

	// Held for the server's whole listening lifetime - released once
	// doClose's Shutdown() actually completes, not when close() is merely
	// called, matching the codebase's existing ref-counting discipline
	// for "does this keep the process alive" (socketState's own
	// beginTrackedExternalOp in net.go).
	s.rt.BeginExternalOp()

	go func() { _ = goSrv.Serve(ln) }()

	s.obj.SetOwn("listening", vm.True)
	s.rt.ScheduleNextTick(func() {
		emitOnObject(s.vmInst, s.obj, "listening")
	})
	return s.self
}

func (s *httpServerState) doClose(cb vm.Value) {
	s.mu.Lock()
	goSrv := s.goSrv
	wasListening := s.listening
	s.listening = false
	s.mu.Unlock()
	s.obj.SetOwn("listening", vm.False)

	if goSrv == nil || !wasListening {
		s.rt.ScheduleNextTick(func() {
			emitOnObject(s.vmInst, s.obj, "close")
			if cb.IsCallable() {
				_, _ = s.vmInst.Call(cb, s.self, nil)
			}
		})
		return
	}

	go func() {
		err := goSrv.Shutdown(context.Background())
		s.rt.EndExternalOp()
		s.rt.ScheduleNextTick(func() {
			emitOnObject(s.vmInst, s.obj, "close")
			if cb.IsCallable() {
				errArg := vm.Undefined
				if err != nil {
					errArg = errorValueFromGo(s.vmInst, err)
				}
				_, _ = s.vmInst.Call(cb, s.self, []vm.Value{errArg})
			}
		})
	}()
}

// handleRequest runs on one of Go's own net/http request goroutines - not
// the VM thread - for the whole lifetime of one HTTP request/response. It
// blocks (draining writeCh) until the JS side actually calls
// ServerResponse.end(), exactly mirroring how a real Node request handler
// keeps `res` alive until end() is called; the JS-visible 'request' event
// itself is only ever dispatched via ScheduleNextTick, never called
// directly from this goroutine, so it still only ever touches VM state on
// the VM's own thread.
func (s *httpServerState) handleRequest(w http.ResponseWriter, r *http.Request) {
	vmInst := s.vmInst
	rt := s.rt

	sockObj := newEventEmitterObject(vmInst)
	sockObj.SetOwn("writable", vm.True)
	sockObj.SetOwn("readable", vm.True)
	sockObj.SetOwn("destroyed", vm.False)
	if host, portStr, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		sockObj.SetOwn("remoteAddress", vm.NewString(host))
		if p, perr := strconv.Atoi(portStr); perr == nil {
			sockObj.SetOwn("remotePort", vm.NumberValue(float64(p)))
		}
	}
	sockSelf := vm.NewValueFromPlainObject(sockObj)

	reqObj, reqSelf := buildServerIncomingMessage(vmInst, r, sockSelf)
	writeCh := make(chan srvWriteItem, 64)
	resObj, resSelf := buildServerResponse(vmInst, sockSelf, writeCh)

	rt.ScheduleNextTick(func() {
		emitOnObject(vmInst, s.obj, "request", reqSelf, resSelf)
	})

	go pumpServerRequestBody(vmInst, r, reqObj)

	for item := range writeCh {
		if item.headerSnapshot != nil {
			maps.Copy(w.Header(), item.headerSnapshot)
			w.WriteHeader(item.status)
		}
		if len(item.data) > 0 {
			_, _ = w.Write(item.data)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if item.cb.IsCallable() {
			cb := item.cb
			rt.ScheduleNextTick(func() {
				_, _ = vmInst.Call(cb, resSelf, nil)
			})
		}
		if item.isEnd {
			break
		}
	}

	rt.ScheduleNextTick(func() {
		emitOnObject(vmInst, resObj, "finish")
	})
}

// pumpServerRequestBody streams the incoming request body into reqObj -
// mirroring pumpHTTPResponseBody's exact shape (http.go) deliberately, the
// same real gap that round already fixed (a stream that looks done before
// its data actually arrived) would recur here otherwise. A GET/HEAD
// request's body is simply an immediate EOF, so this is a fast no-op in
// the common case.
func pumpServerRequestBody(vmInst *vm.VM, r *http.Request, stream *vm.PlainObject) {
	defer r.Body.Close()
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			scheduleEmit(vmInst, stream, "data", wrapBuffer(vmInst, chunk))
		}
		if err != nil {
			stream.SetOwn("complete", vm.True)
			scheduleEmit(vmInst, stream, "end")
			return
		}
	}
}

// buildServerIncomingMessage builds the request-side IncomingMessage
// handed to a Server's 'request' listener. Built on newReadableStream
// (emitter.go) for the same reason buildIncomingMessage (http.go, the
// client-side response object) already is: a real Node IncomingMessage is
// always a Readable, so setEncoding/pipe/destroy come along for free
// instead of a second hand-rolled copy of the same gaps.
func buildServerIncomingMessage(vmInst *vm.VM, r *http.Request, socketSelf vm.Value) (*vm.PlainObject, vm.Value) {
	obj := newReadableStream(vmInst)
	self := vm.NewValueFromPlainObject(obj)

	obj.SetOwn("method", vm.NewString(r.Method))
	// req.url is path+query only, never an absolute URL - real Node's own
	// contract (Koa's ctx.path/ctx.query parse exactly this shape; an
	// absolute URL here breaks routing silently, no thrown error at all).
	obj.SetOwn("url", vm.NewString(r.URL.RequestURI()))

	proto := strings.TrimPrefix(r.Proto, "HTTP/")
	obj.SetOwn("httpVersion", vm.NewString(proto))
	major, minor := 1, 1
	if parts := strings.SplitN(proto, ".", 2); len(parts) == 2 {
		if n, err := strconv.Atoi(parts[0]); err == nil {
			major = n
		}
		if n, err := strconv.Atoi(parts[1]); err == nil {
			minor = n
		}
	}
	obj.SetOwn("httpVersionMajor", vm.NumberValue(float64(major)))
	obj.SetOwn("httpVersionMinor", vm.NumberValue(float64(minor)))

	headersObj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	for k, vs := range r.Header {
		headersObj.SetOwn(strings.ToLower(k), vm.NewString(strings.Join(vs, ", ")))
	}
	obj.SetOwn("headers", vm.NewValueFromPlainObject(headersObj))

	obj.SetOwn("socket", socketSelf)
	obj.SetOwn("connection", socketSelf)
	obj.SetOwn("complete", vm.False)

	return obj, self
}

// buildServerResponse builds the ServerResponse handed to a Server's
// 'request' listener. headersSent/writableEnded/finished all flip
// synchronously, on the VM thread, inside the native call that first
// triggers them (writeHead/write/end/flushHeaders) - matching real Node's
// own contract that headersSent becomes true the instant _storeHeader
// runs, not once bytes actually reach the OS socket. This is what lets
// Koa's own respond() (lib/application.js), which checks
// `res.headersSent` synchronously between successive res.end()/
// res.setHeader() calls, see consistent answers without needing to wait
// on the background goroutine that actually drains writeCh at all.
func buildServerResponse(vmInst *vm.VM, socketSelf vm.Value, writeCh chan<- srvWriteItem) (*vm.PlainObject, vm.Value) {
	obj := newEventEmitterObject(vmInst)
	self := vm.NewValueFromPlainObject(obj)

	var mu sync.Mutex
	headers := http.Header{}
	statusCode := 200
	headersSent := false
	ended := false

	obj.SetOwn("statusCode", vm.NumberValue(200))
	obj.SetOwn("statusMessage", vm.NewString(""))
	obj.SetOwn("headersSent", vm.False)
	obj.SetOwn("writableEnded", vm.False)
	obj.SetOwn("finished", vm.False)
	obj.SetOwn("socket", socketSelf)

	currentStatus := func() int {
		mu.Lock()
		defer mu.Unlock()
		if v, ok := obj.GetOwn("statusCode"); ok && v.IsNumber() {
			statusCode = int(v.ToFloat())
		}
		return statusCode
	}

	// markHeadersSent flips headersSent exactly once and returns a
	// snapshot of the Go-side header map to hand to the one writeCh item
	// that will actually apply it - nil on every later call, so write()/
	// end() calls after the first never re-copy (or re-send) headers.
	markHeadersSent := func() http.Header {
		mu.Lock()
		defer mu.Unlock()
		if headersSent {
			return nil
		}
		headersSent = true
		snapshot := headers.Clone()
		return snapshot
	}

	setEnded := func() {
		mu.Lock()
		ended = true
		mu.Unlock()
	}
	isEnded := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return ended
	}

	setHeaderValues := func(name string, vals []string) {
		mu.Lock()
		headers.Del(name)
		for _, v := range vals {
			headers.Add(name, v)
		}
		mu.Unlock()
	}

	obj.SetOwn("setHeader", vm.NewNativeFunction(2, false, "setHeader", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 2 {
			return self, nil
		}
		name := args[0].ToString()
		setHeaderValues(name, headerValuesFromValue(args[1]))
		return self, nil
	}))
	obj.SetOwn("appendHeader", vm.NewNativeFunction(2, false, "appendHeader", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 2 {
			return self, nil
		}
		mu.Lock()
		headers.Add(args[0].ToString(), args[1].ToString())
		mu.Unlock()
		return self, nil
	}))
	obj.SetOwn("getHeader", vm.NewNativeFunction(1, false, "getHeader", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, nil
		}
		mu.Lock()
		v := headers.Get(args[0].ToString())
		mu.Unlock()
		if v == "" {
			return vm.Undefined, nil
		}
		return vm.NewString(v), nil
	}))
	obj.SetOwn("hasHeader", vm.NewNativeFunction(1, false, "hasHeader", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.False, nil
		}
		mu.Lock()
		_, ok := headers[http.CanonicalHeaderKey(args[0].ToString())]
		mu.Unlock()
		return vm.BooleanValue(ok), nil
	}))
	obj.SetOwn("removeHeader", vm.NewNativeFunction(1, false, "removeHeader", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, nil
		}
		mu.Lock()
		headers.Del(args[0].ToString())
		mu.Unlock()
		return vm.Undefined, nil
	}))
	obj.SetOwn("getHeaders", vm.NewNativeFunction(0, false, "getHeaders", func(_ []vm.Value) (vm.Value, error) {
		out := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		mu.Lock()
		for k, vs := range headers {
			out.SetOwn(strings.ToLower(k), vm.NewString(strings.Join(vs, ", ")))
		}
		mu.Unlock()
		return vm.NewValueFromPlainObject(out), nil
	}))
	obj.SetOwn("getHeaderNames", vm.NewNativeFunction(0, false, "getHeaderNames", func(_ []vm.Value) (vm.Value, error) {
		out := vm.NewArray()
		arr := out.AsArray()
		mu.Lock()
		for k := range headers {
			arr.Append(vm.NewString(strings.ToLower(k)))
		}
		mu.Unlock()
		return out, nil
	}))

	obj.SetOwn("writeHead", vm.NewNativeFunction(1, true, "writeHead", func(args []vm.Value) (vm.Value, error) {
		if isEnded() {
			return self, nil
		}
		if len(args) > 0 && args[0].IsNumber() {
			mu.Lock()
			statusCode = int(args[0].ToFloat())
			mu.Unlock()
			obj.SetOwn("statusCode", vm.NumberValue(float64(statusCode)))
		}
		idx := 1
		if len(args) > 1 && args[1].IsString() {
			msg := args[1].ToString()
			obj.SetOwn("statusMessage", vm.NewString(msg))
			idx = 2
		}
		if len(args) > idx && args[idx].Type() == vm.TypeObject {
			hobj := args[idx].AsPlainObject()
			for _, k := range hobj.OwnKeys() {
				if v, ok := hobj.GetOwn(k); ok {
					setHeaderValues(k, headerValuesFromValue(v))
				}
			}
		}
		if snap := markHeadersSent(); snap != nil {
			obj.SetOwn("headersSent", vm.True)
			writeCh <- srvWriteItem{headerSnapshot: snap, status: currentStatus()}
		}
		return self, nil
	}))
	obj.SetOwn("flushHeaders", vm.NewNativeFunction(0, false, "flushHeaders", func(_ []vm.Value) (vm.Value, error) {
		// end() already closed writeCh once ended - sending into it here
		// would panic ("send on closed channel"). Koa exposes flushHeaders()
		// as ctx.flushHeaders() (response.js), reachable from app code, not
		// just internally, so this guard is load-bearing, not defensive
		// boilerplate: found by the advisor's review, not by any test in
		// this round's own suite.
		if isEnded() {
			return vm.Undefined, nil
		}
		if snap := markHeadersSent(); snap != nil {
			obj.SetOwn("headersSent", vm.True)
			writeCh <- srvWriteItem{headerSnapshot: snap, status: currentStatus()}
		} else {
			writeCh <- srvWriteItem{}
		}
		return vm.Undefined, nil
	}))

	obj.SetOwn("write", vm.NewNativeFunction(1, true, "write", func(args []vm.Value) (vm.Value, error) {
		if isEnded() {
			return vm.False, nil
		}
		var data []byte
		if len(args) > 0 && !args[0].IsUndefined() {
			data = valueToBytes(vmInst, args[0])
		}
		var cb vm.Value = vm.Undefined
		for _, a := range args[1:] {
			if a.IsCallable() {
				cb = a
				break
			}
		}
		item := srvWriteItem{data: data, cb: cb}
		if snap := markHeadersSent(); snap != nil {
			obj.SetOwn("headersSent", vm.True)
			item.headerSnapshot = snap
			item.status = currentStatus()
		}
		writeCh <- item
		return vm.True, nil
	}))

	obj.SetOwn("end", vm.NewNativeFunction(0, true, "end", func(args []vm.Value) (vm.Value, error) {
		if isEnded() {
			return self, nil
		}
		var data []byte
		var cb vm.Value = vm.Undefined
		for _, a := range args {
			if a.IsCallable() {
				cb = a
			} else if !a.IsUndefined() && a.Type() != vm.TypeNull {
				data = valueToBytes(vmInst, a)
			}
		}
		setEnded()
		obj.SetOwn("writableEnded", vm.True)
		obj.SetOwn("finished", vm.True)
		item := srvWriteItem{data: data, isEnd: true, cb: cb}
		if snap := markHeadersSent(); snap != nil {
			obj.SetOwn("headersSent", vm.True)
			item.headerSnapshot = snap
			item.status = currentStatus()
		}
		writeCh <- item
		close(writeCh)
		return self, nil
	}))

	return obj, self
}

func declareHTTPCreateServerNative(vmInst *vm.VM, obj *vm.PlainObject) {
	obj.SetOwn("__noderatiHTTPCreateServer", vm.NewNativeFunction(1, true, "__noderatiHTTPCreateServer", func(args []vm.Value) (vm.Value, error) {
		var requestListener vm.Value = vm.Undefined
		for _, a := range args {
			if a.IsCallable() {
				requestListener = a
				break
			}
		}
		return doHTTPCreateServer(vmInst, requestListener), nil
	}))
}

func installHTTPServerNatives(p *driver.Paserati) {
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
	declareHTTPCreateServerNative(vmInst, obj)
}
