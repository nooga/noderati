package host

import (
	"sync/atomic"
	"time"

	"github.com/nooga/paserati/pkg/builtins"
	"github.com/nooga/paserati/pkg/driver"
	prt "github.com/nooga/paserati/pkg/runtime"
	"github.com/nooga/paserati/pkg/vm"
)

// async_context.go gives node:async_hooks its execution context: every
// callback the engine's async runtime schedules (promise jobs, nextTick,
// setImmediate, timers) is wrapped so it runs inside the frame that was
// current when it was scheduled. A frame carries the async id, trigger
// id, resource object and the AsyncLocalStorage store map (an opaque JS
// value owned by the async_hooks shim).
//
// Known gaps, both needing engine support to close:
//   - The engine schedules a promise's reaction jobs when the promise
//     settles, not when then()/await registers them, so a reaction
//     inherits the context of whatever settled the promise. That is the
//     same context in the usual case (a timer or callback created inside
//     run() settles it).
//   - Work a host goroutine schedules has no context of its own. While the
//     VM thread is parked waiting for events that is detected exactly and
//     such work starts in the root context; but a goroutine scheduling
//     while the VM thread is busy cannot be told apart from the VM thread
//     itself cheaply (identifying the goroutine means unwinding through
//     the interpreter loop, ~100us per call), so it inherits the frame
//     running at that moment.

type asyncFrame struct {
	id       int64
	trigger  int64
	kind     string
	store    vm.Value // the ALS store map, or Undefined
	resource vm.Value // lazily created executionAsyncResource() object
}

type asyncContextRuntime struct {
	prt.AsyncRuntime
	vmInst  *vm.VM
	enabled atomic.Bool
	hooks   atomic.Bool
	parked  atomic.Bool // the VM thread is blocked waiting for events
	inHook  bool
	cur     atomic.Pointer[asyncFrame]
	nextID  atomic.Int64
	// dispatch is the shim's hook dispatcher: (event, asyncId, type,
	// triggerAsyncId, resource).
	dispatch vm.Value
}

func (r *asyncContextRuntime) WaitForIdleProgress() {
	r.parked.Store(true)
	defer r.parked.Store(false)
	r.AsyncRuntime.WaitForIdleProgress()
}

func (r *asyncContextRuntime) WaitForExternalOp() {
	r.parked.Store(true)
	defer r.parked.Store(false)
	r.AsyncRuntime.WaitForExternalOp()
}

func (r *asyncContextRuntime) wrap(kind string, cb func()) func() {
	if !r.enabled.Load() {
		return cb
	}
	parent := r.cur.Load()
	store, trigger := parent.store, parent.id
	if r.parked.Load() {
		// Only a host goroutine can be scheduling while the VM is parked.
		store, trigger = vm.Undefined, 1
	}
	f := &asyncFrame{id: r.nextID.Add(1), trigger: trigger, kind: kind, store: store, resource: vm.Undefined}
	// Hooks only ever run on the VM thread, so init is reported when the
	// callback starts rather than when (possibly off-thread) it's queued.
	return func() {
		prev := r.cur.Swap(f)
		fire := r.hooks.Load()
		if fire {
			r.emit("init", f)
			r.emit("before", f)
		}
		cb()
		if fire {
			r.emit("after", f)
			r.emit("destroy", f)
		}
		r.cur.Store(prev)
	}
}

// bindAsyncContext returns cb wrapped to run in the current async context,
// for host code that dispatches several JS callbacks from one runtime job.
func bindAsyncContext(rt prt.AsyncRuntime, kind string, cb func()) func() {
	if r, ok := rt.(*asyncContextRuntime); ok {
		return r.wrap(kind, cb)
	}
	return cb
}

func (r *asyncContextRuntime) emit(event string, f *asyncFrame) {
	// Work a hook itself schedules is not reported back to the hooks.
	if !r.dispatch.IsCallable() || r.inHook {
		return
	}
	r.inHook = true
	defer func() { r.inHook = false }()
	_, _ = r.vmInst.Call(r.dispatch, vm.Undefined, []vm.Value{
		vm.NewString(event), vm.NumberValue(float64(f.id)), vm.NewString(f.kind),
		vm.NumberValue(float64(f.trigger)), r.resourceOf(f),
	})
}

func (r *asyncContextRuntime) resourceOf(f *asyncFrame) vm.Value {
	if f.resource.IsUndefined() {
		f.resource = vm.NewObject(r.vmInst.ObjectPrototype)
	}
	return f.resource
}

