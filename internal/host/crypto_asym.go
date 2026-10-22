package host

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/mldsa"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/asn1"
	"errors"
	"hash"
	"math/big"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/nooga/paserati/pkg/vm"
)

// Node's padding constants.
const (
	rsaPKCS1Padding = 1
	rsaNoPadding    = 3
	rsaOAEPPadding  = 4
	rsaPSSPadding   = 6
)

var cryptoHashIDs = map[string]crypto.Hash{
	"md5": crypto.MD5, "sha1": crypto.SHA1, "sha224": crypto.SHA224, "sha256": crypto.SHA256,
	"sha384": crypto.SHA384, "sha512": crypto.SHA512, "sha512-224": crypto.SHA512_224,
	"sha512-256": crypto.SHA512_256, "sha3-224": crypto.SHA3_224, "sha3-256": crypto.SHA3_256,
	"sha3-384": crypto.SHA3_384, "sha3-512": crypto.SHA3_512, "ripemd160": crypto.RIPEMD160,
	"md5-sha1": crypto.MD5SHA1,
}

func digestOf(info *digestInfo, data []byte) []byte {
	h := info.newHash()
	h.Write(data)
	return h.Sum(nil)
}

type signOpts struct {
	digest      string // "" = key default
	padding     int    // 0 = default
	saltLen     int    // node semantics: -1 digest, -2 max/auto, n
	saltSet     bool
	p1363       bool
	context     []byte
	contextSet  bool
}

func (b *cryptoBinding) opNotSupported() error {
	return b.osslErr("03000096", "digital envelope routines", "operation not supported for this keytype")
}

func (b *cryptoBinding) invalidDigestOSSL() error {
	return b.osslErr("1C80007A", "Provider routines", "invalid digest")
}

func (b *cryptoBinding) resolveDigest(k *asymKey, o *signOpts) (*digestInfo, error) {
	name := o.digest
	if name == "" {
		if k.pss != nil {
			name = k.pss.hash
		} else {
			name = "sha256"
		}
	}
	info := lookupDigest(name)
	if info == nil || info.xof {
		return nil, b.typeErr("ERR_CRYPTO_INVALID_DIGEST", "Invalid digest: "+o.digest)
	}
	return info, nil
}

// pssSaltLength maps node's saltLength for signing/verifying to Go's.
func pssSaltLength(o *signOpts, k *asymKey, info *digestInfo, signing bool) int {
	if !o.saltSet {
		if k.pss != nil {
			return k.pss.saltLen
		}
		if k.typ == "rsa-pss" && signing {
			// OpenSSL signs with unrestricted rsa-pss keys using a
			// digest-sized salt.
			return rsa.PSSSaltLengthEqualsHash
		}
		if signing {
			return rsa.PSSSaltLengthAuto // max
		}
		return rsa.PSSSaltLengthAuto
	}
	switch o.saltLen {
	case -1:
		return rsa.PSSSaltLengthEqualsHash
	case -2:
		return rsa.PSSSaltLengthAuto
	}
	return o.saltLen
}

func (b *cryptoBinding) signWith(k *asymKey, data []byte, o *signOpts) ([]byte, error) {
	switch k.typ {
	case "ed25519":
		if o.digest != "" {
			return nil, b.invalidDigestOSSL()
		}
		if o.contextSet && len(o.context) > 0 {
			return ed25519.Sign(k.edPriv, data), nil
		}
		return ed25519.Sign(k.edPriv, data), nil
	case "ml-dsa-44", "ml-dsa-65", "ml-dsa-87":
		if o.digest != "" {
			return nil, b.invalidDigestOSSL()
		}
		return k.mldsaPriv.Sign(rand.Reader, data, &mldsa.Options{Context: string(o.context)})
	case "rsa", "rsa-pss":
		info, err := b.resolveDigest(k, o)
		if err != nil {
			return nil, err
		}
		digest := digestOf(info, data)
		padding := o.padding
		if k.typ == "rsa-pss" {
			padding = rsaPSSPadding
		}
		if padding == rsaPSSPadding {
			mgf := info.name
			if k.pss != nil {
				mgf = k.pss.mgf1
			}
			return rsaPSSSign(k.rsaPriv, info, mgf, digest, pssSaltLength(o, k, info, true))
		}
		if padding != 0 && padding != rsaPKCS1Padding {
			return nil, b.osslErr("1C8000A5", "Provider routines", "illegal or unsupported padding mode")
		}
		h, ok := cryptoHashIDs[info.name]
		if !ok {
			return nil, b.invalidDigestOSSL()
		}
		return rsa.SignPKCS1v15(nil, k.rsaPriv, h, digest)
	case "ec":
		info, err := b.resolveDigest(k, o)
		if err != nil {
			return nil, err
		}
		r, s, err := ecSign(k.ec, digestOf(info, data))
		if err != nil {
			return nil, err
		}
		return encodeECSig(k.ec.curve, r, s, o.p1363), nil
	}
	return nil, b.opNotSupported()
}

