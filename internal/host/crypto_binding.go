package host

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // node still offers ripemd160
	"golang.org/x/crypto/scrypt"
)

// The crypto module is a JS shim (crypto_js/*.js) doing node's argument
// validation, error shaping, classes and streams, on top of this binding:
// a bag of byte-level primitives installed as the non-enumerable global
// __noderatiCrypto. Every function here takes already-normalized inputs
// (Uint8Array/Buffer bytes, plain strings and numbers) - the shim converts
// strings with encodings, ArrayBuffers and DataViews before calling in.
//
// Stateful things (hash contexts, cipher contexts, keys, certificates) are
// opaque handle objects carrying a Go value in an internal slot.

// cryptoError is a Go error that throws as a node-shaped JS error: an
// instance of ctor ("Error", "TypeError", "RangeError") with .code and any
// extra string properties. The JS value is built lazily, on the VM thread,
// so these can be produced by background goroutines too.
type cryptoError struct {
	vmInst *vm.VM
	ctor   string
	code   string
	msg    string
	props  map[string]string
}

func (e *cryptoError) Error() string { return e.msg }

func (e *cryptoError) GetExceptionValue() vm.Value {
	ctor := e.ctor
	if ctor == "" {
		ctor = "Error"
	}
	var exc vm.Value
	if c, ok := e.vmInst.GetGlobal(ctor); ok {
		if v, err := e.vmInst.Construct(c, []vm.Value{vm.NewString(e.msg)}); err == nil {
			exc = v
		}
	}
	if exc.Type() != vm.TypeObject {
		obj := vm.NewObject(e.vmInst.ObjectPrototype).AsPlainObject()
		obj.SetOwn("message", vm.NewString(e.msg))
		exc = vm.NewValueFromPlainObject(obj)
	}
	obj := exc.AsPlainObject()
	if e.code != "" {
		obj.SetOwn("code", vm.NewString(e.code))
	}
	for k, v := range e.props {
		obj.SetOwn(k, vm.NewString(v))
	}
	return exc
}

func (b *cryptoBinding) err(code, msg string) error {
	return &cryptoError{vmInst: b.vm, code: code, msg: msg}
}

func (b *cryptoBinding) typeErr(code, msg string) error {
	return &cryptoError{vmInst: b.vm, ctor: "TypeError", code: code, msg: msg}
}

func (b *cryptoBinding) rangeErr(code, msg string) error {
	return &cryptoError{vmInst: b.vm, ctor: "RangeError", code: code, msg: msg}
}

// osslErr mimics an OpenSSL-originated error the way node surfaces it:
// "error:XXXXXXXX:<lib>::<reason>" with code ERR_OSSL_<REASON>, plus
// .library and .reason.
func (b *cryptoBinding) osslErr(hexCode, library, reason string) error {
	return osslError(b.vm, hexCode, library, reason)
}

// osslLibPrefix is the ERR_OSSL_<LIB>_ prefix node derives from the
// OpenSSL library that raised an error.
var osslLibPrefix = map[string]string{
	"digital envelope routines": "EVP_",
	"asn1 encoding routines":    "ASN1_",
	"common libcrypto routines": "CRYPTO_",
	"rsa routines":              "RSA_",
	"elliptic curve routines":   "EC_",
	"PEM routines":              "PEM_",
	"x509 certificate routines": "X509_",
	"bignum routines":           "BN_",
	"Diffie-Hellman routines":   "DH_",
}

func osslError(vmInst *vm.VM, hexCode, library, reason string) error {
	code := "ERR_OSSL_" + osslLibPrefix[library] + strings.ToUpper(strings.NewReplacer(" ", "_", "-", "_").Replace(reason))
	b := &cryptoBinding{vm: vmInst}
	return &cryptoError{vmInst: b.vm, code: code,
		msg:   fmt.Sprintf("error:%s:%s::%s", hexCode, library, reason),
		props: map[string]string{"library": library, "reason": reason}}
}

type cryptoBinding struct {
	p   *driver.Paserati
	vm  *vm.VM
	obj *vm.PlainObject
}

func (b *cryptoBinding) fn(name string, f func(args []vm.Value) (vm.Value, error)) {
	b.obj.SetOwn(name, vm.NewNativeFunction(0, true, name, f))
}

// handle wraps a Go value in an opaque JS object.
func (b *cryptoBinding) handle(v any) vm.Value {
	obj := vm.NewObject(b.vm.ObjectPrototype).AsPlainObject()
	obj.SetInternalSlots(v)
	return vm.NewValueFromPlainObject(obj)
}

