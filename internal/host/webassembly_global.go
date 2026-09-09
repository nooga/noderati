package host

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// webassembly_global.go implements the core WebAssembly 1.0 (MVP) JS
// surface - `WebAssembly.Module`/`Instance`/`Memory` plus the
// `CompileError`/`LinkError`/`RuntimeError` exception types - backed by
// github.com/tetratelabs/wazero (pure Go, no cgo, matching this
// project's own build story). Filed as paserati#375 and closed there in
// favor of doing it here instead (see docs/real-node-plan.md's Round 76
// entry): pkg/vm already exports everything the bridge needs
// (ArrayBufferObject/TypedArrayObject, vm.Call for JS callbacks,
// NewConstructorWithProps, NewExceptionError) - no paserati changes
// required.
//
// Scoped to what real undici's `lib/dispatcher/client-h1.js`
// (`lazyllhttp()`) actually needs: `WebAssembly.Module`/`Instance`
// (both the synchronous constructors *and* the async
// `compile`/`instantiate` statics - paserati#375's own issue text
// quoted an undici version using only the synchronous constructors, but
// a real end-to-end probe against a real installed undici@7.11.0 found
// its actual current lazyllhttp() uses `await WebAssembly.compile(...)`
// + `await WebAssembly.instantiate(mod, {...})` instead - confirmed by
// running the real probe, not assumed from the issue's own summary)
// with plain-number (i32/i64/f32/f64) function imports, and
// `instance.exports.memory.buffer` reflecting current wasm memory
// (including after growth). No WebAssembly.Table, no Global, no
// streaming instantiate, no multi-value returns - none of those are
// needed by the concrete call site this exists for, and this project's
// own discipline is to build the real thing a real call site needs, not
// speculative surface.
//
// Depends on Buffer being real (see buffer.go) - real undici's own
// vendored llhttp-wasm.js does `Buffer.from(base64, 'base64')`, and a
// spec-correct WebAssembly.Module (which only accepts real
// ArrayBuffer/TypedArray input) cannot read the old fake Buffer at all.

const wasmModuleHandleMarker = "__noderatiWasmModuleHandle"

var (
	wasmModuleRegistry sync.Map // uint64 handle -> []byte (validated wasm bytes)
	wasmModuleSeq      atomic.Uint64
)

// wasmErrorCtors bundles the three real Error subclasses WebAssembly
// operations throw, so every construction/link/runtime failure path can
// throw the right, catchable JS type (real undici wraps its SIMD
// compile attempt in `try { ... } catch {}` specifically expecting a
// catchable throw here, not a Go panic, so the non-SIMD fallback path
// actually runs).
type wasmErrorCtors struct {
	compileError vm.Value
	linkError    vm.Value
	runtimeError vm.Value
}