func (b *cryptoBinding) verifyWith(k *asymKey, data, sig []byte, o *signOpts) (bool, error) {
	switch k.typ {
	case "ed25519":
		if o.digest != "" {
			return false, b.invalidDigestOSSL()
		}
		pub := k.public().edPub
		return len(sig) == ed25519.SignatureSize && ed25519.Verify(pub, data, sig), nil
	case "ml-dsa-44", "ml-dsa-65", "ml-dsa-87":
		if o.digest != "" {
			return false, b.invalidDigestOSSL()
		}
		return mldsa.Verify(k.public().mldsaPub, data, sig, &mldsa.Options{Context: string(o.context)}) == nil, nil
	case "rsa", "rsa-pss":
		info, err := b.resolveDigest(k, o)
		if err != nil {
			return false, err
		}
		digest := digestOf(info, data)
		pub := k.rsaPublic()
		padding := o.padding
		if k.typ == "rsa-pss" {
			padding = rsaPSSPadding
		}
		if padding == rsaPSSPadding {
			mgf := info.name
			if k.pss != nil {
				mgf = k.pss.mgf1
			}
			return rsaPSSVerify(pub, info, mgf, digest, sig, pssSaltLength(o, k, info, false)), nil
		}
		h, ok := cryptoHashIDs[info.name]
		if !ok {
			return false, b.invalidDigestOSSL()
		}
		return rsa.VerifyPKCS1v15(pub, h, digest, sig) == nil, nil
	case "ec":
		info, err := b.resolveDigest(k, o)
		if err != nil {
			return false, err
		}
		r, s, ok := decodeECSig(k.ec.curve, sig, o.p1363)
		if !ok {
			return false, nil
		}
		return ecVerify(k.ec, digestOf(info, data), r, s), nil
	}
	return false, b.opNotSupported()
}

// ---- ECDSA ----

func encodeECSig(c *ecCurve, r, s *big.Int, p1363 bool) []byte {
	if p1363 {
		n := (c.curve.Params().N.BitLen() + 7) / 8
		out := make([]byte, 2*n)
		r.FillBytes(out[:n])
		s.FillBytes(out[n:])
		return out
	}
	der, _ := asn1.Marshal(struct{ R, S *big.Int }{r, s})
	return der
}

func decodeECSig(c *ecCurve, sig []byte, p1363 bool) (*big.Int, *big.Int, bool) {
	if p1363 {
		n := (c.curve.Params().N.BitLen() + 7) / 8
		if len(sig) != 2*n {
			return nil, nil, false
		}
		return new(big.Int).SetBytes(sig[:n]), new(big.Int).SetBytes(sig[n:]), true
	}
	var rs struct{ R, S *big.Int }
	if rest, err := asn1.Unmarshal(sig, &rs); err != nil || len(rest) != 0 || rs.R == nil || rs.S == nil {
		return nil, nil, false
	}
	return rs.R, rs.S, true
}

func hashToInt(hash []byte, n *big.Int) *big.Int {
	orderBits := n.BitLen()
	orderBytes := (orderBits + 7) / 8
	if len(hash) > orderBytes {
		hash = hash[:orderBytes]
	}
	ret := new(big.Int).SetBytes(hash)
	if excess := len(hash)*8 - orderBits; excess > 0 {
		ret.Rsh(ret, uint(excess))
	}
	return ret
}

func ecSign(k *ecKey, digest []byte) (*big.Int, *big.Int, error) {
	if k.curve.ecdh != nil {
		priv, err := ecdsa.ParseRawPrivateKey(k.curve.curve, k.d)
		if err != nil {
			return nil, nil, err
		}
		der, err := ecdsa.SignASN1(rand.Reader, priv, digest)
		if err != nil {
			return nil, nil, err
		}
		r, s, _ := decodeECSig(k.curve, der, false)
		return r, s, nil
	}
	// secp256k1: textbook ECDSA over the curve's group.
	params := k.curve.curve.Params()
	n := params.N
	d := new(big.Int).SetBytes(k.d)
	e := hashToInt(digest, n)
	for {
		kk, err := rand.Int(rand.Reader, new(big.Int).Sub(n, big.NewInt(1)))
		if err != nil {
			return nil, nil, err
		}
		kk.Add(kk, big.NewInt(1))
		x, _ := k.curve.curve.ScalarBaseMult(kk.FillBytes(make([]byte, k.curve.size)))
		r := new(big.Int).Mod(x, n)
		if r.Sign() == 0 {
			continue
		}
		s := new(big.Int).Mul(r, d)
		s.Add(s, e)
		s.Mul(s, new(big.Int).ModInverse(kk, n))
		s.Mod(s, n)
		if s.Sign() != 0 {
			return r, s, nil
		}
	}
}

func ecVerify(k *ecKey, digest []byte, r, s *big.Int) bool {
	if k.curve.ecdh != nil {
		pub, err := ecdsa.ParseUncompressedPublicKey(k.curve.curve, k.pub)
		if err != nil {
			return false
		}
		return ecdsa.VerifyASN1(pub, digest, encodeECSig(k.curve, r, s, false))
	}
	c := k.curve.curve
	n := c.Params().N
	if r.Sign() <= 0 || s.Sign() <= 0 || r.Cmp(n) >= 0 || s.Cmp(n) >= 0 {
		return false
	}
	e := hashToInt(digest, n)
	w := new(big.Int).ModInverse(s, n)
	u1 := new(big.Int).Mul(e, w)
	u1.Mod(u1, n)
	u2 := new(big.Int).Mul(r, w)
	u2.Mod(u2, n)
	qx, qy := k.xy()
	x1, y1 := c.ScalarBaseMult(u1.Bytes())
	x2, y2 := c.ScalarMult(qx, qy, u2.Bytes())
	x, y := c.Add(x1, y1, x2, y2)
	if x.Sign() == 0 && y.Sign() == 0 {
		return false
	}
	x.Mod(x, n)
	return x.Cmp(r) == 0
}