func handleOf[T any](args []vm.Value, i int) (T, bool) {
	var zero T
	v := argAt(args, i)
	if v.Type() != vm.TypeObject {
		return zero, false
	}
	t, ok := v.AsPlainObject().InternalSlots().(T)
	return t, ok
}

// bytesArg returns the bytes of a TypedArray/Buffer/ArrayBuffer argument,
// or a string's UTF-8 bytes. nil/undefined gives nil.
func (b *cryptoBinding) bytesArg(args []vm.Value, i int) []byte {
	v := argAt(args, i)
	if v.IsUndefined() || v.IsNull() {
		return nil
	}
	if ab := v.AsArrayBuffer(); ab != nil {
		d := ab.GetData()
		out := make([]byte, len(d))
		copy(out, d)
		return out
	}
	if b2 := valueToBytes(b.vm, v); b2 != nil {
		return b2
	}
	return []byte{}
}

func intArg(args []vm.Value, i int, def int) int {
	v := argAt(args, i)
	if !v.IsNumber() {
		return def
	}
	return int(v.ToFloat())
}

func strArg(args []vm.Value, i int) string {
	v := argAt(args, i)
	if v.IsUndefined() || v.IsNull() {
		return ""
	}
	return v.ToString()
}

func (b *cryptoBinding) buf(data []byte) vm.Value { return wrapBuffer(b.vm, data) }

func (b *cryptoBinding) jsonValue(v any) (vm.Value, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return vm.Undefined, err
	}
	return vm.NewString(string(data)), nil
}

// async runs work off the VM thread when cb is callable, delivering
// cb(err, value) later from the event loop; otherwise it runs work inline
// and returns its value (or throws). work must not touch the VM; finish
// runs on the VM thread to turn the result into a JS value.
func (b *cryptoBinding) async(cb vm.Value, work func() (func() vm.Value, error)) (vm.Value, error) {
	if !cb.IsCallable() {
		finish, err := work()
		if err != nil {
			return vm.Undefined, err
		}
		return finish(), nil
	}
	rt := b.vm.GetAsyncRuntime()
	rt.BeginExternalOp()
	go func() {
		finish, err := work()
		rt.ScheduleMacrotask(func() {
			defer rt.EndExternalOp()
			if err != nil {
				var exc vm.Value
				var ee vm.ExceptionError
				if errors.As(err, &ee) {
					exc = ee.GetExceptionValue()
				} else {
					exc = (&cryptoError{vmInst: b.vm, msg: err.Error()}).GetExceptionValue()
				}
				_, _ = b.vm.Call(cb, vm.Undefined, []vm.Value{exc})
				return
			}
			_, _ = b.vm.Call(cb, vm.Undefined, []vm.Value{vm.Null, finish()})
		})
	}()
	return vm.Undefined, nil
}

func installCryptoBinding(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok || gt.Type() != vm.TypeObject {
		return
	}
	b := &cryptoBinding{p: p, vm: vmInst, obj: vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()}
	b.installHash()
	b.installRandomAndKDF()
	b.installCipher()
	b.installKeys()
	b.installAsym()
	b.installX509()
	gt.AsPlainObject().SetOwnNonEnumerable("__noderatiCrypto", vm.NewValueFromPlainObject(b.obj))
}

// ---- hashes ---------------------------------------------------------------

type digestInfo struct {
	name    string // canonical node name
	newHash func() hash.Hash
	xof     bool // shake128/256
	size    int
}

var digestTable = map[string]*digestInfo{}

