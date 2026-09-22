package host

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
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

// keyObjectMarker backs crypto.KeyObject - a nominal marker only, the
// same "exists so instanceof doesn't throw" shape as http_shim.go's own
// Server/IncomingMessage/ServerResponse. Real Node's KeyObject wraps an
// actual asymmetric/symmetric key with export()/type/asymmetricKeyType/
// etc, none of which is implemented here - found via real, unmodified
// jsonwebtoken: sign.js does
// `secretOrPrivateKey instanceof KeyObject` unconditionally, for every
// algorithm, even HS256 with a plain string secret - with no KeyObject
// export on this module at all, that line threw "Right-hand side of
// 'instanceof' is not an object" immediately, before a single token
// could ever be signed. A plain string/Buffer secret (the only kind
// this file's own createHmac/createHash support today) correctly
// answers `false` to that check without this class needing to do
// anything beyond existing. createSecretKey below builds its own
// separate object (not chained to this marker's prototype - nothing
// downstream ever re-checks `instanceof KeyObject` on its result, only
// `.type`/`.export`, so the two don't need to share a prototype).
type keyObjectMarker struct{}

// secretKeyObjectData is the internal payload createSecretKey's own
// returned object carries - checked directly by keyBytesFromValue
// (below) so createHmac/createHash can recover the real key bytes
// without a JS round-trip through .export().
type secretKeyObjectData struct {
	secret []byte
}

// createSecretKeyObject builds the plain object real Node's own
// crypto.createSecretKey(keyMaterial) returns: `.type === "secret"`
// (checked directly by jsonwebtoken's own sign.js once a plain secret
// has been wrapped) and a callable `.export()` (checked by jwa's own
// checkIsSecretKey guard, which every real HS256 sign/verify call goes
// through) that hands back the raw bytes as a real Buffer.
func createSecretKeyObject(vmInst *vm.VM, secret []byte) vm.Value {
	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	obj.SetInternalSlots(&secretKeyObjectData{secret: secret})
	obj.SetOwn("type", vm.NewString("secret"))
	obj.SetOwn("symmetricKeySize", vm.NumberValue(float64(len(secret))))
	obj.SetOwn("export", vm.NewNativeFunction(0, true, "export", func(_ []vm.Value) (vm.Value, error) {
		return wrapBuffer(vmInst, secret), nil
	}))
	return vm.NewValueFromPlainObject(obj)
}

// keyBytesFromValue recovers raw key material from a value that might
// be one of createSecretKey's own KeyObject-shaped objects (checked via
// its internal slot, no JS call needed) - used by createHmac so a
// KeyObject argument (real Node accepts one directly, and real,
// unmodified jwa's own createHmacSigner passes exactly the
// createSecretKey result straight through) is unwrapped correctly
// instead of being stringified into garbage by valueToBytes' own
// generic ".toString()" fallback for a plain object.
func keyBytesFromValue(vmInst *vm.VM, v vm.Value) []byte {
	if v.Type() == vm.TypeObject {
		if obj := v.AsPlainObject(); obj != nil {
			if data, ok := obj.InternalSlots().(*secretKeyObjectData); ok {
				return data.secret
			}
		}
	}
	return valueToBytes(vmInst, v)
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
		// timingSafeEqual(a, b): missing entirely - found via real,
		// unmodified jsonwebtoken/jwa: jwa's own createHmacVerifier calls
		// `'timingSafeEqual' in crypto ? crypto.timingSafeEqual(...) :
		// require('buffer-equal-constant-time')(...)`, unconditionally,
		// for every HS256 verify() call - with this missing, real code
		// silently took the `buffer-equal-constant-time` fallback branch
		// instead (a real, separate npm package, still installed as a
		// transitive dependency for exactly this compat case), which
		// itself references `require('buffer').SlowBuffer.prototype` - a
		// second, real gap (SlowBuffer isn't implemented at all here)
		// this sidesteps entirely by giving real code the fast path it
		// actually expects to take. Panics (a length mismatch) rather
		// than erroring: real Node's own timingSafeEqual throws
		// synchronously for exactly this, not a JS-catchable rejection.
		m.Function("timingSafeEqual", func(a, b vm.Value) (bool, error) {
			aBytes, bBytes := valueToBytes(vmInst, a), valueToBytes(vmInst, b)
			if len(aBytes) != len(bBytes) {
				return false, fmt.Errorf("Input buffers must have the same byte length")
			}
			return subtle.ConstantTimeCompare(aBytes, bBytes) == 1, nil
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
			keyBytes := keyBytesFromValue(vmInst, key)
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
		m.Class("KeyObject", &keyObjectMarker{}, func() (*keyObjectMarker, error) {
			return nil, fmt.Errorf("crypto.KeyObject is not constructible directly")
		})
		// createSecretKey(keyMaterial): real Node wraps any HMAC secret in
		// one of these before using it, and real, unmodified jsonwebtoken's
		// own sign.js does exactly that for every HS256 token
		// (`createSecretKey(Buffer.from(secretOrPrivateKey))`), then reads
		// `.type` straight off the result to confirm it's usable for an
		// HS* algorithm - a real, unconditional call, not a hypothetical
		// one. See createSecretKeyObject's own doc comment for the shape
		// jwa's own checkIsSecretKey guard additionally requires
		// (`.export()` callable).
		m.Function("createSecretKey", func(key vm.Value) (vm.Value, error) {
			return createSecretKeyObject(vmInst, valueToBytes(vmInst, key)), nil
		})
		// createPublicKey/createPrivateKey: real asymmetric-key support
		// (RSA/EC key parsing, sign()/verify() against them) isn't
		// implemented here at all - an honest, flagged gap, not a silent
		// one. createPublicKey exists as a real function anyway because
		// real, unmodified jwa (jsonwebtoken's own signing/verification
		// engine) feature-detects KeyObject support this exact way:
		// `var supportsKeyObjects = typeof crypto.createPublicKey ===
		// 'function'`, unconditionally, at module load - with it
		// undefined, jwa treats KeyObjects as universally unsupported and
		// rejects createSecretKey's own result (a real KeyObject-shaped
		// value) before ever reaching the `.type`/`.export` checks that
		// would otherwise accept it, breaking plain HS256 signing too,
		// not just RS256/ES256.
		m.Function("createPublicKey", func(_ vm.Value) (vm.Value, error) {
			return vm.Undefined, fmt.Errorf("crypto.createPublicKey is not implemented")
		})
		m.Function("createPrivateKey", func(_ vm.Value) (vm.Value, error) {
			return vm.Undefined, fmt.Errorf("crypto.createPrivateKey is not implemented")
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:crypto", "crypto")
}