func installWebAssemblyGlobal(p *driver.Paserati) {
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
	if _, exists := gobj.GetOwn("WebAssembly"); exists {
		return
	}
	errCtorVal, ok := vmInst.GetGlobal("Error")
	if !ok || !errCtorVal.IsCallable() {
		return // no real Error to subclass - nothing honest to build
	}

	errs := wasmErrorCtors{
		compileError: buildWasmErrorSubclass(vmInst, errCtorVal, "CompileError"),
		linkError:    buildWasmErrorSubclass(vmInst, errCtorVal, "LinkError"),
		runtimeError: buildWasmErrorSubclass(vmInst, errCtorVal, "RuntimeError"),
	}

	memoryProto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	memoryProtoVal := vm.NewValueFromPlainObject(memoryProto)

	moduleProto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	moduleProtoVal := vm.NewValueFromPlainObject(moduleProto)

	instanceProto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	instanceProtoVal := vm.NewValueFromPlainObject(instanceProto)

	moduleCtor := buildWasmModuleConstructor(vmInst, moduleProtoVal, errs)
	if props := moduleCtor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", moduleProtoVal)
	}
	moduleProto.SetOwnNonEnumerable("constructor", moduleCtor)

	instanceCtor := buildWasmInstanceConstructor(vmInst, instanceProtoVal, memoryProtoVal, errs)
	if props := instanceCtor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", instanceProtoVal)
	}
	instanceProto.SetOwnNonEnumerable("constructor", instanceCtor)

	memoryCtor := vm.NewConstructorWithProps(1, false, "Memory", func(args []vm.Value) (vm.Value, error) {
		// Standalone construction (for passing a Memory in as an
		// *import*) needs a synthetic wasm module for wazero to host
		// the memory in - wazero has no free-standing api.Memory
		// outside a module instance. Real undici never does this (it
		// only ever reads instance.exports.memory), so rather than
		// fake a working object here, this refuses honestly - same
		// discipline zlib.go uses for brotli/zstd.
		return vm.Undefined, vmInst.NewTypeError("WebAssembly.Memory: standalone construction is not implemented (only memory obtained via instance.exports is supported)")
	})
	if props := memoryCtor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", memoryProtoVal)
	}
	memoryProto.SetOwnNonEnumerable("constructor", memoryCtor)

	ns := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	ns.SetOwn("Module", moduleCtor)
	ns.SetOwn("Instance", instanceCtor)
	ns.SetOwn("Memory", memoryCtor)
	ns.SetOwn("CompileError", errs.compileError)
	ns.SetOwn("LinkError", errs.linkError)
	ns.SetOwn("RuntimeError", errs.runtimeError)

	// WebAssembly.compile/instantiate (the async statics) - added after
	// the synchronous Module/Instance constructors above were found, via
	// a real end-to-end probe against a real installed undici@7.11.0,
	// to be what that actual current version's own lazyllhttp() calls
	// instead of `new WebAssembly.Module(...)`/`new
	// WebAssembly.Instance(...)`: paserati#375's own issue text quoted
	// an older undici version using the synchronous constructors
	// directly, but the real current package does `await
	// WebAssembly.compile(...)` then `await WebAssembly.instantiate(mod,
	// {...})`. Confirmed by running the real probe before assuming
	// either shape - not by reading the issue text alone. Both are
	// synchronous under the hood (wazero itself is synchronous; there's
	// no real async work to do), so these just wrap
	// compileWasmModule/instantiateWasmModule in an
	// already-resolved-or-rejected Promise rather than doing anything
	// genuinely asynchronous.
	ns.SetOwn("compile", vm.NewNativeFunction(1, false, "compile", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vmInst.NewRejectedPromise(vmInst.ExceptionValueFromError(
				throwWasmError(vmInst, errs.compileError, "WebAssembly.compile: missing bufferSource argument"))), nil
		}
		modVal, err := compileWasmModule(vmInst, moduleProtoVal, errs, args[0])
		if err != nil {
			return vmInst.NewRejectedPromise(vmInst.ExceptionValueFromError(err)), nil
		}
		return vmInst.NewResolvedPromise(modVal), nil
	}))
	ns.SetOwn("instantiate", vm.NewNativeFunction(2, false, "instantiate", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vmInst.NewRejectedPromise(vmInst.ExceptionValueFromError(
				throwWasmError(vmInst, errs.compileError, "WebAssembly.instantiate: missing module/bufferSource argument"))), nil
		}
		importObject := vm.Undefined
		if len(args) > 1 {
			importObject = args[1]
		}
		// Two overloads per spec: instantiate(module, imports) resolves
		// to just the Instance; instantiate(bufferSource, imports)
		// compiles first and resolves to {module, instance}.
		if isWasmModuleValue(args[0]) {
			instVal, ierr := instantiateWasmModule(vmInst, instanceProtoVal, memoryProtoVal, errs, args[0], importObject)
			if ierr != nil {
				return vmInst.NewRejectedPromise(vmInst.ExceptionValueFromError(ierr)), nil
			}
			return vmInst.NewResolvedPromise(instVal), nil
		}
		modVal, cerr := compileWasmModule(vmInst, moduleProtoVal, errs, args[0])
		if cerr != nil {
			return vmInst.NewRejectedPromise(vmInst.ExceptionValueFromError(cerr)), nil
		}
		instVal, ierr := instantiateWasmModule(vmInst, instanceProtoVal, memoryProtoVal, errs, modVal, importObject)
		if ierr != nil {
			return vmInst.NewRejectedPromise(vmInst.ExceptionValueFromError(ierr)), nil
		}
		result := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		result.SetOwn("module", modVal)
		result.SetOwn("instance", instVal)
		return vmInst.NewResolvedPromise(vm.NewValueFromPlainObject(result)), nil
	}))

	gobj.SetOwn("WebAssembly", vm.NewValueFromPlainObject(ns))
}

