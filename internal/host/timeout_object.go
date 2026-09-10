package host

import (
	"time"

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
// here, only the object shape wrapped around the id.
//
// `.ref()`/`.unref()` are REAL, not no-ops (round 87, docs/real-node-plan.md
// - found while chasing a *consistently reproducible* ~4s tail latency
// after real undici's fetch() E2E probe's own script had already
// finished and printed "ALL DONE": undici's own timers.js calls
// `.unref()` on its idle keep-alive timer specifically so that timer
// alone can't keep the process alive, but this file's own `.unref()`
// used to be a pure no-op with a comment claiming "paserati's timer
// initializer has no ref-counted keep-alive concept to hook into" -
// which turned out to be wrong: `pkg/runtime.AsyncRuntime` already
// exports `ScheduleUnrefTimer` (a timer that, while pending, is
// excluded from `HasPendingWork`/`HasPendingTimers`, so a drain loop
// with nothing else outstanding doesn't wait out its delay) - it was
// simply never wired up to this JS-visible `.unref()`/`.ref()` pair.
// Confirmed the gap directly with a minimal, undici-free repro before
// fixing it: a bare `setTimeout(fn, 4000).unref()` with nothing else
// scheduled still blocked process exit for the full 4 seconds *and*
// still fired the callback - real Node does neither once a timer is
// genuinely unref'd with no other keep-alive work pending.
//
// The fix reschedules the *same* callback and *remaining* delay
// through `rt.ScheduleUnrefTimer`/`rt.ScheduleTimer` directly
// (bypassing the JS-level `realSetTimeout` global, which only ever
// calls the always-ref'd `ScheduleTimer`) whenever `.unref()`/`.ref()`
// actually flips the ref state, canceling whichever timer id is
// currently outstanding first - `pkg/driver/host_timers.go`'s own
// `clearTimeout` is a plain `rt.CancelTimer(id)` on the same id space
// `ScheduleTimer`/`ScheduleUnrefTimer` both hand out, so `currentID`
// stays valid for `.close()`/`clearTimeout()` regardless of which of
// the two scheduled it most recently. `.hasRef()` reports the real
// current state instead of always `true`. Only wired up when the
// first argument is actually callable (matching real setTimeout's own
// contract) - an uncallable first argument falls back to the
// pre-existing, harmless-no-op behavior, since there is no real
// callback to safely reschedule.
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

	rt := vmInst.GetAsyncRuntime()

	wrappedSetTimeout := vm.NewNativeFunction(1, true, "setTimeout", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, nil
		}
		callArgs := append([]vm.Value(nil), args...)

		// schedulable mirrors host_timers.go's own setTimeoutFn contract
		// exactly (a non-callable first argument schedules nothing real,
		// returning id 0) - only a genuinely schedulable timer gets the
		// real ref/unref treatment below; the non-schedulable case keeps
		// the pre-existing behavior (forward to realSetTimeout, no-op
		// ref/unref) since there's no real callback to reschedule.
		schedulable := args[0].IsCallable()

		destroyed := false
		obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		self := vm.NewValueFromPlainObject(obj)

		if !schedulable {
			id, err := vmInst.Call(realSetTimeout, vm.Undefined, callArgs)
			if err != nil {
				return vm.Undefined, err
			}
			currentID := id
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
		}

		// Real, ref-counted path: schedule and reschedule directly
		// through the AsyncRuntime (bypassing the always-ref'd
		// realSetTimeout global) so .unref()/.ref() can actually move
		// this timer between ScheduleTimer and ScheduleUnrefTimer - see
		// this function's own doc comment above for why that matters.
		fn := args[0]
		delayMs := 0.0
		if len(args) > 1 {
			delayMs = args[1].ToFloat()
			if delayMs < 0 {
				delayMs = 0
			}
		}
		fnArgs := append([]vm.Value(nil), args[2:]...)
		callback := func() {
			_, _ = vmInst.Call(fn, vm.Undefined, fnArgs)
		}

		scheduledAt := time.Now()
		refed := true
		currentID := rt.ScheduleTimer(time.Duration(delayMs)*time.Millisecond, callback)

		remainingDelay := func() time.Duration {
			remaining := delayMs - float64(time.Since(scheduledAt).Milliseconds())
			if remaining < 0 {
				remaining = 0
			}
			return time.Duration(remaining) * time.Millisecond
		}

		obj.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
			if destroyed || refed {
				return self, nil
			}
			rt.CancelTimer(currentID)
			currentID = rt.ScheduleTimer(remainingDelay(), callback)
			refed = true
			return self, nil
		}))
		obj.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
			if destroyed || !refed {
				return self, nil
			}
			rt.CancelTimer(currentID)
			currentID = rt.ScheduleUnrefTimer(remainingDelay(), callback)
			refed = false
			return self, nil
		}))
		obj.SetOwn("hasRef", vm.NewNativeFunction(0, false, "hasRef", func(_ []vm.Value) (vm.Value, error) {
			if destroyed {
				return vm.False, nil
			}
			return vm.BooleanValue(refed), nil
		}))
		obj.SetOwn("refresh", vm.NewNativeFunction(0, false, "refresh", func(_ []vm.Value) (vm.Value, error) {
			if destroyed {
				return self, nil
			}
			rt.CancelTimer(currentID)
			scheduledAt = time.Now()
			delay := time.Duration(delayMs) * time.Millisecond
			if refed {
				currentID = rt.ScheduleTimer(delay, callback)
			} else {
				currentID = rt.ScheduleUnrefTimer(delay, callback)
			}
			return self, nil
		}))
		obj.SetOwn("close", vm.NewNativeFunction(0, false, "close", func(_ []vm.Value) (vm.Value, error) {
			if !destroyed {
				destroyed = true
				rt.CancelTimer(currentID)
			}
			return self, nil
		}))
		obj.SetOwnNonEnumerable(clearMarkerKey, vm.NewNativeFunction(0, false, "clear", func(_ []vm.Value) (vm.Value, error) {
			if !destroyed {
				destroyed = true
				rt.CancelTimer(currentID)
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
