package host

import (
	"context"
	"errors"
	"fmt"
	"math"
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
// (including after growth). No Global, no streaming instantiate - not
// needed by any real call site this bridge targets, and this project's
// own discipline is to build the real thing a real call site needs, not
// speculative surface.
//
// WebAssembly.Table and multi-value exported-function results were
// added later, once a second real call site needed them (see
// docs/real-node-plan.md's Round 96): @silvia-odwyer/photon-node (a
// wasm-bindgen/Rust image-resizing package) exports its externref
// object heap as a real WebAssembly.Table (`__wbindgen_export_2`,
// read/written by its own generated JS glue via
// `.grow()`/`.get()`/`.set()`), and its
// `photonimage_get_bytes`/`get_bytes_jpeg` exports return a
// pointer+length pair as two wasm results, destructured by that same
// glue as a JS Array (`ret[0]`, `ret[1]`). Upstream wazero has no way
// to obtain an exported table at all, so table support is backed by a
// small fork (github.com/nooga/wazero, branch noderati-table-export)
// adding api.Module.ExportedTable - see wasmTableBridge's own doc
// comment for the table implementation, and go.mod's replace directive
// for the fork.
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

	tableProto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	tableProtoVal := vm.NewValueFromPlainObject(tableProto)

	moduleCtor := buildWasmModuleConstructor(vmInst, moduleProtoVal, errs)
	if props := moduleCtor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", moduleProtoVal)
	}
	moduleProto.SetOwnNonEnumerable("constructor", moduleCtor)

	instanceCtor := buildWasmInstanceConstructor(vmInst, instanceProtoVal, memoryProtoVal, tableProtoVal, errs)
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

	tableCtor := buildWasmTableConstructor(vmInst, tableProtoVal)
	if props := tableCtor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", tableProtoVal)
	}
	tableProto.SetOwnNonEnumerable("constructor", tableCtor)

	ns := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	ns.SetOwn("Module", moduleCtor)
	ns.SetOwn("Instance", instanceCtor)
	ns.SetOwn("Memory", memoryCtor)
	ns.SetOwn("Table", tableCtor)
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
			instVal, ierr := instantiateWasmModule(vmInst, instanceProtoVal, memoryProtoVal, tableProtoVal, errs, args[0], importObject)
			if ierr != nil {
				return vmInst.NewRejectedPromise(vmInst.ExceptionValueFromError(ierr)), nil
			}
			return vmInst.NewResolvedPromise(instVal), nil
		}
		modVal, cerr := compileWasmModule(vmInst, moduleProtoVal, errs, args[0])
		if cerr != nil {
			return vmInst.NewRejectedPromise(vmInst.ExceptionValueFromError(cerr)), nil
		}
		instVal, ierr := instantiateWasmModule(vmInst, instanceProtoVal, memoryProtoVal, tableProtoVal, errs, modVal, importObject)
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