// buildWasmErrorSubclass builds a real Error subclass the same way
// file_global.go builds File on top of Blob: construct a genuine Error
// instance (so real .stack capture, [[ErrorData]], etc. all come from
// paserati's own real Error machinery) and reparent it onto this
// subclass's own prototype.
func buildWasmErrorSubclass(vmInst *vm.VM, errCtorVal vm.Value, name string) vm.Value {
	errProps := errCtorVal.AsNativeFunctionWithProps()
	if errProps == nil || errProps.Properties == nil {
		return vm.Undefined
	}
	errProtoVal, ok := errProps.Properties.GetOwn("prototype")
	if !ok {
		return vm.Undefined
	}
	proto := vm.NewObject(errProtoVal).AsPlainObject()
	protoVal := vm.NewValueFromPlainObject(proto)

	ctor := vm.NewConstructorWithProps(1, false, name, func(args []vm.Value) (vm.Value, error) {
		message := ""
		if len(args) > 0 && !args[0].IsUndefined() {
			message = args[0].ToString()
		}
		instance, err := vmInst.Construct(errCtorVal, []vm.Value{vm.NewString(message)})
		if err != nil {
			return vm.Undefined, err
		}
		obj := instance.AsPlainObject()
		if obj != nil {
			obj.SetPrototype(protoVal)
			// Confirmed empirically: the base Error constructor sets
			// its own OWN "name" property directly on the instance
			// (not just inherited from Error.prototype), which shadows
			// whatever this subclass's own prototype says unless the
			// instance's own copy is overridden too.
			obj.SetOwnNonEnumerable("name", vm.NewString(name))
		}
		return instance, nil
	})
	if props := ctor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", protoVal)
	}
	proto.SetOwnNonEnumerable("constructor", ctor)
	proto.SetOwnNonEnumerable("name", vm.NewString(name))
	return ctor
}

// throwWasmError constructs a real instance of the given WebAssembly
// error subclass and wraps it as a catchable JS exception via
// vmInst.NewExceptionError - this (not a bare Go error/panic) is what
// lets real undici's `try { new WebAssembly.Module(simd) } catch {}`
// fallback actually run on a compile failure.
func throwWasmError(vmInst *vm.VM, ctorVal vm.Value, message string) error {
	instance, err := vmInst.Construct(ctorVal, []vm.Value{vm.NewString(message)})
	if err != nil {
		return vmInst.NewTypeError(message)
	}
	return vmInst.NewExceptionError(instance)
}

// bytesFromBufferSource extracts real bytes from a bufferSource argument
// (ArrayBuffer or any ArrayBufferView/TypedArray) - the WebAssembly JS
// API's own accepted input shape. Returns a fresh copy, safe to keep
// past any later mutation/detach of the source.
func bytesFromBufferSource(v vm.Value) ([]byte, bool) {
	if ta := v.AsTypedArray(); ta != nil {
		if b := typedArrayBytes(ta); b != nil {
			return b, true
		}
		return nil, false
	}
	if ab := v.AsArrayBuffer(); ab != nil {
		if ab.IsDetached() {
			return nil, false
		}
		data := ab.GetData()
		out := make([]byte, len(data))
		copy(out, data)
		return out, true
	}
	return nil, false
}

// compileWasmModule is the actual `WebAssembly.Module` construction
// logic, factored out so both `new WebAssembly.Module(bytes)` and the
// static `WebAssembly.compile(bytes)`/`instantiate(bytes, ...)` async
// functions share it rather than duplicating the validate-and-store
// steps. Compiles the bytes once, against a scratch runtime, purely to
// validate them and surface a real CompileError - the scratch runtime is
// discarded immediately after (see instantiateWasmModule's own doc
// comment for why the *real* compile happens again, per-Instance,
// against that Instance's own runtime, rather than reusing this one).
func compileWasmModule(vmInst *vm.VM, moduleProtoVal vm.Value, errs wasmErrorCtors, source vm.Value) (vm.Value, error) {
	data, ok := bytesFromBufferSource(source)
	if !ok {
		return vm.Undefined, throwWasmError(vmInst, errs.compileError, "WebAssembly.Module: argument must be a BufferSource (ArrayBuffer or TypedArray)")
	}

	ctx := context.Background()
	scratchRT := wazero.NewRuntime(ctx)
	_, err := scratchRT.CompileModule(ctx, data)
	scratchRT.Close(ctx)
	if err != nil {
		return vm.Undefined, throwWasmError(vmInst, errs.compileError, "WebAssembly.Module: "+err.Error())
	}

	id := wasmModuleSeq.Add(1)
	wasmModuleRegistry.Store(id, data)

	obj := vm.NewObject(moduleProtoVal).AsPlainObject()
	obj.SetOwnNonEnumerable(wasmModuleHandleMarker, vm.NumberValue(float64(id)))
	return vm.NewValueFromPlainObject(obj), nil
}

