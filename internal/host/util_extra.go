package host

import (
	"math"
	"sort"
	"strings"
	"syscall"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// addUtilTypePredicates completes util.types with the rest of Node's
// predicates. Like the originals in util.go they read the engine's own
// value tags and internal slots, not Symbol.toStringTag, so a disguised
// object isn't misreported (as with V8's own checks).
func addUtilTypePredicates(typesObj *vm.PlainObject) {
	pred := func(name string, fn func(v vm.Value) bool) {
		typesObj.SetOwn(name, vm.NewNativeFunction(1, false, name, func(args []vm.Value) (vm.Value, error) {
			return vm.BooleanValue(fn(argAt(args, 0))), nil
		}))
	}
	internal := func(v vm.Value, slot string) (vm.Value, bool) {
		if v.Type() != vm.TypeObject {
			return vm.Undefined, false
		}
		return v.AsPlainObject().GetInternal(slot)
	}
	boxed := func(v vm.Value, want ...vm.ValueType) bool {
		pv, ok := internal(v, "[[PrimitiveValue]]")
		if !ok {
			return false
		}
		for _, w := range want {
			if pv.Type() == w {
				return true
			}
		}
		return false
	}
	isType := func(want vm.ValueType) func(vm.Value) bool {
		return func(v vm.Value) bool { return v.Type() == want }
	}
	iterKind := func(v vm.Value, set bool) bool {
		if v.Type() != vm.TypeObject {
			return false
		}
		st := v.AsPlainObject().InternalIterState()
		if st == nil {
			return false
		}
		if set {
			return st.Kind == vm.IterKindSetValues || st.Kind == vm.IterKindSetEntries
		}
		return st.Kind == vm.IterKindMapKeys || st.Kind == vm.IterKindMapValues || st.Kind == vm.IterKindMapEntries
	}
	fnFlags := func(v vm.Value) (async, gen bool) {
		switch v.Type() {
		case vm.TypeClosure:
			fn := v.AsClosure().Fn
			return fn.IsAsync, fn.IsGenerator
		case vm.TypeFunction:
			fn := v.AsFunction()
			return fn.IsAsync, fn.IsGenerator
		}
		return false, false
	}
	typedKind := func(kind vm.TypedArrayKind) func(vm.Value) bool {
		return func(v vm.Value) bool {
			ta := v.AsTypedArray()
			return v.Type() == vm.TypeTypedArray && ta != nil && ta.GetElementType() == kind
		}
	}

	pred("isArgumentsObject", isType(vm.TypeArguments))
	pred("isAsyncFunction", func(v vm.Value) bool { a, _ := fnFlags(v); return a })
	pred("isGeneratorFunction", func(v vm.Value) bool { _, g := fnFlags(v); return g })
	pred("isGeneratorObject", func(v vm.Value) bool {
		return v.Type() == vm.TypeGenerator || v.Type() == vm.TypeAsyncGenerator
	})
	pred("isDate", func(v vm.Value) bool { _, ok := internal(v, "__timestamp__"); return ok })
	pred("isNativeError", func(v vm.Value) bool { _, ok := internal(v, "[[ErrorData]]"); return ok })
	pred("isMap", isType(vm.TypeMap))
	pred("isSet", isType(vm.TypeSet))
	pred("isWeakMap", isType(vm.TypeWeakMap))
	pred("isWeakSet", isType(vm.TypeWeakSet))
	pred("isRegExp", isType(vm.TypeRegExp))
	pred("isMapIterator", func(v vm.Value) bool { return iterKind(v, false) })
	pred("isSetIterator", func(v vm.Value) bool { return iterKind(v, true) })
	pred("isModuleNamespaceObject", func(v vm.Value) bool {
		return v.Type() == vm.TypeObject && v.AsPlainObject().IsModuleNamespace()
	})
	pred("isBoxedPrimitive", func(v vm.Value) bool {
		return boxed(v, vm.TypeFloatNumber, vm.TypeIntegerNumber, vm.TypeString, vm.TypeBoolean, vm.TypeBigInt, vm.TypeSymbol)
	})
	pred("isNumberObject", func(v vm.Value) bool { return boxed(v, vm.TypeFloatNumber, vm.TypeIntegerNumber) })
	pred("isStringObject", func(v vm.Value) bool { return boxed(v, vm.TypeString) })
	pred("isBooleanObject", func(v vm.Value) bool { return boxed(v, vm.TypeBoolean) })
	pred("isBigIntObject", func(v vm.Value) bool { return boxed(v, vm.TypeBigInt) })
	pred("isSymbolObject", func(v vm.Value) bool { return boxed(v, vm.TypeSymbol) })
	pred("isExternal", func(vm.Value) bool { return false })
	pred("isKeyObject", func(vm.Value) bool { return false })
	pred("isCryptoKey", func(vm.Value) bool { return false })
	for name, kind := range map[string]vm.TypedArrayKind{
		"isInt8Array": vm.TypedArrayInt8, "isUint8ClampedArray": vm.TypedArrayUint8Clamped,
		"isInt16Array": vm.TypedArrayInt16, "isUint16Array": vm.TypedArrayUint16,
		"isInt32Array": vm.TypedArrayInt32, "isUint32Array": vm.TypedArrayUint32,
		"isFloat16Array": vm.TypedArrayFloat16, "isFloat32Array": vm.TypedArrayFloat32,
		"isFloat64Array": vm.TypedArrayFloat64, "isBigInt64Array": vm.TypedArrayBigInt64,
		"isBigUint64Array": vm.TypedArrayBigUint64,
	} {
		pred(name, typedKind(kind))
	}
}

// uvErrno is the libuv error table getSystemErrorName & co. read: every
// platform errno Node knows plus libuv's own (UV_EOF, UV_UNKNOWN, EAI_*).
func uvErrorTable() map[int][2]string {
	table := map[int][2]string{}
	for name, e := range sysErrno {
		if _, dup := table[-int(e)]; dup && (name == "EWOULDBLOCK" || name == "ENOTSUP") {
			continue // aliases: libuv reports EAGAIN / its own ENOTSUP entry
		}
		table[-int(e)] = [2]string{name, errnoMessage(e)}
	}
	for code, entry := range map[int][2]string{
		-4095: {"EOF", "end of file"}, -4094: {"UNKNOWN", "unknown error"},
		-3000: {"EAI_ADDRFAMILY", "address family not supported"}, -3001: {"EAI_AGAIN", "temporary failure"},
		-3002: {"EAI_BADFLAGS", "bad ai_flags value"}, -3003: {"EAI_CANCELED", "request canceled"},
		-3004: {"EAI_FAIL", "permanent failure"}, -3005: {"EAI_FAMILY", "ai_family not supported"},
		-3006: {"EAI_MEMORY", "out of memory"}, -3007: {"EAI_NODATA", "no address"},
		-3008: {"EAI_NONAME", "unknown node or service"}, -3009: {"EAI_OVERFLOW", "argument buffer overflow"},
		-3010: {"EAI_SERVICE", "service not available for socket type"}, -3011: {"EAI_SOCKTYPE", "socket type not supported"},
		-3013: {"EAI_BADHINTS", "invalid value for hints"}, -3014: {"EAI_PROTOCOL", "resolved protocol is unknown"},
	} {
		table[code] = entry
	}
	return table
}

// errnoMessage is libuv's uv_strerror text: the C library's strerror,
// which Go's syscall.Errno.Error() mirrors (lower-cased first letter).
func errnoMessage(e syscall.Errno) string {
	msg := e.Error()
	if msg == "" {
		return msg
	}
	return strings.ToLower(msg[:1]) + msg[1:]
}

func installSystemErrorHelpers(vmInst *vm.VM, exports map[string]vm.Value) {
	table := uvErrorTable()
	checkErr := func(args []vm.Value) (int, error) {
		v := argAt(args, 0)
		if !v.IsNumber() {
			return 0, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE", `The "err" argument must be of type number.`+receivedSuffix(vmInst, v))
		}
		f := v.ToFloat()
		if f >= 0 || f != math.Trunc(f) || f < -9007199254740991 {
			return 0, newNodeRangeError(vmInst,
				`The value of "err" is out of range. It must be a negative integer. Received `+v.ToString())
		}
		return int(f), nil
	}
	exports["getSystemErrorName"] = vm.NewNativeFunction(1, false, "getSystemErrorName", func(args []vm.Value) (vm.Value, error) {
		n, err := checkErr(args)
		if err != nil {
			return vm.Undefined, err
		}
		if e, ok := table[n]; ok {
			return vm.NewString(e[0]), nil
		}
		return vm.NewString("Unknown system error " + argAt(args, 0).ToString()), nil
	})
	exports["getSystemErrorMessage"] = vm.NewNativeFunction(1, false, "getSystemErrorMessage", func(args []vm.Value) (vm.Value, error) {
		n, err := checkErr(args)
		if err != nil {
			return vm.Undefined, err
		}
		if e, ok := table[n]; ok {
			return vm.NewString(e[1]), nil
		}
		return vm.NewString("Unknown system error " + argAt(args, 0).ToString()), nil
	})
	exports["getSystemErrorMap"] = vm.NewNativeFunction(0, false, "getSystemErrorMap", func(_ []vm.Value) (vm.Value, error) {
		mapCtor, _ := vmInst.GetGlobal("Map")
		m, err := vmInst.Construct(mapCtor, nil)
		if err != nil {
			return vm.Undefined, err
		}
		set, err := vmInst.GetProperty(m, "set")
		if err != nil {
			return vm.Undefined, err
		}
		codes := make([]int, 0, len(table))
		for c := range table {
			codes = append(codes, c)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(codes)))
		for _, c := range codes {
			pair := vm.NewArrayWithArgs([]vm.Value{vm.NewString(table[c][0]), vm.NewString(table[c][1])})
			if _, err := vmInst.Call(set, m, []vm.Value{vm.NumberValue(float64(c)), pair}); err != nil {
				return vm.Undefined, err
			}
		}
		return m, nil
	})
}

// installUtilLazyExtras: util functions written in JS (util_extra_shim.go),
// loaded on first use.
func installUtilLazyExtras(p *driver.Paserati) {
	installLazyJSExports(p, "util", "noderati-internal:util-extra",
		[]string{"callbackify", "parseArgs", "styleText", "aborted", "isDeepStrictEqual", "_extend", "toUSVString"},
		nil)
}
