package host

import (
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// file_global.go implements the WHATWG `File` global - real Node (and
// paserati's own fetch/FormData/Blob implementation, already present
// and real) exposes it unconditionally, no import needed. Found missing
// while probing real undici (round 73, docs/real-node-plan.md):
// lib/web/webidl/index.js does `webidl.is.File =
// webidl.util.MakeTypeAssertion(File)` at its own module top level (not
// inside a function - so a missing `File` throws immediately at
// require() time, unconditionally, before any actual File usage), and
// paserati has real `Blob`/`FormData`/`Headers`/`Request`/`Response`/
// `fetch` globals already but no `File` alongside them.
//
// A real File genuinely just is a Blob with a name and lastModified
// timestamp added (the WHATWG spec defines it exactly this way - File
// inherits every one of Blob's own methods/behavior unchanged), so this
// builds a real subclass of paserati's own real Blob - `.slice()`,
// `.arrayBuffer()`, `.text()`, `.stream()` etc. all come from the real
// Blob implementation for free, via vmInst.Construct(blobCtor, ...)
// building a genuine Blob instance and re-parenting it onto File's own
// prototype (which itself points at Blob.prototype).
//
// Built as a native Go constructor rather than JS source run through
// EvalCode/RunCode - a real, encountered bug (bisected down to a
// minimal repro, filed as paserati#298) makes running *any* second
// top-level script/module on the same *driver.Paserati instance corrupt
// a later script's ability to propagate a synchronous throw out of a
// native-function call (e.g. runInAsyncScope(() => { throw ... })) -
// reproduced even with a prior script as trivial as `1+1`, regardless
// of RunCode vs EvalCode vs Script-mode vs module-mode. Every other
// global this codebase installs at Paserati-construction time (Buffer,
// util.types/promisify, ...) already avoids this entirely by building
// vm.Values directly in Go rather than evaluating JS source - this file
// follows that same, now clearly-necessary, discipline.
func installFileGlobal(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	blobCtorVal, ok := vmInst.GetGlobal("Blob")
	if !ok || !blobCtorVal.IsCallable() {
		return // no real Blob to build a real File on top of - nothing honest to do here
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	gobj := gt.AsPlainObject()
	if gobj == nil {
		return
	}
	if _, exists := gobj.GetOwn("File"); exists {
		return
	}

	blobProps := blobCtorVal.AsNativeFunctionWithProps()
	if blobProps == nil || blobProps.Properties == nil {
		return
	}
	blobProtoVal, ok := blobProps.Properties.GetOwn("prototype")
	if !ok {
		return
	}

	fileProto := vm.NewObject(blobProtoVal).AsPlainObject()

	fileCtor := vm.NewConstructorWithProps(2, false, "File", func(args []vm.Value) (vm.Value, error) {
		fileBits := vm.Undefined
		var fileName string
		options := vm.Undefined
		if len(args) > 0 {
			fileBits = args[0]
		}
		if len(args) > 1 {
			fileName = args[1].ToString()
		}
		if len(args) > 2 {
			options = args[2]
		}

		blobArgs := []vm.Value{fileBits}
		if !options.IsUndefined() {
			blobArgs = append(blobArgs, options)
		}
		instance, err := vmInst.Construct(blobCtorVal, blobArgs)
		if err != nil {
			return vm.Undefined, err
		}
		obj := instance.AsPlainObject()
		if obj == nil {
			return instance, nil
		}
		obj.SetPrototype(vm.NewValueFromPlainObject(fileProto))
		obj.SetOwn("name", vm.NewString(fileName))

		lastModified := float64(time.Now().UnixMilli())
		if options.Type() == vm.TypeObject {
			if optObj := options.AsPlainObject(); optObj != nil {
				if v, ok := optObj.GetOwn("lastModified"); ok && v.IsNumber() {
					lastModified = v.ToFloat()
				}
			}
		}
		obj.SetOwn("lastModified", vm.NumberValue(lastModified))
		return instance, nil
	})
	if props := fileCtor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", vm.NewValueFromPlainObject(fileProto))
	}
	fileProto.SetOwnNonEnumerable("constructor", fileCtor)

	enumerableFalse, configurableTrue := false, true
	fileProto.DefineAccessorPropertyByKey(
		vm.NewSymbolKey(vmInst.SymbolToStringTag),
		vm.NewNativeFunction(0, false, "get [Symbol.toStringTag]", func(_ []vm.Value) (vm.Value, error) {
			return vm.NewString("File"), nil
		}),
		true,
		vm.Undefined,
		false,
		&enumerableFalse,
		&configurableTrue,
	)

	gobj.SetOwn("File", fileCtor)
}