// buildWasmModuleConstructor implements `new WebAssembly.Module(bytes)`.
func buildWasmModuleConstructor(vmInst *vm.VM, moduleProtoVal vm.Value, errs wasmErrorCtors) vm.Value {
	return vm.NewConstructorWithProps(1, false, "Module", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, throwWasmError(vmInst, errs.compileError, "WebAssembly.Module: missing bufferSource argument")
		}
		return compileWasmModule(vmInst, moduleProtoVal, errs, args[0])
	})
}

// isWasmModuleValue reports whether v is a WebAssembly.Module built by
// compileWasmModule (carries the real registry handle marker), as
// opposed to a plain BufferSource - the distinction
// WebAssembly.instantiate's overload resolution depends on.
func isWasmModuleValue(v vm.Value) bool {
	obj := asPlainObjectSafe(v)
	if obj == nil {
		return false
	}
	_, ok := obj.GetOwn(wasmModuleHandleMarker)
	return ok
}

// asPlainObjectSafe is Value.AsPlainObject, but safe to call on an
// arbitrary caller-supplied argument of unknown type: AsPlainObject
// itself panics on anything whose type tag isn't exactly TypeObject
// (confirmed directly in pkg/vm/value.go, not assumed) - a real, easy
// mistake this file made twice (here and in instantiateWasmModule)
// before a Go-level panic surfaced it: `new WebAssembly.Instance(module,
// ...)`/`WebAssembly.instantiate(bufferSource, ...)` both hand this
// function a real user-supplied argument that could be any type at all
// (a TypedArray for the instantiate(bufferSource, ...) overload, in
// particular).
func asPlainObjectSafe(v vm.Value) *vm.PlainObject {
	if v.Type() != vm.TypeObject {
		return nil
	}
	return v.AsPlainObject()
}

// wasmMemoryBridge is the copy-based JS<->wasm linear memory bridge:
// wazero's api.Memory.Read/Write are real, write-through views into the
// actual wasm linear memory (confirmed directly against a real fixture
// module, not assumed from docs alone), but that view *disconnects* on
// any capacity change (memory.grow), and paserati's own
// ArrayBufferObject has no exported way to alias an externally-owned
// []byte as its backing store (`data` is unexported - see buffer.go's
// own doc comment on the same limitation). So this bridges by copying
// at the two points where JS and wasm code can actually observe each
// other's writes:
//
//   - syncIn(): pushes the current cached ArrayBuffer's bytes into real
//     wasm memory, called immediately before any call that crosses into
//     wasm (an exported function invocation).
//   - markDirty()+bufferValue(): after such a call returns, wasm may have
//     written into its own memory (and/or grown it) - rather than
//     eagerly copying out every time (real undici's own llhttp Parser
//     always re-reads `.memory.buffer` fresh rather than caching it, so
//     eager sync-out isn't needed for that real call site), the dirty
//     flag defers the actual copy to the next `.buffer` access.
//
// Never syncs out over an uncommitted JS-side write: bufferValue() only
// refreshes from wasm when dirty or when wasm's own memory size changed
// (a real grow), so a JS write made via `new Uint8Array(mem.buffer).set(...)`
// that hasn't crossed into wasm yet is never silently discarded.
type wasmMemoryBridge struct {
	mu    sync.Mutex
	mem   api.Memory
	ab    *vm.ArrayBufferObject
	abVal vm.Value
	dirty bool
	size  int
}

func newWasmMemoryBridge(mem api.Memory) *wasmMemoryBridge {
	return &wasmMemoryBridge{mem: mem, abVal: vm.Undefined}
}

func (b *wasmMemoryBridge) syncIn() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ab == nil {
		return // JS never touched .buffer - nothing to push into wasm
	}
	data := b.ab.GetData()
	if len(data) > 0 {
		b.mem.Write(0, data)
	}
}

func (b *wasmMemoryBridge) markDirty() {
	b.mu.Lock()
	b.dirty = true
	b.mu.Unlock()
}