func ecdhCompute(priv, pub *ecKey) ([]byte, error) {
	if priv.curve != pub.curve {
		return nil, errors.New("curve mismatch")
	}
	if priv.curve.ecdh != nil {
		p, err := priv.curve.ecdh.NewPrivateKey(priv.d)
		if err != nil {
			return nil, err
		}
		q, err := priv.curve.ecdh.NewPublicKey(pub.pub)
		if err != nil {
			return nil, err
		}
		return p.ECDH(q)
	}
	p := secp256k1.PrivKeyFromBytes(priv.d)
	q, err := secp256k1.ParsePubKey(pub.pub)
	if err != nil {
		return nil, err
	}
	return secp256k1.GenerateSharedSecret(p, q), nil
}

// ---- RSA-PSS with an independent MGF1 hash, and raw RSA ----

func mgf1XOR(out []byte, newHash func() hash.Hash, seed []byte) {
	var counter [4]byte
	done := 0
	for done < len(out) {
		h := newHash()
		h.Write(seed)
		h.Write(counter[:])
		d := h.Sum(nil)
		for i := 0; i < len(d) && done < len(out); i++ {
			out[done] ^= d[i]
			done++
		}
		for i := 3; i >= 0; i-- {
			counter[i]++
			if counter[i] != 0 {
				break
			}
		}
	}
}

func rsaRawPrivate(priv *rsa.PrivateKey, m *big.Int) *big.Int {
	return new(big.Int).Exp(m, priv.D, priv.N)
}

func rsaRawPublic(pub *rsa.PublicKey, c *big.Int) *big.Int {
	return new(big.Int).Exp(c, big.NewInt(int64(pub.E)), pub.N)
}

func rsaPSSSign(priv *rsa.PrivateKey, info *digestInfo, mgf string, digest []byte, saltLen int) ([]byte, error) {
	if mgf == info.name {
		if h, ok := cryptoHashIDs[info.name]; ok {
			return rsa.SignPSS(rand.Reader, priv, h, digest, &rsa.PSSOptions{SaltLength: saltLen})
		}
	}
	mgfInfo := lookupDigest(mgf)
	emBits := priv.N.BitLen() - 1
	emLen := (emBits + 7) / 8
	hLen := len(digest)
	if saltLen == rsa.PSSSaltLengthAuto {
		saltLen = emLen - hLen - 2
	} else if saltLen == rsa.PSSSaltLengthEqualsHash {
		saltLen = hLen
	}
	if saltLen < 0 || emLen < hLen+saltLen+2 {
		return nil, errors.New("key size too small for PSS")
	}
	salt := make([]byte, saltLen)
	rand.Read(salt)
	h := info.newHash()
	h.Write(make([]byte, 8))
	h.Write(digest)
	h.Write(salt)
	H := h.Sum(nil)
	db := make([]byte, emLen-hLen-1)
	db[len(db)-saltLen-1] = 1
	copy(db[len(db)-saltLen:], salt)
	mgf1XOR(db, mgfInfo.newHash, H)
	db[0] &= 0xff >> (8*emLen - emBits)
	em := append(append(db, H...), 0xbc)
	s := rsaRawPrivate(priv, new(big.Int).SetBytes(em))
	return s.FillBytes(make([]byte, priv.Size())), nil
}

func rsaPSSVerify(pub *rsa.PublicKey, info *digestInfo, mgf string, digest, sig []byte, saltLen int) bool {
	if mgf == info.name {
		if h, ok := cryptoHashIDs[info.name]; ok {
			return rsa.VerifyPSS(pub, h, digest, sig, &rsa.PSSOptions{SaltLength: saltLen}) == nil
		}
	}
	mgfInfo := lookupDigest(mgf)
	if len(sig) != pub.Size() {
		return false
	}
	emBits := pub.N.BitLen() - 1
	emLen := (emBits + 7) / 8
	m := rsaRawPublic(pub, new(big.Int).SetBytes(sig))
	if m.BitLen() > emBits {
		return false
	}
	em := m.FillBytes(make([]byte, emLen))
	hLen := len(digest)
	if emLen < hLen+2 || em[emLen-1] != 0xbc {
		return false
	}
	db := append([]byte(nil), em[:emLen-hLen-1]...)
	H := em[emLen-hLen-1 : emLen-1]
	mgf1XOR(db, mgfInfo.newHash, H)
	db[0] &= 0xff >> (8*emLen - emBits)
	i := 0
	for i < len(db) && db[i] == 0 {
		i++
	}
	if i == len(db) || db[i] != 1 {
		return false
	}
	salt := db[i+1:]
	if saltLen >= 0 && len(salt) != saltLen {
		return false
	}
	h := info.newHash()
	h.Write(make([]byte, 8))
	h.Write(digest)
	h.Write(salt)
	return subtle.ConstantTimeCompare(h.Sum(nil), H) == 1
}

// ---- RSA encryption ----

type rsaCryptOpts struct {
	padding  int
	oaepHash string
	label    []byte
}

func (b *cryptoBinding) rsaErr(hex, reason string) error {
	return b.osslErr(hex, "rsa routines", reason)
}

