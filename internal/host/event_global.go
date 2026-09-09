package host

import (
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// event_global.go implements the WHATWG `Event`/`EventTarget`/
// `CustomEvent` globals - found missing the same way `File`/
// `MessagePort` were (round 73, docs/real-node-plan.md): real undici's
// lib/web/websocket/events.js does `class MessageEvent extends Event`
// and `class CloseEvent extends Event` at module top level, so a
// missing `Event` throws immediately at require() time. Grepped every
// real Event/EventTarget reference across the vendored package first:
// every real construction/subclass site is confined to
// lib/web/websocket/ and lib/web/eventsource/ (WebSocket and
// EventSource support) - never reached by a plain fetch() call, same as
// File/MessagePort.
//
// Built as real, spec-shaped classes (not placeholders that merely
// exist): Event carries real bubbles/cancelable/composed/
// defaultPrevented/target/currentTarget/timeStamp state and real
// preventDefault/stopPropagation/stopImmediatePropagation/composedPath
// methods; EventTarget's addEventListener/removeEventListener/
// dispatchEvent are built directly on this codebase's own
// newEventEmitterObject/addListener/removeListener/emitOnObject
// (emitter.go) - the same real, already-tested listener machinery every
// other emitter here uses, not a second implementation of the same
// bookkeeping. Native Go constructors, not JS run through EvalCode/
// RunCode, for the same reason file_global.go is (see its own doc
// comment - a real, filed paserati bug, #298, makes a second top-level
// script run on the same instance corrupt later exception propagation).
func installEventGlobals(p *driver.Paserati) {
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
	if _, exists := gobj.GetOwn("Event"); exists {
		return
	}

	eventCtor := buildEventConstructor(vmInst, "Event", false)
	gobj.SetOwn("Event", eventCtor)
	gobj.SetOwn("CustomEvent", buildEventConstructor(vmInst, "CustomEvent", true))
	gobj.SetOwn("EventTarget", buildEventTargetConstructor(vmInst))
}

// buildEventConstructor builds Event (withDetail=false) or CustomEvent
// (withDetail=true - the only real difference the WHATWG spec defines
// between them is CustomEvent's extra `.detail` property, read from
// `eventInitDict.detail`).
func buildEventConstructor(vmInst *vm.VM, name string, withDetail bool) vm.Value {
	// A real, if minimal, prototype object - needed for `class X extends
	// Event {}` to resolve at all (found the hard way: a constructor
	// with no "prototype" property throws "Class extends value does not
	// have valid prototype property" the instant something tries to
	// subclass it, exactly what real undici's MessageEvent/CloseEvent do).
	proto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	ctor := vm.NewConstructorWithProps(2, false, name, func(args []vm.Value) (vm.Value, error) {
		obj := vm.NewObject(vm.NewValueFromPlainObject(proto)).AsPlainObject()
		self := vm.NewValueFromPlainObject(obj)

		typ := ""
		if len(args) > 0 {
			typ = args[0].ToString()
		}
		bubbles, cancelable, composed := false, false, false
		var detail vm.Value = vm.Undefined
		if len(args) > 1 && args[1].Type() == vm.TypeObject {
			init := args[1].AsPlainObject()
			if v, ok := init.GetOwn("bubbles"); ok {
				bubbles = v.IsTruthy()
			}
			if v, ok := init.GetOwn("cancelable"); ok {
				cancelable = v.IsTruthy()
			}
			if v, ok := init.GetOwn("composed"); ok {
				composed = v.IsTruthy()
			}
			if withDetail {
				if v, ok := init.GetOwn("detail"); ok {
					detail = v
				}
			}
		}

		obj.SetOwn("type", vm.NewString(typ))
		obj.SetOwn("bubbles", vm.BooleanValue(bubbles))
		obj.SetOwn("cancelable", vm.BooleanValue(cancelable))
		obj.SetOwn("composed", vm.BooleanValue(composed))
		obj.SetOwn("defaultPrevented", vm.False)
		obj.SetOwn("target", vm.Null)
		obj.SetOwn("currentTarget", vm.Null)
		obj.SetOwn("eventPhase", vm.NumberValue(0))
		obj.SetOwn("isTrusted", vm.False)
		obj.SetOwn("timeStamp", vm.NumberValue(float64(time.Now().UnixMilli())))
		if withDetail {
			obj.SetOwn("detail", detail)
		}

		obj.SetOwn("preventDefault", vm.NewNativeFunction(0, false, "preventDefault", func(_ []vm.Value) (vm.Value, error) {
			if cv, ok := obj.GetOwn("cancelable"); ok && cv.IsTruthy() {
				obj.SetOwn("defaultPrevented", vm.True)
			}
			return vm.Undefined, nil
		}))
		obj.SetOwn("stopPropagation", vm.NewNativeFunction(0, false, "stopPropagation", func(_ []vm.Value) (vm.Value, error) {
			obj.SetOwn("__noderatiStopped", vm.True)
			return vm.Undefined, nil
		}))
		obj.SetOwn("stopImmediatePropagation", vm.NewNativeFunction(0, false, "stopImmediatePropagation", func(_ []vm.Value) (vm.Value, error) {
			obj.SetOwn("__noderatiStopped", vm.True)
			obj.SetOwn("__noderatiStoppedImmediate", vm.True)
			return vm.Undefined, nil
		}))
		obj.SetOwn("composedPath", vm.NewNativeFunction(0, false, "composedPath", func(_ []vm.Value) (vm.Value, error) {
			arr := vm.NewArray()
			if tv, ok := obj.GetOwn("target"); ok && tv.Type() != vm.TypeNull {
				arr.AsArray().Append(tv)
			}
			return arr, nil
		}))

		return self, nil
	})
	if props := ctor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", vm.NewValueFromPlainObject(proto))
	}
	proto.SetOwnNonEnumerable("constructor", ctor)
	return ctor
}

