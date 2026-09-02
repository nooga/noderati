package host

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/nooga/paserati/pkg/vm"
)

// atob/btoa are Web-standard globals real Node also provides (since Node
// 16, via lib/internal/buffer.js) — base64 encode/decode of a "binary
// string" (each JS character represents one byte, 0-255, not a full
// Unicode code point). Real usage found via pi-ai's OAuth PKCE flow
// (btoa) and pi-coding-agent's own HTML export template (atob), both of
// which noderati didn't provide at all — `typeof atob` was `undefined`.
//
// Implemented to match real Node's actual behavior, not just the happy
// path: btoa throws on any input character outside Latin1 range (real
// Node does too — silently truncating high bits instead would be exactly
// the kind of silent-wrong-output bug this whole project chases down
// when found in third-party code); atob strips ASCII whitespace per the
// WHATWG spec Node's own implementation follows, and throws on
// non-base64-alphabet characters or invalid padding rather than
// decoding something wrong without complaint.
//
// A decoded/encoded byte becomes one JS "character" via its own code
// point (matching String.fromCharCode(n)/charCodeAt(n) semantics,
// confirmed directly against plain paserati) — not a raw appended byte,
// which would corrupt anything >= 0x80 once paserati's own rune-based
// string handling re-decodes it as UTF-8.

func btoaFn() vm.Value {
	return vm.NewNativeFunction(1, false, "btoa", func(args []vm.Value) (vm.Value, error) {
		input := ""
		if len(args) > 0 {
			input = args[0].ToString()
		}
		raw := make([]byte, 0, len(input))
		for _, r := range input {
			if r > 0xFF {
				return vm.Undefined, fmt.Errorf("Invalid character")
			}
			raw = append(raw, byte(r))
		}
		return vm.NewString(base64.StdEncoding.EncodeToString(raw)), nil
	})
}

func atobFn() vm.Value {
	return vm.NewNativeFunction(1, false, "atob", func(args []vm.Value) (vm.Value, error) {
		input := ""
		if len(args) > 0 {
			input = args[0].ToString()
		}
		// Strip ASCII whitespace (tab, LF, FF, CR, space) per the WHATWG
		// forgiving-base64 algorithm real Node's atob follows.
		input = strings.Map(func(r rune) rune {
			switch r {
			case '\t', '\n', '\f', '\r', ' ':
				return -1
			default:
				return r
			}
		}, input)
		// Go's base64.StdEncoding requires exact padding; real base64 text
		// (and real-world callers) sometimes omit it. Restore it rather
		// than rejecting otherwise-valid input over a missing '='.
		if rem := len(input) % 4; rem != 0 {
			input += strings.Repeat("=", 4-rem)
		}
		decoded, err := base64.StdEncoding.DecodeString(input)
		if err != nil {
			return vm.Undefined, fmt.Errorf("Invalid character")
		}
		var sb strings.Builder
		sb.Grow(len(decoded))
		for _, b := range decoded {
			sb.WriteRune(rune(b))
		}
		return vm.NewString(sb.String()), nil
	})
}