// rsaCrypt implements publicEncrypt (op "pubenc"), privateDecrypt
// ("privdec"), privateEncrypt ("privenc") and publicDecrypt ("pubdec").
func (b *cryptoBinding) rsaCrypt(op string, k *asymKey, data []byte, o *rsaCryptOpts) ([]byte, error) {
	if k.typ != "rsa" {
		return nil, b.opNotSupported()
	}
	pub := k.rsaPublic()
	size := pub.Size()
	padding := o.padding
	if padding == 0 {
		if op == "pubenc" || op == "privdec" {
			padding = rsaOAEPPadding
		} else {
			padding = rsaPKCS1Padding
		}
	}
	switch padding {
	case rsaNoPadding:
		if len(data) > size {
			return nil, b.rsaErr("0200006E", "data too large for key size")
		}
		if len(data) < size {
			return nil, b.rsaErr("0200007A", "data too small for key size")
		}
		m := new(big.Int).SetBytes(data)
		if m.Cmp(pub.N) >= 0 {
			return nil, b.rsaErr("02000084", "data too large for modulus")
		}
		var r *big.Int
		if op == "pubenc" || op == "pubdec" {
			r = rsaRawPublic(pub, m)
		} else {
			r = rsaRawPrivate(k.rsaPriv, m)
		}
		return r.FillBytes(make([]byte, size)), nil
	case rsaOAEPPadding:
		if op == "privenc" || op == "pubdec" {
			return nil, b.rsaErr("0200008E", "unknown padding type")
		}
		name := o.oaepHash
		if name == "" {
			name = "sha1"
		}
		info := lookupDigest(name)
		if info == nil || info.xof {
			return nil, b.typeErr("ERR_OSSL_EVP_INVALID_DIGEST", "Invalid digest used")
		}
		h := info.newHash()
		if op == "pubenc" {
			if len(data) > size-2*h.Size()-2 {
				return nil, b.rsaErr("0200006E", "data too large for key size")
			}
			return rsa.EncryptOAEP(h, rand.Reader, pub, data, o.label)
		}
		out, err := rsa.DecryptOAEP(h, nil, k.rsaPriv, data, o.label)
		if err != nil {
			return nil, b.rsaErr("02000079", "oaep decoding error")
		}
		return out, nil
	case rsaPKCS1Padding:
		switch op {
		case "pubenc":
			if len(data) > size-11 {
				return nil, b.rsaErr("0200006E", "data too large for key size")
			}
			return rsa.EncryptPKCS1v15(rand.Reader, pub, data) //nolint:staticcheck // node still offers it
		case "privdec":
			out, err := rsa.DecryptPKCS1v15(nil, k.rsaPriv, data) //nolint:staticcheck
			if err != nil {
				return nil, b.rsaErr("02000072", "padding check failed")
			}
			return out, nil
		case "privenc":
			if len(data) > size-11 {
				return nil, b.rsaErr("0200006E", "data too large for key size")
			}
			return rsa.SignPKCS1v15(nil, k.rsaPriv, crypto.Hash(0), data)
		case "pubdec":
			if len(data) != size {
				return nil, b.rsaErr("02000071", "data greater than mod len")
			}
			em := rsaRawPublic(pub, new(big.Int).SetBytes(data)).FillBytes(make([]byte, size))
			if em[0] != 0 || em[1] != 1 {
				return nil, b.rsaErr("02000066", "invalid padding")
			}
			i := 2
			for i < len(em) && em[i] == 0xff {
				i++
			}
			if i == len(em) || em[i] != 0 || i < 10 {
				return nil, b.rsaErr("02000066", "invalid padding")
			}
			return em[i+1:], nil
		}
	}
	return nil, b.rsaErr("0200008E", "unknown padding type")
}

// ---- DH / primes ----

func dhVerifyError(p *big.Int, g *big.Int) int {
	flags := 0
	if p.BitLen() < 512 {
		flags |= 0x80
	}
	if !p.ProbablyPrime(20) {
		flags |= 1
	} else {
		q := new(big.Int).Rsh(p, 1)
		if !q.ProbablyPrime(20) {
			flags |= 2
		}
	}
	if g.Cmp(big.NewInt(2)) < 0 {
		flags |= 8
	}
	return flags
}

func generatePrime(bits int, add, rem *big.Int, safe bool) (*big.Int, error) {
	for {
		var p *big.Int
		var err error
		if add == nil {
			p, err = rand.Prime(rand.Reader, bits)
			if err != nil {
				return nil, err
			}
		} else {
			p, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), uint(bits)))
			if err != nil {
				return nil, err
			}
			p.SetBit(p, bits-1, 1)
			r := rem
			if r == nil {
				r = big.NewInt(1)
			}
			p.Sub(p, new(big.Int).Mod(p, add))
			p.Add(p, r)
			if p.BitLen() != bits || !p.ProbablyPrime(20) {
				continue
			}
		}
		if safe && !new(big.Int).Rsh(p, 1).ProbablyPrime(20) {
			continue
		}
		return p, nil
	}
}

// ---- binding ----

func (b *cryptoBinding) parseSignOpts(args []vm.Value, i int) *signOpts {
	o := &signOpts{digest: strArg(args, i)}
	if v := argAt(args, i+1); v.IsNumber() {
		o.padding = int(v.ToFloat())
	}
	if v := argAt(args, i+2); v.IsNumber() {
		o.saltLen, o.saltSet = int(v.ToFloat()), true
	}
	o.p1363 = strArg(args, i+3) == "ieee-p1363"
	if v := argAt(args, i+4); !v.IsUndefined() && !v.IsNull() {
		o.context, o.contextSet = b.bytesArg(args, i+4), true
	}
	return o
}