// buildEventTargetConstructor's addEventListener/removeEventListener
// are thin adapters onto emitter.go's real addListener/removeListener -
// `once` support included since options.once is common real usage
// (undici's own WebSocket internals use it); `capture`/`passive`/
// `signal` are accepted (so a real options object never throws) but not
// interpreted - no reachable caller here has more than one listener per
// event type where capture ordering would matter, and honoring `signal`
// would need a real AbortSignal 'abort' listener wired up for no
// reachable benefit.
func buildEventTargetConstructor(vmInst *vm.VM) vm.Value {
	proto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	ctor := vm.NewConstructorWithProps(0, false, "EventTarget", func(_ []vm.Value) (vm.Value, error) {
		obj := newEventEmitterObject(vmInst)
		// Re-parented onto a real, shared EventTarget.prototype (rather
		// than newEventEmitterObject's own default ObjectPrototype) so
		// `instanceof EventTarget` and a future `class X extends
		// EventTarget` both resolve correctly - the same "prototype
		// property" the Event/CustomEvent constructors above need.
		obj.SetPrototype(vm.NewValueFromPlainObject(proto))
		self := vm.NewValueFromPlainObject(obj)

		obj.SetOwn("addEventListener", vm.NewNativeFunction(2, true, "addEventListener", func(a []vm.Value) (vm.Value, error) {
			if len(a) < 2 || !a[1].IsCallable() {
				return vm.Undefined, nil
			}
			once := false
			if len(a) > 2 && a[2].Type() == vm.TypeObject {
				if opts := a[2].AsPlainObject(); opts != nil {
					if v, ok := opts.GetOwn("once"); ok {
						once = v.IsTruthy()
					}
				}
			}
			addListener(vmInst, obj, a[0].ToString(), a[1], once, false)
			return vm.Undefined, nil
		}))
		obj.SetOwn("removeEventListener", vm.NewNativeFunction(2, true, "removeEventListener", func(a []vm.Value) (vm.Value, error) {
			if len(a) < 2 {
				return vm.Undefined, nil
			}
			removeListener(obj, a[0].ToString(), a[1])
			return vm.Undefined, nil
		}))
		obj.SetOwn("dispatchEvent", vm.NewNativeFunction(1, false, "dispatchEvent", func(a []vm.Value) (vm.Value, error) {
			if len(a) == 0 || a[0].Type() != vm.TypeObject {
				return vm.True, nil
			}
			evt := a[0]
			evtObj := evt.AsPlainObject()
			evtObj.SetOwn("target", self)
			evtObj.SetOwn("currentTarget", self)
			typeVal, _ := evtObj.GetOwn("type")
			emitOnObject(vmInst, obj, typeVal.ToString(), evt)
			cancelable, _ := evtObj.GetOwn("cancelable")
			defaultPrevented, _ := evtObj.GetOwn("defaultPrevented")
			return vm.BooleanValue(!(cancelable.IsTruthy() && defaultPrevented.IsTruthy())), nil
		}))

		return self, nil
	})
	if props := ctor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", vm.NewValueFromPlainObject(proto))
	}
	proto.SetOwnNonEnumerable("constructor", ctor)
	return ctor
}