// bufferValue returns the current `.buffer` value, refreshing it from
// real wasm memory first if wasm ran since the last vend or if wasm's
// memory size changed (a grow - which, per spec, must detach the
// previously-vended ArrayBuffer and hand back a new one).
func (b *wasmMemoryBridge) bufferValue() vm.Value {
	b.mu.Lock()
	defer b.mu.Unlock()
	currentSize := int(b.mem.Size())
	if b.ab == nil || b.dirty || currentSize != b.size {
		view, ok := b.mem.Read(0, uint32(currentSize))
		if !ok {
			// Reading a module's own full current memory range
			// (offset 0, length mem.Size()) should never fail - if it
			// somehow does, don't fabricate a plausible-looking but
			// wrong zero-filled buffer; hand back whatever was
			// previously vended (stale but honestly so) instead.
			if !b.abVal.IsUndefined() {
				return b.abVal
			}
			view = nil
		}
		if b.ab != nil {
			b.ab.Detach()
		}
		newABVal := vm.NewArrayBuffer(currentSize)
		newAB := newABVal.AsArrayBuffer()
		copy(newAB.GetData(), view)
		b.ab = newAB
		b.abVal = newABVal
		b.size = currentSize
		b.dirty = false
	}
	return b.abVal
}

func (b *wasmMemoryBridge) grow(delta int) (int, error) {
	if delta < 0 {
		return 0, fmt.Errorf("grow delta must not be negative")
	}
	prevPages, ok := b.mem.Grow(uint32(delta))
	if !ok {
		return 0, fmt.Errorf("failed to grow memory by %d page(s)", delta)
	}
	return int(prevPages), nil
}

// buildWasmMemoryValue wraps a wazero-owned memory as a real
// WebAssembly.Memory-shaped JS object (`.buffer` accessor, `.grow()`).
func buildWasmMemoryValue(vmInst *vm.VM, memoryProtoVal vm.Value, mem api.Memory) (vm.Value, *wasmMemoryBridge) {
	bridge := newWasmMemoryBridge(mem)
	obj := vm.NewObject(memoryProtoVal).AsPlainObject()

	enumerableFalse, configurableTrue := false, true
	obj.DefineAccessorProperty(
		"buffer",
		vm.NewNativeFunction(0, false, "get buffer", func(_ []vm.Value) (vm.Value, error) {
			return bridge.bufferValue(), nil
		}),
		true,
		vm.Undefined,
		false,
		&enumerableFalse,
		&configurableTrue,
	)
	obj.SetOwnNonEnumerable("grow", vm.NewNativeFunction(1, false, "grow", func(args []vm.Value) (vm.Value, error) {
		delta := 0
		if len(args) > 0 && args[0].IsNumber() {
			delta = int(args[0].ToFloat())
		}
		prevPages, err := bridge.grow(delta)
		if err != nil {
			return vm.Undefined, vmInst.NewRangeError("WebAssembly.Memory.grow: " + err.Error())
		}
		return vm.NumberValue(float64(prevPages)), nil
	}))
	return vm.NewValueFromPlainObject(obj), bridge
}

// jsValueToWazeroArg converts one JS argument into wazero's uint64 call
// convention per the wasm function's declared parameter type. i64 routes
// to a real BigInt (matching the real WebAssembly JS API, where i64
// values are represented as BigInt, never Number) rather than through
// float64 - silently routing i64 through ToFloat/NumberValue would
// truncate/corrupt any value outside float64's 53-bit safe integer
// range.
func jsValueToWazeroArg(vmInst *vm.VM, v vm.Value, t api.ValueType) (uint64, error) {
	switch t {
	case api.ValueTypeI32:
		return api.EncodeI32(int32(vmInst.ToNumber(v))), nil
	case api.ValueTypeF32:
		return api.EncodeF32(float32(vmInst.ToNumber(v))), nil
	case api.ValueTypeF64:
		return api.EncodeF64(vmInst.ToNumber(v)), nil
	case api.ValueTypeI64:
		bi := v.AsBigInt()
		if bi == nil {
			return 0, vmInst.NewTypeError("WebAssembly: i64 argument must be a BigInt, not a Number")
		}
		return vm.BigToUint64Wrapped(bi), nil
	default:
		return 0, vmInst.NewTypeError("WebAssembly: unsupported wasm value type in call signature")
	}
}