// syncIn must never push a *dirty* cached buffer into wasm memory - a
// real, confirmed bug (round 87, docs/real-node-plan.md) fixed here:
// dirty means "wasm may have written to memory more recently than this
// cache reflects", so the cache is stale relative to real memory by
// definition, and blindly writing it back would roll back whatever
// wasm itself wrote in between, discarding real progress rather than
// committing a pending JS write. This bit real undici's own
// llhttp_execute/malloc/free/llhttp_execute pattern (routine for any
// response whose body needs a bigger scratch buffer mid-stream, i.e.
// almost every response above the initial ~4KB default): the first
// execute() call updates the parser's own internal state directly in
// wasm memory and leaves the bridge dirty; free()+malloc() are two
// more exported calls that run *without* JS ever touching `.buffer`
// in between (buffer growth only re-touches `.buffer` afterward, to
// write the next chunk) - so under the old code, each of those two
// calls' own syncIn() re-pushed the *pre-first-execute* stale
// snapshot, silently resetting the parser's progress. The second
// execute() call then ran against a wasm-memory image that looked
// like the parser had never processed the first chunk at all -
// confirmed directly via a minimal, undici-free repro (two
// llhttp_execute() calls on one parser, split response, a free()+
// malloc() growth cycle between them): llhttp incorrectly re-fired
// on_message_begin and returned a parse error on the second call,
// while the exact same two-call split with no malloc/free growth in
// between (a single, sufficiently-large buffer allocated once)
// completed correctly (on_body + on_message_complete as expected).
// This is what silently hung real undici's fetch() for any GET/POST
// whose body arrived in more than one physical socket read once it
// exceeded the scratch buffer's current size - see Round 87's entry
// for the full diagnosis chain (net.go's own paused-mode Readable
// delivery was fully exonerated first, via a separate trace, before
// landing here).
func (b *wasmMemoryBridge) syncIn() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ab == nil || b.dirty {
		return // no cached buffer, or the cache is already known-stale
		// relative to wasm's own more recent writes - nothing here is a
		// genuine pending JS write to commit; the bug above (a stale
		// dirty cache being pushed anyway) is exactly what this guards.
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

// detachIfGrownLocked detaches the currently-vended ArrayBuffer (if any)
// the moment a real memory.grow is observed (currentSize != b.size),
// instead of waiting for the next `.buffer` property read to notice.
// Callers must hold b.mu.
//
// A real `WebAssembly.Memory.buffer`'s identity only changes on a grow,
// and a real engine detaches every existing view over the old buffer the
// instant that grow happens - there's no "wait for JS to ask again"
// step. This copy-based bridge (paserati's ArrayBufferObject can't alias
// an externally-owned []byte, so `.buffer` is never a true zero-copy
// view) can't do that for free, so it needs to detach proactively to
// match. Code that never caches `.buffer` across calls (real undici's
// llhttp call site re-reads it fresh every time) doesn't notice the
// difference. Code that does cache it, the spec-compliant way -
// wasm-bindgen's own getUint8ArrayMemory0 only re-wraps
// `wasm.memory.buffer` when the cached view's `.byteLength` reads back
// 0 - needs this: without it, a grow that happens while nothing asks
// for `.buffer` leaves the old cached view looking valid (non-zero
// byteLength) but frozen at stale, pre-grow contents. That's exactly
// what made the real photon probe's get_bytes()/get_bytes_jpeg() come
// back length-correct but all-zero, until this fix (see
// docs/real-node-plan.md's Round 96 for the verification).
func (b *wasmMemoryBridge) detachIfGrownLocked(currentSize int) {
	if currentSize == b.size {
		return
	}
	if b.ab != nil {
		b.ab.Detach()
		b.ab = nil
		b.abVal = vm.Undefined
	}
	b.size = currentSize
	b.dirty = true
}

// syncOut eagerly copies wasm's current memory bytes into the
// already-vended ArrayBuffer, rather than deferring that copy to the
// next `.buffer` property read the way markDirty() alone would (a read
// that, per detachIfGrownLocked's own doc comment, real caller code is
// entitled to never make again once it has cached a view).
func (b *wasmMemoryBridge) syncOut() {
	b.mu.Lock()
	defer b.mu.Unlock()
	currentSize := int(b.mem.Size())
	b.detachIfGrownLocked(currentSize)
	if b.ab == nil {
		// Nothing vended yet, or just detached above by a grow - the
		// next `.buffer` access builds a fresh buffer via bufferValue().
		return
	}
	if view, ok := b.mem.Read(0, uint32(currentSize)); ok {
		copy(b.ab.GetData(), view)
	}
	b.dirty = false
}

// bufferValue returns the current `.buffer` value: the already-vended
// ArrayBuffer (refreshed in place first if dirty - see syncOut, which
// runs this same refresh eagerly after every exported-function call, so
// this is typically a no-op by the time JS reads `.buffer`), or a fresh
// one built from wasm's current memory if none has been vended yet
// (including right after detachIfGrownLocked cleared the old one).
func (b *wasmMemoryBridge) bufferValue() vm.Value {
	b.mu.Lock()
	defer b.mu.Unlock()
	currentSize := int(b.mem.Size())
	b.detachIfGrownLocked(currentSize)

	if b.ab != nil {
		if b.dirty {
			if view, ok := b.mem.Read(0, uint32(currentSize)); ok {
				copy(b.ab.GetData(), view)
			}
			b.dirty = false
		}
		return b.abVal
	}

	view, ok := b.mem.Read(0, uint32(currentSize))
	if !ok {
		// Reading a module's own full current memory range
		// (offset 0, length mem.Size()) should never fail - if it
		// somehow does, don't fabricate a plausible-looking but wrong
		// zero-filled buffer's worth of *garbage*; a zero-filled one is
		// the most honest fallback left once detachIfGrownLocked has
		// already dropped the old reference.
		view = nil
	}
	newABVal := vm.NewArrayBuffer(currentSize)
	newAB := newABVal.AsArrayBuffer()
	copy(newAB.GetData(), view)
	b.ab = newAB
	b.abVal = newABVal
	b.dirty = false
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
	// Explicit JS-driven grow needs the same eager detach
	// detachIfGrownLocked gives implicit (wasm-internal) growth via
	// syncOut - per spec, growing memory.buffer detaches the
	// previously-vended ArrayBuffer immediately, not on next access.
	b.mu.Lock()
	b.detachIfGrownLocked(int(b.mem.Size()))
	b.mu.Unlock()
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

// wasmValueTypeFuncref is the WASM binary format's funcref type byte
// (0x70) - present internally as internal/wasm.ValueTypeFuncref/
// RefTypeFuncref in the wazero fork, but deliberately not exposed as a
// named api.ValueType constant upstream (only api.ValueTypeExternref
// is - see the fork's api/wasm.go). This bridge's own real call site
// (wasm-bindgen's externref object-heap table) never exports a funcref
// table, so funcref support below is minimal completeness for the JS
// `new WebAssembly.Table({element: 'anyfunc', ...})` constructor path,
// not something any real call site exercises.
const wasmValueTypeFuncref api.ValueType = 0x70

// tableBackend is the shared surface behind a JS-facing WebAssembly.Table
// object, letting one wrapper (buildWasmTableValue) drive either a real
// wazero-exported table (wazeroTableBackend, backed by the actual WASM
// engine's own table storage via the fork's ExportedTable addition) or a
// standalone, host-only table built via `new WebAssembly.Table(...)`
// (standaloneTableBackend) that was never passed into any
// wazero-instantiated module - nothing this bridge's real call site
// imports a table, so a standalone table never touches wazero at all.
type tableBackend interface {
	size() uint32
	// grow must treat delta==0 the same way the fork's TableInstance.Grow
	// does: return the current size without consuming raw, matching the
	// real table.grow instruction's own no-op-but-still-succeeds shape.
	grow(delta uint32, raw uint64) (previousSize uint32, ok bool)
	get(i uint32) (raw uint64, err error)
	set(i uint32, raw uint64) error
}

// wazeroTableBackend adapts a real wazero api.Table (obtained via
// mod.ExportedTable) to tableBackend.
type wazeroTableBackend struct{ t api.Table }

func (b wazeroTableBackend) size() uint32 { return b.t.Size() }
func (b wazeroTableBackend) grow(delta uint32, raw uint64) (uint32, bool) {
	return b.t.Grow(delta, raw)
}
func (b wazeroTableBackend) get(i uint32) (uint64, error)   { return b.t.Get(i) }
func (b wazeroTableBackend) set(i uint32, raw uint64) error { return b.t.Set(i, raw) }

// standaloneTableBackend backs a `new WebAssembly.Table(...)` built
// without ever instantiating a module: a plain Go slice standing in for
// the fork's own TableInstance.References, with the identical
// grow/bounds-check semantics (see internal/wasm/table.go's
// TableInstance.Grow in the fork) so behavior matches an exported table
// exactly. Real undici/wasm-bindgen never constructs a table this way
// (they only ever read a table off instance.exports) - this exists
// purely so `new WebAssembly.Table(...)` itself doesn't throw, per this
// bridge's own doc comment on the divide between the two.
type standaloneTableBackend struct {
	mu   sync.Mutex
	refs []uint64
	max  *uint32
}

func (b *standaloneTableBackend) size() uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return uint32(len(b.refs))
}

func (b *standaloneTableBackend) grow(delta uint32, raw uint64) (uint32, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	prev := uint32(len(b.refs))
	if delta == 0 {
		return prev, true
	}
	newLen := uint64(prev) + uint64(delta)
	if newLen > math.MaxUint32 || (b.max != nil && newLen > uint64(*b.max)) {
		return 0, false
	}
	grown := make([]uint64, delta)
	for i := range grown {
		grown[i] = raw
	}
	b.refs = append(b.refs, grown...)
	return prev, true
}

func (b *standaloneTableBackend) get(i uint32) (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i >= uint32(len(b.refs)) {
		return 0, fmt.Errorf("out of bounds table access %d >= %d", i, len(b.refs))
	}
	return b.refs[i], nil
}

func (b *standaloneTableBackend) set(i uint32, raw uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i >= uint32(len(b.refs)) {
		return fmt.Errorf("out of bounds table access %d >= %d", i, len(b.refs))
	}
	b.refs[i] = raw
	return nil
}

// wasmTableBridge is the real-value-preserving JS<->wasm table bridge.
//
// wazero's Reference type is a raw uintptr. That can't safely round-trip
// an arbitrary paserati vm.Value: Go's GC doesn't trace a value reachable
// only through a uintptr, so stashing a real JS object's address there
// would let it get collected while still "in" the table. This matters in
// practice, not just in theory - disassembling photon_rs_bg.wasm (via
// wasm2wat) shows its `__wbindgen_init_externref_table` storing real JS
// values (undefined, null, true, false) directly into its exported
// externref table via table.set, not a numeric handle of its own. So
// whatever this bridge hands back from .get() has to be that exact JS
// value, not something reconstructed from a bit pattern.
//
// Real values never go into wazero's own Reference array. Every
// JS-driven .set()/.grow() allocates a small integer handle in this
// bridge's own registry (a live Go map, immune to the GC hazard above),
// maps it to the real vm.Value, and writes only the handle through to
// the backend. .get() reverses this: read the raw handle from the
// backend, look it up in the registry.
//
// The backend stays the single source of truth for length and null
// state, because photon_rs_bg.wasm's own compiled code, not just its JS
// glue, mutates the same table directly at the WASM bytecode level:
// `__externref_table_alloc` (`ref.null extern; table.grow 1`) and
// `__externref_table_dealloc` (`ref.null extern; table.set 1`) both run
// real WASM table instructions inside the wasm engine, invisible to this
// bridge. Any parallel length/contents tracking would desync the moment
// either ran, and that's routine, not an edge case -
// addToExternrefTable0/takeFromExternrefTable0 call them on every
// exception-handling round trip in the real glue. Reading length and raw
// slot state through the backend on every access, instead of caching
// either, keeps this correct regardless of which side touched the table
// last.
//
// Handle 0 is reserved and never allocated: it's exactly the raw value
// wasm's own `ref.null extern`/fresh growth without an init produce.
// Decoded as JS `undefined` for an externref table (matching real
// WebAssembly.Table.get's own default) or `null` for a funcref one
// (funcref's spec null). Every other value for an externref table,
// including explicit `null` and `undefined`, gets its own handle rather
// than collapsing onto 0: externref values are never coerced by the
// spec (unlike funcref, which requires an Exported Function or null),
// and the real init function above sets `table.set(0, undefined)` then
// `table.set(offset+1, null)`, both of which need to read back as those
// exact distinct values.
//
// One accepted gap: overwriting a JS-registered slot via .set() releases
// the old handle, so a slot reused repeatedly through JS doesn't leak.
// A slot nulled by wasm bytecode directly (dealloc, above) bypasses
// this bridge, so that handle lingers until process exit. Harmless for
// the real call site - a bounded handful of
// undefined/null/true/false/Error values over a process's lifetime -
// and no worse than any alternative that also can't observe a
// wasm-internal write.
type wasmTableBridge struct {
	backend  tableBackend
	elemKind api.ValueType // api.ValueTypeExternref or wasmValueTypeFuncref

	mu       sync.Mutex
	nextID   uint64
	registry map[uint64]vm.Value
}

func newWasmTableBridge(backend tableBackend, elemKind api.ValueType) *wasmTableBridge {
	return &wasmTableBridge{backend: backend, elemKind: elemKind, registry: map[uint64]vm.Value{}}
}

func (b *wasmTableBridge) size() uint32 { return b.backend.size() }

// decode turns a raw backend reference into the JS value it represents.
func (b *wasmTableBridge) decode(raw uint64) vm.Value {
	if raw != 0 {
		b.mu.Lock()
		v, ok := b.registry[raw]
		b.mu.Unlock()
		if ok {
			return v
		}
	}
	if b.elemKind == wasmValueTypeFuncref {
		return vm.Null
	}
	return vm.Undefined
}

// encode allocates a fresh handle for v, or returns the raw null
// reference 0 directly for funcref's spec-null (needing no handle).
func (b *wasmTableBridge) encode(v vm.Value) uint64 {
	if b.elemKind == wasmValueTypeFuncref && v.Type() == vm.TypeNull {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	b.registry[id] = v
	return id
}

func (b *wasmTableBridge) release(raw uint64) {
	if raw == 0 {
		return
	}
	b.mu.Lock()
	delete(b.registry, raw)
	b.mu.Unlock()
}

func (b *wasmTableBridge) get(i uint32) (vm.Value, error) {
	raw, err := b.backend.get(i)
	if err != nil {
		return vm.Undefined, err
	}
	return b.decode(raw), nil
}

func (b *wasmTableBridge) set(i uint32, v vm.Value) error {
	oldRaw, _ := b.backend.get(i) // ignore error: the Set call below surfaces the real OOB error
	newRaw := b.encode(v)
	if err := b.backend.set(i, newRaw); err != nil {
		b.release(newRaw) // don't leak the handle we just made if the write itself failed
		return err
	}
	b.release(oldRaw)
	return nil
}

func (b *wasmTableBridge) grow(delta uint32, v vm.Value) (uint32, bool) {
	if delta == 0 {
		return b.backend.grow(0, 0)
	}
	// Every new slot shares one handle for the same value v, matching the
	// real table.grow instruction's own behavior (and the fork's
	// TableInstance.Grow, which fills every new slot with one identical
	// initialRef) - all `delta` new slots really are the same JS value
	// reference, not `delta` independent copies.
	raw := b.encode(v)
	prev, ok := b.backend.grow(delta, raw)
	if !ok {
		b.release(raw)
	}
	return prev, ok
}

// buildWasmTableValue wraps a tableBackend (via a fresh wasmTableBridge) as
// a real WebAssembly.Table-shaped JS object: `.length` getter, `.grow()`,
// `.get()`, `.set()`.
func buildWasmTableValue(vmInst *vm.VM, tableProtoVal vm.Value, bridge *wasmTableBridge) vm.Value {
	obj := vm.NewObject(tableProtoVal).AsPlainObject()

	enumerableFalse, configurableTrue := false, true
	obj.DefineAccessorProperty(
		"length",
		vm.NewNativeFunction(0, false, "get length", func(_ []vm.Value) (vm.Value, error) {
			return vm.NumberValue(float64(bridge.size())), nil
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
		if delta < 0 {
			return vm.Undefined, vmInst.NewRangeError("WebAssembly.Table.grow: delta must not be negative")
		}
		initVal := vm.Undefined
		if len(args) > 1 {
			initVal = args[1]
		}
		prev, ok := bridge.grow(uint32(delta), initVal)
		if !ok {
			return vm.Undefined, vmInst.NewRangeError("WebAssembly.Table.grow: failed to grow table")
		}
		return vm.NumberValue(float64(prev)), nil
	}))
	obj.SetOwnNonEnumerable("get", vm.NewNativeFunction(1, false, "get", func(args []vm.Value) (vm.Value, error) {
		idx := 0
		if len(args) > 0 && args[0].IsNumber() {
			idx = int(args[0].ToFloat())
		}
		if idx < 0 {
			return vm.Undefined, vmInst.NewRangeError("WebAssembly.Table.get: index out of range")
		}
		v, err := bridge.get(uint32(idx))
		if err != nil {
			return vm.Undefined, vmInst.NewRangeError("WebAssembly.Table.get: " + err.Error())
		}
		return v, nil
	}))
	obj.SetOwnNonEnumerable("set", vm.NewNativeFunction(2, false, "set", func(args []vm.Value) (vm.Value, error) {
		idx := 0
		if len(args) > 0 && args[0].IsNumber() {
			idx = int(args[0].ToFloat())
		}
		if idx < 0 {
			return vm.Undefined, vmInst.NewRangeError("WebAssembly.Table.set: index out of range")
		}
		v := vm.Undefined
		if len(args) > 1 {
			v = args[1]
		}
		if err := bridge.set(uint32(idx), v); err != nil {
			return vm.Undefined, vmInst.NewRangeError("WebAssembly.Table.set: " + err.Error())
		}
		return vm.Undefined, nil
	}))
	return vm.NewValueFromPlainObject(obj)
}

// buildWasmTableConstructor implements `new WebAssembly.Table({element,
// initial, maximum})` - always a standaloneTableBackend (see its own doc
// comment: nothing this bridge's real call site does ever imports a
// table, so a host-constructed one never touches wazero). Passing one as
// an *import* to WebAssembly.Instance is not implemented - table imports
// aren't resolved at all by instantiateWasmModule's import-linking loop
// below (only function imports are), so a module actually declaring a
// table import surfaces wazero's own real LinkError for the missing
// import at Instantiate time, same honest-refusal shape as
// WebAssembly.Memory's standalone-construction refusal above.
func buildWasmTableConstructor(vmInst *vm.VM, tableProtoVal vm.Value) vm.Value {
	return vm.NewConstructorWithProps(1, false, "Table", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsObject() {
			return vm.Undefined, vmInst.NewTypeError("WebAssembly.Table: descriptor argument must be an object")
		}
		desc := args[0]

		elementVal, _ := vmInst.GetProperty(desc, "element")
		var elemKind api.ValueType
		switch elementVal.ToString() {
		case "anyfunc", "funcref":
			elemKind = wasmValueTypeFuncref
		case "externref":
			elemKind = api.ValueTypeExternref
		default:
			return vm.Undefined, vmInst.NewTypeError("WebAssembly.Table: descriptor.element must be 'anyfunc' or 'externref'")
		}

		initialVal, _ := vmInst.GetProperty(desc, "initial")
		if !initialVal.IsNumber() {
			return vm.Undefined, vmInst.NewTypeError("WebAssembly.Table: descriptor.initial must be a number")
		}
		initial := uint32(initialVal.ToFloat())

		var max *uint32
		if maxVal, _ := vmInst.GetProperty(desc, "maximum"); maxVal.IsNumber() {
			m := uint32(maxVal.ToFloat())
			max = &m
		}

		backend := &standaloneTableBackend{refs: make([]uint64, initial), max: max}
		bridge := newWasmTableBridge(backend, elemKind)
		return buildWasmTableValue(vmInst, tableProtoVal, bridge), nil
	})
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
func buildWasmInstanceConstructor(vmInst *vm.VM, instanceProtoVal, memoryProtoVal, tableProtoVal vm.Value, errs wasmErrorCtors) vm.Value {
	return vm.NewConstructorWithProps(2, false, "Instance", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, vmInst.NewTypeError("WebAssembly.Instance: missing module argument")
		}
		importObject := vm.Undefined
		if len(args) > 1 {
			importObject = args[1]
		}
		return instantiateWasmModule(vmInst, instanceProtoVal, memoryProtoVal, tableProtoVal, errs, args[0], importObject)
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
func instantiateWasmModule(vmInst *vm.VM, instanceProtoVal, memoryProtoVal, tableProtoVal vm.Value, errs wasmErrorCtors, moduleVal, importObject vm.Value) (vm.Value, error) {
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
	// Exported tables, via the fork's ExportedTable addition: real
	// wasm-bindgen glue (see wasmTableBridge's own doc comment) reads its
	// externref object heap off exactly this, as instance.exports.<name>.
	for name, def := range compiled.ExportedTables() {
		tbl := mod.ExportedTable(name)
		if tbl == nil {
			continue
		}
		bridge := newWasmTableBridge(wazeroTableBackend{t: tbl}, def.Type())
		exportsObj.SetOwn(name, buildWasmTableValue(vmInst, tableProtoVal, bridge))
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
			// Eager, not just markDirty()+defer-to-next-access: see
			// syncOut's own doc comment for why a deferred sync alone
			// isn't enough for glue that caches its own TypedArray view
			// across calls (real wasm-bindgen glue does exactly this).
			// A real grow is still handled lazily by bufferValue() on
			// next access (syncOut defers to that itself when the size
			// changed), so this remains correct for the llhttp call
			// site's own memory.grow-mid-stream pattern too.
			b.syncOut()
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
		switch len(resultsT) {
		case 0:
			return vm.Undefined, nil
		case 1:
			return wazeroU64ToJSValue(results[0], resultsT[0]), nil
		default:
			// Per the WebAssembly JS API spec (ToJSValueMultiple), a
			// multi-value exported function's JS-visible return is a
			// plain Array of the results in order. wasm-bindgen's own
			// generated glue destructures exactly this shape
			// (`const ret = wasm.photonimage_get_bytes(ptr); ret[0];
			// ret[1];`, a pointer+length pair, the standard wasm-bindgen
			// ABI for returning an owned buffer).
			vals := make([]vm.Value, len(resultsT))
			for i, t := range resultsT {
				vals[i] = wazeroU64ToJSValue(results[i], t)
			}
			return vm.NewArrayWithArgs(vals), nil
		}
	})
}
