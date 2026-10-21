package host

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/nooga/paserati/pkg/vm"
)

// buffer_methods.go: the rest of Buffer.prototype on top of buffer.go's
// toString/write/copy - the fixed-width numeric readers and writers
// (read/writeUInt32LE and friends, the variable-width readUIntBE(offset,
// byteLength) family, the BigInt 64-bit pair, floats/doubles), equals,
// compare, swap16/32/64, the per-encoding utf8Slice/hexWrite/... fast
// paths, Node's string-aware fill/indexOf/lastIndexOf/includes, the
// legacy parent/offset getters, and Buffer.compare. Argument validation
// and error messages follow lib/internal/buffer.js, since code branches
// on err.code (ERR_OUT_OF_RANGE vs ERR_BUFFER_OUT_OF_BOUNDS).

// nodeCodedError builds a real JS error of the named constructor with
// Node's .code set, returned as a Go error that throws that value.
func nodeCodedError(vmInst *vm.VM, ctorName, code, message string) error {
	exception := vm.Undefined
	if ctor, ok := vmInst.GetGlobal(ctorName); ok {
		if v, err := vmInst.Construct(ctor, []vm.Value{vm.NewString(message)}); err == nil {
			exception = v
		}
	}
	if obj := exception.AsPlainObject(); obj != nil {
		obj.SetOwn("code", vm.NewString(code))
	}
	return &fsSystemError{exception: exception, message: message}
}

// addNumericalSeparator is Node's 4_294_967_296 formatting for large
// integers in ERR_OUT_OF_RANGE messages.
func addNumericalSeparator(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	return sign + strings.Join(append([]string{s}, out...), "_")
}

func outOfRangeReceived(v vm.Value) string {
	if v.Type() == vm.TypeBigInt {
		b := v.AsBigInt()
		s := b.String()
		lim := new(big.Int).Lsh(big.NewInt(1), 32)
		if b.CmpAbs(lim) > 0 {
			s = addNumericalSeparator(s)
		}
		return s + "n"
	}
	if v.IsNumber() {
		f := v.ToFloat()
		if f == math.Trunc(f) && math.Abs(f) > 1<<32 && !math.IsInf(f, 0) {
			return addNumericalSeparator(strconv.FormatFloat(f, 'f', -1, 64))
		}
		if f == 0 && math.Signbit(f) {
			return "-0"
		}
	}
	if v.IsString() {
		return "'" + v.ToString() + "'"
	}
	return v.ToString()
}

func errOutOfRange(vmInst *vm.VM, name, rng string, received vm.Value) error {
	return nodeCodedError(vmInst, "RangeError", "ERR_OUT_OF_RANGE",
		fmt.Sprintf("The value of %q is out of range. It must be %s. Received %s", name, rng, outOfRangeReceived(received)))
}

func errInvalidArgType(vmInst *vm.VM, name, expected string, received vm.Value) error {
	return nodeCodedError(vmInst, "TypeError", "ERR_INVALID_ARG_TYPE",
		fmt.Sprintf("The %q argument must be %s.%s", name, expected, receivedSuffix(vmInst, received)))
}

// bufferOffset validates an offset argument the way Node's checkBounds/
// boundsError do for an access of size bytes, returning the integer
// offset. An undefined offset is 0.
func bufferOffset(vmInst *vm.VM, length int, v vm.Value, size int) (int, error) {
	if v.IsUndefined() {
		v = vm.NumberValue(0)
	}
	if !v.IsNumber() {
		return 0, errInvalidArgType(vmInst, "offset", "of type number", v)
	}
	f := v.ToFloat()
	if f != math.Floor(f) || math.IsInf(f, 0) {
		return 0, errOutOfRange(vmInst, "offset", "an integer", v)
	}
	if f < 0 || f+float64(size) > float64(length) {
		if length-size < 0 {
			return 0, nodeCodedError(vmInst, "RangeError", "ERR_BUFFER_OUT_OF_BOUNDS",
				"Attempt to access memory outside buffer bounds")
		}
		return 0, errOutOfRange(vmInst, "offset", fmt.Sprintf(">= 0 and <= %d", length-size), v)
	}
	return int(f), nil
}

