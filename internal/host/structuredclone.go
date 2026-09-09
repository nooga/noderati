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
	// Real Node's node:worker_threads.markAsUncloneable(obj) marks an
	// object so a later structuredClone()/postMessage attempt on it
	// throws DataCloneError - real undici's own CacheStorage/Cache/
	// Request/Response classes call it on themselves at construction
	// time (found while probing real undici, round 74,
	// docs/real-node-plan.md). Checked here via the same hidden marker
	// worker_threads.go's markAsUncloneable sets.
	if marked, ok := obj.GetOwn(uncloneableMarker); ok && marked.IsTruthy() {
		return vm.Undefined, fmt.Errorf("DataCloneError: object cannot be cloned")
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