func init() {
	add := func(info *digestInfo, aliases ...string) {
		if info.newHash != nil && info.size == 0 {
			info.size = info.newHash().Size()
		}
		digestTable[info.name] = info
		for _, a := range aliases {
			digestTable[a] = info
		}
	}
	add(&digestInfo{name: "md5", newHash: md5.New}, "rsa-md5", "md5withrsaencryption", "ssl3-md5", "ssl2-md5")
	add(&digestInfo{name: "sha1", newHash: sha1.New}, "rsa-sha1", "sha1withrsaencryption", "ssl3-sha1", "rsa-sha1-2", "sha-1", "dsa-sha1", "ecdsa-with-sha1", "dsawithsha1")
	add(&digestInfo{name: "sha224", newHash: sha256.New224}, "rsa-sha224", "sha224withrsaencryption", "sha-224", "sha2-224")
	add(&digestInfo{name: "sha256", newHash: sha256.New}, "rsa-sha256", "sha256withrsaencryption", "sha-256", "sha2-256")
	add(&digestInfo{name: "sha384", newHash: sha512.New384}, "rsa-sha384", "sha384withrsaencryption", "sha-384", "sha2-384")
	add(&digestInfo{name: "sha512", newHash: sha512.New}, "rsa-sha512", "sha512withrsaencryption", "sha-512", "sha2-512")
	add(&digestInfo{name: "sha512-224", newHash: sha512.New512_224}, "rsa-sha512/224", "sha512-224withrsaencryption", "sha2-512/224", "sha-512/224")
	add(&digestInfo{name: "sha512-256", newHash: sha512.New512_256}, "rsa-sha512/256", "sha512-256withrsaencryption", "sha2-512/256", "sha-512/256")
	add(&digestInfo{name: "sha3-224", newHash: func() hash.Hash { return sha3.New224() }}, "rsa-sha3-224", "id-rsassa-pkcs1-v1_5-with-sha3-224")
	add(&digestInfo{name: "sha3-256", newHash: func() hash.Hash { return sha3.New256() }}, "rsa-sha3-256", "id-rsassa-pkcs1-v1_5-with-sha3-256")
	add(&digestInfo{name: "sha3-384", newHash: func() hash.Hash { return sha3.New384() }}, "rsa-sha3-384", "id-rsassa-pkcs1-v1_5-with-sha3-384")
	add(&digestInfo{name: "sha3-512", newHash: func() hash.Hash { return sha3.New512() }}, "rsa-sha3-512", "id-rsassa-pkcs1-v1_5-with-sha3-512")
	add(&digestInfo{name: "shake128", xof: true, size: 16}, "shake-128")
	add(&digestInfo{name: "shake256", xof: true, size: 32}, "shake-256")
	add(&digestInfo{name: "ripemd160", newHash: func() hash.Hash { return newReplayHash(ripemd160.New) }}, "ripemd", "rmd160", "rsa-ripemd160", "ripemd160withrsa", "ripemd-160")
	add(&digestInfo{name: "blake2b512", newHash: func() hash.Hash { h, _ := blake2b.New512(nil); return h }})
	add(&digestInfo{name: "blake2s256", newHash: func() hash.Hash { h, _ := blake2s.New256(nil); return h }})
	add(&digestInfo{name: "md5-sha1", newHash: newMD5SHA1, size: 36})
	add(&digestInfo{name: "sm3", newHash: newSM3}, "rsa-sm3", "sm3withrsaencryption")
}

func lookupDigest(name string) *digestInfo {
	return digestTable[strings.ToLower(name)]
}

// hashNames is what getHashes() reports, in node's order.
var hashNames = []string{"RSA-MD5", "RSA-RIPEMD160", "RSA-SHA1", "RSA-SHA1-2", "RSA-SHA224", "RSA-SHA256", "RSA-SHA3-224", "RSA-SHA3-256", "RSA-SHA3-384", "RSA-SHA3-512", "RSA-SHA384", "RSA-SHA512", "RSA-SHA512/224", "RSA-SHA512/256", "RSA-SM3", "blake2b512", "blake2s256", "id-rsassa-pkcs1-v1_5-with-sha3-224", "id-rsassa-pkcs1-v1_5-with-sha3-256", "id-rsassa-pkcs1-v1_5-with-sha3-384", "id-rsassa-pkcs1-v1_5-with-sha3-512", "md5", "md5-sha1", "md5WithRSAEncryption", "ripemd", "ripemd160", "ripemd160WithRSA", "rmd160", "sha1", "sha1WithRSAEncryption", "sha224", "sha224WithRSAEncryption", "sha256", "sha256WithRSAEncryption", "sha3-224", "sha3-256", "sha3-384", "sha3-512", "sha384", "sha384WithRSAEncryption", "sha512", "sha512-224", "sha512-224WithRSAEncryption", "sha512-256", "sha512-256WithRSAEncryption", "sha512WithRSAEncryption", "shake128", "shake256", "sm3", "sm3WithRSAEncryption", "ssl3-md5", "ssl3-sha1"}

// replayHash makes a hash without exportable state (x/crypto's ripemd160)
// cloneable by remembering its input.
type replayHash struct {
	hash.Hash
	fresh func() hash.Hash
	data  []byte
}

func newReplayHash(fresh func() hash.Hash) hash.Hash {
	return &replayHash{Hash: fresh(), fresh: fresh}
}

