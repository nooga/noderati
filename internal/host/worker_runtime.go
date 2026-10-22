package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nooga/paserati/pkg/driver"
	perrors "github.com/nooga/paserati/pkg/errors"
	prt "github.com/nooga/paserati/pkg/runtime"
	"github.com/nooga/paserati/pkg/vm"
)

// worker_runtime.go is the Go half of worker_threads: real Workers, each a
// separate paserati VM (its own host.New session) running its script on
// its own goroutine. VMs never share JS values: everything crossing a
// thread is a byte slice (node:v8 serialization done by the JS shim on
// each side), queued on the receiving VM's mailbox and delivered there by
// a macrotask, which is the only thing that ever touches that VM from
// outside its own goroutine (the async runtime's queues are mutex-guarded).

const workerShimSpec = "noderati-internal:worker"

func init() { registerJSShim(workerShimSpec, workerShim) }

type mail struct {
	kind string
	from int
	data []byte
}

// mailbox is one receiving end, owned by one VM. push is safe from any
// goroutine; deliver runs on the owner's thread.
type mailbox struct {
	mu        sync.Mutex
	rt        prt.AsyncRuntime
	deliver   func(m mail)
	queue     []mail
	scheduled bool
	closed    bool
	refs      int
	held      bool
}

// rawRuntime skips the async-context wrapper: work arriving from another
// thread starts in the root context, not whatever frame is current here.
func rawRuntime(rt prt.AsyncRuntime) prt.AsyncRuntime {
	if r, ok := rt.(*asyncContextRuntime); ok {
		return r.AsyncRuntime
	}
	return rt
}

func (m *mailbox) attach(vmInst *vm.VM, deliver func(mail)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rt = rawRuntime(vmInst.GetAsyncRuntime())
	m.deliver = deliver
	m.scheduleLocked()
	m.updateHoldLocked()
}

func (m *mailbox) scheduleLocked() {
	if m.rt == nil || m.scheduled || m.closed || len(m.queue) == 0 {
		return
	}
	m.scheduled = true
	m.rt.ScheduleMacrotask(m.drain)
}

func (m *mailbox) push(kind string, from int, data []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	m.queue = append(m.queue, mail{kind: kind, from: from, data: data})
	m.scheduleLocked()
	return true
}

func (m *mailbox) drain() {
	m.mu.Lock()
	m.scheduled = false
	q := m.queue
	m.queue = nil
	deliver := m.deliver
	m.mu.Unlock()
	for _, x := range q {
		m.mu.Lock()
		closed := m.closed
		m.mu.Unlock()
		if closed && x.kind != "exit" {
			continue
		}
		deliver(x)
	}
}

func (m *mailbox) updateHoldLocked() {
	want := m.rt != nil && m.refs > 0 && !m.closed
	if want && !m.held {
		m.held = true
		m.rt.BeginExternalOp()
	} else if !want && m.held {
		m.held = false
		m.rt.EndExternalOp()
	}
}

func (m *mailbox) ref() {
	m.mu.Lock()
	m.refs++
	m.updateHoldLocked()
	m.mu.Unlock()
}

func (m *mailbox) unref() {
	m.mu.Lock()
	if m.refs > 0 {
		m.refs--
	}
	m.updateHoldLocked()
	m.mu.Unlock()
}

func (m *mailbox) setRefs(n int) {
	m.mu.Lock()
	m.refs = n
	m.updateHoldLocked()
	m.mu.Unlock()
}

func (m *mailbox) close() {
	m.mu.Lock()
	m.closed = true
	m.queue = nil
	m.updateHoldLocked()
	m.mu.Unlock()
}

// wake makes a VM parked in its event loop look again (at its cancelled
// flag, after a terminate).
func (m *mailbox) wake() {
	m.mu.Lock()
	rt := m.rt
	m.mu.Unlock()
	if rt != nil {
		rt.ScheduleMacrotask(func() {})
	}
}

// threadState is what each VM knows about the thread it runs on.
type threadState struct {
	id     int
	p      *driver.Paserati
	worker *workerState // nil on the main thread
	inbox  *mailbox     // postMessageToThread deliveries
	lazy   *lazyJSModule
	root   *threadState // the main thread this one descends from (thread 0)
	mu     sync.Mutex
	subs   []*bcSub
}

var (
	threadsByVM  sync.Map // *vm.VM -> *threadState
	threadsByID  sync.Map // int -> *threadState
	nextThreadID atomic.Int64
)

func threadOf(vmInst *vm.VM) *threadState {
	if v, ok := threadsByVM.Load(vmInst); ok {
		return v.(*threadState)
	}
	return nil
}

