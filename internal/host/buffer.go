package host

import (
	"encoding/base64"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

const bufferMarker = "__noderatiBuffer"

func declareBuffer(p *driver.Paserati) {
	p.DeclareModule("buffer", func(m *driver.ModuleBuilder) {
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:buffer", "buffer")
}

func installBufferGlobal(p *driver.Paserati) {
	vmInst := p.GetVM()
	ctor := buildBufferConstructor(vmInst)

	rec, err := p.LoadModule("buffer", ".")
	if err != nil {
		return
	}
	exports := rec.GetExportValues()
	exports["Buffer"] = ctor

	proto := vm.Undefined
	if vmInst != nil {
		proto = vmInst.ObjectPrototype
	}
	ns := vm.NewObject(proto).AsPlainObject()
	for name, val := range exports {
		if name == "default" {
			continue
		}
		ns.SetOwn(name, val)
	}
	exports["default"] = vm.NewValueFromPlainObject(ns)

	if gt, ok := vmInst.GetGlobal("globalThis"); ok {
		if obj := gt.AsPlainObject(); obj != nil {
			obj.SetOwn("Buffer", ctor)
		}
	}
}

func buildBufferConstructor(vmInst *vm.VM) vm.Value {
	fromFn := vm.NewNativeFunction(1, true, "from", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return wrapBuffer(vmInst, ""), nil
		}
		data := args[0].ToString()
		enc := ""
		if len(args) > 1 {
			enc = args[1].ToString()
		}
		if enc == "base64" {
			out, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				return wrapBuffer(vmInst, data), nil
			}
			return wrapBuffer(vmInst, string(out)), nil
		}
		return wrapBuffer(vmInst, data), nil
	})

	allocFn := vm.NewNativeFunction(1, false, "alloc", func(args []vm.Value) (vm.Value, error) {
		n := 0
		if len(args) > 0 && args[0].IsNumber() {
			n = int(args[0].ToFloat())
		}
		if n < 0 {
			n = 0
		}
		return wrapBuffer(vmInst, string(make([]byte, n))), nil
	})

	isBufferFn := vm.NewNativeFunction(1, false, "isBuffer", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.False, nil
		}
		return vm.BooleanValue(isBufferValue(args[0])), nil
	})

	bufferFn := vm.NewNativeFunctionWithProps(1, true, "Buffer", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return wrapBuffer(vmInst, ""), nil
		}
		if isBufferValue(args[0]) {
			return args[0], nil
		}
		return wrapBuffer(vmInst, args[0].ToString()), nil
	})
	if props := bufferFn.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.SetOwn("from", fromFn)
		props.Properties.SetOwn("alloc", allocFn)
		props.Properties.SetOwn("isBuffer", isBufferFn)
	}
	return bufferFn
}

func wrapBuffer(vmInst *vm.VM, data string) vm.Value {
	proto := vm.Undefined
	if vmInst != nil {
		proto = vmInst.ObjectPrototype
	}
	obj := vm.NewObject(proto).AsPlainObject()
	obj.SetOwn(bufferMarker, vm.True)
	obj.SetOwn("length", vm.NumberValue(float64(len(data))))
	obj.SetOwn("toString", vm.NewNativeFunction(0, false, "toString", func(_ []vm.Value) (vm.Value, error) {
		return vm.NewString(data), nil
	}))
	return vm.NewValueFromPlainObject(obj)
}

func isBufferValue(v vm.Value) bool {
	if !v.IsObject() {
		return false
	}
	obj := v.AsPlainObject()
	if obj == nil {
		return false
	}
	marked, ok := obj.GetOwn(bufferMarker)
	return ok && marked.IsBoolean() && marked.AsBoolean()
}
