package host

import (
	"math"
	"math/big"
	"sync"

	"github.com/tetratelabs/wazero/api"

	"github.com/nooga/paserati/pkg/vm"
)

// webassembly_globals.go adds WebAssembly.Global for globals a module
// exports: instance.exports.<name> is a WebAssembly.Global whose `value`
// reads (and, when mutable, writes) the live wasm global. Real
// es-module-lexer (vite's import analysis) sizes its buffers from
// `exports.__heap_base.value` on every parse.

var wasmGlobalProtos sync.Map // *vm.VM -> vm.Value (WebAssembly.Global.prototype)

// wasmExportedGlobalNames lists a module's exported globals by walking
// its export section (id 7): wazero's CompiledModule enumerates exported
// functions/memories/tables but not globals.
func wasmExportedGlobalNames(wasm []byte) []string {
	if len(wasm) < 8 {
		return nil
	}
	pos := 8
	readLEB := func() (uint64, bool) {
		var result uint64
		var shift uint
		for pos < len(wasm) {
			b := wasm[pos]
			pos++
			result |= uint64(b&0x7f) << shift
			if b&0x80 == 0 {
				return result, true
			}
			shift += 7
		}
		return 0, false
	}
	for pos < len(wasm) {
		id := wasm[pos]
		pos++
		size, ok := readLEB()
		if !ok || pos+int(size) > len(wasm) {
			return nil
		}
		end := pos + int(size)
		if id != 7 {
			pos = end
			continue
		}
		count, ok := readLEB()
		if !ok {
			return nil
		}
		var names []string
		for i := uint64(0); i < count && pos < end; i++ {
			n, ok := readLEB()
			if !ok || pos+int(n) > end {
				return names
			}
			name := string(wasm[pos : pos+int(n)])
			pos += int(n)
			if pos >= end {
				return names
			}
			kind := wasm[pos]
			pos++
			if _, ok := readLEB(); !ok {
				return names
			}
			if kind == 3 {
				names = append(names, name)
			}
		}
		return names
	}
	return nil
}

func wasmGlobalToJS(g api.Global) vm.Value {
	bits := g.Get()
	switch g.Type() {
	case api.ValueTypeI32:
		return vm.NumberValue(float64(int32(uint32(bits))))
	case api.ValueTypeI64:
		return vm.NewBigInt(new(big.Int).SetInt64(int64(bits)))
	case api.ValueTypeF32:
		return vm.NumberValue(float64(math.Float32frombits(uint32(bits))))
	case api.ValueTypeF64:
		return vm.NumberValue(math.Float64frombits(bits))
	}
	return vm.Undefined
}

func jsToWasmGlobalBits(v vm.Value, t api.ValueType) uint64 {
	switch t {
	case api.ValueTypeI32:
		return uint64(uint32(int32(int64(v.ToFloat()))))
	case api.ValueTypeI64:
		if v.Type() == vm.TypeBigInt {
			return uint64(v.AsBigInt().Int64())
		}
		return uint64(int64(v.ToFloat()))
	case api.ValueTypeF32:
		return uint64(math.Float32bits(float32(v.ToFloat())))
	case api.ValueTypeF64:
		return math.Float64bits(v.ToFloat())
	}
	return 0
}

func buildWasmGlobalValue(vmInst *vm.VM, g api.Global) vm.Value {
	proto := vm.Undefined
	if p, ok := wasmGlobalProtos.Load(vmInst); ok {
		proto = p.(vm.Value)
	}
	obj := vm.NewObject(proto).AsPlainObject()
	getter := vm.NewNativeFunction(0, false, "get value", func(_ []vm.Value) (vm.Value, error) {
		return wasmGlobalToJS(g), nil
	})
	setter := vm.NewNativeFunction(1, false, "set value", func(args []vm.Value) (vm.Value, error) {
		mg, ok := g.(api.MutableGlobal)
		if !ok {
			return vm.Undefined, vmInst.NewTypeError("set WebAssembly.Global.value): Can't set the value of an immutable global.")
		}
		mg.Set(jsToWasmGlobalBits(argAt(args, 0), g.Type()))
		return vm.Undefined, nil
	})
	yes, no := true, false
	obj.DefineAccessorProperty("value", getter, true, setter, true, &no, &yes)
	obj.SetOwnNonEnumerable("valueOf", vm.NewNativeFunction(0, false, "valueOf", func(_ []vm.Value) (vm.Value, error) {
		return wasmGlobalToJS(g), nil
	}))
	return vm.NewValueFromPlainObject(obj)
}

// buildWasmGlobalConstructor: WebAssembly.Global exists for instanceof
// checks on exported globals; creating a free-standing one (only useful
// as an import) is refused honestly, like WebAssembly.Memory's.
func buildWasmGlobalConstructor(vmInst *vm.VM) vm.Value {
	proto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	protoVal := vm.NewValueFromPlainObject(proto)
	ctor := vm.NewConstructorWithProps(2, false, "Global", func(_ []vm.Value) (vm.Value, error) {
		return vm.Undefined, vmInst.NewTypeError("WebAssembly.Global: standalone construction is not implemented (only globals obtained via instance.exports are supported)")
	})
	if props := ctor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", protoVal)
	}
	proto.SetOwnNonEnumerable("constructor", ctor)
	wasmGlobalProtos.Store(vmInst, protoVal)
	return ctor
}
