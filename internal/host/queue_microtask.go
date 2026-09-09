package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// queue_microtask.go implements the real global `queueMicrotask(callback)`.
// Found missing while re-probing real undici after paserati#302 was fixed
// (round 75, docs/real-node-plan.md): real Node's own
// node:events#addAbortListener queues the listener as a microtask when the
// signal is already aborted, and that's a real call path once undici's
// fetch() reaches a request carrying an already-aborted signal.
//
// Built on the same real primitive as Promise.withResolvers
// (vmInst.NewPromiseFromExecutor) rather than anything synthetic: this
// resolves a real Promise immediately and hands `callback` to its real
// `.then`, which schedules it as a genuine microtask via the engine's own
// promise reaction-job queue - the same queue every `await` and
// `Promise.prototype.then` callback already runs on. This is pure Go, not
// EvalCode/RunCode, for the same reason file_global.go/
// promise_with_resolvers.go are (paserati#298, fixed upstream now, but
// there's no need to reintroduce an EvalCode dependency for something this
// small).
//
// Two real, documented deviations from spec, found by directly measuring
// ordering against setTimeout/Promise.resolve() rather than assuming:
//  1. A `callback` that throws becomes an unhandled promise rejection
//     (driven through `.then`) rather than an immediately-reported
//     uncaught exception. Not currently reachable by any real call site
//     this project has hit (addAbortListener's own callback is a plain
//     listener invocation, not expected to throw).
//  2. The callback runs one microtask tick later than V8's native
//     queueMicrotask (which needs only the single tick `.then` on an
//     already-resolved promise takes) - resolving the promise via a
//     Go-side executor call adds an extra scheduling hop. Still
//     correctly ordered *before* any macrotask (verified directly
//     against a setTimeout(...,10)), which is the guarantee real call
//     sites (addAbortListener's already-aborted branch) actually depend
//     on - just not tick-for-tick identical to native V8 timing.
func installQueueMicrotask(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	gobj := gt.AsPlainObject()
	if gobj == nil {
		return
	}
	if _, exists := gobj.GetOwn("queueMicrotask"); exists {
		return
	}

	gobj.SetOwn("queueMicrotask", vm.NewNativeFunction(1, false, "queueMicrotask", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsCallable() {
			return vm.Undefined, nil
		}
		callback := args[0]

		var resolveFn vm.Value
		executor := vm.NewNativeFunction(2, false, "executor", func(rr []vm.Value) (vm.Value, error) {
			if len(rr) > 0 {
				resolveFn = rr[0]
			}
			return vm.Undefined, nil
		})
		promise, err := vmInst.NewPromiseFromExecutor(executor)
		if err != nil {
			return vm.Undefined, err
		}
		if resolveFn.IsCallable() {
			if _, err := vmInst.Call(resolveFn, vm.Undefined, nil); err != nil {
				return vm.Undefined, err
			}
		}

		thenFn, err := vmInst.GetProperty(promise, "then")
		if err != nil {
			return vm.Undefined, err
		}
		if _, err := vmInst.Call(thenFn, promise, []vm.Value{callback}); err != nil {
			return vm.Undefined, err
		}
		return vm.Undefined, nil
	}))
}
