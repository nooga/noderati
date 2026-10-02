package host

import (
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/runtime"
	"github.com/nooga/paserati/pkg/vm"
)

// timeout_object.go replaces paserati's own global setTimeout/clearTimeout
// (driver.NewHostTimerInitializer, which returns plain numeric ids) with
// Node's timer API: setTimeout/setInterval return a Timeout object and
// clearTimeout/clearInterval accept one (or its numeric id), all
// scheduled straight on the AsyncRuntime.
//
// History worth keeping:
//   - A Timeout object at all (round 75): real undici does
//     `fastNowTimeout = setTimeout(onTick, TICK_MS); fastNowTimeout?.unref()`
//     on every request, which threw on a plain number.
//   - Real .ref()/.unref() (round 87): an unref'd timer must not keep the
//     process alive; `rt.ScheduleUnrefTimer` is a timer excluded from
//     HasPendingWork, and ref/unref move the pending timer between it and
//     ScheduleTimer for the remaining delay.
//   - setInterval/clearInterval (round 151): they didn't exist at all.
//
// As in Node (lib/timers.js, lib/internal/timers.js): the callback must
// be a function (ERR_INVALID_ARG_TYPE otherwise); a delay that isn't in
// [1, 2^31-1] becomes 1; the callback runs with `this` = the Timeout;
// clearTimeout and clearInterval are interchangeable; +timeout (via
// Symbol.toPrimitive) is an id clearTimeout also accepts; and
// timeout[Symbol.dispose]() clears it.
type timerState struct {
	rt          runtime.AsyncRuntime
	id          uint64 // the runtime's current timer id
	asyncID     int64  // the id Symbol.toPrimitive reports
	delay       time.Duration
	repeat      bool
	refed       bool
	destroyed   bool
	scheduledAt time.Time
	deadline    time.Time
	fire        func() // runs the callback (and reschedules an interval)
	wake        func() // what the runtime timer calls: drains every due timer in order
	obj         *vm.PlainObject
}

const timeoutMaxMs = 2147483647

var timerAsyncIDs atomic.Int64

func (t *timerState) schedule(delay time.Duration) {
	t.scheduledAt = time.Now()
	t.deadline = t.scheduledAt.Add(delay)
	if t.refed {
		t.id = t.rt.ScheduleTimer(delay, t.wake)
	} else {
		t.id = t.rt.ScheduleUnrefTimer(delay, t.wake)
	}
}

func (t *timerState) remaining() time.Duration {
	r := t.delay - time.Since(t.scheduledAt)
	if r < 0 {
		return 0
	}
	return r
}

