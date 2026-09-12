package host

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

type hashHasher struct {
	h      hash.Hash
	vmInst *vm.VM
}

// Update accepts a real vm.Value (not a plain Go string) and extracts
// its raw bytes via valueToBytes (net.go) - real Node's own
// hash.update()/hmac.update() take a string, Buffer, or TypedArray,
// and real callers pass all three shapes. Found the hard way chasing
// the real Bedrock investigation (docs/real-node-plan.md, round 100):
// this used to take a plain Go `string` parameter, so a Uint8Array
// argument (paserati/reflection's own string-conversion path for a
// typed array, not a UTF-8-safe byte extraction) silently produced a
// WRONG digest for any byte >= 0x80 - confirmed directly, not assumed:
// hashing the same 4 bytes (0xFF 0x80 0x01 0x02) via
// `crypto.createHash('sha256').update(new Uint8Array(...))` gave a
// completely different hex digest than real Node's own output. Real
// @smithy/signature-v4 code always calls `.update()` with a real
// Uint8Array (`hash.update(serde.toUint8Array(data))`), not a string,
// so this wasn't a hypothetical gap - it would have silently computed
// wrong AWS SigV4 signatures.
func (h *hashHasher) Update(data vm.Value) *hashHasher {
	_, _ = h.h.Write(valueToBytes(h.vmInst, data))
	return h
}

// Digest returns a real Buffer when no encoding is given, matching real
// Node's own hash.digest() contract - not a Go string of raw byte
// values reinterpreted as JS UTF-16 code units (which silently
// corrupts any byte >= 0x80 the moment it's used as input to anything
// encoding/decoding-aware downstream, e.g. real @smithy/signature-v4's
// own getSigningKey(), which iteratively HMACs one digest's raw output
// bytes as the next HMAC's key - `AWS4` + secret -> date -> region ->
// service -> "aws4_request").
func (h *hashHasher) Digest(encoding string) vm.Value {
	sum := h.h.Sum(nil)
	switch strings.ToLower(encoding) {
	case "hex":
		return vm.NewString(hex.EncodeToString(sum))
	case "base64":
		return vm.NewString(base64.StdEncoding.EncodeToString(sum))
	default:
		return wrapBuffer(h.vmInst, sum)
	}
}