func (ts *threadState) shim(name string) (vm.Value, error) {
	return ts.lazy.get(name)
}

func newThreadState(p *driver.Paserati, id int, w *workerState) *threadState {
	ts := &threadState{id: id, p: p, worker: w, inbox: &mailbox{}, lazy: &lazyJSModule{p: p, spec: workerShimSpec}}
	ts.root = ts
	if w != nil && w.root != nil {
		ts.root = w.root
	}
	vmInst := p.GetVM()
	ts.inbox.attach(vmInst, func(m mail) {
		fn, err := ts.shim("deliverThreadMessage")
		if err != nil {
			return
		}
		if _, err := vmInst.Call(fn, vm.Undefined, []vm.Value{wrapBuffer(vmInst, m.data), vm.NumberValue(float64(m.from))}); err != nil {
			reportUncaughtCallbackException(vmInst, err)
		}
	})
	threadsByVM.Store(vmInst, ts)
	return ts
}

// workerState links a Worker object in the parent with the child VM.
type workerState struct {
	id       int
	parentVM *vm.VM
	root     *threadState
	toParent *mailbox // in the parent: online/message/error/exit
	toChild  *mailbox // in the child: parentPort messages

	filename, code, name string
	isEval               bool
	argv                 []string
	workerData, env      []byte
	environmentData      []byte
	resourceLimits       []byte

	mu            sync.Mutex
	child         *driver.Paserati
	terminated    bool
	exitRequested bool
	errored       bool
	exitCode      int

	// parent thread only
	alive bool
	refed bool
}

func (ws *workerState) stopping() bool {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.terminated || ws.exitRequested || ws.errored
}

func (ws *workerState) cancelChild() {
	ws.mu.Lock()
	child := ws.child
	ws.mu.Unlock()
	if child != nil {
		child.GetVM().Cancel()
		rawRuntime(child.GetVM().GetAsyncRuntime()).ScheduleMacrotask(func() {})
	}
}

func (ws *workerState) updateParentHold() {
	if ws.alive && ws.refed {
		ws.toParent.setRefs(1)
	} else {
		ws.toParent.setRefs(0)
	}
}

// workerProcessExit is process.exit inside a worker: it stops that thread
// only. Reports whether vmInst is a worker.
func workerProcessExit(vmInst *vm.VM, code int) bool {
	ts := threadOf(vmInst)
	if ts == nil || ts.worker == nil {
		return false
	}
	ws := ts.worker
	ws.mu.Lock()
	if !ws.exitRequested {
		ws.exitRequested = true
		ws.exitCode = code
	}
	ws.mu.Unlock()
	vmInst.Cancel()
	return true
}

// workerUncaught handles an exception escaping a callback inside a
// worker: it becomes the Worker's 'error' event and ends the thread.
func workerUncaught(vmInst *vm.VM, err error) bool {
	ts := threadOf(vmInst)
	if ts == nil || ts.worker == nil {
		return false
	}
	ws := ts.worker
	if ws.stopping() {
		return true
	}
	ws.reportError(ts, vmInst.ExceptionValueFromError(err))
	return true
}

func (ws *workerState) reportError(ts *threadState, exc vm.Value) {
	vmInst := ts.p.GetVM()
	ws.mu.Lock()
	ws.errored = true
	ws.mu.Unlock()
	var data []byte
	if fn, err := ts.shim("encodeError"); err == nil {
		if buf, err := vmInst.Call(fn, vm.Undefined, []vm.Value{exc}); err == nil {
			data = append([]byte(nil), valueToBytes(vmInst, buf)...)
		}
	}
	ws.toParent.push("error", ts.id, data)
	vmInst.Cancel()
}

func workerExecPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return os.Args[0]
}

