package host

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// The rest of node:buffer's module surface: atob/btoa (the globals),
// constants/kMaxLength/kStringMaxLength/INSPECT_MAX_BYTES with Node's
// 64-bit values, isUtf8/isAscii, and transcode.
func installBufferExtras(p *driver.Paserati) {
	vmInst := p.GetVM()
	for _, name := range []string{"atob", "btoa"} {
		if v, ok := vmInst.GetGlobal(name); ok {
			setNativeExport(p, "buffer", name, v)
		}
	}
	const maxLength, maxStringLength = 9007199254740991, 536870888
	constants := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	constants.SetOwn("MAX_LENGTH", vm.NumberValue(maxLength))
	constants.SetOwn("MAX_STRING_LENGTH", vm.NumberValue(maxStringLength))
	setNativeExport(p, "buffer", "constants", vm.NewValueFromPlainObject(constants))
	setNativeExport(p, "buffer", "kMaxLength", vm.NumberValue(maxLength))
	setNativeExport(p, "buffer", "kStringMaxLength", vm.NumberValue(maxStringLength))
	setNativeExport(p, "buffer", "INSPECT_MAX_BYTES", vm.NumberValue(50))

	inputBytes := func(args []vm.Value) ([]byte, error) {
		v := argAt(args, 0)
		switch v.Type() {
		case vm.TypeTypedArray:
			return typedArrayLiveBytes(v.AsTypedArray()), nil
		case vm.TypeArrayBuffer:
			return v.AsArrayBuffer().GetData(), nil
		case vm.TypeDataView:
			dv := v.AsDataView()
			if buf := dv.GetBuffer(); buf != nil {
				data := buf.GetData()
				return data[dv.GetByteOffset() : dv.GetByteOffset()+dv.GetByteLength()], nil
			}
		}
		return nil, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE",
			`The "input" argument must be an instance of ArrayBuffer, Buffer, or TypedArray.`+receivedSuffix(vmInst, v))
	}
	setNativeExport(p, "buffer", "isUtf8", vm.NewNativeFunction(1, false, "isUtf8", func(args []vm.Value) (vm.Value, error) {
		b, err := inputBytes(args)
		if err != nil {
			return vm.Undefined, err
		}
		return vm.BooleanValue(utf8.Valid(b)), nil
	}))
	setNativeExport(p, "buffer", "isAscii", vm.NewNativeFunction(1, false, "isAscii", func(args []vm.Value) (vm.Value, error) {
		b, err := inputBytes(args)
		if err != nil {
			return vm.Undefined, err
		}
		for _, c := range b {
			if c >= 0x80 {
				return vm.False, nil
			}
		}
		return vm.True, nil
	}))
	setNativeExport(p, "buffer", "transcode", vm.NewNativeFunction(3, false, "transcode", func(args []vm.Value) (vm.Value, error) {
		src := argAt(args, 0)
		if src.Type() != vm.TypeTypedArray {
			return vm.Undefined, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE",
				`The "source" argument must be an instance of Buffer or Uint8Array.`+receivedSuffix(vmInst, src))
		}
		data := typedArrayLiveBytes(src.AsTypedArray())
		from, to := transcodeEncoding(argAt(args, 1).ToString()), transcodeEncoding(argAt(args, 2).ToString())
		if from == "" || to == "" {
			e := newJSError(vmInst, "Unable to transcode Buffer [U_ILLEGAL_ARGUMENT_ERROR]")
			if obj := e.AsPlainObject(); obj != nil {
				obj.SetOwn("code", vm.NewString("U_ILLEGAL_ARGUMENT_ERROR"))
				obj.SetOwn("errno", vm.NumberValue(1))
			}
			return vm.Undefined, &fsSystemError{exception: e, message: "Unable to transcode Buffer [U_ILLEGAL_ARGUMENT_ERROR]"}
		}
		return wrapBuffer(vmInst, encodeRunes(decodeRunes(data, from), to)), nil
	}))
}

// transcodeEncoding normalizes the encodings ICU transcode supports.
func transcodeEncoding(e string) string {
	switch strings.ToLower(e) {
	case "utf8", "utf-8":
		return "utf8"
	case "ucs2", "ucs-2", "utf16le", "utf-16le":
		return "utf16le"
	case "latin1", "binary":
		return "latin1"
	case "ascii":
		return "ascii"
	}
	return ""
}

func decodeRunes(b []byte, enc string) []rune {
	switch enc {
	case "utf16le":
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		return utf16.Decode(u)
	case "latin1", "ascii":
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		return r
	}
	return []rune(string(b))
}

// encodeRunes writes runes in enc; like ICU, characters latin1/ascii can't
// hold become "?".
func encodeRunes(r []rune, enc string) []byte {
	switch enc {
	case "utf16le":
		u := utf16.Encode(r)
		out := make([]byte, 2*len(u))
		for i, c := range u {
			out[2*i], out[2*i+1] = byte(c), byte(c>>8)
		}
		return out
	case "latin1", "ascii":
		limit := rune(0xff)
		if enc == "ascii" {
			limit = 0x7f
		}
		out := make([]byte, len(r))
		for i, c := range r {
			if c > limit {
				c = '?'
			}
			out[i] = byte(c)
		}
		return out
	}
	return []byte(string(r))
}