func declareCrypto(p *driver.Paserati) {
	vmInst := p.GetVM()
	p.DeclareModule("crypto", func(m *driver.ModuleBuilder) {
		m.Function("randomUUID", func() string {
			b := make([]byte, 16)
			_, _ = rand.Read(b)
			b[6] = (b[6] & 0x0f) | 0x40
			b[8] = (b[8] & 0x3f) | 0x80
			return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
				b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
		})
		m.Function("randomBytes", func(n float64) (vm.Value, error) {
			size := int(n)
			if size < 0 {
				size = 0
			}
			b := make([]byte, size)
			if _, err := rand.Read(b); err != nil {
				return vm.Undefined, err
			}
			return wrapBuffer(vmInst, b), nil
		})
		// getRandomValues(typedArray): fills the array in place with
		// cryptographically random bytes and returns the same array -
		// real Node exposes this both as the WHATWG Crypto method
		// (globalThis.crypto.getRandomValues, not implemented here - a
		// separate, larger gap: the whole WebCrypto global is absent,
		// not just this one method) and directly on the `crypto` module
		// itself, which is the shape real code actually reaches for.
		// Found missing the hard way chasing the real Bedrock
		// investigation (docs/real-node-plan.md, round 100):
		// @smithy/core's own dist-cjs submodules/serde/index.js does
		// `const _getRandomValues = node_crypto.getRandomValues;` at
		// module top level and uses it to seed UUID v4 generation for
		// every request's real invocation-id header - a real,
		// unconditional call, not a hypothetical one. Unlike
		// typedArrayBytes (buffer.go), which deliberately copies (every
		// other real caller of it only reads), this has to write
		// directly into the typed array's own backing buffer - copying
		// out, filling the copy, and discarding it would leave the
		// caller's own array untouched, silently wrong instead of
		// crashing.
		m.Function("getRandomValues", func(data vm.Value) (vm.Value, error) {
			ta := data.AsTypedArray()
			if ta == nil {
				return vm.Undefined, fmt.Errorf("The provided value is not of type 'ArrayBufferView'")
			}
			buf := ta.GetBuffer()
			if buf == nil || buf.IsDetached() {
				return vm.Undefined, fmt.Errorf("getRandomValues: buffer is detached")
			}
			bufData := buf.GetData()
			off, ln := ta.GetByteOffset(), ta.GetByteLength()
			if off < 0 || ln < 0 || off+ln > len(bufData) {
				return vm.Undefined, fmt.Errorf("getRandomValues: out of bounds")
			}
			if _, err := rand.Read(bufData[off : off+ln]); err != nil {
				return vm.Undefined, err
			}
			return data, nil
		})
		// getHashes(): real Node returns every digest algorithm name
		// OpenSSL supports on the running system (a large list).
		// Returning exactly (and only) the five algorithms createHash
		// below actually implements is the honest analogue here - found
		// missing while probing real undici (round 74,
		// docs/real-node-plan.md): undici's own
		// lib/web/subresource-integrity/subresource-integrity.js calls
		// this unconditionally at module load time
		// (crypto.getHashes()) to check SRI support, so a missing
		// export threw before any of undici's own fetch code ran.
		m.Function("getHashes", func() vm.Value {
			arr := vm.NewArray()
			a := arr.AsArray()
			for _, name := range []string{"md5", "sha1", "sha256", "sha384", "sha512"} {
				a.Append(vm.NewString(name))
			}
			return arr
		})
		m.Function("createHash", func(algo string) (*hashHasher, error) {
			switch strings.ToLower(algo) {
			case "md5":
				// Found via jiti's own filesystem cache-key hashing
				// (getCache -> utils_hash) - a completely ordinary
				// non-cryptographic use; md5 is still real Node's
				// default fast hash for this kind of thing.
				return &hashHasher{h: md5.New(), vmInst: vmInst}, nil
			case "sha1":
				return &hashHasher{h: sha1.New(), vmInst: vmInst}, nil
			case "sha256":
				return &hashHasher{h: sha256.New(), vmInst: vmInst}, nil
			case "sha384":
				return &hashHasher{h: sha512.New384(), vmInst: vmInst}, nil
			case "sha512":
				return &hashHasher{h: sha512.New(), vmInst: vmInst}, nil
			default:
				return nil, fmt.Errorf("Digest algorithm %q is not supported", algo)
			}
		})
		// createHmac(algorithm, key): found missing alongside
		// createHash's own Update/Digest correctness fix (see
		// hashHasher's doc comments above) - real
		// @smithy/signature-v4's own real SigV4 key-derivation chain
		// (getSigningKey -> hmac(sha256, "AWS4"+secret, date) ->
		// hmac(sha256, kDate, region) -> ... -> "aws4_request") is
		// built entirely on `crypto.createHmac`, called via
		// @smithy/core's own `Hash` class (dist-cjs/submodules/serde/
		// index.js) whenever a `secret` is present - a real,
		// unconditional call for every signed AWS request, not a
		// hypothetical one. `key` accepts a vm.Value (not a plain Go
		// string) for the same reason Update does: real code passes a
		// Buffer/Uint8Array key (the previous HMAC round's own raw
		// digest output) just as often as a plain string one.
		m.Function("createHmac", func(algo string, key vm.Value) (*hashHasher, error) {
			keyBytes := valueToBytes(vmInst, key)
			switch strings.ToLower(algo) {
			case "md5":
				return &hashHasher{h: hmac.New(md5.New, keyBytes), vmInst: vmInst}, nil
			case "sha1":
				return &hashHasher{h: hmac.New(sha1.New, keyBytes), vmInst: vmInst}, nil
			case "sha256":
				return &hashHasher{h: hmac.New(sha256.New, keyBytes), vmInst: vmInst}, nil
			case "sha384":
				return &hashHasher{h: hmac.New(sha512.New384, keyBytes), vmInst: vmInst}, nil
			case "sha512":
				return &hashHasher{h: hmac.New(sha512.New, keyBytes), vmInst: vmInst}, nil
			default:
				return nil, fmt.Errorf("Digest algorithm %q is not supported", algo)
			}
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:crypto", "crypto")
}