func (h *replayHash) Write(p []byte) (int, error) {
	h.data = append(h.data, p...)
	return h.Hash.Write(p)
}

func (h *replayHash) Reset() { h.Hash.Reset(); h.data = nil }

func (h *replayHash) Clone() (hash.Cloner, error) {
	n := &replayHash{Hash: h.fresh(), fresh: h.fresh, data: append([]byte(nil), h.data...)}
	n.Hash.Write(n.data)
	return n, nil
}

type md5sha1 struct{ m, s hash.Hash }

func newMD5SHA1() hash.Hash { return &md5sha1{md5.New(), sha1.New()} }
func (h *md5sha1) Write(p []byte) (int, error) {
	h.m.Write(p)
	return h.s.Write(p)
}
func (h *md5sha1) Sum(b []byte) []byte { return h.s.Sum(h.m.Sum(b)) }
func (h *md5sha1) Reset()              { h.m.Reset(); h.s.Reset() }
func (h *md5sha1) Size() int           { return 36 }
func (h *md5sha1) BlockSize() int      { return 64 }
func (h *md5sha1) Clone() (hash.Cloner, error) {
	m, err := cloneHash(h.m)
	if err != nil {
		return nil, err
	}
	s, err := cloneHash(h.s)
	if err != nil {
		return nil, err
	}
	return &md5sha1{m, s}, nil
}

func cloneHash(h hash.Hash) (hash.Hash, error) {
	if c, ok := h.(hash.Cloner); ok {
		return c.Clone()
	}
	return nil, fmt.Errorf("hash state cannot be copied")
}

// cloneHashVia clones h, falling back to a marshal/unmarshal round trip
// into a fresh instance (x/crypto's ripemd160 and blake2 support that but
// not hash.Cloner).
func cloneHashVia(h hash.Hash, fresh func() hash.Hash) (hash.Hash, error) {
	if c, err := cloneHash(h); err == nil {
		return c, nil
	}
	m, ok := h.(encoding.BinaryMarshaler)
	if !ok {
		return nil, fmt.Errorf("hash state cannot be copied")
	}
	state, err := m.MarshalBinary()
	if err != nil {
		return nil, err
	}
	n := fresh()
	u, ok := n.(encoding.BinaryUnmarshaler)
	if !ok {
		return nil, fmt.Errorf("hash state cannot be copied")
	}
	if err := u.UnmarshalBinary(state); err != nil {
		return nil, err
	}
	return n, nil
}

// cryptoHash is a digest or HMAC context. For HMAC, h is the inner state
// and the context is cloned through hmac's own Cloner support.
type cryptoHash struct {
	info   *digestInfo
	h      hash.Hash
	shake  *sha3.SHAKE
	outLen int
}

func newCryptoHash(info *digestInfo, outLen int) *cryptoHash {
	ch := &cryptoHash{info: info, outLen: outLen}
	if info.xof {
		if info.name == "shake128" {
			ch.shake = sha3.NewSHAKE128()
		} else {
			ch.shake = sha3.NewSHAKE256()
		}
	} else {
		ch.h = info.newHash()
	}
	return ch
}

func (ch *cryptoHash) write(p []byte) {
	if ch.shake != nil {
		ch.shake.Write(p)
	} else {
		ch.h.Write(p)
	}
}

func (ch *cryptoHash) sum() []byte {
	if ch.shake != nil {
		out := make([]byte, ch.outLen)
		ch.shake.Read(out)
		return out
	}
	return ch.h.Sum(nil)
}

func (ch *cryptoHash) clone(outLen int) (*cryptoHash, error) {
	n := &cryptoHash{info: ch.info, outLen: outLen}
	if ch.shake != nil {
		state, err := ch.shake.MarshalBinary()
		if err != nil {
			return nil, err
		}
		if ch.info.name == "shake128" {
			n.shake = sha3.NewSHAKE128()
		} else {
			n.shake = sha3.NewSHAKE256()
		}
		if err := n.shake.UnmarshalBinary(state); err != nil {
			return nil, err
		}
		return n, nil
	}
	h, err := cloneHashVia(ch.h, ch.info.newHash)
	if err != nil {
		return nil, err
	}
	n.h = h
	return n, nil
}

