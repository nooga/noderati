package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// timeout_object.go wraps paserati's own global setTimeout/clearTimeout
// (driver.NewHostTimerInitializer, a real host.go/New() initializer - not
// anything noderati implements itself) so setTimeout(...) returns a real
// Node-shaped Timeout object instead of paserati's own plain numeric id.
//
// Found missing while re-probing real undici's fetch() after paserati#302
// was fixed (round 75, docs/real-node-plan.md): undici's own
// lib/util/timers.js#refreshTimeout does
// `fastNowTimeout = setTimeout(onTick, TICK_MS); fastNowTimeout?.unref()` -
// a real, unconditional call on every FastTimer construction (which
// dispatch() hits on every single request). Since a plain number has no
// `.unref` property, `numberValue?.unref` evaluates to undefined (the `?.`
// only guards against the *receiver* being nullish, not the looked-up
// property) and then calling it throws "undefined is not a function" -
// confirmed directly, not assumed, by isolating the crash to this exact
// property lookup before writing this fix.
//
// This wraps the real scheduling primitive rather than replacing it: the
// underlying numeric timer id from paserati's real setTimeout is kept in a
// Go closure per Timeout object (mutated in place by `.refresh()`), so
// every real tick/cancellation still goes through paserati's own real
// timer wheel - nothing about *when* callbacks fire is reimplemented
// here, only the object shape wrapped around the id. `.ref()`/`.unref()`
// are real, harmless no-ops that return `this` (matching the call
// signature real code depends on): paserati's timer initializer has no
// ref-counted keep-alive concept to hook into, so there's nothing for
// them to toggle - `.hasRef()` always reports true, honestly reflecting
// that.
func installTimeoutObjects(p *driver.Paserati) {
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

	realSetTimeout, ok := gobj.GetOwn("setTimeout")
	if !ok || !realSetTimeout.IsCallable() {
		return
	}
	realClearTimeout, ok := gobj.GetOwn("clearTimeout")
	if !ok || !realClearTimeout.IsCallable() {
		return
	}
	// Already wrapped (e.g. installTimeoutObjects called twice) - don't
	// double-wrap and lose the real underlying functions.
	if _, exists := gobj.GetOwn(timeoutClearMarkerKeyProbe); exists {
		return
	}
	gobj.SetOwnNonEnumerable(timeoutClearMarkerKeyProbe, vm.True)

	const clearMarkerKey = "__noderatiTimeoutClear"

	wrappedSetTimeout := vm.NewNativeFunction(1, true, "setTimeout", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, nil
		}
		callArgs := append([]vm.Value(nil), args...)

		id, err := vmInst.Call(realSetTimeout, vm.Undefined, callArgs)
		if err != nil {
			return vm.Undefined, err
		}

		currentID := id
		destroyed := false
		obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		self := vm.NewValueFromPlainObject(obj)

		obj.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
			return self, nil
		}))
		obj.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
			return self, nil
		}))
		obj.SetOwn("hasRef", vm.NewNativeFunction(0, false, "hasRef", func(_ []vm.Value) (vm.Value, error) {
			if destroyed {
				return vm.False, nil
			}
			return vm.True, nil
		}))
		obj.SetOwn("refresh", vm.NewNativeFunction(0, false, "refresh", func(_ []vm.Value) (vm.Value, error) {
			if destroyed {
				return self, nil
			}
			if _, err := vmInst.Call(realClearTimeout, vm.Undefined, []vm.Value{currentID}); err != nil {
				return vm.Undefined, err
			}
			newID, err := vmInst.Call(realSetTimeout, vm.Undefined, callArgs)
			if err != nil {
				return vm.Undefined, err
			}
			currentID = newID
			return self, nil
		}))
		obj.SetOwn("close", vm.NewNativeFunction(0, false, "close", func(_ []vm.Value) (vm.Value, error) {
			if !destroyed {
				destroyed = true
				if _, err := vmInst.Call(realClearTimeout, vm.Undefined, []vm.Value{currentID}); err != nil {
					return vm.Undefined, err
				}
			}
			return self, nil
		}))
		obj.SetOwnNonEnumerable(clearMarkerKey, vm.NewNativeFunction(0, false, "clear", func(_ []vm.Value) (vm.Value, error) {
			if !destroyed {
				destroyed = true
				return vmInst.Call(realClearTimeout, vm.Undefined, []vm.Value{currentID})
			}
			return vm.Undefined, nil
		}))

		return self, nil
	})

	wrappedClearTimeout := vm.NewNativeFunction(1, false, "clearTimeout", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, nil
		}
		arg := args[0]
		if arg.Type() == vm.TypeObject {
			if obj := arg.AsPlainObject(); obj != nil {
				if clearFn, exists := obj.GetOwn(clearMarkerKey); exists && clearFn.IsCallable() {
					return vmInst.Call(clearFn, arg, nil)
				}
			}
		}
		// Not one of our Timeout wrappers (e.g. a raw id from before this
		// install ran, or from code that bypassed the wrapper) - pass
		// through to the real clearTimeout unchanged.
		return vmInst.Call(realClearTimeout, vm.Undefined, args)
	})

	// Plain `gobj.SetOwn("setTimeout", ...)` here would silently NOT take
	// effect for bare `setTimeout(...)` calls in later scripts - found the
	// hard way by testing directly rather than assuming. setTimeout/
	// clearTimeout are core globals paserati's own HostTimerInitializer
	// registers with a dedicated compile-time-resolved heap slot (unlike
	// a brand-new name such as this file's own queueMicrotask, which has
	// no such slot and correctly falls back to a globalThis property
	// lookup for bare identifiers - confirmed both ways: GetGlobal("setTimeout")
	// and globalThis's own-property GetOwn("setTimeout") returned two
	// *different* underlying function values even right after `New()`,
	// proving they're backed by separate storage). Writing to globalThis's
	// own-property map via the Go PlainObject API only touches that
	// second, cold copy - bare-identifier reads keep resolving to the
	// original real setTimeout via the heap slot. A real
	// `globalThis.setTimeout = ...` assignment *executed as bytecode*,
	// by contrast, measurably does update what bare identifiers
	// subsequently resolve to (confirmed directly) - so the override
	// itself is done through one EvalCode call (safe now that
	// paserati#298, the second-eval exception-propagation bug, is fixed
	// upstream) rather than the raw Go property API used everywhere else
	// in this file.
	gobj.SetOwn("__noderatiWrappedSetTimeout", wrappedSetTimeout)
	gobj.SetOwn("__noderatiWrappedClearTimeout", wrappedClearTimeout)
	_, evalErrs := p.EvalCode(`
		globalThis.setTimeout = globalThis.__noderatiWrappedSetTimeout;
		globalThis.clearTimeout = globalThis.__noderatiWrappedClearTimeout;
		delete globalThis.__noderatiWrappedSetTimeout;
		delete globalThis.__noderatiWrappedClearTimeout;
	`, false)
	if len(evalErrs) > 0 {
		// Leave the real setTimeout/clearTimeout in place rather than a
		// half-applied override.
		gobj.SetOwn("__noderatiWrappedSetTimeout", vm.Undefined)
		gobj.SetOwn("__noderatiWrappedClearTimeout", vm.Undefined)
	}
}

// timeoutClearMarkerKeyProbe guards against double-wrapping if
// installTimeoutObjects were ever called twice.
const timeoutClearMarkerKeyProbe = "__noderatiTimeoutObjectsInstalled"
