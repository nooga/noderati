package host

import (
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// buffer.go implements Node's `Buffer` as a real subclass of paserati's
// own real `Uint8Array` - real Node's Buffer literally is a Uint8Array
// subclass, and paserati already has a real, spec-correct
// ArrayBuffer/Uint8Array implementation (including ES2024
// toBase64/fromBase64/toHex/fromHex on the shared TypedArray machinery)
// to build on, via `pkg/vm`'s exported ArrayBufferObject/TypedArrayObject
// API.
//
// Previously (through round 75) this was a complete fake: a PlainObject
// carrying its bytes hidden inside a Go string closure, with no indexed
// access, no ArrayBuffer backing, and no interop with real
// ArrayBuffer/TypedArray values at all - `AsTypedArray()`/
// `AsArrayBuffer()` both failed on it. That was silently fine as long as
// nothing needed real bytes, but it's a hard blocker for
// paserati#375/WebAssembly: real undici's `lazyllhttp()` does `new
// WebAssembly.Module(require('../llhttp/llhttp-wasm.js'))`, and that
// `require()` returns a real Node Buffer built via
// `Buffer.from('<base64>', 'base64')` - a `WebAssembly.Module` bridge
// that only accepts real ArrayBuffer/TypedArray input (the only sane
// design - see webassembly_global.go) could not read the fake Buffer's
// bytes at all. Same root problem existed silently for any UTF-8-hostile
// binary data ever routed through the old wrapBuffer(vmInst, string) -
// Go strings round-tripped through paserati's own JS string
// representation aren't a safe carrier for arbitrary bytes.
//
// Built as a real Go-side prototype-chain subclass (same reparenting
// trick file_global.go uses to build File on top of the real Blob):
// `Buffer.prototype`'s own [[Prototype]] is `Uint8Array.prototype`, so
// indexed access, `.length`, `.slice()`/`.subarray()`,
// `.toBase64()`/`.fromBase64()`, iteration, and everything else
// %TypedArray%.prototype already provides come for free, for real, no
// shimming needed. Only the genuinely Buffer-specific surface
// (`toString(encoding)`, `write()`, the `from`/`alloc`/`concat`
// statics) is added here.
func declareBuffer(p *driver.Paserati) {
	p.DeclareModule("buffer", func(m *driver.ModuleBuilder) {
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:buffer", "buffer")
}

func installBufferGlobal(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	ctor := buildBufferConstructor(vmInst)
	if ctor.IsUndefined() {
		return // no real Uint8Array to build a real Buffer on top of
	}

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

// typedArrayBytes returns a fresh copy of a TypedArray's own byte range
// (respecting byteOffset/byteLength, not the whole possibly-larger
// backing ArrayBuffer). A copy, not a view, so callers are safe to keep
// the result past any later detach/resize of the source.
func typedArrayBytes(ta *vm.TypedArrayObject) []byte {
	if ta == nil {
		return nil
	}
	buf := ta.GetBuffer()
	if buf == nil || buf.IsDetached() {
		return nil
	}
	data := buf.GetData()
	off, ln := ta.GetByteOffset(), ta.GetByteLength()
	if off < 0 || ln < 0 || off+ln > len(data) {
		return nil
	}
	out := make([]byte, ln)
	copy(out, data[off:off+ln])
	return out
}

func normalizeBufferEncoding(e string) string {
	switch strings.ToLower(strings.TrimSpace(e)) {
	case "utf-8":
		return "utf8"
	case "ucs2", "ucs-2", "utf16le", "utf-16le":
		// Not really supported (paserati strings are already real
		// Unicode) - treated as utf8 rather than silently mangling
		// bytes, since nothing real this codebase has hit yet needs
		// genuine UTF-16LE byte semantics.
		return "utf8"
	default:
		return strings.ToLower(strings.TrimSpace(e))
	}
}

func decodeBufferString(s, encoding string) ([]byte, error) {
	switch normalizeBufferEncoding(encoding) {
	case "", "utf8":
		return []byte(s), nil
	case "base64", "base64url":
		std := base64.StdEncoding
		if normalizeBufferEncoding(encoding) == "base64url" {
			std = base64.URLEncoding
		}
		if b, err := std.DecodeString(s); err == nil {
			return b, nil
		}
		// Real Node's base64 decoder tolerates missing padding.
		raw := base64.RawStdEncoding
		if normalizeBufferEncoding(encoding) == "base64url" {
			raw = base64.RawURLEncoding
		}
		return raw.DecodeString(s)
	case "hex":
		return hex.DecodeString(s)
	case "latin1", "binary":
		b := make([]byte, 0, len(s))
		for _, r := range s {
			b = append(b, byte(r))
		}
		return b, nil
	case "ascii":
		b := make([]byte, 0, len(s))
		for _, r := range s {
			b = append(b, byte(r&0x7f))
		}
		return b, nil
	default:
		return []byte(s), nil
	}
}

func encodeBufferBytes(data []byte, encoding string) string {
	switch normalizeBufferEncoding(encoding) {
	case "base64":
		return base64.StdEncoding.EncodeToString(data)
	case "base64url":
		return base64.URLEncoding.EncodeToString(data)
	case "hex":
		return hex.EncodeToString(data)
	case "latin1", "binary":
		var sb strings.Builder
		for _, b := range data {
			sb.WriteRune(rune(b))
		}
		return sb.String()
	case "ascii":
		var sb strings.Builder
		for _, b := range data {
			sb.WriteRune(rune(b & 0x7f))
		}
		return sb.String()
	default: // utf8
		return string(data)
	}
}

// bytesFromArrayLike reads a JS array-like's own bytes via its `length`
// property and indexed access (covers `Buffer.from([1,2,3])` and
// similar array-of-octets input).
func bytesFromArrayLike(vmInst *vm.VM, v vm.Value) ([]byte, bool) {
	if !v.IsObject() {
		return nil, false
	}
	lengthVal, err := vmInst.GetProperty(v, "length")
	if err != nil || !lengthVal.IsNumber() {
		return nil, false
	}
	n := int(lengthVal.ToFloat())
	if n < 0 {
		return nil, false
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		elVal, err := vmInst.GetProperty(v, strconv.Itoa(i))
		if err != nil {
			return nil, false
		}
		out[i] = byte(int64(elVal.ToFloat()) & 0xff)
	}
	return out, true
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func buildBufferConstructor(vmInst *vm.VM) vm.Value {
	uint8ArrayCtorVal, ok := vmInst.GetGlobal("Uint8Array")
	if !ok || !uint8ArrayCtorVal.IsCallable() {
		return vm.Undefined
	}
	u8Props := uint8ArrayCtorVal.AsNativeFunctionWithProps()
	if u8Props == nil || u8Props.Properties == nil {
		return vm.Undefined
	}
	u8ProtoVal, ok := u8Props.Properties.GetOwn("prototype")
	if !ok {
		return vm.Undefined
	}

	bufferProto := vm.NewObject(u8ProtoVal).AsPlainObject()
	bufferProtoVal := vm.NewValueFromPlainObject(bufferProto)

	wrapBytes := func(data []byte) vm.Value {
		ab := vm.NewArrayBuffer(len(data))
		abo := ab.AsArrayBuffer()
		copy(abo.GetData(), data)
		v := vm.NewTypedArray(vm.TypedArrayUint8, abo, 0, -1)
		if ta := v.AsTypedArray(); ta != nil {
			ta.SetPrototype(bufferProtoVal)
		}
		return v
	}

	wrapView := func(ab *vm.ArrayBufferObject, byteOffset, length int) vm.Value {
		v := vm.NewTypedArray(vm.TypedArrayUint8, ab, byteOffset, length)
		if ta := v.AsTypedArray(); ta != nil {
			ta.SetPrototype(bufferProtoVal)
		}
		return v
	}

	isOurBuffer := func(v vm.Value) bool {
		ta := v.AsTypedArray()
		if ta == nil {
			return false
		}
		return ta.GetPrototype() == bufferProtoVal
	}

	// fromInput implements the shared decode logic behind both `new
	// Buffer(...)` (legacy, still real code's own call shape in places)
	// and `Buffer.from(...)`.
	fromInput := func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return wrapBytes(nil), nil
		}
		arg := args[0]
		switch {
		case arg.IsNumber():
			n := int(arg.ToFloat())
			if n < 0 {
				n = 0
			}
			return wrapBytes(make([]byte, n)), nil
		case arg.AsTypedArray() != nil:
			return wrapBytes(typedArrayBytes(arg.AsTypedArray())), nil
		case arg.AsArrayBuffer() != nil:
			ab := arg.AsArrayBuffer()
			total := len(ab.GetData())
			off, ln := 0, total
			if len(args) > 1 && args[1].IsNumber() {
				off = clampInt(int(args[1].ToFloat()), 0, total)
			}
			ln = total - off
			if len(args) > 2 && args[2].IsNumber() {
				ln = clampInt(int(args[2].ToFloat()), 0, total-off)
			}
			return wrapView(ab, off, ln), nil
		case arg.Type() == vm.TypeString:
			enc := "utf8"
			if len(args) > 1 && !args[1].IsUndefined() {
				enc = args[1].ToString()
			}
			data, derr := decodeBufferString(arg.ToString(), enc)
			if derr != nil {
				return vm.Undefined, vmInst.NewTypeError("Buffer: " + derr.Error())
			}
			return wrapBytes(data), nil
		default:
			if data, ok := bytesFromArrayLike(vmInst, arg); ok {
				return wrapBytes(data), nil
			}
			return vm.Undefined, vmInst.NewTypeError("Buffer: unsupported argument type")
		}
	}

	ctor := vm.NewConstructorWithProps(1, true, "Buffer", fromInput)

	fromFn := vm.NewNativeFunction(1, true, "from", func(args []vm.Value) (vm.Value, error) {
		return fromInput(args)
	})

	allocFn := vm.NewNativeFunction(1, true, "alloc", func(args []vm.Value) (vm.Value, error) {
		n := 0
		if len(args) > 0 && args[0].IsNumber() {
			n = int(args[0].ToFloat())
		}
		if n < 0 {
			n = 0
		}
		data := make([]byte, n)
		if len(args) > 1 && !args[1].IsUndefined() {
			fill := args[1]
			switch {
			case fill.Type() == vm.TypeString:
				enc := "utf8"
				if len(args) > 2 && !args[2].IsUndefined() {
					enc = args[2].ToString()
				}
				if fb, err := decodeBufferString(fill.ToString(), enc); err == nil && len(fb) > 0 {
					for i := range data {
						data[i] = fb[i%len(fb)]
					}
				}
			case fill.IsNumber():
				b := byte(int64(fill.ToFloat()) & 0xff)
				for i := range data {
					data[i] = b
				}
			}
		}
		return wrapBytes(data), nil
	})

	// allocUnsafe(size): real Node's version skips zero-filling the
	// returned memory for speed, on the understanding that the caller
	// will overwrite every byte before reading any of it - the returned
	// bytes are explicitly *unspecified* old memory, not a promised
	// value. Aliasing it to the same zero-filled alloc() above is a
	// legitimate implementation of that contract (zero is one valid
	// choice among "unspecified"), not a shortcut around it - found
	// missing while probing real undici (round 74,
	// docs/real-node-plan.md): its own websocket/constants.js calls
	// `Buffer.allocUnsafe(0)` at module load time.
	allocUnsafeFn := vm.NewNativeFunction(1, false, "allocUnsafe", func(args []vm.Value) (vm.Value, error) {
		n := 0
		if len(args) > 0 && args[0].IsNumber() {
			n = int(args[0].ToFloat())
		}
		if n < 0 {
			n = 0
		}
		return wrapBytes(make([]byte, n)), nil
	})

	isBufferFn := vm.NewNativeFunction(1, false, "isBuffer", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.False, nil
		}
		return vm.BooleanValue(isOurBuffer(args[0])), nil
	})

	// Buffer.byteLength - see buffer_test.go/round-74 doc comment history
	// for why this matters (pi-coding-agent's truncateTail() calls it on
	// every real tool-call output line). A real Buffer/TypedArray
	// argument reports its own real byte length; a string argument is
	// measured *as encoded*, matching real Node (Buffer.byteLength(str,
	// 'base64') differs from Buffer.byteLength(str, 'utf8')).
	byteLengthFn := vm.NewNativeFunction(2, false, "byteLength", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.NumberValue(0), nil
		}
		if ta := args[0].AsTypedArray(); ta != nil {
			return vm.NumberValue(float64(ta.GetByteLength())), nil
		}
		if ab := args[0].AsArrayBuffer(); ab != nil {
			return vm.NumberValue(float64(len(ab.GetData()))), nil
		}
		enc := "utf8"
		if len(args) > 1 && !args[1].IsUndefined() {
			enc = args[1].ToString()
		}
		if normalizeBufferEncoding(enc) == "utf8" {
			// Go strings from ToString() are UTF-8 natively, so their
			// own byte length already matches Node's default encoding
			// without needing to actually re-encode.
			return vm.NumberValue(float64(len(args[0].ToString()))), nil
		}
		data, err := decodeBufferString(args[0].ToString(), enc)
		if err != nil {
			return vm.NumberValue(0), nil
		}
		return vm.NumberValue(float64(len(data))), nil
	})

	concatFn := vm.NewNativeFunction(1, true, "concat", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return wrapBytes(nil), nil
		}
		list := args[0]
		lengthVal, err := vmInst.GetProperty(list, "length")
		n := 0
		if err == nil && lengthVal.IsNumber() {
			n = int(lengthVal.ToFloat())
		}
		var out []byte
		for i := 0; i < n; i++ {
			elVal, err := vmInst.GetProperty(list, strconv.Itoa(i))
			if err != nil {
				continue
			}
			if ta := elVal.AsTypedArray(); ta != nil {
				out = append(out, typedArrayBytes(ta)...)
			}
		}
		if len(args) > 1 && args[1].IsNumber() {
			total := int(args[1].ToFloat())
			if total < 0 {
				total = 0
			}
			switch {
			case len(out) > total:
				out = out[:total]
			case len(out) < total:
				padded := make([]byte, total)
				copy(padded, out)
				out = padded
			}
		}
		return wrapBytes(out), nil
	})

	if props := ctor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", bufferProtoVal)
		props.Properties.SetOwn("from", fromFn)
		props.Properties.SetOwn("alloc", allocFn)
		props.Properties.SetOwn("allocUnsafe", allocUnsafeFn)
		props.Properties.SetOwn("isBuffer", isBufferFn)
		props.Properties.SetOwn("byteLength", byteLengthFn)
		props.Properties.SetOwn("concat", concatFn)
		// Buffer.prototype's [[Prototype]] was reparented onto
		// Uint8Array.prototype above (instance-side inheritance - real
		// indexed access, iteration, toBase64/fromHex, etc.), but the
		// *constructor's own* [[Prototype]] (the static side: real
		// Node's `Object.getPrototypeOf(Buffer) === Uint8Array`) was
		// never set, leaving Buffer statically parentless. A native
		// function-with-props' static [[Prototype]] lives on its own
		// Properties table (see pkg/vm/proto.go's TypeNativeFunctionWithProps
		// case - GetPrototypeOf reads nfp.Properties.GetPrototype()), so
		// this is the one call needed. Found the hard way: real undici's
		// client-h1.js reads `Buffer[Symbol.species]` directly
		// (`const FastBuffer = Buffer[Symbol.species]`) - paserati's
		// Symbol.species accessor lives on %TypedArray% and is reached
		// via static inheritance (paserati#381/#383), so without this,
		// Buffer[Symbol.species] stayed undefined even after #383
		// landed, and `new FastBuffer(...)` inside a real wasm host
		// callback threw "undefined is not a constructor" - confirmed
		// directly via a live real-undici E2E trace before writing this
		// (docs/real-node-plan.md, Round 79).
		props.Properties.SetPrototype(uint8ArrayCtorVal)
	}
	bufferProto.SetOwnNonEnumerable("constructor", ctor)

	// subarray()/slice() inherited straight from %TypedArray%.prototype
	// would return a plain Uint8Array, not a Buffer - real engines make
	// these species-construct through `this.constructor`, matching
	// Node's own `Buffer.prototype.slice()` returning a Buffer;
	// paserati's generic TypedArray subarray/slice doesn't do species
	// construction (confirmed directly: a vanilla `new
	// Uint8Array(...).subarray()` on an unrelated subclass stays the
	// base class as well, not a paserati-vs-noderati-specific gap). Real
	// bytes call through to the actual inherited implementation via
	// vmInst.GetProperty (walking the prototype chain, since
	// subarray/slice aren't own properties of Uint8Array.prototype
	// itself) + vmInst.Call with the real `this`, then reparent just the
	// *result* onto Buffer.prototype - same reparenting trick used
	// everywhere else in this file, just applied post-call instead of
	// post-construction.
	wrapInheritedTypedArrayMethod := func(name string) {
		inherited, err := vmInst.GetProperty(u8ProtoVal, name)
		if err != nil || !inherited.IsCallable() {
			return
		}
		bufferProto.SetOwnNonEnumerable(name, vm.NewNativeFunction(2, true, name, func(args []vm.Value) (vm.Value, error) {
			thisVal := vmInst.GetThis()
			result, cerr := vmInst.Call(inherited, thisVal, args)
			if cerr != nil {
				return vm.Undefined, cerr
			}
			if ta := result.AsTypedArray(); ta != nil {
				ta.SetPrototype(bufferProtoVal)
			}
			return result, nil
		}))
	}
	wrapInheritedTypedArrayMethod("subarray")
	wrapInheritedTypedArrayMethod("slice")

	// toString(encoding?, start?, end?) - the one real behavioral
	// addition Buffer makes over inherited %TypedArray%.prototype: a
	// generic byte-range-to-string decode, defaulting to utf8. Real
	// `.toBase64()`/`.toHex()`/indexed access/iteration all already come
	// from Uint8Array.prototype via the real prototype chain set up
	// above - nothing to add for those.
	bufferProto.SetOwnNonEnumerable("toString", vm.NewNativeFunction(0, true, "toString", func(args []vm.Value) (vm.Value, error) {
		thisVal := vmInst.GetThis()
		ta := thisVal.AsTypedArray()
		if ta == nil {
			return vm.NewString(""), nil
		}
		data := typedArrayBytes(ta)
		start, end := 0, len(data)
		enc := "utf8"
		if len(args) > 0 && !args[0].IsUndefined() {
			enc = args[0].ToString()
		}
		if len(args) > 1 && args[1].IsNumber() {
			start = clampInt(int(args[1].ToFloat()), 0, len(data))
		}
		if len(args) > 2 && args[2].IsNumber() {
			end = clampInt(int(args[2].ToFloat()), start, len(data))
		}
		return vm.NewString(encodeBufferBytes(data[start:end], enc)), nil
	}))

	// write(string, offset?, length?, encoding?) - and the two-arg
	// write(string, encoding) overload real Node also accepts.
	bufferProto.SetOwnNonEnumerable("write", vm.NewNativeFunction(1, true, "write", func(args []vm.Value) (vm.Value, error) {
		thisVal := vmInst.GetThis()
		ta := thisVal.AsTypedArray()
		if ta == nil || len(args) == 0 {
			return vm.NumberValue(0), nil
		}
		str := args[0].ToString()
		offset, enc := 0, "utf8"
		switch {
		case len(args) == 2 && args[1].Type() == vm.TypeString:
			enc = args[1].ToString()
		case len(args) == 2 && args[1].IsNumber():
			offset = int(args[1].ToFloat())
		case len(args) >= 3:
			if args[1].IsNumber() {
				offset = int(args[1].ToFloat())
			}
			if args[2].Type() == vm.TypeString {
				enc = args[2].ToString()
			} else if len(args) >= 4 && args[3].Type() == vm.TypeString {
				enc = args[3].ToString()
			}
		}
		data, derr := decodeBufferString(str, enc)
		if derr != nil {
			return vm.NumberValue(0), nil
		}
		buf := ta.GetBuffer()
		if buf == nil || buf.IsDetached() {
			return vm.NumberValue(0), nil
		}
		dst := buf.GetData()
		byteStart := ta.GetByteOffset() + offset
		byteEnd := ta.GetByteOffset() + ta.GetByteLength()
		n := len(data)
		if byteStart+n > byteEnd {
			n = byteEnd - byteStart
		}
		if n < 0 {
			n = 0
		}
		if n > 0 {
			copy(dst[byteStart:byteStart+n], data[:n])
		}
		return vm.NumberValue(float64(n)), nil
	}))

	return ctor
}

