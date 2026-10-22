package host

import (
	"embed"
	"sort"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// node:crypto is a JS shim assembled from crypto_js/*.js (concatenated in
// file-name order into a single ES module, so the pieces share one scope)
// over the Go primitives in crypto_binding.go and its sibling files.
//
// History worth keeping from the Go-declared module this replaced: real
// packages reach for crypto in ways that fail loudly when a piece is
// missing - jsonwebtoken/jwa test `secret instanceof KeyObject` and
// `typeof crypto.createPublicKey === 'function'` at load time, undici
// calls getHashes() at module load, @smithy/core grabs getRandomValues at
// top level and HMACs raw Buffer digests for SigV4 (so update() and keys
// must take bytes, never strings of byte values).

//go:embed crypto_js/*.js
var cryptoJSFiles embed.FS

func cryptoShimSource() string {
	entries, err := cryptoJSFiles.ReadDir("crypto_js")
	if err != nil {
		panic(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		data, err := cryptoJSFiles.ReadFile("crypto_js/" + n)
		if err != nil {
			panic(err)
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func declareCrypto(p *driver.Paserati) {
	registerJSShim("crypto", cryptoShimSource())
	installCryptoBinding(p)
	installWebCryptoGlobals(p)
}

// installWebCryptoGlobals exposes crypto, Crypto, CryptoKey and
// SubtleCrypto as lazy accessors on globalThis: node:crypto (and the
// node:stream it pulls in) is only loaded the first time one is read.
func installWebCryptoGlobals(p *driver.Paserati) {
	vmInst := p.GetVM()
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok || gt.AsPlainObject() == nil {
		return
	}
	gobj := gt.AsPlainObject()
	lazy := &lazyJSModule{p: p, spec: "crypto"}
	var globals *vm.PlainObject
	load := func(name string) (vm.Value, error) {
		if globals == nil {
			if _, err := lazy.get("webcrypto"); err != nil {
				return vm.Undefined, err
			}
			bv, _ := gobj.GetOwn("__noderatiCrypto")
			if bv.Type() != vm.TypeObject {
				return vm.Undefined, nil
			}
			wg, _ := bv.AsPlainObject().GetOwn("webGlobals")
			if wg.Type() != vm.TypeObject {
				return vm.Undefined, nil
			}
			globals = wg.AsPlainObject()
		}
		v, _ := globals.GetOwn(name)
		return v, nil
	}
	yes, no := true, false
	for _, name := range []string{"crypto", "Crypto", "CryptoKey", "SubtleCrypto"} {
		name := name
		getter := vm.NewNativeFunction(0, false, "get "+name, func(_ []vm.Value) (vm.Value, error) {
			return load(name)
		})
		if name == "crypto" {
			// node: a getter without a setter, enumerable.
			gobj.DefineAccessorProperty(name, getter, true, vm.Undefined, false, &yes, &yes)
			continue
		}
		setter := vm.NewNativeFunction(1, false, "set "+name, func(args []vm.Value) (vm.Value, error) {
			gobj.DeleteOwn(name)
			gobj.SetOwn(name, argAt(args, 0))
			return vm.Undefined, nil
		})
		gobj.DefineAccessorProperty(name, getter, true, setter, true, &no, &yes)
	}
}