func (b *cryptoBinding) installHash() {
	// hashCreate(name, outputLength|-1) -> handle; throws node's plain
	// "Digest method not supported" for unknown names.
	b.fn("hashCreate", func(args []vm.Value) (vm.Value, error) {
		info := lookupDigest(strArg(args, 0))
		if info == nil {
			return vm.Undefined, b.err("", "Digest method not supported")
		}
		outLen := intArg(args, 1, -1)
		if outLen >= 0 && !info.xof && outLen != info.size {
			return vm.Undefined, b.osslErr("030000B2", "digital envelope routines", "not XOF or invalid length")
		}
		if outLen < 0 {
			outLen = info.size
		}
		return b.handle(newCryptoHash(info, outLen)), nil
	})
	b.fn("hmacCreate", func(args []vm.Value) (vm.Value, error) {
		name := strArg(args, 0)
		info := lookupDigest(name)
		if info == nil || info.xof {
			return vm.Undefined, b.typeErr("ERR_CRYPTO_INVALID_DIGEST", "Invalid digest: "+name)
		}
		key := b.bytesArg(args, 1)
		return b.handle(&cryptoHash{info: info, h: hmac.New(info.newHash, key), outLen: info.size}), nil
	})
	b.fn("hashUpdate", func(args []vm.Value) (vm.Value, error) {
		ch, ok := handleOf[*cryptoHash](args, 0)
		if !ok {
			return vm.Undefined, fmt.Errorf("invalid hash handle")
		}
		v := argAt(args, 1)
		if ta := v.AsTypedArray(); ta != nil {
			buf := ta.GetBuffer()
			if buf != nil && !buf.IsDetached() {
				d := buf.GetData()
				off, ln := ta.GetByteOffset(), ta.GetByteLength()
				if off >= 0 && ln >= 0 && off+ln <= len(d) {
					ch.write(d[off : off+ln])
					return vm.Undefined, nil
				}
			}
		}
		ch.write(b.bytesArg(args, 1))
		return vm.Undefined, nil
	})
	b.fn("hashDigest", func(args []vm.Value) (vm.Value, error) {
		ch, ok := handleOf[*cryptoHash](args, 0)
		if !ok {
			return vm.Undefined, fmt.Errorf("invalid hash handle")
		}
		return b.buf(ch.sum()), nil
	})
	b.fn("hashCopy", func(args []vm.Value) (vm.Value, error) {
		ch, ok := handleOf[*cryptoHash](args, 0)
		if !ok {
			return vm.Undefined, fmt.Errorf("invalid hash handle")
		}
		outLen := intArg(args, 1, -1)
		if outLen < 0 {
			outLen = ch.outLen
		} else if !ch.info.xof && outLen != ch.info.size {
			return vm.Undefined, b.osslErr("030000B2", "digital envelope routines", "not XOF or invalid length")
		}
		n, err := ch.clone(outLen)
		if err != nil {
			return vm.Undefined, err
		}
		return b.handle(n), nil
	})
	// digestInfo(name) -> {name,size,xof} or undefined
	b.fn("digestInfo", func(args []vm.Value) (vm.Value, error) {
		info := lookupDigest(strArg(args, 0))
		if info == nil {
			return vm.Undefined, nil
		}
		return b.jsonValue(map[string]any{"name": info.name, "size": info.size, "xof": info.xof})
	})
	b.fn("getHashes", func(args []vm.Value) (vm.Value, error) {
		arr := vm.NewArray()
		for _, n := range hashNames {
			arr.AsArray().Append(vm.NewString(n))
		}
		return arr, nil
	})
}

// ---- random & KDFs ---------------------------------------------------------