func (t *timerState) clear() {
	if !t.destroyed {
		t.destroyed = true
		t.rt.CancelTimer(t.id)
		t.obj.SetOwn("_idleTimeout", vm.NumberValue(-1))
		t.obj.SetOwn("_destroyed", vm.True)
	}
}

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
	realClearTimeout, ok := gobj.GetOwn("clearTimeout")
	if !ok || !realClearTimeout.IsCallable() {
		return
	}
	// Already installed (e.g. installTimeoutObjects called twice) - don't
	// wrap the wrappers.
	if _, exists := gobj.GetOwn(timeoutClearMarkerKeyProbe); exists {
		return
	}
	gobj.SetOwnNonEnumerable(timeoutClearMarkerKeyProbe, vm.True)

	rt := vmInst.GetAsyncRuntime()
	byAsyncID := map[int64]*timerState{}

	// drainDue runs every live timer whose deadline has passed, earliest
	// first and in creation order among equal deadlines (Node's FIFO rule).
	// The runtime's own due-timer order is effectively random
	// (paserati#564), so each runtime wakeup drains all due timers here
	// and later wakeups for already-run timers find nothing to do.
	drainDue := func() {
		now := time.Now()
		var due []*timerState
		for _, t := range byAsyncID {
			if !t.destroyed && !t.deadline.After(now) {
				due = append(due, t)
			}
		}
		sort.Slice(due, func(i, j int) bool {
			if !due[i].deadline.Equal(due[j].deadline) {
				return due[i].deadline.Before(due[j].deadline)
			}
			return due[i].asyncID < due[j].asyncID
		})
		for _, t := range due {
			// An earlier callback may have cleared or refreshed this one.
			if !t.destroyed && !t.deadline.After(now) {
				rt.CancelTimer(t.id)
				t.fire()
			}
		}
	}

	stateOf := func(v vm.Value) *timerState {
		if v.Type() != vm.TypeObject {
			return nil
		}
		st, _ := v.AsPlainObject().InternalSlots().(*timerState)
		return st
	}
	this := func() *timerState { return stateOf(vmInst.GetThis()) }

	proto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	protoVal := vm.NewValueFromPlainObject(proto)
	method := func(name string, fn func(t *timerState) vm.Value) {
		proto.SetOwnNonEnumerable(name, vm.NewNativeFunction(0, false, name, func(_ []vm.Value) (vm.Value, error) {
			self := vmInst.GetThis()
			if t := stateOf(self); t != nil {
				if r := fn(t); r != vm.Undefined {
					return r, nil
				}
			}
			return self, nil
		}))
	}
	method("ref", func(t *timerState) vm.Value {
		if !t.destroyed && !t.refed {
			rt.CancelTimer(t.id)
			t.refed = true
			t.id = rt.ScheduleTimer(t.remaining(), t.wake)
		}
		return vm.Undefined
	})
	method("unref", func(t *timerState) vm.Value {
		if !t.destroyed && t.refed {
			rt.CancelTimer(t.id)
			t.refed = false
			t.id = rt.ScheduleUnrefTimer(t.remaining(), t.wake)
		}
		return vm.Undefined
	})
	method("hasRef", func(t *timerState) vm.Value {
		return vm.BooleanValue(!t.destroyed && t.refed)
	})
	method("refresh", func(t *timerState) vm.Value {
		if !t.destroyed {
			rt.CancelTimer(t.id)
			t.schedule(t.delay)
		}
		return vm.Undefined
	})
	method("close", func(t *timerState) vm.Value {
		t.clear()
		delete(byAsyncID, t.asyncID)
		return vm.Undefined
	})
	toPrimitive := vm.NewNativeFunction(1, false, "[Symbol.toPrimitive]", func(_ []vm.Value) (vm.Value, error) {
		if t := this(); t != nil {
			return vm.NumberValue(float64(t.asyncID)), nil
		}
		return vm.NaN, nil
	})
	dispose := vm.NewNativeFunction(0, false, "[Symbol.dispose]", func(_ []vm.Value) (vm.Value, error) {
		if t := this(); t != nil {
			t.clear()
			delete(byAsyncID, t.asyncID)
		}
		return vm.Undefined, nil
	})
	for key, fn := range map[string]vm.Value{"toPrimitive": toPrimitive, "dispose": dispose} {
		if sym, err := vmInst.GetProperty(mustGlobal(vmInst, "Symbol"), key); err == nil {
			_ = jsDefineMethod(vmInst, protoVal, sym, fn)
		}
	}
	ctor := vm.NewConstructorWithProps(0, false, "Timeout", func(_ []vm.Value) (vm.Value, error) {
		return vm.Undefined, newNodeTypeError(vmInst, "ERR_ILLEGAL_CONSTRUCTOR", "Illegal constructor")
	})
	if props := ctor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", protoVal)
	}
	proto.SetOwnNonEnumerable("constructor", ctor)

	newTimer := func(repeat bool) vm.Value {
		name := "setTimeout"
		if repeat {
			name = "setInterval"
		}
		return vm.NewNativeFunction(1, true, name, func(args []vm.Value) (vm.Value, error) {
			fn := argAt(args, 0)
			if !fn.IsCallable() {
				return vm.Undefined, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE",
					`The "callback" argument must be of type function.`+receivedSuffix(vmInst, fn))
			}
			delayMs := 1.0
			if len(args) > 1 {
				if d := args[1].ToFloat(); d >= 1 && d <= timeoutMaxMs {
					delayMs = d
				}
			}
			var fnArgs []vm.Value
			if len(args) > 2 {
				fnArgs = append(fnArgs, args[2:]...)
			}
			obj := vm.NewObject(protoVal).AsPlainObject()
			self := vm.NewValueFromPlainObject(obj)
			t := &timerState{
				rt:      rt,
				asyncID: timerAsyncIDs.Add(1),
				delay:   time.Duration(math.Trunc(delayMs)) * time.Millisecond,
				repeat:  repeat,
				refed:   true,
			}
			t.wake = drainDue
			t.fire = func() {
				if t.destroyed {
					return
				}
				if !t.repeat {
					t.destroyed = true
					obj.SetOwn("_destroyed", vm.True)
					delete(byAsyncID, t.asyncID)
				}
				if _, err := vmInst.Call(fn, self, fnArgs); err != nil {
					reportUncaughtCallbackException(vmInst, err)
				}
				if t.repeat && !t.destroyed {
					t.schedule(t.delay)
				}
			}
			t.obj = obj
			// Node's own Timeout fields, which some libraries read.
			obj.SetOwn("_idleTimeout", vm.NumberValue(math.Trunc(delayMs)))
			obj.SetOwn("_onTimeout", fn)
			timerArgs := vm.Undefined
			if fnArgs != nil {
				timerArgs = vm.NewArrayWithArgs(fnArgs)
			}
			obj.SetOwn("_timerArgs", timerArgs)
			repeatVal := vm.Null
			if repeat {
				repeatVal = vm.NumberValue(math.Trunc(delayMs))
			}
			obj.SetOwn("_repeat", repeatVal)
			obj.SetOwn("_destroyed", vm.False)
			obj.SetInternalSlots(t)
			byAsyncID[t.asyncID] = t
			t.schedule(t.delay)
			return self, nil
		})
	}
	clearTimer := func(name string) vm.Value {
		return vm.NewNativeFunction(1, false, name, func(args []vm.Value) (vm.Value, error) {
			arg := argAt(args, 0)
			if t := stateOf(arg); t != nil {
				t.clear()
				delete(byAsyncID, t.asyncID)
				return vm.Undefined, nil
			}
			if arg.IsNumber() || arg.IsString() {
				if t, ok := byAsyncID[int64(arg.ToFloat())]; ok {
					t.clear()
					delete(byAsyncID, t.asyncID)
					return vm.Undefined, nil
				}
			}
			if arg.IsNumber() {
				// A raw id from paserati's own timers.
				return vmInst.Call(realClearTimeout, vm.Undefined, args)
			}
			return vm.Undefined, nil
		})
	}

	// setTimeout/clearTimeout are core globals with compile-time-resolved
	// heap slots, so writing globalThis's property map from Go doesn't
	// change what bare `setTimeout(...)` resolves to; a real
	// `globalThis.setTimeout = ...` assignment executed as JS does.
	gobj.SetOwn("__noderatiTimers", vm.NewArrayWithArgs([]vm.Value{
		newTimer(false), clearTimer("clearTimeout"), newTimer(true), clearTimer("clearInterval"),
	}))
	_, evalErrs := p.EvalCode(`
		globalThis.setTimeout = globalThis.__noderatiTimers[0];
		globalThis.clearTimeout = globalThis.__noderatiTimers[1];
		globalThis.setInterval = globalThis.__noderatiTimers[2];
		globalThis.clearInterval = globalThis.__noderatiTimers[3];
		delete globalThis.__noderatiTimers;
	`, false)
	if len(evalErrs) > 0 {
		gobj.SetOwn("__noderatiTimers", vm.Undefined)
	}
}

func mustGlobal(vmInst *vm.VM, name string) vm.Value {
	v, _ := vmInst.GetGlobal(name)
	return v
}

// timeoutClearMarkerKeyProbe guards against double-wrapping if
// installTimeoutObjects were ever called twice.
const timeoutClearMarkerKeyProbe = "__noderatiTimeoutObjectsInstalled"
