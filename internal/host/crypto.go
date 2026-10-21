package host

import (
	"embed"
	"sort"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
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
}
