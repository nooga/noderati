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

	// Buffer.byteLength was missing entirely - real Node's static method,
	// used (36 separate call sites, by far the most common Buffer static
	// after Buffer.from itself) throughout pi-coding-agent's own real
	// tool-output-truncation code (core/tools/truncate.js's truncateTail,
	// on the path every single real bash-tool call result goes through) to
	// measure a string's byte length before deciding whether to truncate
	// it. Missing this meant `Buffer.byteLength(...)` was `undefined(...)`,
	// throwing a synchronous TypeError on every real tool call's very
	// first output line - the actual, final cause behind "works until the
	// agent makes tool calls," found by tracing the exact real call chain
	// (createBashToolDefinition's execute() -> OutputAccumulator.snapshot()
	// -> truncateTail() -> Buffer.byteLength()) rather than assumed from
	// the child_process-level gaps found earlier in this investigation,
	// which were real too but turned out not to be what was actually
	// still breaking this. A Buffer argument's own .length is already a
	// byte count under this shim's model (wrapBuffer stores content as a
	// raw byte-aliased Go string); a plain string argument's UTF-8 byte
	// count is exactly len() of the Go string ToString() produces, since
	// Go strings are UTF-8 natively - matching Node's own default 'utf8'
	// encoding without needing to actually consult the encoding argument.
	byteLengthFn := vm.NewNativeFunction(2, false, "byteLength", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.NumberValue(0), nil
		}
		if isBufferValue(args[0]) {
			if obj := args[0].AsPlainObject(); obj != nil {
				if lengthVal, ok := obj.GetOwn("length"); ok {
					return lengthVal, nil
				}
			}
			return vm.NumberValue(0), nil
		}
		return vm.NumberValue(float64(len(args[0].ToString()))), nil
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
		props.Properties.SetOwn("byteLength", byteLengthFn)
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