// run is the worker thread itself.
func (ws *workerState) run() {
	code := 0
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "noderati: worker %d crashed: %v\n", ws.id, r)
			code = 1
		}
		ws.toChild.close()
		ws.toParent.push("exit", ws.id, []byte(strconv.Itoa(code)))
	}()

	argv := append([]string{workerExecPath(), ws.filename}, ws.argv...)
	child := New(argv)
	child.SetSkipTypeCheck(true)
	cvm := child.GetVM()
	ts := newThreadState(child, ws.id, ws)
	threadsByID.Store(ws.id, ts)
	defer func() {
		threadsByID.Delete(ws.id)
		threadsByVM.Delete(cvm)
		ts.inbox.close()
		ts.mu.Lock()
		subs := ts.subs
		ts.subs = nil
		ts.mu.Unlock()
		for _, s := range subs {
			s.close()
		}
	}()

	ws.mu.Lock()
	ws.child = child
	terminated := ws.terminated
	ws.mu.Unlock()
	if terminated {
		code = 1
		return
	}

	cvm.SetUnhandledRejectionHandler(func(reason, _ vm.Value) {
		if !ws.stopping() {
			ws.reportError(ts, reason)
		}
	})
	installWorkerSelf(ws, ts)
	if err := ws.initChildModule(ts); err != nil {
		fmt.Fprintf(os.Stderr, "noderati: worker %d: %v\n", ws.id, err)
		code = 1
		return
	}
	ws.toParent.push("online", ws.id, nil)

	var errs []perrors.PaseratiError
	source := ws.code
	if ws.isEval {
		if looksLikeESMSource(source) {
			_, errs = child.RunCode(source, driver.RunOptions{ModuleName: filepath.Join(mustCwd(), "[worker eval].mjs")})
		} else {
			_, errs = RunCJS(child, source, filepath.Join(mustCwd(), "[worker eval]"))
		}
	} else {
		src, err := os.ReadFile(ws.filename)
		if err != nil {
			ws.reportError(ts, newJSError(cvm, err.Error()))
		} else {
			source = StripShebang(string(src))
			ext := strings.ToLower(filepath.Ext(ws.filename))
			if ext == ".mjs" || ext == ".ts" || ext == ".mts" || (ext != ".cjs" && looksLikeESMSource(source)) {
				_, errs = child.RunCode(source, driver.RunOptions{ModuleName: ws.filename})
			} else {
				_, errs = RunCJS(child, source, ws.filename)
			}
		}
	}
	if len(errs) > 0 && !ws.stopping() {
		ws.reportError(ts, thrownValue(cvm, errs[0]))
	}
	if !ws.stopping() {
		cvm.DrainUntilIdle()
	}

	ws.mu.Lock()
	switch {
	case ws.exitRequested:
		code = ws.exitCode
	case ws.errored, ws.terminated:
		code = 1
	default:
		code = ProcessExitCode(child)
	}
	ws.mu.Unlock()
}

func mustCwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "/"
	}
	return wd
}

// thrownValue recovers the JS value behind an error RunCode/RunCJS
// reported, building a real Error for compile errors.
func thrownValue(vmInst *vm.VM, perr perrors.PaseratiError) vm.Value {
	if rt, ok := perr.(*perrors.RuntimeError); ok && rt.Thrown != nil {
		if v, ok := rt.Thrown.(vm.Value); ok {
			return v
		}
	}
	ctorName := "Error"
	if _, ok := perr.(*perrors.SyntaxError); ok {
		ctorName = "SyntaxError"
	}
	if ctor, ok := vmInst.GetGlobal(ctorName); ok {
		if v, err := vmInst.Construct(ctor, []vm.Value{vm.NewString(perr.Message())}); err == nil {
			return v
		}
	}
	return newJSError(vmInst, perr.Message())
}

// installWorkerSelf gives the child's shim its end of the connection.
func installWorkerSelf(ws *workerState, ts *threadState) {
	vmInst := ts.p.GetVM()
	self := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	self.SetOwn("threadId", vm.NumberValue(float64(ws.id)))
	self.SetOwn("workerData", wrapBuffer(vmInst, ws.workerData))
	self.SetOwn("env", wrapBuffer(vmInst, ws.env))
	self.SetOwn("environmentData", wrapBuffer(vmInst, ws.environmentData))
	self.SetOwn("resourceLimits", wrapBuffer(vmInst, ws.resourceLimits))
	self.SetOwn("post", vm.NewNativeFunction(2, false, "post", func(args []vm.Value) (vm.Value, error) {
		data := append([]byte(nil), valueToBytes(vmInst, argAt(args, 1))...)
		ws.toParent.push(argAt(args, 0).ToString(), ws.id, data)
		return vm.Undefined, nil
	}))
	self.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
		ws.toChild.ref()
		return vm.Undefined, nil
	}))
	self.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
		ws.toChild.unref()
		return vm.Undefined, nil
	}))
	self.SetOwn("setDeliver", vm.NewNativeFunction(1, false, "setDeliver", func(args []vm.Value) (vm.Value, error) {
		fn := argAt(args, 0)
		ws.toChild.attach(vmInst, func(m mail) {
			if _, err := vmInst.Call(fn, vm.Undefined, []vm.Value{vm.NewString(m.kind), wrapBuffer(vmInst, m.data)}); err != nil {
				reportUncaughtCallbackException(vmInst, err)
			}
		})
		return vm.Undefined, nil
	}))
	vmInst.GlobalObject.SetOwn("__noderatiWorkerSelf", vm.NewValueFromPlainObject(self))
}