func (b *cryptoBinding) installAsym() {
	// sign(handle, data, digest, padding, saltLength, dsaEncoding, context, cb?)
	b.fn("sign", func(args []vm.Value) (vm.Value, error) {
		k, ok := handleOf[*asymKey](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid key handle")
		}
		data := b.bytesArg(args, 1)
		o := b.parseSignOpts(args, 2)
		return b.async(argAt(args, 7), func() (func() vm.Value, error) {
			sig, err := b.signWith(k, data, o)
			if err != nil {
				return nil, err
			}
			return func() vm.Value { return b.buf(sig) }, nil
		})
	})
	// verify(handle, data, sig, digest, padding, saltLength, dsaEncoding, context, cb?)
	b.fn("verify", func(args []vm.Value) (vm.Value, error) {
		k, ok := handleOf[*asymKey](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid key handle")
		}
		data, sig := b.bytesArg(args, 1), b.bytesArg(args, 2)
		o := b.parseSignOpts(args, 3)
		return b.async(argAt(args, 8), func() (func() vm.Value, error) {
			ok, err := b.verifyWith(k, data, sig, o)
			if err != nil {
				return nil, err
			}
			return func() vm.Value { return vm.BooleanValue(ok) }, nil
		})
	})
	// rsaCrypt(op, handle, data, padding, oaepHash, oaepLabel)
	b.fn("rsaCrypt", func(args []vm.Value) (vm.Value, error) {
		k, ok := handleOf[*asymKey](args, 1)
		if !ok {
			return vm.Undefined, errors.New("invalid key handle")
		}
		o := &rsaCryptOpts{padding: intArg(args, 3, 0), oaepHash: strArg(args, 4)}
		if v := argAt(args, 5); !v.IsUndefined() && !v.IsNull() {
			o.label = b.bytesArg(args, 5)
		}
		out, err := b.rsaCrypt(strArg(args, 0), k, b.bytesArg(args, 2), o)
		if err != nil {
			return vm.Undefined, err
		}
		return b.buf(out), nil
	})
	// diffieHellman(privHandle, pubHandle)
	b.fn("diffieHellman", func(args []vm.Value) (vm.Value, error) {
		priv, ok1 := handleOf[*asymKey](args, 0)
		pub, ok2 := handleOf[*asymKey](args, 1)
		if !ok1 || !ok2 {
			return vm.Undefined, errors.New("invalid key handle")
		}
		if priv.typ != pub.typ || (priv.typ != "ec" && priv.typ != "x25519") {
			return vm.Undefined, b.err("ERR_CRYPTO_INCOMPATIBLE_KEY", "Incompatible key types for Diffie-Hellman: "+priv.typ+" and "+pub.typ)
		}
		var out []byte
		var err error
		if priv.typ == "x25519" {
			out, err = priv.xPriv.ECDH(pub.public().xPub)
		} else {
			if priv.ec.curve != pub.ec.curve {
				return vm.Undefined, b.osslErr("03000079", "digital envelope routines", "different parameters")
			}
			out, err = ecdhCompute(priv.ec, pub.ec)
		}
		if err != nil {
			return vm.Undefined, b.err("ERR_CRYPTO_OPERATION_FAILED", "Failed to derive shared secret")
		}
		return b.buf(out), nil
	})
	// ECDH class primitives on raw bytes.
	b.fn("ecdhGenerate", func(args []vm.Value) (vm.Value, error) {
		c := lookupCurve(strArg(args, 0))
		if c == nil {
			return vm.Undefined, b.typeErr("ERR_CRYPTO_INVALID_CURVE", "Invalid EC curve name")
		}
		k, err := generateAsymKey("ec", keyGenOptions{NamedCurve: c.name})
		if err != nil {
			return vm.Undefined, err
		}
		return b.buf(k.ec.d), nil
	})
	b.fn("ecdhPublicFromPrivate", func(args []vm.Value) (vm.Value, error) {
		c := lookupCurve(strArg(args, 0))
		d := b.bytesArg(args, 1)
		if c == nil || len(d) > c.size {
			return vm.Undefined, b.rangeErr("ERR_CRYPTO_INVALID_KEYTYPE", "Private key is not valid for specified curve.")
		}
		k, err := newECPrivate(c, d)
		if err != nil || new(big.Int).SetBytes(d).Cmp(c.curve.Params().N) >= 0 {
			return vm.Undefined, b.rangeErr("ERR_CRYPTO_INVALID_KEYTYPE", "Private key is not valid for specified curve.")
		}
		return b.buf(k.pub), nil
	})
	b.fn("ecdhCompute", func(args []vm.Value) (vm.Value, error) {
		c := lookupCurve(strArg(args, 0))
		priv, err := newECPrivate(c, b.bytesArg(args, 1))
		if err != nil {
			return vm.Undefined, err
		}
		pub, err := newECPublic(c, b.bytesArg(args, 2))
		if err != nil {
			return vm.Undefined, b.err("ERR_CRYPTO_ECDH_INVALID_PUBLIC_KEY", "Public key is not valid for specified curve")
		}
		out, err := ecdhCompute(priv, pub)
		if err != nil {
			return vm.Undefined, b.err("ERR_CRYPTO_ECDH_INVALID_PUBLIC_KEY", "Public key is not valid for specified curve")
		}
		return b.buf(out), nil
	})
	// ecPointConvert(curve, point, format) -> point in format
	b.fn("ecPointConvert", func(args []vm.Value) (vm.Value, error) {
		c := lookupCurve(strArg(args, 0))
		if c == nil {
			return vm.Undefined, b.typeErr("ERR_CRYPTO_INVALID_CURVE", "Invalid EC curve name")
		}
		k, err := newECPublic(c, b.bytesArg(args, 1))
		if err != nil {
			return vm.Undefined, b.err("ERR_CRYPTO_ECDH_INVALID_PUBLIC_KEY", "Public key is not valid for specified curve")
		}
		pt := append([]byte(nil), k.pub...)
		n := c.size
		odd := pt[2*n]&1 == 1
		switch strArg(args, 2) {
		case "compressed":
			pt = pt[:1+n]
			pt[0] = 2
			if odd {
				pt[0] = 3
			}
		case "hybrid":
			pt[0] = 6
			if odd {
				pt[0] = 7
			}
		}
		return b.buf(pt), nil
	})
	b.fn("modPow", func(args []vm.Value) (vm.Value, error) {
		base := new(big.Int).SetBytes(b.bytesArg(args, 0))
		exp := new(big.Int).SetBytes(b.bytesArg(args, 1))
		mod := new(big.Int).SetBytes(b.bytesArg(args, 2))
		if mod.Sign() == 0 {
			return vm.Undefined, errors.New("division by zero")
		}
		r := new(big.Int).Exp(base, exp, mod)
		return b.buf(r.FillBytes(make([]byte, len(mod.Bytes())))), nil
	})
	b.fn("dhGroup", func(args []vm.Value) (vm.Value, error) {
		p, ok := modpGroups[strArg(args, 0)]
		if !ok {
			return vm.Undefined, nil
		}
		v, _ := new(big.Int).SetString(p, 16)
		return b.buf(v.Bytes()), nil
	})
	b.fn("dhVerifyError", func(args []vm.Value) (vm.Value, error) {
		p := new(big.Int).SetBytes(b.bytesArg(args, 0))
		g := new(big.Int).SetBytes(b.bytesArg(args, 1))
		return vm.NumberValue(float64(dhVerifyError(p, g))), nil
	})
	b.fn("dhGenerateKey", func(args []vm.Value) (vm.Value, error) {
		p := new(big.Int).SetBytes(b.bytesArg(args, 0))
		bits := p.BitLen() - 1
		if bits < 2 {
			bits = 2
		}
		x, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), uint(bits)))
		if err != nil {
			return vm.Undefined, err
		}
		x.SetBit(x, bits-1, 1)
		return b.buf(x.Bytes()), nil
	})
	// checkPrime(candidate, checks, cb?)
	b.fn("checkPrime", func(args []vm.Value) (vm.Value, error) {
		n := new(big.Int).SetBytes(b.bytesArg(args, 0))
		checks := intArg(args, 1, 0)
		if checks <= 0 {
			checks = 20
		}
		return b.async(argAt(args, 2), func() (func() vm.Value, error) {
			ok := n.ProbablyPrime(checks)
			return func() vm.Value { return vm.BooleanValue(ok) }, nil
		})
	})
	// generatePrime(bits, add|undefined, rem|undefined, safe, cb?)
	b.fn("generatePrime", func(args []vm.Value) (vm.Value, error) {
		bits := intArg(args, 0, 0)
		var add, rem *big.Int
		if v := argAt(args, 1); !v.IsUndefined() && !v.IsNull() {
			add = new(big.Int).SetBytes(b.bytesArg(args, 1))
		}
		if v := argAt(args, 2); !v.IsUndefined() && !v.IsNull() {
			rem = new(big.Int).SetBytes(b.bytesArg(args, 2))
		}
		safe := argAt(args, 3).IsTruthy()
		return b.async(argAt(args, 4), func() (func() vm.Value, error) {
			p, err := generatePrime(bits, add, rem, safe)
			if err != nil {
				return nil, err
			}
			return func() vm.Value {
				out := p.FillBytes(make([]byte, (bits+7)/8))
				ab := vm.NewArrayBuffer(len(out))
				copy(ab.AsArrayBuffer().GetData(), out)
				return ab
			}, nil
		})
	})
	// encapsulate(handle) -> [sharedKey, ciphertext]
	b.fn("encapsulate", func(args []vm.Value) (vm.Value, error) {
		k, ok := handleOf[*asymKey](args, 0)
		if !ok || !strings.HasPrefix(k.typ, "ml-kem") {
			return vm.Undefined, b.opNotSupported()
		}
		var shared, ct []byte
		if k.typ == "ml-kem-768" {
			ek, err := mlkem.NewEncapsulationKey768(k.mlkemPub)
			if err != nil {
				return vm.Undefined, err
			}
			shared, ct = ek.Encapsulate()
		} else {
			ek, err := mlkem.NewEncapsulationKey1024(k.mlkemPub)
			if err != nil {
				return vm.Undefined, err
			}
			shared, ct = ek.Encapsulate()
		}
		arr := vm.NewArray()
		arr.AsArray().Append(b.buf(shared))
		arr.AsArray().Append(b.buf(ct))
		return arr, nil
	})
	b.fn("decapsulate", func(args []vm.Value) (vm.Value, error) {
		k, ok := handleOf[*asymKey](args, 0)
		if !ok || !strings.HasPrefix(k.typ, "ml-kem") || !k.private {
			return vm.Undefined, b.opNotSupported()
		}
		ct := b.bytesArg(args, 1)
		var shared []byte
		var err error
		if k.typ == "ml-kem-768" {
			var dk *mlkem.DecapsulationKey768
			if dk, err = mlkem.NewDecapsulationKey768(k.mlkemSeed); err == nil {
				shared, err = dk.Decapsulate(ct)
			}
		} else {
			var dk *mlkem.DecapsulationKey1024
			if dk, err = mlkem.NewDecapsulationKey1024(k.mlkemSeed); err == nil {
				shared, err = dk.Decapsulate(ct)
			}
		}
		if err != nil {
			return vm.Undefined, b.err("ERR_CRYPTO_OPERATION_FAILED", "Decapsulation failed")
		}
		return b.buf(shared), nil
	})
}

