package host

import (
	"fmt"

	"github.com/nooga/paserati/pkg/vm"
)

func structuredCloneFn(vmInst *vm.VM) vm.Value {
	return vm.NewNativeFunction(1, false, "structuredClone", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, nil
		}
		return structuredCloneValue(vmInst, args[0], make(map[any]vm.Value))
	})
}

func structuredCloneValue(vmInst *vm.VM, v vm.Value, seen map[any]vm.Value) (vm.Value, error) {
	if v.IsUndefined() || v.Type() == vm.TypeNull || v.IsBoolean() || v.IsNumber() || v.IsString() {
		return v, nil
	}
	if v.IsCallable() {
		return vm.Undefined, fmt.Errorf("DataCloneError: function objects cannot be cloned")
	}
	if v.IsArray() {
		arr := v.AsArray()
		if cloned, ok := seen[arr]; ok {
			return cloned, nil
		}
		out := vm.NewArray()
		seen[arr] = out
		dst := out.AsArray()
		for i := 0; i < arr.Length(); i++ {
			elem, err := structuredCloneValue(vmInst, arr.Get(i), seen)
			if err != nil {
				return vm.Undefined, err
			}
			dst.Append(elem)
		}
		return out, nil
	}
	obj := v.AsPlainObject()
	if obj == nil {
		return v, nil
	}
	if cloned, ok := seen[obj]; ok {
		return cloned, nil
	}
	out := vm.NewObject(vmInst.ObjectPrototype)
	seen[obj] = out
	dst := out.AsPlainObject()
	for _, key := range obj.OwnKeys() {
		prop, ok := obj.GetOwn(key)
		if !ok {
			continue
		}
		cloned, err := structuredCloneValue(vmInst, prop, seen)
		if err != nil {
			return vm.Undefined, err
		}
		dst.SetOwn(key, cloned)
	}
	return out, nil
}