// bufferByteLength validates the byteLength argument of the
// read/writeIntLE family: an integer in 1..6.
func bufferByteLength(vmInst *vm.VM, v vm.Value) (int, error) {
	if !v.IsNumber() {
		return 0, errInvalidArgType(vmInst, "byteLength", "of type number", v)
	}
	f := v.ToFloat()
	if f != math.Floor(f) {
		return 0, errOutOfRange(vmInst, "byteLength", "an integer", v)
	}
	if f < 1 || f > 6 {
		return 0, errOutOfRange(vmInst, "byteLength", ">= 1 and <= 6", v)
	}
	return int(f), nil
}

func readUintN(b []byte, littleEndian bool) uint64 {
	var u uint64
	if littleEndian {
		for i := len(b) - 1; i >= 0; i-- {
			u = u<<8 | uint64(b[i])
		}
	} else {
		for _, c := range b {
			u = u<<8 | uint64(c)
		}
	}
	return u
}

func writeUintN(b []byte, u uint64, littleEndian bool) {
	n := len(b)
	for i := 0; i < n; i++ {
		c := byte(u >> (8 * i))
		if littleEndian {
			b[i] = c
		} else {
			b[n-1-i] = c
		}
	}
}

// intRangeText is Node's checkInt range wording: explicit bounds up to 32
// bits, powers of two beyond.
func intRangeText(size int, signed bool, min, max float64) string {
	if size > 4 {
		if !signed {
			return fmt.Sprintf(">= 0 and < 2 ** %d", size*8)
		}
		return fmt.Sprintf(">= -(2 ** %d) and < 2 ** %d", size*8-1, size*8-1)
	}
	return fmt.Sprintf(">= %s and <= %s", strconv.FormatFloat(min, 'f', -1, 64), strconv.FormatFloat(max, 'f', -1, 64))
}

func intBounds(size int, signed bool) (float64, float64) {
	if signed {
		return -math.Pow(2, float64(size*8-1)), math.Pow(2, float64(size*8-1)) - 1
	}
	return 0, math.Pow(2, float64(size*8)) - 1
}