func (r *asyncContextRuntime) ScheduleMicrotask(cb func()) {
	r.AsyncRuntime.ScheduleMicrotask(r.wrap("PROMISE", cb))
}
func (r *asyncContextRuntime) ScheduleNextTick(cb func()) {
	r.AsyncRuntime.ScheduleNextTick(r.wrap("TickObject", cb))
}
func (r *asyncContextRuntime) ScheduleMacrotask(cb func()) {
	r.AsyncRuntime.ScheduleMacrotask(r.wrap("Immediate", cb))
}
func (r *asyncContextRuntime) ScheduleTimer(d time.Duration, cb func()) uint64 {
	return r.AsyncRuntime.ScheduleTimer(d, r.wrap("Timeout", cb))
}
func (r *asyncContextRuntime) ScheduleUnrefTimer(d time.Duration, cb func()) uint64 {
	return r.AsyncRuntime.ScheduleUnrefTimer(d, r.wrap("Timeout", cb))
}

// asyncContextInitializer wraps the VM's async runtime before any other
// initializer can capture it (process.nextTick, timers and others keep
// the runtime they saw at init time).
type asyncContextInitializer struct{}

func (asyncContextInitializer) Name() string                          { return "noderati-async-context" }
func (asyncContextInitializer) Priority() int                         { return -1000 }
func (asyncContextInitializer) InitTypes(*builtins.TypeContext) error { return nil }
func (asyncContextInitializer) InitRuntime(ctx *builtins.RuntimeContext) error {
	vmInst := ctx.VM
	if _, ok := vmInst.GetAsyncRuntime().(*asyncContextRuntime); ok {
		return nil
	}
	r := &asyncContextRuntime{AsyncRuntime: vmInst.GetAsyncRuntime(), vmInst: vmInst, dispatch: vm.Undefined}
	r.nextID.Store(1)
	r.cur.Store(&asyncFrame{id: 1, trigger: 0, store: vm.Undefined, resource: vm.Undefined})
	vmInst.SetAsyncRuntime(r)
	return nil
}

// installAsyncContextAPI exposes the frame operations to the async_hooks
// shim as globalThis.__noderatiAsyncContext.
func installAsyncContextAPI(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	r, ok := vmInst.GetAsyncRuntime().(*asyncContextRuntime)
	if !ok {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok || gt.AsPlainObject() == nil {
		return
	}
	api := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	fn := func(name string, arity int, f func(args []vm.Value) vm.Value) {
		api.SetOwn(name, vm.NewNativeFunction(arity, false, name, func(args []vm.Value) (vm.Value, error) {
			return f(args), nil
		}))
	}
	fn("enable", 0, func(_ []vm.Value) vm.Value {
		r.enabled.Store(true)
		return vm.Undefined
	})
	fn("getStore", 0, func(_ []vm.Value) vm.Value { return r.cur.Load().store })
	// setStore replaces the current frame (frames are immutable once
	// captured by scheduled callbacks) and returns the previous one's
	// handle, for restoreFrame.
	fn("setStore", 1, func(args []vm.Value) vm.Value {
		cur := r.cur.Load()
		next := *cur
		next.store = argAt(args, 0)
		r.cur.Store(&next)
		return vm.Undefined
	})
	fn("executionAsyncId", 0, func(_ []vm.Value) vm.Value { return vm.NumberValue(float64(r.cur.Load().id)) })
	fn("triggerAsyncId", 0, func(_ []vm.Value) vm.Value { return vm.NumberValue(float64(r.cur.Load().trigger)) })
	fn("executionAsyncResource", 0, func(_ []vm.Value) vm.Value { return r.resourceOf(r.cur.Load()) })
	fn("newAsyncId", 0, func(_ []vm.Value) vm.Value { return vm.NumberValue(float64(r.nextID.Add(1))) })
	fn("setHooks", 1, func(args []vm.Value) vm.Value {
		r.dispatch = argAt(args, 0)
		r.hooks.Store(r.dispatch.IsCallable())
		if r.dispatch.IsCallable() {
			r.enabled.Store(true)
		}
		return vm.Undefined
	})
	// runInFrame(id, trigger, store, resource, fn, thisArg, args): what
	// AsyncResource.runInAsyncScope and the bind helpers run inside.
	api.SetOwn("runInFrame", vm.NewNativeFunction(7, false, "runInFrame", func(args []vm.Value) (vm.Value, error) {
		f := &asyncFrame{
			id: int64(argAt(args, 0).ToFloat()), trigger: int64(argAt(args, 1).ToFloat()),
			kind: "AsyncResource", store: argAt(args, 2), resource: argAt(args, 3),
		}
		var callArgs []vm.Value
		if a := argAt(args, 6); a.Type() == vm.TypeArray {
			arr := a.AsArray()
			for i := 0; i < arr.Length(); i++ {
				callArgs = append(callArgs, arr.Get(i))
			}
		}
		prev := r.cur.Swap(f)
		defer r.cur.Store(prev)
		return vmInst.Call(argAt(args, 4), argAt(args, 5), callArgs)
	}))
	gt.AsPlainObject().SetOwnNonEnumerable("__noderatiAsyncContext", vm.NewValueFromPlainObject(api))
}
