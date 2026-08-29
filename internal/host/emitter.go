package host

import (
	"github.com/nooga/paserati/pkg/vm"
)

func newEventEmitterObject(vmInst *vm.VM) *vm.PlainObject {
	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	eventsTable := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	obj.SetOwn("_events", vm.NewValueFromPlainObject(eventsTable))

	obj.SetOwn("on", vm.NewNativeFunction(2, false, "on", func(args []vm.Value) (vm.Value, error) {
		return addListener(vmInst, obj, args[0].ToString(), args[1], false), nil
	}))
	obj.SetOwn("once", vm.NewNativeFunction(2, false, "once", func(args []vm.Value) (vm.Value, error) {
		return addListener(vmInst, obj, args[0].ToString(), args[1], true), nil
	}))
	obj.SetOwn("off", vm.NewNativeFunction(2, false, "off", func(args []vm.Value) (vm.Value, error) {
		return removeListener(obj, args[0].ToString(), args[1]), nil
	}))
	obj.SetOwn("removeListener", vm.NewNativeFunction(2, false, "removeListener", func(args []vm.Value) (vm.Value, error) {
		return removeListener(obj, args[0].ToString(), args[1]), nil
	}))
	obj.SetOwn("emit", vm.NewNativeFunction(1, true, "emit", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.False, nil
		}
		event := args[0].ToString()
		payload := args[1:]
		return vm.BooleanValue(emitOnObject(vmInst, obj, event, payload...)), nil
	}))

	return obj
}

func newReadableStream(vmInst *vm.VM) *vm.PlainObject {
	obj := newEventEmitterObject(vmInst)
	obj.SetOwn("readable", vm.True)
	obj.SetOwn("pipe", vm.NewNativeFunction(1, false, "pipe", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, nil
		}
		dest := args[0]
		addListener(vmInst, obj, "data", vm.NewNativeFunction(1, false, "pipeData", func(dataArgs []vm.Value) (vm.Value, error) {
			if len(dataArgs) == 0 {
				return vm.Undefined, nil
			}
			if destObj := dest.AsPlainObject(); destObj != nil {
				if writeFn, ok := destObj.GetOwn("write"); ok && writeFn.IsCallable() {
					_, _ = vmInst.Call(writeFn, dest, []vm.Value{dataArgs[0]})
				}
			}
			return vm.Undefined, nil
		}), false)
		addListener(vmInst, obj, "end", vm.NewNativeFunction(0, false, "pipeEnd", func(_ []vm.Value) (vm.Value, error) {
			if destObj := dest.AsPlainObject(); destObj != nil {
				if endFn, ok := destObj.GetOwn("end"); ok && endFn.IsCallable() {
					_, _ = vmInst.Call(endFn, dest, nil)
				}
			}
			return vm.Undefined, nil
		}), false)
		return dest, nil
	}))
	return obj
}

func newWritableStream(vmInst *vm.VM, writeFn func([]vm.Value) (vm.Value, error), endFn func([]vm.Value) (vm.Value, error)) *vm.PlainObject {
	obj := newEventEmitterObject(vmInst)
	obj.SetOwn("writable", vm.True)
	obj.SetOwn("write", vm.NewNativeFunction(1, false, "write", writeFn))
	if endFn != nil {
		obj.SetOwn("end", vm.NewNativeFunction(0, true, "end", endFn))
	}
	return obj
}

func getListenerArray(eventsTable *vm.PlainObject, event string) *vm.ArrayObject {
	if existing, ok := eventsTable.GetOwn(event); ok {
		if arr := existing.AsArray(); arr != nil {
			return arr
		}
	}
	arr := vm.NewArray()
	eventsTable.SetOwn(event, arr)
	return arr.AsArray()
}

func addListener(vmInst *vm.VM, obj *vm.PlainObject, event string, listener vm.Value, once bool) vm.Value {
	if !listener.IsCallable() {
		return vm.NewValueFromPlainObject(obj)
	}
	eventsVal, ok := obj.GetOwn("_events")
	if !ok {
		return vm.NewValueFromPlainObject(obj)
	}
	eventsTable := eventsVal.AsPlainObject()
	if eventsTable == nil {
		return vm.NewValueFromPlainObject(obj)
	}
	fn := listener
	if once {
		fn = vm.NewNativeFunction(0, true, "onceWrapper", func(args []vm.Value) (vm.Value, error) {
			removeListener(obj, event, fn)
			_, err := vmInst.Call(listener, vm.Undefined, args)
			return vm.Undefined, err
		})
	}
	arr := getListenerArray(eventsTable, event)
	arr.Append(fn)
	return vm.NewValueFromPlainObject(obj)
}

func removeListener(obj *vm.PlainObject, event string, listener vm.Value) vm.Value {
	eventsVal, ok := obj.GetOwn("_events")
	if !ok {
		return vm.NewValueFromPlainObject(obj)
	}
	eventsTable := eventsVal.AsPlainObject()
	if eventsTable == nil {
		return vm.NewValueFromPlainObject(obj)
	}
	existing, ok := eventsTable.GetOwn(event)
	if !ok {
		return vm.NewValueFromPlainObject(obj)
	}
	arr := existing.AsArray()
	if arr == nil {
		return vm.NewValueFromPlainObject(obj)
	}
	for i := 0; i < arr.Length(); i++ {
		if arr.Get(i) == listener {
			for j := i; j < arr.Length()-1; j++ {
				arr.Set(j, arr.Get(j+1))
			}
			arr.SetLength(arr.Length() - 1)
			break
		}
	}
	return vm.NewValueFromPlainObject(obj)
}

func emitOnObject(vmInst *vm.VM, obj *vm.PlainObject, event string, args ...vm.Value) bool {
	eventsVal, ok := obj.GetOwn("_events")
	if !ok {
		return false
	}
	eventsTable := eventsVal.AsPlainObject()
	if eventsTable == nil {
		return false
	}
	existing, ok := eventsTable.GetOwn(event)
	if !ok {
		return false
	}
	arr := existing.AsArray()
	if arr == nil || arr.Length() == 0 {
		return false
	}
	listeners := make([]vm.Value, arr.Length())
	for i := 0; i < arr.Length(); i++ {
		listeners[i] = arr.Get(i)
	}
	for _, fn := range listeners {
		if fn.IsCallable() {
			_, _ = vmInst.Call(fn, vm.Undefined, args)
		}
	}
	return true
}

func scheduleEmit(vmInst *vm.VM, obj *vm.PlainObject, event string, args ...vm.Value) {
	rt := vmInst.GetAsyncRuntime()
	rt.ScheduleNextTick(func() {
		emitOnObject(vmInst, obj, event, args...)
	})
}