func (b *cryptoBinding) installRandomAndKDF() {
	// randomFill(typedArray, offset, size): fills the view's bytes in place.
	b.fn("randomFill", func(args []vm.Value) (vm.Value, error) {
		v := argAt(args, 0)
		var data []byte
		if ta := v.AsTypedArray(); ta != nil {
			buf := ta.GetBuffer()
			if buf == nil || buf.IsDetached() {
				return vm.Undefined, nil
			}
			d := buf.GetData()
			off, ln := ta.GetByteOffset(), ta.GetByteLength()
			data = d[off : off+ln]
		} else if dv := v.AsDataView(); dv != nil {
			buf := dv.GetBuffer()
			d := buf.GetData()
			off, ln := dv.GetByteOffset(), dv.GetByteLength()
			data = d[off : off+ln]
		} else if ab := v.AsArrayBuffer(); ab != nil {
			data = ab.GetData()
		} else {
			return vm.Undefined, nil
		}
		off := intArg(args, 1, 0)
		size := intArg(args, 2, len(data)-off)
		if off < 0 || size < 0 || off+size > len(data) {
			return vm.Undefined, fmt.Errorf("randomFill: out of range")
		}
		_, _ = rand.Read(data[off : off+size])
		return v, nil
	})
	b.fn("randomBytes", func(args []vm.Value) (vm.Value, error) {
		n := intArg(args, 0, 0)
		out := make([]byte, n)
		_, _ = rand.Read(out)
		return b.buf(out), nil
	})
	b.fn("pbkdf2", func(args []vm.Value) (vm.Value, error) {
		pass, salt := b.bytesArg(args, 0), b.bytesArg(args, 1)
		iter, keylen := intArg(args, 2, 1), intArg(args, 3, 0)
		name := strArg(args, 4)
		info := lookupDigest(name)
		if info == nil || info.xof {
			return vm.Undefined, b.typeErr("ERR_CRYPTO_INVALID_DIGEST", "Invalid digest: "+name)
		}
		return b.async(argAt(args, 5), func() (func() vm.Value, error) {
			var out []byte
			if keylen == 0 {
				out = []byte{}
			} else {
				var err error
				out, err = pbkdf2.Key(info.newHash, string(pass), salt, iter, keylen)
				if err != nil {
					// crypto/pbkdf2 rejects empty passwords etc. in FIPS mode
					// only; anything else is a real failure.
					return nil, err
				}
			}
			return func() vm.Value { return b.buf(out) }, nil
		})
	})
	b.fn("scrypt", func(args []vm.Value) (vm.Value, error) {
		pass, salt := b.bytesArg(args, 0), b.bytesArg(args, 1)
		N, r, p, keylen := intArg(args, 2, 16384), intArg(args, 3, 8), intArg(args, 4, 1), intArg(args, 5, 64)
		return b.async(argAt(args, 6), func() (func() vm.Value, error) {
			var out []byte
			if keylen == 0 {
				out = []byte{}
			} else {
				var err error
				out, err = scrypt.Key(pass, salt, N, r, p, keylen)
				if err != nil {
					return nil, b.osslErr("030000AC", "digital envelope routines", "invalid scrypt parameters")
				}
			}
			return func() vm.Value { return b.buf(out) }, nil
		})
	})
	b.fn("hkdf", func(args []vm.Value) (vm.Value, error) {
		name := strArg(args, 0)
		info := lookupDigest(name)
		if info == nil || info.xof {
			return vm.Undefined, b.typeErr("ERR_CRYPTO_INVALID_DIGEST", "Invalid digest: "+name)
		}
		ikm, salt, inf := b.bytesArg(args, 1), b.bytesArg(args, 2), b.bytesArg(args, 3)
		keylen := intArg(args, 4, 0)
		return b.async(argAt(args, 5), func() (func() vm.Value, error) {
			var out []byte
			if keylen == 0 {
				out = []byte{}
			} else {
				prk, err := hkdf.Extract(info.newHash, ikm, salt)
				if err != nil {
					return nil, err
				}
				out, err = hkdf.Expand(info.newHash, prk, string(inf), keylen)
				if err != nil {
					return nil, err
				}
			}
			return func() vm.Value {
				ab := vm.NewArrayBuffer(len(out))
				copy(ab.AsArrayBuffer().GetData(), out)
				return ab
			}, nil
		})
	})
	// argon2(algorithm, message, nonce, secret, ad, parallelism, tagLength,
	// memoryKiB, passes, cb?)
	b.fn("argon2", func(args []vm.Value) (vm.Value, error) {
		alg := strArg(args, 0)
		msg, nonce, secret, ad := b.bytesArg(args, 1), b.bytesArg(args, 2), b.bytesArg(args, 3), b.bytesArg(args, 4)
		par, tagLen, mem, passes := intArg(args, 5, 1), intArg(args, 6, 32), intArg(args, 7, 0), intArg(args, 8, 3)
		var mode int
		switch alg {
		case "argon2d":
			mode = argon2d
		case "argon2i":
			mode = argon2i
		case "argon2id":
			mode = argon2id
		default:
			return vm.Undefined, b.typeErr("ERR_INVALID_ARG_VALUE", "The argument 'algorithm' is invalid. Received '"+alg+"'")
		}
		return b.async(argAt(args, 9), func() (func() vm.Value, error) {
			out := argon2Key(mode, msg, nonce, secret, ad, uint32(passes), uint32(mem), uint32(par), uint32(tagLen))
			return func() vm.Value { return b.buf(out) }, nil
		})
	})
}