// wrapBuffer builds a real Buffer instance from raw Go bytes - the
// bridge used by other host files (crypto.go's randomBytes, net.go's
// socket data events, zlib.go's decompressed chunks) that already hold
// real []byte and just need a real Buffer wrapper around it. Builds the
// value directly (real ArrayBuffer + real Uint8Array, reparented onto
// Buffer.prototype) rather than round-tripping through a JS-level
// Buffer.from call - same construction buildBufferConstructor's own
// wrapBytes closure does, duplicated here since that closure isn't
// exported past its own function.
//
// Reads "Buffer" off globalThis's own properties, not via
// vmInst.GetGlobal("Buffer") - GetGlobal only resolves compile-time
// reserved global slots (pkg/vm/vm.go's name->index heap map), and
// Buffer, like every other host-injected global in this codebase
// (file_global.go, message_port_global.go, ...), is installed by
// writing directly onto globalThis's own-property map instead, which
// GetGlobal does not fall back to (see host.go's installModules doc
// comment for the same lesson learned the hard way for
// setTimeout/timeout_object.go).
func wrapBuffer(vmInst *vm.VM, data []byte) vm.Value {
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return vm.Undefined
	}
	gobj := gt.AsPlainObject()
	if gobj == nil {
		return vm.Undefined
	}
	bufferCtorVal, ok := gobj.GetOwn("Buffer")
	if !ok {
		return vm.Undefined
	}
	props := bufferCtorVal.AsNativeFunctionWithProps()
	if props == nil || props.Properties == nil {
		return vm.Undefined
	}
	protoVal, ok := props.Properties.GetOwn("prototype")
	if !ok {
		return vm.Undefined
	}
	ab := vm.NewArrayBuffer(len(data))
	abo := ab.AsArrayBuffer()
	copy(abo.GetData(), data)
	v := vm.NewTypedArray(vm.TypedArrayUint8, abo, 0, -1)
	if ta := v.AsTypedArray(); ta != nil {
		ta.SetPrototype(protoVal)
	}
	return v
}
