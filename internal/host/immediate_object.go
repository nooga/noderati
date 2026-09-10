package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// immediate_object.go implements the real global setImmediate/
// clearImmediate. Missing entirely from paserati (confirmed directly: no
// such global exists at all, unlike setTimeout/clearTimeout which come
// from paserati's own HostTimerInitializer) - found the hard way while
// probing real undici's fetch() end to end (round 79,
// docs/real-node-plan.md): client-h1.js's own onMessageComplete() calls
// `setImmediate(() => client[kResume]())` directly and unconditionally
// once a response finishes parsing, to let the current call stack unwind
// before pulling the next queued request off the pipeline - a real,
// unavoidable call site, reachable only once a response actually parses
// successfully (which nothing could reach before this round's other
// fixes).
//
// Built on the async runtime's own real macrotask queue
// (ScheduleMacrotask/RunMacrotasks - already part of paserati's
// AsyncRuntime interface and already driven by DrainUntilIdle, just
// never exposed to JS as setImmediate before now) rather than
// setTimeout(fn, 0): real Node's setImmediate callbacks run in the event
// loop's "check" phase, always after any timers already due in the same
// iteration - DrainUntilIdle's own loop order (RunDueTimers before
// RunMacrotasks) already matches that relative ordering for free, no
// extra work needed.
//
// clearImmediate(): ScheduleMacrotask has no native cancellation, so
// this uses the standard technique for that - the scheduled closure
// checks a `cancelled` flag before invoking the real callback, and
// clearImmediate just flips it. A cancelled Immediate still occupies a
// macrotask-queue slot until its turn comes; it just does nothing when
// it gets there - functionally equivalent to a real cancel from JS's own
// observable perspective, the only place this would ever matter.
func installSetImmediate(p *driver.Paserati) {
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
	if _, exists := gobj.GetOwn("setImmediate"); exists {
		return
	}

	rt := vmInst.GetAsyncRuntime()
	const clearMarkerKey = "__noderatiImmediateClear"

	gobj.SetOwn("setImmediate", vm.NewNativeFunction(1, true, "setImmediate", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsCallable() {
			return vm.Undefined, nil
		}
		callback := args[0]
		extraArgs := append([]vm.Value(nil), args[1:]...)

		cancelled := false
		obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		self := vm.NewValueFromPlainObject(obj)

		obj.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
			return self, nil
		}))
		obj.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
			return self, nil
		}))
		obj.SetOwn("hasRef", vm.NewNativeFunction(0, false, "hasRef", func(_ []vm.Value) (vm.Value, error) {
			return vm.BooleanValue(!cancelled), nil
		}))
		obj.SetOwnNonEnumerable(clearMarkerKey, vm.NewNativeFunction(0, false, "clear", func(_ []vm.Value) (vm.Value, error) {
			cancelled = true
			return vm.Undefined, nil
		}))

		// ScheduleMacrotask's callback runs from RunMacrotasks(), which
		// (like RunDueTimers()) only ever executes on the VM's own
		// interpreter goroutine via DrainUntilIdle - so calling
		// vmInst.Call directly here, with no ScheduleNextTick hop, is
		// safe (unlike callbacks invoked from a background goroutine,
		// which must defer any VM-value construction/call to a tick).
		// The returned error (a throwing callback) is deliberately
		// discarded, not silently dropped by oversight: confirmed
		// directly that paserati's own RunDueTimers() does the exact
		// same thing for a throwing setTimeout(fn, 0) callback (no
		// uncaughtException, no nonzero exit, no trace at all) - this
		// matches that existing engine-level house pattern for
		// DrainUntilIdle-driven callbacks rather than inventing a new,
		// inconsistent one for setImmediate alone. A real gap from
		// Node (which does report it), but a pre-existing, paserati-wide
		// one, not something introduced here - flagged, not fixed, per
		// this round's own actual scope.
		rt.ScheduleMacrotask(func() {
			if cancelled {
				return
			}
			_, _ = vmInst.Call(callback, vm.Undefined, extraArgs)
		})

		return self, nil
	}))

	gobj.SetOwn("clearImmediate", vm.NewNativeFunction(1, false, "clearImmediate", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || args[0].Type() != vm.TypeObject {
			return vm.Undefined, nil
		}
		obj := args[0].AsPlainObject()
		if obj == nil {
			return vm.Undefined, nil
		}
		if clearFn, ok := obj.GetOwn(clearMarkerKey); ok && clearFn.IsCallable() {
			_, _ = vmInst.Call(clearFn, args[0], nil)
		}
		return vm.Undefined, nil
	}))
}