// initChildModule runs the shim's initChild and makes the child's
// worker_threads exports describe a worker thread.
func (ws *workerState) initChildModule(ts *threadState) error {
	vmInst := ts.p.GetVM()
	fn, err := ts.shim("initChild")
	if err != nil {
		return err
	}
	res, err := vmInst.Call(fn, vm.Undefined, nil)
	if err != nil {
		return err
	}
	rec, err := ts.p.LoadModule("worker_threads", ".")
	if err != nil {
		return err
	}
	exports := rec.GetExportValues()
	set := func(name string, v vm.Value) {
		exports[name] = v
		if d, ok := exports["default"]; ok && d.Type() == vm.TypeObject {
			d.AsPlainObject().SetOwn(name, v)
		}
	}
	get := func(name string) vm.Value {
		v, _ := objOption(res, name)
		return v
	}
	set("isMainThread", vm.False)
	set("parentPort", get("parentPort"))
	set("workerData", get("workerData"))
	set("resourceLimits", get("resourceLimits"))
	set("threadId", vm.NumberValue(float64(ws.id)))
	set("threadName", vm.NewString(ws.name))
	return nil
}

// --- BroadcastChannel ---

// Channels are scoped to one process tree (a main thread and its
// workers), so separate host sessions in one Go process never cross-talk.
type bcKey struct {
	root *threadState
	name string
}

type bcSub struct {
	key   bcKey
	mb    *mailbox
	owner *threadState
}

var (
	bcMu   sync.Mutex
	bcSubs = map[bcKey][]*bcSub{}
)

func (s *bcSub) close() {
	bcMu.Lock()
	list := bcSubs[s.key]
	for i, x := range list {
		if x == s {
			bcSubs[s.key] = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	if len(bcSubs[s.key]) == 0 {
		delete(bcSubs, s.key)
	}
	bcMu.Unlock()
	s.mb.close()
}

func (s *bcSub) post(data []byte) {
	bcMu.Lock()
	targets := append([]*bcSub(nil), bcSubs[s.key]...)
	bcMu.Unlock()
	for _, t := range targets {
		if t != s {
			t.mb.push("message", s.owner.id, data)
		}
	}
}

// --- the native object the shim talks to ---

func installWorkerNative(p *driver.Paserati) {
	vmInst := p.GetVM()
	ts := threadOf(vmInst)
	if ts == nil {
		newThreadState(p, 0, nil)
	}
	native := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	native.SetOwn("SHARE_ENV", workerShareEnvSymbol(vmInst))
	native.SetOwn("resolvePath", vm.NewNativeFunction(1, false, "resolvePath", func(args []vm.Value) (vm.Value, error) {
		abs, err := filepath.Abs(argAt(args, 0).ToString())
		if err != nil {
			return argAt(args, 0), nil
		}
		return vm.NewString(abs), nil
	}))
	native.SetOwn("spawn", vm.NewNativeFunction(1, false, "spawn", func(args []vm.Value) (vm.Value, error) {
		return spawnWorker(vmInst, argAt(args, 0)), nil
	}))
	native.SetOwn("postToThread", vm.NewNativeFunction(2, false, "postToThread", func(args []vm.Value) (vm.Value, error) {
		id := int(argAt(args, 0).ToFloat())
		cur := threadOf(vmInst)
		if cur == nil {
			return vm.False, nil
		}
		var target *threadState
		if id == 0 {
			target = cur.root
		} else if v, ok := threadsByID.Load(id); ok {
			target = v.(*threadState)
		}
		if target == nil || target.root != cur.root {
			return vm.False, nil
		}
		data := append([]byte(nil), valueToBytes(vmInst, argAt(args, 1))...)
		return vm.BooleanValue(target.inbox.push("thread", cur.id, data)), nil
	}))
	native.SetOwn("broadcastOpen", vm.NewNativeFunction(2, false, "broadcastOpen", func(args []vm.Value) (vm.Value, error) {
		return openBroadcast(vmInst, argAt(args, 0).ToString(), argAt(args, 1)), nil
	}))
	vmInst.GlobalObject.SetOwn("__noderatiWorkerNative", vm.NewValueFromPlainObject(native))
}

var shareEnvSymbols sync.Map // *vm.VM -> vm.Value

func workerShareEnvSymbol(vmInst *vm.VM) vm.Value {
	if v, ok := shareEnvSymbols.Load(vmInst); ok {
		return v.(vm.Value)
	}
	sym := vm.NewSymbol("nodejs.worker_threads.SHARE_ENV")
	v, _ := shareEnvSymbols.LoadOrStore(vmInst, sym)
	return v.(vm.Value)
}

func openBroadcast(vmInst *vm.VM, name string, cb vm.Value) vm.Value {
	ts := threadOf(vmInst)
	s := &bcSub{key: bcKey{root: ts.root, name: name}, mb: &mailbox{refs: 1}, owner: ts}
	s.mb.attach(vmInst, func(m mail) {
		if _, err := vmInst.Call(cb, vm.Undefined, []vm.Value{wrapBuffer(vmInst, m.data)}); err != nil {
			reportUncaughtCallbackException(vmInst, err)
		}
	})
	bcMu.Lock()
	bcSubs[s.key] = append(bcSubs[s.key], s)
	bcMu.Unlock()
	ts.mu.Lock()
	ts.subs = append(ts.subs, s)
	ts.mu.Unlock()
	h := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	h.SetOwn("post", vm.NewNativeFunction(1, false, "post", func(args []vm.Value) (vm.Value, error) {
		s.post(append([]byte(nil), valueToBytes(vmInst, argAt(args, 0))...))
		return vm.Undefined, nil
	}))
	h.SetOwn("close", vm.NewNativeFunction(0, false, "close", func(_ []vm.Value) (vm.Value, error) {
		s.close()
		return vm.Undefined, nil
	}))
	h.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
		s.mb.setRefs(1)
		return vm.Undefined, nil
	}))
	h.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
		s.mb.setRefs(0)
		return vm.Undefined, nil
	}))
	return vm.NewValueFromPlainObject(h)
}