// wazeroU64ToJSValue converts one wazero uint64 result back to a JS
// value per its declared wasm result type.
func wazeroU64ToJSValue(raw uint64, t api.ValueType) vm.Value {
	switch t {
	case api.ValueTypeI32:
		return vm.NumberValue(float64(api.DecodeI32(raw)))
	case api.ValueTypeF32:
		return vm.NumberValue(float64(api.DecodeF32(raw)))
	case api.ValueTypeF64:
		return vm.NumberValue(api.DecodeF64(raw))
	case api.ValueTypeI64:
		return vm.NewBigInt(big.NewInt(int64(raw)))
	default:
		return vm.Undefined
	}
}

// wasmJSCallPanic carries a JS exception (or a JS-argument conversion
// error) out of a host import trampoline via panic, so it can cross
// wazero's stack-based host-function ABI (which has no error return
// path of its own) and come back out through fn.Call's returned error -
// confirmed directly (a standalone panic-inside-host-function repro)
// that wazero recovers such a panic and surfaces it as fn.Call's error,
// recoverable via errors.As, rather than crashing the process.
type wasmJSCallPanic struct{ err error }

func (e *wasmJSCallPanic) Error() string { return e.err.Error() }
func (e *wasmJSCallPanic) Unwrap() error { return e.err }

// makeHostImportTrampoline builds the dynamic-signature wazero host
// function (api.GoFunc) that bridges one JS import function into wasm's
// call convention: converts the incoming []uint64 stack to JS values per
// the wasm-declared param types, calls the real JS function via
// vmInst.Call (the same JS-callback mechanism host_timers.go's
// setTimeout and this codebase's own emitter.go already use elsewhere -
// re-entrant here one level deeper: JS -> native export call -> wazero
// -> this trampoline -> vm.Call -> JS again), and writes the JS return
// value back onto the stack per the wasm-declared result type.
//
// A thrown JS import callback is real, reachable behavior here - real
// undici's llhttp wasm_on_* callbacks do throw (e.g. on maxHeaderSize
// exceeded), and the wasm code calling them has no way to see that
// unless it's surfaced as a real trap: silently handing back zero(es)
// and letting execution continue would mean the parser proceeds on
// corrupt state instead of failing. So a callback error panics with
// wasmJSCallPanic instead, letting wrapWasmExportedFunction recover the
// original JS exception value out of fn.Call's error and re-throw it
// faithfully.
func makeHostImportTrampoline(vmInst *vm.VM, jsFn vm.Value, params, results []api.ValueType) api.GoFunc {
	if len(results) > 1 {
		// Not needed by any real call site this bridge targets (every
		// wasm_on_* import here is single-result-or-void per
		// paserati#375's own investigation) - refuse honestly rather
		// than silently dropping every result past the first.
		return func(ctx context.Context, stack []uint64) {
			panic(&wasmJSCallPanic{err: fmt.Errorf("WebAssembly: multi-value function imports are not supported")})
		}
	}
	return func(ctx context.Context, stack []uint64) {
		args := make([]vm.Value, len(params))
		for i, t := range params {
			args[i] = wazeroU64ToJSValue(stack[i], t)
		}
		result, err := vmInst.Call(jsFn, vm.Undefined, args)
		if err != nil {
			panic(&wasmJSCallPanic{err: err})
		}
		if len(results) == 0 {
			return
		}
		raw, cerr := jsValueToWazeroArg(vmInst, result, results[0])
		if cerr != nil {
			panic(&wasmJSCallPanic{err: cerr})
		}
		stack[0] = raw
	}
}

// buildWasmInstanceConstructor implements `new WebAssembly.Instance(module,
// importObject)` (see instantiateWasmModule for the actual logic).
func buildWasmInstanceConstructor(vmInst *vm.VM, instanceProtoVal, memoryProtoVal vm.Value, errs wasmErrorCtors) vm.Value {
	return vm.NewConstructorWithProps(2, false, "Instance", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, vmInst.NewTypeError("WebAssembly.Instance: missing module argument")
		}
		importObject := vm.Undefined
		if len(args) > 1 {
			importObject = args[1]
		}
		return instantiateWasmModule(vmInst, instanceProtoVal, memoryProtoVal, errs, args[0], importObject)
	})
}