func installBufferMethods(vmInst *vm.VM, proto *vm.PlainObject, statics *vm.PlainObject, wrapBytes func([]byte) vm.Value) {
	thisBytes := func() ([]byte, error) {
		ta := vmInst.GetThis().AsTypedArray()
		if ta == nil {
			return nil, vmInst.NewTypeError("argument must be a buffer")
		}
		return typedArrayLiveBytes(ta), nil
	}
	method := func(name string, arity int, fn func(b []byte, args []vm.Value) (vm.Value, error)) {
		proto.SetOwnNonEnumerable(name, vm.NewNativeFunction(arity, true, name, func(args []vm.Value) (vm.Value, error) {
			b, err := thisBytes()
			if err != nil {
				return vm.Undefined, err
			}
			return fn(b, args)
		}))
	}
	// alias defines each name (Node keeps both readUInt32LE and
	// readUint32LE) as the same function.
	alias := func(names []string, arity int, fn func(b []byte, args []vm.Value) (vm.Value, error)) {
		for _, name := range names {
			method(name, arity, fn)
		}
	}
	endians := []struct {
		suffix string
		little bool
	}{{"LE", true}, {"BE", false}}

	// Fixed-width integers: 8/16/32 bits, signed and unsigned.
	for _, bits := range []int{8, 16, 32} {
		size := bits / 8
		for _, e := range endians {
			suffix := e.suffix
			if size == 1 {
				suffix = ""
			}
			little := e.little
			for _, signed := range []bool{false, true} {
				signed := signed
				min, max := intBounds(size, signed)
				var readNames, writeNames []string
				if signed {
					readNames = []string{fmt.Sprintf("readInt%d%s", bits, suffix)}
					writeNames = []string{fmt.Sprintf("writeInt%d%s", bits, suffix)}
				} else {
					readNames = []string{fmt.Sprintf("readUInt%d%s", bits, suffix), fmt.Sprintf("readUint%d%s", bits, suffix)}
					writeNames = []string{fmt.Sprintf("writeUInt%d%s", bits, suffix), fmt.Sprintf("writeUint%d%s", bits, suffix)}
				}
				alias(readNames, 0, func(b []byte, args []vm.Value) (vm.Value, error) {
					off, err := bufferOffset(vmInst, len(b), argAt(args, 0), size)
					if err != nil {
						return vm.Undefined, err
					}
					u := readUintN(b[off:off+size], little)
					if signed {
						shift := 64 - uint(size*8)
						return vm.NumberValue(float64(int64(u<<shift) >> shift)), nil
					}
					return vm.NumberValue(float64(u)), nil
				})
				alias(writeNames, 1, func(b []byte, args []vm.Value) (vm.Value, error) {
					value := argAt(args, 0)
					f, err := toNumberErr(vmInst, value)
					if err != nil {
						return vm.Undefined, err
					}
					if f > max || f < min {
						return vm.Undefined, errOutOfRange(vmInst, "value", intRangeText(size, signed, min, max), vm.NumberValue(f))
					}
					off, err := bufferOffset(vmInst, len(b), argAt(args, 1), size)
					if err != nil {
						return vm.Undefined, err
					}
					writeUintN(b[off:off+size], uint64(int64(math.Trunc(nanToZero(f)))), little)
					return vm.NumberValue(float64(off + size)), nil
				})
			}
			if size == 1 {
				break
			}
		}
	}

	// Variable-width integers: readUIntLE(offset, byteLength) etc.
	for _, e := range endians {
		little := e.little
		for _, signed := range []bool{false, true} {
			signed := signed
			var readNames, writeNames []string
			if signed {
				readNames = []string{"readInt" + e.suffix}
				writeNames = []string{"writeInt" + e.suffix}
			} else {
				readNames = []string{"readUInt" + e.suffix, "readUint" + e.suffix}
				writeNames = []string{"writeUInt" + e.suffix, "writeUint" + e.suffix}
			}
			alias(readNames, 2, func(b []byte, args []vm.Value) (vm.Value, error) {
				size, err := bufferByteLength(vmInst, argAt(args, 1))
				if err != nil {
					return vm.Undefined, err
				}
				off, err := bufferOffset(vmInst, len(b), argAt(args, 0), size)
				if err != nil {
					return vm.Undefined, err
				}
				u := readUintN(b[off:off+size], little)
				if signed {
					shift := 64 - uint(size*8)
					return vm.NumberValue(float64(int64(u<<shift) >> shift)), nil
				}
				return vm.NumberValue(float64(u)), nil
			})
			alias(writeNames, 3, func(b []byte, args []vm.Value) (vm.Value, error) {
				size, err := bufferByteLength(vmInst, argAt(args, 2))
				if err != nil {
					return vm.Undefined, err
				}
				f, err := toNumberErr(vmInst, argAt(args, 0))
				if err != nil {
					return vm.Undefined, err
				}
				min, max := intBounds(size, signed)
				if f > max || f < min {
					return vm.Undefined, errOutOfRange(vmInst, "value", intRangeText(size, signed, min, max), vm.NumberValue(f))
				}
				off, err := bufferOffset(vmInst, len(b), argAt(args, 1), size)
				if err != nil {
					return vm.Undefined, err
				}
				writeUintN(b[off:off+size], uint64(int64(math.Trunc(nanToZero(f)))), little)
				return vm.NumberValue(float64(off + size)), nil
			})
		}
	}

	// 64-bit BigInt.
	two64 := new(big.Int).Lsh(big.NewInt(1), 64)
	two63 := new(big.Int).Lsh(big.NewInt(1), 63)
	for _, e := range endians {
		little := e.little
		for _, signed := range []bool{false, true} {
			signed := signed
			var readNames, writeNames []string
			if signed {
				readNames = []string{"readBigInt64" + e.suffix}
				writeNames = []string{"writeBigInt64" + e.suffix}
			} else {
				readNames = []string{"readBigUInt64" + e.suffix, "readBigUint64" + e.suffix}
				writeNames = []string{"writeBigUInt64" + e.suffix, "writeBigUint64" + e.suffix}
			}
			alias(readNames, 0, func(b []byte, args []vm.Value) (vm.Value, error) {
				off, err := bufferOffset(vmInst, len(b), argAt(args, 0), 8)
				if err != nil {
					return vm.Undefined, err
				}
				u := readUintN(b[off:off+8], little)
				if signed {
					return vm.NewBigInt(big.NewInt(int64(u))), nil
				}
				return vm.NewBigInt(new(big.Int).SetUint64(u)), nil
			})
			alias(writeNames, 1, func(b []byte, args []vm.Value) (vm.Value, error) {
				value := argAt(args, 0)
				if value.Type() != vm.TypeBigInt {
					return vm.Undefined, vmInst.NewTypeError("Cannot mix BigInt and other types, use explicit conversions")
				}
				n := value.AsBigInt()
				if signed {
					if n.Cmp(new(big.Int).Neg(two63)) < 0 || n.Cmp(two63) >= 0 {
						return vm.Undefined, errOutOfRange(vmInst, "value", ">= -(2n ** 63n) and < 2 ** 63n", value)
					}
				} else if n.Sign() < 0 || n.Cmp(two64) >= 0 {
					return vm.Undefined, errOutOfRange(vmInst, "value", ">= 0n and < 2n ** 64n", value)
				}
				off, err := bufferOffset(vmInst, len(b), argAt(args, 1), 8)
				if err != nil {
					return vm.Undefined, err
				}
				u := new(big.Int).And(n, new(big.Int).Sub(two64, big.NewInt(1))).Uint64()
				writeUintN(b[off:off+8], u, little)
				return vm.NumberValue(float64(off + 8)), nil
			})
		}
	}

	// Floats and doubles.
	for _, e := range endians {
		var order binary.ByteOrder = binary.BigEndian
		if e.little {
			order = binary.LittleEndian
		}
		method("readFloat"+e.suffix, 0, func(b []byte, args []vm.Value) (vm.Value, error) {
			off, err := bufferOffset(vmInst, len(b), argAt(args, 0), 4)
			if err != nil {
				return vm.Undefined, err
			}
			return vm.NumberValue(float64(math.Float32frombits(order.Uint32(b[off:])))), nil
		})
		method("readDouble"+e.suffix, 0, func(b []byte, args []vm.Value) (vm.Value, error) {
			off, err := bufferOffset(vmInst, len(b), argAt(args, 0), 8)
			if err != nil {
				return vm.Undefined, err
			}
			return vm.NumberValue(math.Float64frombits(order.Uint64(b[off:]))), nil
		})
		method("writeFloat"+e.suffix, 1, func(b []byte, args []vm.Value) (vm.Value, error) {
			f, err := toNumberErr(vmInst, argAt(args, 0))
			if err != nil {
				return vm.Undefined, err
			}
			off, err := bufferOffset(vmInst, len(b), argAt(args, 1), 4)
			if err != nil {
				return vm.Undefined, err
			}
			order.PutUint32(b[off:], math.Float32bits(float32(f)))
			return vm.NumberValue(float64(off + 4)), nil
		})
		method("writeDouble"+e.suffix, 1, func(b []byte, args []vm.Value) (vm.Value, error) {
			f, err := toNumberErr(vmInst, argAt(args, 0))
			if err != nil {
				return vm.Undefined, err
			}
			off, err := bufferOffset(vmInst, len(b), argAt(args, 1), 8)
			if err != nil {
				return vm.Undefined, err
			}
			order.PutUint64(b[off:], math.Float64bits(f))
			return vm.NumberValue(float64(off + 8)), nil
		})
	}

	// equals / compare.
	uint8Arg := func(name string, v vm.Value) ([]byte, error) {
		ta := v.AsTypedArray()
		if ta == nil || ta.GetElementType() != vm.TypedArrayUint8 {
			return nil, errInvalidArgType(vmInst, name, "an instance of Buffer or Uint8Array", v)
		}
		return typedArrayLiveBytes(ta), nil
	}
	method("equals", 1, func(b []byte, args []vm.Value) (vm.Value, error) {
		other, err := uint8Arg("otherBuffer", argAt(args, 0))
		if err != nil {
			return vm.Undefined, err
		}
		return vm.BooleanValue(bytes.Equal(b, other)), nil
	})
	rangeArg := func(name string, v vm.Value, def, max int) (int, error) {
		if v.IsUndefined() {
			return def, nil
		}
		if !v.IsNumber() {
			return 0, errInvalidArgType(vmInst, name, "of type number", v)
		}
		f := v.ToFloat()
		if f != math.Floor(f) {
			return 0, errOutOfRange(vmInst, name, "an integer", v)
		}
		if f < 0 || f > float64(max) {
			return 0, errOutOfRange(vmInst, name, fmt.Sprintf(">= 0 and <= %d", max), v)
		}
		return int(f), nil
	}
	method("compare", 1, func(b []byte, args []vm.Value) (vm.Value, error) {
		target, err := uint8Arg("target", argAt(args, 0))
		if err != nil {
			return vm.Undefined, err
		}
		tStart, err := rangeArg("targetStart", argAt(args, 1), 0, len(target))
		if err != nil {
			return vm.Undefined, err
		}
		tEnd, err := rangeArg("targetEnd", argAt(args, 2), len(target), len(target))
		if err != nil {
			return vm.Undefined, err
		}
		sStart, err := rangeArg("sourceStart", argAt(args, 3), 0, len(b))
		if err != nil {
			return vm.Undefined, err
		}
		sEnd, err := rangeArg("sourceEnd", argAt(args, 4), len(b), len(b))
		if err != nil {
			return vm.Undefined, err
		}
		if sStart >= sEnd {
			if tStart >= tEnd {
				return vm.NumberValue(0), nil
			}
			return vm.NumberValue(-1), nil
		}
		if tStart >= tEnd {
			return vm.NumberValue(1), nil
		}
		return vm.NumberValue(float64(bytes.Compare(b[sStart:sEnd], target[tStart:tEnd]))), nil
	})
	statics.SetOwn("compare", vm.NewNativeFunction(2, false, "compare", func(args []vm.Value) (vm.Value, error) {
		a, err := uint8Arg("buf1", argAt(args, 0))
		if err != nil {
			return vm.Undefined, err
		}
		b, err := uint8Arg("buf2", argAt(args, 1))
		if err != nil {
			return vm.Undefined, err
		}
		return vm.NumberValue(float64(bytes.Compare(a, b))), nil
	}))
	statics.SetOwn("poolSize", vm.NumberValue(65536))
	if alloc, ok := statics.GetOwn("alloc"); ok {
		statics.SetOwn("allocUnsafeSlow", vm.NewNativeFunction(1, false, "allocUnsafeSlow", func(args []vm.Value) (vm.Value, error) {
			return vmInst.Call(alloc, vm.Undefined, args[:min(len(args), 1)])
		}))
	}
	// Buffer.copyBytesFrom(view, offset?, length?): offset/length in
	// elements of the view, not bytes.
	statics.SetOwn("copyBytesFrom", vm.NewNativeFunction(1, false, "copyBytesFrom", func(args []vm.Value) (vm.Value, error) {
		ta := argAt(args, 0).AsTypedArray()
		if ta == nil {
			return vm.Undefined, errInvalidArgType(vmInst, "view", "an instance of TypedArray", argAt(args, 0))
		}
		data := typedArrayLiveBytes(ta)
		elemSize := 1
		if n := ta.GetLength(); n > 0 {
			elemSize = len(data) / n
		}
		count := ta.GetLength()
		off := 0
		if v := argAt(args, 1); !v.IsUndefined() {
			off = clampInt(int(v.ToFloat()), 0, count)
		}
		n := count - off
		if v := argAt(args, 2); !v.IsUndefined() {
			n = clampInt(int(v.ToFloat()), 0, count-off)
		}
		return wrapBytes(append([]byte(nil), data[off*elemSize:(off+n)*elemSize]...)), nil
	}))

	// swap16/32/64: in-place byte-order swaps.
	for _, width := range []int{2, 4, 8} {
		width := width
		method(fmt.Sprintf("swap%d", width*8), 0, func(b []byte, args []vm.Value) (vm.Value, error) {
			if len(b)%width != 0 {
				return vm.Undefined, nodeCodedError(vmInst, "RangeError", "ERR_INVALID_BUFFER_SIZE",
					fmt.Sprintf("Buffer size must be a multiple of %d-bits", width*8))
			}
			for i := 0; i < len(b); i += width {
				for l, r := i, i+width-1; l < r; l, r = l+1, r-1 {
					b[l], b[r] = b[r], b[l]
				}
			}
			return vmInst.GetThis(), nil
		})
	}

	// Per-encoding fast paths: latin1Slice(start, end) and
	// latin1Write(string, offset, length).
	for _, enc := range []struct{ prefix, encoding string }{
		{"utf8", "utf8"}, {"ascii", "ascii"}, {"latin1", "latin1"}, {"hex", "hex"},
		{"base64", "base64"}, {"base64url", "base64url"}, {"ucs2", "utf16le"},
	} {
		encoding := enc.encoding
		method(enc.prefix+"Slice", 2, func(b []byte, args []vm.Value) (vm.Value, error) {
			start, end := 0, len(b)
			if v := argAt(args, 0); !v.IsUndefined() {
				start = clampInt(int(v.ToFloat()), 0, len(b))
			}
			if v := argAt(args, 1); !v.IsUndefined() {
				end = clampInt(int(v.ToFloat()), 0, len(b))
			}
			if end < start {
				return vm.Undefined, nodeCodedError(vmInst, "RangeError", "ERR_OUT_OF_RANGE", "Index out of range")
			}
			return vm.NewString(encodeBufferBytes(b[start:end], encoding)), nil
		})
		method(enc.prefix+"Write", 3, func(b []byte, args []vm.Value) (vm.Value, error) {
			off := 0
			if v := argAt(args, 1); !v.IsUndefined() {
				off = int(v.ToFloat())
			}
			if off < 0 || off > len(b) {
				return vm.Undefined, nodeCodedError(vmInst, "RangeError", "ERR_BUFFER_OUT_OF_BOUNDS",
					`"offset" is outside of buffer bounds`)
			}
			max := len(b) - off
			if v := argAt(args, 2); !v.IsUndefined() {
				max = clampInt(int(v.ToFloat()), 0, len(b)-off)
			}
			data, err := decodeBufferString(argAt(args, 0).ToString(), encoding)
			if err != nil {
				return vm.NumberValue(0), nil
			}
			return vm.NumberValue(float64(copy(b[off:off+max], data))), nil
		})
	}

	// fill(value, offset?, end?, encoding?): strings and buffers repeat
	// as a byte pattern; numbers are taken & 255.
	method("fill", 1, func(b []byte, args []vm.Value) (vm.Value, error) {
		value := argAt(args, 0)
		offArg, endArg, encArg := argAt(args, 1), argAt(args, 2), argAt(args, 3)
		if offArg.IsString() {
			encArg, offArg, endArg = offArg, vm.Undefined, vm.Undefined
		} else if endArg.IsString() {
			encArg, endArg = endArg, vm.Undefined
		}
		off, end := 0, len(b)
		var err error
		if off, err = rangeArg("offset", offArg, 0, len(b)); err != nil {
			return vm.Undefined, err
		}
		if end, err = rangeArg("end", endArg, len(b), len(b)); err != nil {
			return vm.Undefined, err
		}
		if off >= end {
			return vmInst.GetThis(), nil
		}
		var pattern []byte
		switch {
		case value.IsString():
			enc := "utf8"
			if !encArg.IsUndefined() {
				enc = encArg.ToString()
				if !isBufferEncoding(enc) {
					return vm.Undefined, nodeCodedError(vmInst, "TypeError", "ERR_UNKNOWN_ENCODING", "Unknown encoding: "+enc)
				}
			}
			pattern, err = decodeBufferString(value.ToString(), enc)
			if err != nil || (len(pattern) == 0 && value.ToString() != "") {
				return vm.Undefined, nodeCodedError(vmInst, "TypeError", "ERR_INVALID_ARG_VALUE",
					"The argument 'value' is invalid. Received '"+value.ToString()+"'")
			}
			if len(pattern) == 0 {
				pattern = []byte{0}
			}
		case value.AsTypedArray() != nil:
			pattern = append([]byte(nil), typedArrayLiveBytes(value.AsTypedArray())...)
			if len(pattern) == 0 {
				return vm.Undefined, nodeCodedError(vmInst, "TypeError", "ERR_INVALID_ARG_VALUE",
					"The argument 'value' is invalid. Received "+receivedSuffix(vmInst, value)[len(" Received "):])
			}
		default:
			f, err := toNumberErr(vmInst, value)
			if err != nil {
				return vm.Undefined, err
			}
			pattern = []byte{byte(int64(nanToZero(f)) & 255)}
		}
		for i := off; i < end; i += len(pattern) {
			copy(b[i:end], pattern)
		}
		return vmInst.GetThis(), nil
	})

	// indexOf/lastIndexOf/includes(value, byteOffset?, encoding?).
	search := func(b []byte, args []vm.Value, last bool) (int, error) {
		value, offArg, encArg := argAt(args, 0), argAt(args, 1), argAt(args, 2)
		if offArg.IsString() {
			encArg, offArg = offArg, vm.Undefined
		}
		var needle []byte
		switch {
		case value.IsString():
			enc := "utf8"
			if !encArg.IsUndefined() {
				enc = encArg.ToString()
				if !isBufferEncoding(enc) {
					return 0, nodeCodedError(vmInst, "TypeError", "ERR_UNKNOWN_ENCODING", "Unknown encoding: "+enc)
				}
			}
			needle, _ = decodeBufferString(value.ToString(), enc)
		case value.AsTypedArray() != nil:
			needle = typedArrayLiveBytes(value.AsTypedArray())
		case value.IsNumber():
			needle = []byte{byte(int64(nanToZero(value.ToFloat())) & 255)}
		default:
			return 0, errInvalidArgType(vmInst, "value", "one of type number or string or an instance of Buffer or Uint8Array", value)
		}
		n := len(b)
		var off float64
		if last {
			off = float64(n)
		}
		if !offArg.IsUndefined() {
			f, err := toNumberErr(vmInst, offArg)
			if err != nil {
				return 0, err
			}
			if !math.IsNaN(f) {
				off = math.Trunc(f)
			}
		}
		if off < 0 {
			off += float64(n)
		}
		if last {
			if off < 0 {
				return -1, nil
			}
			start := int(math.Min(off, float64(n)))
			if start+len(needle) > n {
				start = n - len(needle)
			}
			for i := start; i >= 0; i-- {
				if bytes.Equal(b[i:i+len(needle)], needle) {
					return i, nil
				}
			}
			return -1, nil
		}
		if off < 0 {
			off = 0
		}
		if off > float64(n) {
			if len(needle) == 0 {
				return n, nil
			}
			return -1, nil
		}
		i := bytes.Index(b[int(off):], needle)
		if i < 0 {
			return -1, nil
		}
		return i + int(off), nil
	}
	method("indexOf", 1, func(b []byte, args []vm.Value) (vm.Value, error) {
		i, err := search(b, args, false)
		return vm.NumberValue(float64(i)), err
	})
	method("lastIndexOf", 1, func(b []byte, args []vm.Value) (vm.Value, error) {
		i, err := search(b, args, true)
		return vm.NumberValue(float64(i)), err
	})
	method("includes", 1, func(b []byte, args []vm.Value) (vm.Value, error) {
		i, err := search(b, args, false)
		return vm.BooleanValue(i >= 0), err
	})

	// inspect(): <Buffer 68 69 ... 10 more bytes>, INSPECT_MAX_BYTES = 50.
	method("inspect", 0, func(b []byte, args []vm.Value) (vm.Value, error) {
		const maxBytes = 50
		var sb strings.Builder
		sb.WriteString("<Buffer")
		for i, c := range b {
			if i == maxBytes {
				break
			}
			fmt.Fprintf(&sb, " %02x", c)
		}
		if len(b) > maxBytes {
			fmt.Fprintf(&sb, " ... %d more byte", len(b)-maxBytes)
			if len(b)-maxBytes > 1 {
				sb.WriteString("s")
			}
		}
		sb.WriteString(">")
		return vm.NewString(sb.String()), nil
	})
	if inspectFn, ok := proto.GetOwn("inspect"); ok {
		no := false
		proto.DefineOwnPropertyByKey(vm.NewSymbolKey(jsSymbolFor(vmInst, "nodejs.util.inspect.custom")), inspectFn, &no, &no, &no)
	}
	// toLocaleString is Buffer's toString (defined after this runs, so
	// looked up per call).
	proto.SetOwnNonEnumerable("toLocaleString", vm.NewNativeFunction(0, true, "toLocaleString", func(args []vm.Value) (vm.Value, error) {
		ts, err := vmInst.GetProperty(vmInst.GetThis(), "toString")
		if err != nil {
			return vm.Undefined, err
		}
		return vmInst.Call(ts, vmInst.GetThis(), args)
	}))

	// Legacy parent/offset getters: the backing ArrayBuffer and byteOffset.
	getter := func(name string, fn func(ta *vm.TypedArrayObject) vm.Value) {
		no := false
		proto.DefineAccessorProperty(name, vm.NewNativeFunction(0, false, name, func(args []vm.Value) (vm.Value, error) {
			ta := vmInst.GetThis().AsTypedArray()
			if ta == nil {
				return vm.Undefined, nil
			}
			return fn(ta), nil
		}), true, vm.Undefined, false, &no, &no)
	}
	getter("parent", func(ta *vm.TypedArrayObject) vm.Value {
		if buf := ta.GetBuffer(); buf != nil {
			return vm.NewArrayBufferFromObject(buf)
		}
		return vm.Undefined
	})
	getter("offset", func(ta *vm.TypedArrayObject) vm.Value {
		return vm.NumberValue(float64(ta.GetByteOffset()))
	})
}

func nanToZero(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

func isBufferEncoding(e string) bool {
	switch strings.ToLower(e) {
	case "utf8", "utf-8", "ucs2", "ucs-2", "utf16le", "utf-16le", "latin1", "binary",
		"base64", "base64url", "hex", "ascii":
		return true
	}
	return false
}

// toNumberErr is vmInst.ToNumber in the (value, error) shape the callers
// above use; a throwing valueOf() unwinds through the VM on its own.
func toNumberErr(vmInst *vm.VM, v vm.Value) (float64, error) {
	return vmInst.ToNumber(v), nil
}