var modpGroups = map[string]string{
	"modp1": "ffffffffffffffffc90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b139b22514a08798e3404ddef9519b3cd3a431b302b0a6df25f14374fe1356d6d51c245e485b576625e7ec6f44c42e9a63a3620ffffffffffffffff",
	"modp2": "ffffffffffffffffc90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b139b22514a08798e3404ddef9519b3cd3a431b302b0a6df25f14374fe1356d6d51c245e485b576625e7ec6f44c42e9a637ed6b0bff5cb6f406b7edee386bfb5a899fa5ae9f24117c4b1fe649286651ece65381ffffffffffffffff",
	"modp5": "ffffffffffffffffc90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b139b22514a08798e3404ddef9519b3cd3a431b302b0a6df25f14374fe1356d6d51c245e485b576625e7ec6f44c42e9a637ed6b0bff5cb6f406b7edee386bfb5a899fa5ae9f24117c4b1fe649286651ece45b3dc2007cb8a163bf0598da48361c55d39a69163fa8fd24cf5f83655d23dca3ad961c62f356208552bb9ed529077096966d670c354e4abc9804f1746c08ca237327ffffffffffffffff",
	"modp14": "ffffffffffffffffc90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b139b22514a08798e3404ddef9519b3cd3a431b302b0a6df25f14374fe1356d6d51c245e485b576625e7ec6f44c42e9a637ed6b0bff5cb6f406b7edee386bfb5a899fa5ae9f24117c4b1fe649286651ece45b3dc2007cb8a163bf0598da48361c55d39a69163fa8fd24cf5f83655d23dca3ad961c62f356208552bb9ed529077096966d670c354e4abc9804f1746c08ca18217c32905e462e36ce3be39e772c180e86039b2783a2ec07a28fb5c55df06f4c52c9de2bcbf6955817183995497cea956ae515d2261898fa051015728e5a8aacaa68ffffffffffffffff",
	"modp15": "ffffffffffffffffc90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b139b22514a08798e3404ddef9519b3cd3a431b302b0a6df25f14374fe1356d6d51c245e485b576625e7ec6f44c42e9a637ed6b0bff5cb6f406b7edee386bfb5a899fa5ae9f24117c4b1fe649286651ece45b3dc2007cb8a163bf0598da48361c55d39a69163fa8fd24cf5f83655d23dca3ad961c62f356208552bb9ed529077096966d670c354e4abc9804f1746c08ca18217c32905e462e36ce3be39e772c180e86039b2783a2ec07a28fb5c55df06f4c52c9de2bcbf6955817183995497cea956ae515d2261898fa051015728e5a8aaac42dad33170d04507a33a85521abdf1cba64ecfb850458dbef0a8aea71575d060c7db3970f85a6e1e4c7abf5ae8cdb0933d71e8c94e04a25619dcee3d2261ad2ee6bf12ffa06d98a0864d87602733ec86a64521f2b18177b200cbbe117577a615d6c770988c0bad946e208e24fa074e5ab3143db5bfce0fd108e4b82d120a93ad2caffffffffffffffff",
	"modp16": "ffffffffffffffffc90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b139b22514a08798e3404ddef9519b3cd3a431b302b0a6df25f14374fe1356d6d51c245e485b576625e7ec6f44c42e9a637ed6b0bff5cb6f406b7edee386bfb5a899fa5ae9f24117c4b1fe649286651ece45b3dc2007cb8a163bf0598da48361c55d39a69163fa8fd24cf5f83655d23dca3ad961c62f356208552bb9ed529077096966d670c354e4abc9804f1746c08ca18217c32905e462e36ce3be39e772c180e86039b2783a2ec07a28fb5c55df06f4c52c9de2bcbf6955817183995497cea956ae515d2261898fa051015728e5a8aaac42dad33170d04507a33a85521abdf1cba64ecfb850458dbef0a8aea71575d060c7db3970f85a6e1e4c7abf5ae8cdb0933d71e8c94e04a25619dcee3d2261ad2ee6bf12ffa06d98a0864d87602733ec86a64521f2b18177b200cbbe117577a615d6c770988c0bad946e208e24fa074e5ab3143db5bfce0fd108e4b82d120a92108011a723c12a787e6d788719a10bdba5b2699c327186af4e23c1a946834b6150bda2583e9ca2ad44ce8dbbbc2db04de8ef92e8efc141fbecaa6287c59474e6bc05d99b2964fa090c3a2233ba186515be7ed1f612970cee2d7afb81bdd762170481cd0069127d5b05aa993b4ea988d8fddc186ffb7dc90a6c08f4df435c934063199ffffffffffffffff",
	"modp17": "ffffffffffffffffc90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b139b22514a08798e3404ddef9519b3cd3a431b302b0a6df25f14374fe1356d6d51c245e485b576625e7ec6f44c42e9a637ed6b0bff5cb6f406b7edee386bfb5a899fa5ae9f24117c4b1fe649286651ece45b3dc2007cb8a163bf0598da48361c55d39a69163fa8fd24cf5f83655d23dca3ad961c62f356208552bb9ed529077096966d670c354e4abc9804f1746c08ca18217c32905e462e36ce3be39e772c180e86039b2783a2ec07a28fb5c55df06f4c52c9de2bcbf6955817183995497cea956ae515d2261898fa051015728e5a8aaac42dad33170d04507a33a85521abdf1cba64ecfb850458dbef0a8aea71575d060c7db3970f85a6e1e4c7abf5ae8cdb0933d71e8c94e04a25619dcee3d2261ad2ee6bf12ffa06d98a0864d87602733ec86a64521f2b18177b200cbbe117577a615d6c770988c0bad946e208e24fa074e5ab3143db5bfce0fd108e4b82d120a92108011a723c12a787e6d788719a10bdba5b2699c327186af4e23c1a946834b6150bda2583e9ca2ad44ce8dbbbc2db04de8ef92e8efc141fbecaa6287c59474e6bc05d99b2964fa090c3a2233ba186515be7ed1f612970cee2d7afb81bdd762170481cd0069127d5b05aa993b4ea988d8fddc186ffb7dc90a6c08f4df435c93402849236c3fab4d27c7026c1d4dcb2602646dec9751e763dba37bdf8ff9406ad9e530ee5db382f413001aeb06a53ed9027d831179727b0865a8918da3edbebcf9b14ed44ce6cbaced4bb1bdb7f1447e6cc254b332051512bd7af426fb8f401378cd2bf5983ca01c64b92ecf032ea15d1721d03f482d7ce6e74fef6d55e702f46980c82b5a84031900b1c9e59e7c97fbec7e8f323a97a7e36cc88be0f1d45b7ff585ac54bd407b22b4154aacc8f6d7ebf48e1d814cc5ed20f8037e0a79715eef29be32806a1d58bb7c5da76f550aa3d8a1fbff0eb19ccb1a313d55cda56c9ec2ef29632387fe8d76e3c0468043e8f663f4860ee12bf2d5b0b7474d6e694f91e6dcc4024ffffffffffffffff",
	"modp18": "ffffffffffffffffc90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b139b22514a08798e3404ddef9519b3cd3a431b302b0a6df25f14374fe1356d6d51c245e485b576625e7ec6f44c42e9a637ed6b0bff5cb6f406b7edee386bfb5a899fa5ae9f24117c4b1fe649286651ece45b3dc2007cb8a163bf0598da48361c55d39a69163fa8fd24cf5f83655d23dca3ad961c62f356208552bb9ed529077096966d670c354e4abc9804f1746c08ca18217c32905e462e36ce3be39e772c180e86039b2783a2ec07a28fb5c55df06f4c52c9de2bcbf6955817183995497cea956ae515d2261898fa051015728e5a8aaac42dad33170d04507a33a85521abdf1cba64ecfb850458dbef0a8aea71575d060c7db3970f85a6e1e4c7abf5ae8cdb0933d71e8c94e04a25619dcee3d2261ad2ee6bf12ffa06d98a0864d87602733ec86a64521f2b18177b200cbbe117577a615d6c770988c0bad946e208e24fa074e5ab3143db5bfce0fd108e4b82d120a92108011a723c12a787e6d788719a10bdba5b2699c327186af4e23c1a946834b6150bda2583e9ca2ad44ce8dbbbc2db04de8ef92e8efc141fbecaa6287c59474e6bc05d99b2964fa090c3a2233ba186515be7ed1f612970cee2d7afb81bdd762170481cd0069127d5b05aa993b4ea988d8fddc186ffb7dc90a6c08f4df435c93402849236c3fab4d27c7026c1d4dcb2602646dec9751e763dba37bdf8ff9406ad9e530ee5db382f413001aeb06a53ed9027d831179727b0865a8918da3edbebcf9b14ed44ce6cbaced4bb1bdb7f1447e6cc254b332051512bd7af426fb8f401378cd2bf5983ca01c64b92ecf032ea15d1721d03f482d7ce6e74fef6d55e702f46980c82b5a84031900b1c9e59e7c97fbec7e8f323a97a7e36cc88be0f1d45b7ff585ac54bd407b22b4154aacc8f6d7ebf48e1d814cc5ed20f8037e0a79715eef29be32806a1d58bb7c5da76f550aa3d8a1fbff0eb19ccb1a313d55cda56c9ec2ef29632387fe8d76e3c0468043e8f663f4860ee12bf2d5b0b7474d6e694f91e6dbe115974a3926f12fee5e438777cb6a932df8cd8bec4d073b931ba3bc832b68d9dd300741fa7bf8afc47ed2576f6936ba424663aab639c5ae4f5683423b4742bf1c978238f16cbe39d652de3fdb8befc848ad922222e04a4037c0713eb57a81a23f0c73473fc646cea306b4bcbc8862f8385ddfa9d4b7fa2c087e879683303ed5bdd3a062b3cf5b3a278a66d2a13f83f44f82ddf310ee074ab6a364597e899a0255dc164f31cc50846851df9ab48195ded7ea1b1d510bd7ee74d73faf36bc31ecfa268359046f4eb879f924009438b481c6cd7889a002ed5ee382bc9190da6fc026e479558e4475677e9aa9e3050e2765694dfc81f56e880b96e7160c980dd98edd3dfffffffffffffffff",
}