func optBytes(vmInst *vm.VM, opts vm.Value, name string) []byte {
	v, _ := objOption(opts, name)
	return append([]byte(nil), valueToBytes(vmInst, v)...)
}

func spawnWorker(vmInst *vm.VM, opts vm.Value) vm.Value {
	str := func(name string) string {
		v, _ := objOption(opts, name)
		if v.IsUndefined() {
			return ""
		}
		return v.ToString()
	}
	ws := &workerState{
		id:              int(nextThreadID.Add(1)),
		parentVM:        vmInst,
		root:            threadOf(vmInst).root,
		toParent:        &mailbox{},
		toChild:         &mailbox{},
		filename:        str("filename"),
		code:            str("code"),
		name:            str("name"),
		workerData:      optBytes(vmInst, opts, "workerData"),
		env:             optBytes(vmInst, opts, "env"),
		environmentData: optBytes(vmInst, opts, "environmentData"),
		resourceLimits:  optBytes(vmInst, opts, "resourceLimits"),
		alive:           true,
		refed:           true,
	}
	if ws.name == "" {
		ws.name = "WorkerThread"
	}
	if v, _ := objOption(opts, "isEval"); v.IsTruthy() {
		ws.isEval = true
	}
	if v, _ := objOption(opts, "argv"); v.Type() == vm.TypeArray {
		for _, a := range arrayValues(v.AsArray()) {
			ws.argv = append(ws.argv, a.ToString())
		}
	}
	deliver, _ := objOption(opts, "deliver")
	ws.toParent.attach(vmInst, func(m mail) {
		if m.kind == "exit" {
			ws.alive = false
			ws.updateParentHold()
			ws.toParent.close()
		}
		if _, err := vmInst.Call(deliver, vm.Undefined, []vm.Value{vm.NewString(m.kind), wrapBuffer(vmInst, m.data)}); err != nil {
			reportUncaughtCallbackException(vmInst, err)
		}
	})
	ws.updateParentHold()

	h := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	h.SetOwn("threadId", vm.NumberValue(float64(ws.id)))
	h.SetOwn("post", vm.NewNativeFunction(1, false, "post", func(args []vm.Value) (vm.Value, error) {
		ws.toChild.push("message", 0, append([]byte(nil), valueToBytes(vmInst, argAt(args, 0))...))
		return vm.Undefined, nil
	}))
	h.SetOwn("terminate", vm.NewNativeFunction(0, false, "terminate", func(_ []vm.Value) (vm.Value, error) {
		ws.mu.Lock()
		ws.terminated = true
		ws.mu.Unlock()
		ws.cancelChild()
		return vm.Undefined, nil
	}))
	h.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
		ws.refed = true
		ws.updateParentHold()
		return vm.Undefined, nil
	}))
	h.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
		ws.refed = false
		ws.updateParentHold()
		return vm.Undefined, nil
	}))
	go ws.run()
	return vm.NewValueFromPlainObject(h)
}
