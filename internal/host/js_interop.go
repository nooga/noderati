package host

import (
	"github.com/nooga/paserati/pkg/vm"
)

// Helpers for reaching JS semantics the Go-side property API doesn't
// cover (symbol keys, property attributes), by calling the real
// builtins, plus Node's ERR_INVALID_ARG_TYPE "Received ..." suffix.

func jsGlobalMethod(vmInst *vm.VM, global, method string) vm.Value {
	g, ok := vmInst.GetGlobal(global)
	if !ok {
		return vm.Undefined
	}
	fn, err := vmInst.GetProperty(g, method)
	if err != nil {
		return vm.Undefined
	}
	return fn
}

// jsSymbolFor is Symbol.for(key).
func jsSymbolFor(vmInst *vm.VM, key string) vm.Value {
	v, err := vmInst.Call(jsGlobalMethod(vmInst, "Symbol", "for"), vm.Undefined, []vm.Value{vm.NewString(key)})
	if err != nil {
		return vm.Undefined
	}
	return v
}

// jsReflectGet is Reflect.get(target, key), for any key type.
func jsReflectGet(vmInst *vm.VM, target, key vm.Value) (vm.Value, error) {
	return vmInst.Call(jsGlobalMethod(vmInst, "Reflect", "get"), vm.Undefined, []vm.Value{target, key})
}

// jsDefineHidden defines target[key] = value as a non-enumerable,
// non-writable, configurable data property (Node's shape for
// util.promisify.custom tags).
func jsDefineHidden(vmInst *vm.VM, target, key, value vm.Value) error {
	desc := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	desc.SetOwn("value", value)
	desc.SetOwn("enumerable", vm.False)
	desc.SetOwn("writable", vm.False)
	desc.SetOwn("configurable", vm.True)
	_, err := vmInst.Call(jsGlobalMethod(vmInst, "Object", "defineProperty"), vm.Undefined,
		[]vm.Value{target, key, vm.NewValueFromPlainObject(desc)})
	return err
}

// jsDefineMethod defines target[key] = fn the way a class method is:
// writable, configurable, non-enumerable.
func jsDefineMethod(vmInst *vm.VM, target, key, fn vm.Value) error {
	desc := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	desc.SetOwn("value", fn)
	desc.SetOwn("enumerable", vm.False)
	desc.SetOwn("writable", vm.True)
	desc.SetOwn("configurable", vm.True)
	_, err := vmInst.Call(jsGlobalMethod(vmInst, "Object", "defineProperty"), vm.Undefined,
		[]vm.Value{target, key, vm.NewValueFromPlainObject(desc)})
	return err
}

// receivedSuffix is the " Received ..." tail Node's ERR_INVALID_ARG_TYPE
// messages end with.
func receivedSuffix(vmInst *vm.VM, v vm.Value) string {
	switch {
	case v.IsUndefined():
		return " Received undefined"
	case v.Type() == vm.TypeNull:
		return " Received null"
	case v.IsCallable():
		name := ""
		if n, err := vmInst.GetProperty(v, "name"); err == nil && n.IsString() {
			name = n.ToString()
		}
		if name == "" {
			return " Received function <anonymous>"
		}
		return " Received function " + name
	case v.IsString():
		s := []rune(v.ToString())
		if len(s) > 25 {
			return " Received type string ('" + string(s[:25]) + "...')"
		}
		return " Received type string ('" + string(s) + "')"
	case v.IsNumber():
		return " Received type number (" + v.ToString() + ")"
	case v.Type() == vm.TypeBoolean:
		return " Received type boolean (" + v.ToString() + ")"
	case v.Type() == vm.TypeBigInt:
		return " Received type bigint (" + v.ToString() + "n)"
	case v.Type() == vm.TypeSymbol:
		return " Received type symbol (" + v.ToString() + ")"
	}
	if ctor, err := vmInst.GetProperty(v, "constructor"); err == nil && ctor.IsCallable() {
		if n, err := vmInst.GetProperty(ctor, "name"); err == nil && n.IsString() && n.ToString() != "" {
			return " Received an instance of " + n.ToString()
		}
	}
	return " Received an instance of Object"
}

// jsDefineAccessorFree defines target[name] = value as a configurable,
// enumerable, writable data property, replacing any inherited accessor.
func jsDefineAccessorFree(vmInst *vm.VM, target vm.Value, name string, value vm.Value) error {
	desc := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	desc.SetOwn("value", value)
	desc.SetOwn("enumerable", vm.True)
	desc.SetOwn("writable", vm.True)
	desc.SetOwn("configurable", vm.True)
	_, err := vmInst.Call(jsGlobalMethod(vmInst, "Object", "defineProperty"), vm.Undefined,
		[]vm.Value{target, vm.NewString(name), vm.NewValueFromPlainObject(desc)})
	return err
}