// instantiateWasmModule is the actual `WebAssembly.Instance` construction
// logic, factored out so both `new WebAssembly.Instance(module, imports)`
// and the static `WebAssembly.instantiate(module, imports)` async
// function share it.
//
// One wazero Runtime per Instance, deliberately, not one shared runtime
// for the whole process/session: confirmed directly (a two-runtime
// cross-instantiate repro) that a wazero CompiledModule can only be
// instantiated on the exact Runtime that compiled it, and that
// instantiating a second, distinct "env" host module under the same
// name on a Runtime that already has one collides. Two Instances built
// from the same Module (each wanting its *own* "env" pointing at its
// own JS import functions) would therefore corrupt each other on a
// shared runtime. The tradeoff: the Module's own bytes get recompiled
// here, once per Instance, rather than the compiled artifact being
// reused - correctness over that one avoidable recompile (and, as
// docs/real-node-plan.md's Round 76 entry notes, each of these Runtimes
// is never closed - fine at real usage, which memoizes lazyllhttp()'s
// result to 1-2 Instances per process, but worth knowing the shape of).
func instantiateWasmModule(vmInst *vm.VM, instanceProtoVal, memoryProtoVal vm.Value, errs wasmErrorCtors, moduleVal, importObject vm.Value) (vm.Value, error) {
	moduleObj := asPlainObjectSafe(moduleVal)
	var idVal vm.Value
	var ok bool
	if moduleObj != nil {
		idVal, ok = moduleObj.GetOwn(wasmModuleHandleMarker)
	}
	if !ok {
		return vm.Undefined, vmInst.NewTypeError("WebAssembly.Instance: first argument must be a WebAssembly.Module")
	}
	rawBytesAny, ok := wasmModuleRegistry.Load(uint64(idVal.ToFloat()))
	if !ok {
		return vm.Undefined, vmInst.NewTypeError("WebAssembly.Instance: stale or invalid WebAssembly.Module")
	}
	wasmBytes := rawBytesAny.([]byte)

	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		rt.Close(ctx)
		return vm.Undefined, throwWasmError(vmInst, errs.compileError, "WebAssembly.Instance: "+err.Error())
	}

	// Group the module's declared function imports by their own
	// module namespace (e.g. "env"), building one wazero host
	// module per namespace and one trampoline per function, each
	// resolved against importObject[moduleName][funcName].
	importsByModule := map[string][]api.FunctionDefinition{}
	var importOrder []string
	for _, fd := range compiled.ImportedFunctions() {
		modName, _, _ := fd.Import()
		if _, seen := importsByModule[modName]; !seen {
			importOrder = append(importOrder, modName)
		}
		importsByModule[modName] = append(importsByModule[modName], fd)
	}
	for _, modName := range importOrder {
		fnDefs := importsByModule[modName]
		var nsVal vm.Value = vm.Undefined
		if importObject.IsObject() {
			if v, gerr := vmInst.GetProperty(importObject, modName); gerr == nil {
				nsVal = v
			}
		}
		builder := rt.NewHostModuleBuilder(modName)
		for _, fd := range fnDefs {
			_, fname, _ := fd.Import()
			var jsFn vm.Value = vm.Undefined
			if nsVal.IsObject() {
				if v, gerr := vmInst.GetProperty(nsVal, fname); gerr == nil {
					jsFn = v
				}
			}
			if !jsFn.IsCallable() {
				rt.Close(ctx)
				return vm.Undefined, throwWasmError(vmInst, errs.linkError,
					fmt.Sprintf("WebAssembly.Instance: import #%s.%s is not a function", modName, fname))
			}
			params := fd.ParamTypes()
			resultsT := fd.ResultTypes()
			builder = builder.NewFunctionBuilder().
				WithGoFunction(api.GoFunc(makeHostImportTrampoline(vmInst, jsFn, params, resultsT)), params, resultsT).
				Export(fname)
		}
		if _, ierr := builder.Instantiate(ctx); ierr != nil {
			rt.Close(ctx)
			return vm.Undefined, throwWasmError(vmInst, errs.linkError, "WebAssembly.Instance: "+ierr.Error())
		}
	}

	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		rt.Close(ctx)
		return vm.Undefined, throwWasmError(vmInst, errs.linkError, "WebAssembly.Instance: "+err.Error())
	}

	exportsObj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	var bridges []*wasmMemoryBridge

	for name := range compiled.ExportedMemories() {
		mem := mod.ExportedMemory(name)
		if mem == nil {
			continue
		}
		memVal, bridge := buildWasmMemoryValue(vmInst, memoryProtoVal, mem)
		bridges = append(bridges, bridge)
		exportsObj.SetOwn(name, memVal)
	}
	for name := range compiled.ExportedFunctions() {
		fn := mod.ExportedFunction(name)
		if fn == nil {
			continue
		}
		def := fn.Definition()
		params := def.ParamTypes()
		resultsT := def.ResultTypes()
		exportsObj.SetOwn(name, wrapWasmExportedFunction(vmInst, fn, params, resultsT, bridges, errs.runtimeError))
	}

	instObj := vm.NewObject(instanceProtoVal).AsPlainObject()
	instObj.SetOwnNonEnumerable("exports", vm.NewValueFromPlainObject(exportsObj))
	return vm.NewValueFromPlainObject(instObj), nil
}

