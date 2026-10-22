package host

import (
	"unicode/utf8"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// installTextEncoderEncodeInto adds TextEncoder.prototype.encodeInto, which
// the engine's TextEncoder lacks: UTF-8 encode source into the Uint8Array
// destination, stopping before the first code point that doesn't fit, and
// report {read (UTF-16 units), written (bytes)}.
func installTextEncoderEncodeInto(p *driver.Paserati) {
	vmInst := p.GetVM()
	ctor, ok := vmInst.GetGlobal("TextEncoder")
	if !ok {
		return
	}
	proto, err := jsReflectGet(vmInst, ctor, vm.NewString("prototype"))
	if err != nil || isNullish(proto) {
		return
	}
	if existing, err := jsReflectGet(vmInst, proto, vm.NewString("encodeInto")); err == nil && existing.IsCallable() {
		return
	}
	fn := vm.NewNativeFunction(2, false, "encodeInto", func(args []vm.Value) (vm.Value, error) {
		dest := argAt(args, 1)
		ta := dest.AsTypedArray()
		if dest.Type() != vm.TypeTypedArray || ta == nil || ta.GetElementType() != vm.TypedArrayUint8 {
			return vm.Undefined, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE",
				`The "dest" argument must be an instance of Uint8Array.`+receivedSuffix(vmInst, dest))
		}
		src := ""
		if len(args) > 0 && !args[0].IsUndefined() {
			src = args[0].ToString()
		}
		var out []byte
		if bd := ta.GetBufferData(); bd != nil && !bd.IsDetached() {
			data := bd.GetData()
			off := ta.GetByteOffset()
			end := off + ta.GetLength()
			if off <= len(data) && end <= len(data) {
				out = data[off:end]
			}
		}
		read, written := 0, 0
		var buf [4]byte
		for i := 0; i < len(src); {
			r, size := utf8.DecodeRuneInString(src[i:])
			units := 1
			if r == utf8.RuneError && size == 1 {
				// A lone surrogate kept as its 3-byte WTF-8 form encodes
				// as U+FFFD, as a USVString conversion would.
				if i+2 < len(src) && src[i] == 0xED && src[i+1] >= 0xA0 {
					size = 3
				}
			} else if r > 0xFFFF {
				units = 2
			}
			n := utf8.EncodeRune(buf[:], r)
			if written+n > len(out) {
				break
			}
			copy(out[written:], buf[:n])
			written += n
			read += units
			i += size
		}
		res := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		res.SetOwn("read", vm.IntegerValue(int32(read)))
		res.SetOwn("written", vm.IntegerValue(int32(written)))
		return vm.NewValueFromPlainObject(res), nil
	})
	d := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	d.SetOwn("value", fn)
	d.SetOwn("writable", vm.True)
	d.SetOwn("enumerable", vm.True)
	d.SetOwn("configurable", vm.True)
	_, _ = vmInst.Call(jsGlobalMethod(vmInst, "Object", "defineProperty"), vm.Undefined,
		[]vm.Value{proto, vm.NewString("encodeInto"), vm.NewValueFromPlainObject(d)})
}