// wrapWasmExportedFunction wraps one wazero-exported function as a real
// JS-callable native function: syncs every known memory bridge in
// (JS-side writes made since the last crossing) immediately before the
// call, invokes the real wasm function, then marks those bridges dirty
// so the next `.buffer` access re-syncs out from wasm's own current
// state (including any grow that happened during this very call).
func wrapWasmExportedFunction(vmInst *vm.VM, fn api.Function, params, resultsT []api.ValueType, bridges []*wasmMemoryBridge, runtimeErrorCtor vm.Value) vm.Value {
	name := fn.Definition().DebugName()
	if len(resultsT) > 1 {
		// Not needed by any real call site this bridge targets (every
		// exported function llhttp needs returns 0 or 1 values) -
		// refuse honestly rather than silently returning only the
		// first of several real results.
		return vm.NewNativeFunction(len(params), false, name, func(args []vm.Value) (vm.Value, error) {
			return vm.Undefined, vmInst.NewTypeError(fmt.Sprintf("WebAssembly: exported function %q returns %d values - multi-value results are not supported", name, len(resultsT)))
		})
	}
	return vm.NewNativeFunction(len(params), false, name, func(args []vm.Value) (vm.Value, error) {
		for _, b := range bridges {
			b.syncIn()
			// Mark dirty immediately, *before* the call, not just after
			// it returns. syncIn() just committed every pending JS write
			// into wasm memory, so wasm's own memory is authoritative
			// starting now - and wasm may call back into JS (a host
			// import) *during* fn.Call below, before this wrapper ever
			// returns. If a host import reads memory.buffer expecting to
			// see bytes wasm wrote earlier in this same call (real
			// llhttp callbacks do exactly this: they're invoked with
			// pointers into memory wasm just populated), dirty must
			// already be true so bufferValue() re-reads from wasm rather
			// than handing back a stale cached ArrayBuffer.
			b.markDirty()
		}

		callArgs := make([]uint64, len(params))
		for i, t := range params {
			var argVal vm.Value = vm.Undefined
			if i < len(args) {
				argVal = args[i]
			}
			raw, err := jsValueToWazeroArg(vmInst, argVal, t)
			if err != nil {
				return vm.Undefined, err
			}
			callArgs[i] = raw
		}

		results, callErr := fn.Call(context.Background(), callArgs...)

		for _, b := range bridges {
			// Redundant with the pre-call markDirty above for the common
			// case, but cheap and covers a memory.grow that happened
			// during the call (grow already forces a re-read via the
			// size-changed check in bufferValue, but keeping this here
			// costs nothing and documents the invariant: wasm is always
			// considered authoritative after any call that could have
			// touched its memory).
			b.markDirty()
		}

		if callErr != nil {
			// A JS import callback's own thrown exception surfaces here
			// as a wasmJSCallPanic wrapped by wazero's panic recovery
			// (see makeHostImportTrampoline's doc comment) - unwrap and
			// re-throw the *original* JS exception value faithfully
			// rather than flattening it into a generic RuntimeError
			// string.
			var jsPanic *wasmJSCallPanic
			if errors.As(callErr, &jsPanic) {
				return vm.Undefined, jsPanic.err
			}
			return vm.Undefined, throwWasmError(vmInst, runtimeErrorCtor, "WebAssembly: "+callErr.Error())
		}
		if len(resultsT) == 0 {
			return vm.Undefined, nil
		}
		return wazeroU64ToJSValue(results[0], resultsT[0]), nil
	})
}
