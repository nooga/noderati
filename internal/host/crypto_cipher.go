package host

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"sort"
	"strings"

	"github.com/nooga/paserati/pkg/vm"
	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"
)

// cipherSpec mirrors what node's getCipherInfo reports, plus how to build
// the cipher here.
type cipherSpec struct {
	name      string // node's canonical name (getCipherInfo().name)
	mode      string // getCipherInfo().mode
	impl      string // cbc ecb ctr cfb cfb8 cfb1 ofb gcm ccm wrap wrappad chacha20 chachapoly
	family    string // aes des3 des2 chacha
	nid       int
	keyLen    int
	blockSize int
	ivLen     int
}

var cipherTable = map[string]*cipherSpec{}
var cipherNames []string

func init() {
	add := func(alias string, s cipherSpec) {
		sp := s
		cipherTable[strings.ToLower(alias)] = &sp
		cipherNames = append(cipherNames, alias)
	}
	aesNids := map[int]map[string]int{
		128: {"ecb": 418, "cbc": 419, "ofb": 420, "cfb": 421, "cfb1": 650, "cfb8": 653, "ctr": 904, "gcm": 895, "ccm": 896, "wrap": 788, "wrappad": 897},
		192: {"ecb": 422, "cbc": 423, "ofb": 424, "cfb": 425, "cfb1": 651, "cfb8": 654, "ctr": 905, "gcm": 898, "ccm": 899, "wrap": 789, "wrappad": 900},
		256: {"ecb": 426, "cbc": 427, "ofb": 428, "cfb": 429, "cfb1": 652, "cfb8": 655, "ctr": 906, "gcm": 901, "ccm": 902, "wrap": 790, "wrappad": 903},
	}
	for _, bits := range []int{128, 192, 256} {
		b := itoa(bits)
		kl := bits / 8
		n := aesNids[bits]
		pre := "aes-" + b + "-"
		add(pre+"cbc", cipherSpec{name: pre + "cbc", mode: "cbc", impl: "cbc", family: "aes", nid: n["cbc"], keyLen: kl, blockSize: 16, ivLen: 16})
		add(pre+"ccm", cipherSpec{name: "id-aes" + b + "-ccm", mode: "ccm", impl: "ccm", family: "aes", nid: n["ccm"], keyLen: kl, blockSize: 1, ivLen: 12})
		add(pre+"cfb", cipherSpec{name: pre + "cfb", mode: "cfb", impl: "cfb", family: "aes", nid: n["cfb"], keyLen: kl, blockSize: 1, ivLen: 16})
		add(pre+"cfb1", cipherSpec{name: pre + "cfb1", mode: "cfb", impl: "cfb1", family: "aes", nid: n["cfb1"], keyLen: kl, blockSize: 1, ivLen: 16})
		add(pre+"cfb8", cipherSpec{name: pre + "cfb8", mode: "cfb", impl: "cfb8", family: "aes", nid: n["cfb8"], keyLen: kl, blockSize: 1, ivLen: 16})
		add(pre+"ctr", cipherSpec{name: pre + "ctr", mode: "ctr", impl: "ctr", family: "aes", nid: n["ctr"], keyLen: kl, blockSize: 1, ivLen: 16})
		add(pre+"ecb", cipherSpec{name: pre + "ecb", mode: "ecb", impl: "ecb", family: "aes", nid: n["ecb"], keyLen: kl, blockSize: 16})
		add(pre+"gcm", cipherSpec{name: "id-aes" + b + "-gcm", mode: "gcm", impl: "gcm", family: "aes", nid: n["gcm"], keyLen: kl, blockSize: 1, ivLen: 12})
		add(pre+"ofb", cipherSpec{name: pre + "ofb", mode: "ofb", impl: "ofb", family: "aes", nid: n["ofb"], keyLen: kl, blockSize: 1, ivLen: 16})
		add("aes"+b, cipherSpec{name: pre + "cbc", mode: "cbc", impl: "cbc", family: "aes", nid: n["cbc"], keyLen: kl, blockSize: 16, ivLen: 16})
		wrap := cipherSpec{name: "id-aes" + b + "-wrap", mode: "wrap", impl: "wrap", family: "aes", nid: n["wrap"], keyLen: kl, blockSize: 8, ivLen: 8}
		wrapPad := cipherSpec{name: "id-aes" + b + "-wrap-pad", mode: "wrap", impl: "wrappad", family: "aes", nid: n["wrappad"], keyLen: kl, blockSize: 8, ivLen: 4}
		add("aes"+b+"-wrap", wrap)
		add("aes"+b+"-wrap-pad", wrapPad)
		add("id-aes"+b+"-CCM", *cipherTable[pre+"ccm"])
		add("id-aes"+b+"-GCM", *cipherTable[pre+"gcm"])
		add("id-aes"+b+"-wrap", wrap)
		add("id-aes"+b+"-wrap-pad", wrapPad)
	}
	add("chacha20", cipherSpec{name: "chacha20", mode: "stream", impl: "chacha20", family: "chacha", nid: 1019, keyLen: 32, ivLen: 16})
	add("chacha20-poly1305", cipherSpec{name: "chacha20-poly1305", mode: "stream", impl: "chachapoly", family: "chacha", nid: 1018, keyLen: 32, ivLen: 12})
	for _, d := range []struct {
		family string
		keyLen int
		base   string
		nids   map[string]int
	}{
		{"des2", 16, "des-ede", map[string]int{"ecb": 32, "cbc": 43, "cfb": 60, "ofb": 62}},
		{"des3", 24, "des-ede3", map[string]int{"ecb": 33, "cbc": 44, "cfb": 61, "ofb": 63, "cfb1": 658, "cfb8": 659}},
	} {
		ecb := cipherSpec{name: d.base, mode: "ecb", impl: "ecb", family: d.family, nid: d.nids["ecb"], keyLen: d.keyLen, blockSize: 8}
		add(d.base, ecb)
		add(d.base+"-ecb", ecb)
		add(d.base+"-cbc", cipherSpec{name: d.base + "-cbc", mode: "cbc", impl: "cbc", family: d.family, nid: d.nids["cbc"], keyLen: d.keyLen, blockSize: 8, ivLen: 8})
		add(d.base+"-cfb", cipherSpec{name: d.base + "-cfb", mode: "cfb", impl: "cfb", family: d.family, nid: d.nids["cfb"], keyLen: d.keyLen, blockSize: 1, ivLen: 8})
		add(d.base+"-ofb", cipherSpec{name: d.base + "-ofb", mode: "ofb", impl: "ofb", family: d.family, nid: d.nids["ofb"], keyLen: d.keyLen, blockSize: 1, ivLen: 8})
		if d.family == "des3" {
			add(d.base+"-cfb1", cipherSpec{name: d.base + "-cfb1", mode: "cfb", impl: "cfb1", family: d.family, nid: d.nids["cfb1"], keyLen: d.keyLen, blockSize: 1, ivLen: 8})
			add(d.base+"-cfb8", cipherSpec{name: d.base + "-cfb8", mode: "cfb", impl: "cfb8", family: d.family, nid: d.nids["cfb8"], keyLen: d.keyLen, blockSize: 1, ivLen: 8})
		}
	}
	add("des3", *cipherTable["des-ede3-cbc"])
	sort.Strings(cipherNames)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func lookupCipher(name string) *cipherSpec { return cipherTable[strings.ToLower(name)] }

// validIVLen reports whether ivLen is acceptable for s (node accepts
// variable IV lengths only for the AEAD modes).
func (s *cipherSpec) validIVLen(n int) bool {
	switch s.impl {
	case "gcm":
		return n > 0
	case "ccm":
		return n >= 7 && n <= 13
	}
	return n == s.ivLen
}

type cipherCtx struct {
	spec    *cipherSpec
	decrypt bool
	block   cipher.Block
	key, iv []byte

	stream  cipher.Stream // ctr cfb ofb chacha20, and the keystream of gcm/chachapoly
	pending []byte        // cbc/ecb partial blocks
	reg     []byte        // cfb1/cfb8 shift register
	cbcPrev []byte
	padding bool

	// AEAD state
	aead       bool
	tagLen     int // -1 when not fixed at init (gcm decrypt)
	aad        []byte
	plain      []byte // whole plaintext, for the tag computed at final
	tag        []byte
	tagSet     bool
	ccmLen     int // plaintextLength from setAAD, -1 if unset
	ccmDone    bool
	ccmFail    bool
	started    bool // update called
	finalized  bool
	authFailed bool
}

func newBlock(spec *cipherSpec, key []byte) (cipher.Block, error) {
	switch spec.family {
	case "aes":
		return aes.NewCipher(key)
	case "des3":
		return des.NewTripleDESCipher(key)
	case "des2":
		k := append(append(append([]byte{}, key[:16]...)), key[:8]...)
		return des.NewTripleDESCipher(k)
	}
	return nil, errors.New("no block cipher")
}

func (b *cryptoBinding) cipherInit(name string, key, iv []byte, hasIV bool, decrypt bool, authTagLen int) (*cipherCtx, error) {
	spec := lookupCipher(name)
	if spec == nil {
		return nil, b.err("ERR_CRYPTO_UNKNOWN_CIPHER", "Unknown cipher")
	}
	invalidIV := b.typeErr("ERR_CRYPTO_INVALID_IV", "Invalid initialization vector")
	isAEAD := spec.impl == "gcm" || spec.impl == "ccm" || spec.impl == "chachapoly"
	ivLen := len(iv)
	if !hasIV {
		ivLen = 0
	}
	if spec.ivLen == 0 {
		if ivLen != 0 {
			return nil, invalidIV
		}
	} else {
		if ivLen == 0 {
			return nil, invalidIV
		}
		if !isAEAD && ivLen != spec.ivLen {
			return nil, invalidIV
		}
		if spec.impl == "chachapoly" && ivLen != 12 {
			return nil, invalidIV
		}
	}
	if len(key) != spec.keyLen {
		return nil, b.rangeErr("ERR_CRYPTO_INVALID_KEYLEN", "Invalid key length")
	}
	c := &cipherCtx{spec: spec, decrypt: decrypt, key: key, iv: iv, padding: true, tagLen: -1, ccmLen: -1}
	badTag := func() error {
		return b.typeErr("ERR_CRYPTO_INVALID_AUTH_TAG", "Invalid authentication tag length: "+itoa(authTagLen))
	}
	switch spec.impl {
	case "gcm":
		c.aead = true
		if authTagLen >= 0 {
			if !(authTagLen == 4 || authTagLen == 8 || (authTagLen >= 12 && authTagLen <= 16)) {
				return nil, badTag()
			}
			c.tagLen = authTagLen
		} else if !decrypt {
			c.tagLen = 16
		}
	case "ccm":
		c.aead = true
		if authTagLen < 0 {
			return nil, b.typeErr("ERR_CRYPTO_INVALID_AUTH_TAG", "authTagLength required for "+name)
		}
		if authTagLen < 4 || authTagLen > 16 || authTagLen%2 != 0 {
			return nil, badTag()
		}
		if ivLen < 7 || ivLen > 13 {
			return nil, invalidIV
		}
		c.tagLen = authTagLen
	case "chachapoly":
		c.aead = true
		if authTagLen >= 0 {
			if authTagLen < 1 || authTagLen > 16 {
				return nil, badTag()
			}
			c.tagLen = authTagLen
		} else {
			c.tagLen = 16
		}
	}
	var err error
	switch spec.impl {
	case "chacha20":
		ctr := binary.LittleEndian.Uint32(iv[:4])
		s, e := chacha20.NewUnauthenticatedCipher(key, iv[4:16])
		if e != nil {
			return nil, e
		}
		s.SetCounter(ctr)
		c.stream = s
		return c, nil
	case "chachapoly":
		s, e := chacha20.NewUnauthenticatedCipher(key, iv)
		if e != nil {
			return nil, e
		}
		s.SetCounter(1)
		c.stream = s
		return c, nil
	}
	c.block, err = newBlock(spec, key)
	if err != nil {
		return nil, err
	}
	bs := c.block.BlockSize()
	switch spec.impl {
	case "cbc":
		c.cbcPrev = append([]byte(nil), iv...)
	case "ctr":
		c.stream = cipher.NewCTR(c.block, iv)
	case "ofb":
		c.stream = cipher.NewOFB(c.block, iv) //nolint:staticcheck // node offers OFB
	case "cfb":
		if decrypt {
			c.stream = cipher.NewCFBDecrypter(c.block, iv) //nolint:staticcheck
		} else {
			c.stream = cipher.NewCFBEncrypter(c.block, iv) //nolint:staticcheck
		}
	case "cfb8", "cfb1":
		c.reg = append([]byte(nil), iv[:bs]...)
	case "gcm":
		c.stream = &ctr32{block: c.block, ctr: gcmCounter0(c.block, iv)}
		c.stream.(*ctr32).inc()
	}
	return c, nil
}

// ctr32 is GCM's counter mode: only the low 32 bits of the counter block
// increment.
type ctr32 struct {
	block cipher.Block
	ctr   [16]byte
	ks    [16]byte
	used  int
}

func (c *ctr32) inc() {
	n := binary.BigEndian.Uint32(c.ctr[12:]) + 1
	binary.BigEndian.PutUint32(c.ctr[12:], n)
}

func (c *ctr32) XORKeyStream(dst, src []byte) {
	if c.used == 0 {
		c.used = 16
	}
	for i := range src {
		if c.used == 16 {
			c.block.Encrypt(c.ks[:], c.ctr[:])
			c.inc()
			c.used = 0
		}
		dst[i] = src[i] ^ c.ks[c.used]
		c.used++
	}
}

// gcmCounter0 computes GCM's pre-counter block J0 for iv.
func gcmCounter0(block cipher.Block, iv []byte) [16]byte {
	var j0 [16]byte
	if len(iv) == 12 {
		copy(j0[:], iv)
		j0[15] = 1
		return j0
	}
	var h [16]byte
	block.Encrypt(h[:], h[:])
	var y [16]byte
	for i := 0; i < len(iv); i += 16 {
		var blk [16]byte
		copy(blk[:], iv[i:])
		for k := range y {
			y[k] ^= blk[k]
		}
		y = gfMul(y, h)
	}
	var lenBlk [16]byte
	binary.BigEndian.PutUint64(lenBlk[8:], uint64(len(iv))*8)
	for k := range y {
		y[k] ^= lenBlk[k]
	}
	return gfMul(y, h)
}

// gfMul multiplies in GF(2^128) with GCM's bit order.
func gfMul(x, y [16]byte) [16]byte {
	var z [16]byte
	v := y
	for i := 0; i < 128; i++ {
		if x[i/8]&(0x80>>(i%8)) != 0 {
			for k := range z {
				z[k] ^= v[k]
			}
		}
		lsb := v[15] & 1
		for k := 15; k > 0; k-- {
			v[k] = v[k]>>1 | v[k-1]<<7
		}
		v[0] >>= 1
		if lsb != 0 {
			v[0] ^= 0xe1
		}
	}
	return z
}

var errUnsupportedState = errors.New("Trying to add data in unsupported state")

func (c *cipherCtx) update(in []byte) ([]byte, error) {
	if c.finalized {
		return nil, errUnsupportedState
	}
	c.started = true
	switch c.spec.impl {
	case "ctr", "ofb", "cfb", "chacha20":
		out := make([]byte, len(in))
		c.stream.XORKeyStream(out, in)
		return out, nil
	case "gcm", "chachapoly":
		out := make([]byte, len(in))
		c.stream.XORKeyStream(out, in)
		if c.decrypt {
			c.plain = append(c.plain, out...)
		} else {
			c.plain = append(c.plain, in...)
		}
		return out, nil
	case "cfb8":
		return c.cfb8(in), nil
	case "cfb1":
		return c.cfb1(in), nil
	case "ccm":
		return c.ccmUpdate(in)
	case "wrap", "wrappad":
		return c.wrapUpdate(in)
	}
	// cbc / ecb
	bs := c.block.BlockSize()
	c.pending = append(c.pending, in...)
	n := len(c.pending) / bs * bs
	if c.decrypt && c.padding && n == len(c.pending) && n > 0 {
		n -= bs // hold back the block that may carry padding
	}
	out := c.blocks(c.pending[:n])
	c.pending = append([]byte(nil), c.pending[n:]...)
	return out, nil
}

func (c *cipherCtx) blocks(data []byte) []byte {
	bs := c.block.BlockSize()
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += bs {
		src := data[i : i+bs]
		dst := out[i : i+bs]
		switch {
		case c.spec.impl == "ecb" && c.decrypt:
			c.block.Decrypt(dst, src)
		case c.spec.impl == "ecb":
			c.block.Encrypt(dst, src)
		case c.decrypt:
			c.block.Decrypt(dst, src)
			subtle.XORBytes(dst, dst, c.cbcPrev)
			copy(c.cbcPrev, src)
		default:
			var x [16]byte
			subtle.XORBytes(x[:bs], src, c.cbcPrev)
			c.block.Encrypt(dst, x[:bs])
			copy(c.cbcPrev, dst)
		}
	}
	return out
}

func (c *cipherCtx) cfb8(in []byte) []byte {
	bs := len(c.reg)
	out := make([]byte, len(in))
	ks := make([]byte, bs)
	for i, p := range in {
		c.block.Encrypt(ks, c.reg)
		o := p ^ ks[0]
		ct := o
		if c.decrypt {
			ct = p
		}
		copy(c.reg, c.reg[1:])
		c.reg[bs-1] = ct
		out[i] = o
	}
	return out
}

func (c *cipherCtx) cfb1(in []byte) []byte {
	bs := len(c.reg)
	out := make([]byte, len(in))
	ks := make([]byte, bs)
	for i, p := range in {
		var ob byte
		for bit := 7; bit >= 0; bit-- {
			c.block.Encrypt(ks, c.reg)
			pb := (p >> uint(bit)) & 1
			o := pb ^ (ks[0] >> 7)
			ct := o
			if c.decrypt {
				ct = pb
			}
			// shift register left one bit, appending ct
			for k := 0; k < bs-1; k++ {
				c.reg[k] = c.reg[k]<<1 | c.reg[k+1]>>7
			}
			c.reg[bs-1] = c.reg[bs-1]<<1 | ct
			ob |= o << uint(bit)
		}
		out[i] = ob
	}
	return out
}

type cipherFinalError struct{ hex, reason string }

func (c *cipherCtx) final() ([]byte, *cipherFinalError, error) {
	if c.finalized {
		return nil, nil, errors.New("invalid state")
	}
	c.finalized = true
	switch c.spec.impl {
	case "cbc", "ecb":
		bs := c.block.BlockSize()
		if !c.decrypt {
			if c.padding {
				pad := bs - len(c.pending)%bs
				for i := 0; i < pad; i++ {
					c.pending = append(c.pending, byte(pad))
				}
			} else if len(c.pending)%bs != 0 {
				return nil, &cipherFinalError{"1C80006B", "wrong final block length"}, nil
			}
			return c.blocks(c.pending), nil, nil
		}
		if len(c.pending)%bs != 0 || (c.padding && len(c.pending) == 0) {
			return nil, &cipherFinalError{"1C80006B", "wrong final block length"}, nil
		}
		out := c.blocks(c.pending)
		if !c.padding {
			return out, nil, nil
		}
		pad := int(out[len(out)-1])
		if pad == 0 || pad > bs {
			return nil, &cipherFinalError{"1C800064", "bad decrypt"}, nil
		}
		for _, x := range out[len(out)-pad:] {
			if int(x) != pad {
				return nil, &cipherFinalError{"1C800064", "bad decrypt"}, nil
			}
		}
		return out[:len(out)-pad], nil, nil
	case "gcm", "chachapoly":
		full := c.computeTag()
		if !c.decrypt {
			c.tag = full[:c.tagLen]
			return []byte{}, nil, nil
		}
		if !c.tagSet || subtle.ConstantTimeCompare(full[:len(c.tag)], c.tag) != 1 {
			c.authFailed = true
			return nil, nil, errors.New("Unsupported state or unable to authenticate data")
		}
		return []byte{}, nil, nil
	case "ccm":
		if !c.ccmDone {
			// no update(): process the empty message now
			if _, err := c.ccmUpdate(nil); err != nil {
				return nil, nil, err
			}
		}
		if c.decrypt && c.ccmFail {
			c.authFailed = true
			return nil, nil, errors.New("Unsupported state or unable to authenticate data")
		}
		return []byte{}, nil, nil
	}
	return []byte{}, nil, nil
}

func (c *cipherCtx) computeTag() []byte {
	var sealed []byte
	if c.spec.impl == "gcm" {
		g, err := cipher.NewGCMWithNonceSize(c.block, len(c.iv))
		if err != nil {
			return make([]byte, 16)
		}
		sealed = g.Seal(nil, c.iv, c.plain, c.aad)
	} else {
		a, err := chacha20poly1305.New(c.key)
		if err != nil {
			return make([]byte, 16)
		}
		sealed = a.Seal(nil, c.iv, c.plain, c.aad)
	}
	return sealed[len(sealed)-16:]
}

// ---- CCM (RFC 3610) ----

func (c *cipherCtx) ccmUpdate(in []byte) ([]byte, error) {
	if c.ccmDone {
		return nil, errUnsupportedState
	}
	if c.ccmLen >= 0 && len(in) != c.ccmLen {
		return nil, errUnsupportedState
	}
	c.ccmDone = true
	nonce := c.iv
	L := 15 - len(nonce)
	if L < 8 && uint64(len(in)) >= uint64(1)<<(8*uint(L)) {
		return nil, errUnsupportedState
	}
	ctrBlock := func(i uint64) []byte {
		a := make([]byte, 16)
		a[0] = byte(L - 1)
		copy(a[1:], nonce)
		for k := 0; k < L; k++ {
			a[15-k] = byte(i >> (8 * uint(k)))
		}
		return a
	}
	xorCtr := func(data []byte) []byte {
		out := make([]byte, len(data))
		ks := make([]byte, 16)
		for i := 0; i < len(data); i += 16 {
			c.block.Encrypt(ks, ctrBlock(uint64(i/16)+1))
			end := i + 16
			if end > len(data) {
				end = len(data)
			}
			subtle.XORBytes(out[i:end], data[i:end], ks)
		}
		return out
	}
	mac := func(msg []byte) []byte {
		b0 := make([]byte, 16)
		flags := byte(((c.tagLen - 2) / 2) << 3)
		flags |= byte(L - 1)
		if len(c.aad) > 0 {
			flags |= 0x40
		}
		b0[0] = flags
		copy(b0[1:], nonce)
		n := uint64(len(msg))
		for k := 0; k < L; k++ {
			b0[15-k] = byte(n >> (8 * uint(k)))
		}
		var buf []byte
		buf = append(buf, b0...)
		if len(c.aad) > 0 {
			var hdr []byte
			if len(c.aad) < 0xff00 {
				hdr = []byte{byte(len(c.aad) >> 8), byte(len(c.aad))}
			} else {
				hdr = []byte{0xff, 0xfe, 0, 0, 0, 0}
				binary.BigEndian.PutUint32(hdr[2:], uint32(len(c.aad)))
			}
			a := append(hdr, c.aad...)
			for len(a)%16 != 0 {
				a = append(a, 0)
			}
			buf = append(buf, a...)
		}
		m := append([]byte(nil), msg...)
		for len(m)%16 != 0 {
			m = append(m, 0)
		}
		buf = append(buf, m...)
		x := make([]byte, 16)
		for i := 0; i < len(buf); i += 16 {
			subtle.XORBytes(x, x, buf[i:i+16])
			c.block.Encrypt(x, x)
		}
		s0 := make([]byte, 16)
		c.block.Encrypt(s0, ctrBlock(0))
		subtle.XORBytes(x, x, s0)
		return x[:c.tagLen]
	}
	if !c.decrypt {
		c.tag = mac(in)
		return xorCtr(in), nil
	}
	plain := xorCtr(in)
	if !c.tagSet || subtle.ConstantTimeCompare(mac(plain), c.tag) != 1 {
		c.ccmFail = true
		return []byte{}, nil
	}
	return plain, nil
}

// ---- AES key wrap (RFC 3394 / RFC 5649) ----

func (c *cipherCtx) wrapUpdate(in []byte) ([]byte, error) {
	if c.started && c.pending != nil {
		return nil, errUnsupportedState
	}
	c.pending = []byte{}
	var out []byte
	var ok bool
	if c.spec.impl == "wrap" {
		if c.decrypt {
			out, ok = aesUnwrap(c.block, c.iv, in)
		} else {
			out, ok = aesWrap(c.block, c.iv, in)
		}
	} else {
		if c.decrypt {
			out, ok = aesUnwrapPad(c.block, c.iv, in)
		} else {
			out, ok = aesWrapPad(c.block, c.iv, in)
		}
	}
	if !ok {
		return nil, errUnsupportedState
	}
	return out, nil
}

func aesWrapCore(block cipher.Block, a []byte, p []byte) []byte {
	n := len(p) / 8
	r := make([]byte, len(p))
	copy(r, p)
	A := append([]byte(nil), a...)
	b := make([]byte, 16)
	for j := 0; j < 6; j++ {
		for i := 0; i < n; i++ {
			copy(b, A)
			copy(b[8:], r[i*8:i*8+8])
			block.Encrypt(b, b)
			t := uint64(n*j + i + 1)
			v := binary.BigEndian.Uint64(b[:8]) ^ t
			binary.BigEndian.PutUint64(A, v)
			copy(r[i*8:], b[8:])
		}
	}
	return append(A, r...)
}

func aesUnwrapCore(block cipher.Block, c []byte) (a []byte, p []byte) {
	n := len(c)/8 - 1
	A := append([]byte(nil), c[:8]...)
	r := append([]byte(nil), c[8:]...)
	b := make([]byte, 16)
	for j := 5; j >= 0; j-- {
		for i := n - 1; i >= 0; i-- {
			t := uint64(n*j + i + 1)
			binary.BigEndian.PutUint64(b, binary.BigEndian.Uint64(A)^t)
			copy(b[8:], r[i*8:i*8+8])
			block.Decrypt(b, b)
			copy(A, b[:8])
			copy(r[i*8:], b[8:])
		}
	}
	return A, r
}

var defaultWrapIV = []byte{0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6}

func aesWrap(block cipher.Block, iv, p []byte) ([]byte, bool) {
	if len(p) < 16 || len(p)%8 != 0 {
		return nil, false
	}
	if len(iv) != 8 {
		iv = defaultWrapIV
	}
	return aesWrapCore(block, iv, p), true
}

func aesUnwrap(block cipher.Block, iv, c []byte) ([]byte, bool) {
	if len(c) < 24 || len(c)%8 != 0 {
		return nil, false
	}
	if len(iv) != 8 {
		iv = defaultWrapIV
	}
	a, p := aesUnwrapCore(block, c)
	if subtle.ConstantTimeCompare(a, iv) != 1 {
		return nil, false
	}
	return p, true
}

func aesWrapPad(block cipher.Block, iv, p []byte) ([]byte, bool) {
	if len(p) == 0 {
		return nil, false
	}
	aiv := make([]byte, 8)
	copy(aiv, []byte{0xa6, 0x59, 0x59, 0xa6})
	if len(iv) == 4 {
		copy(aiv, iv)
	}
	binary.BigEndian.PutUint32(aiv[4:], uint32(len(p)))
	padded := append([]byte(nil), p...)
	for len(padded)%8 != 0 {
		padded = append(padded, 0)
	}
	if len(padded) == 8 {
		out := make([]byte, 16)
		copy(out, aiv)
		copy(out[8:], padded)
		block.Encrypt(out, out)
		return out, true
	}
	return aesWrapCore(block, aiv, padded), true
}

func aesUnwrapPad(block cipher.Block, iv, c []byte) ([]byte, bool) {
	if len(c) < 16 || len(c)%8 != 0 {
		return nil, false
	}
	prefix := []byte{0xa6, 0x59, 0x59, 0xa6}
	if len(iv) == 4 {
		prefix = iv
	}
	var a, p []byte
	if len(c) == 16 {
		b := make([]byte, 16)
		block.Decrypt(b, c)
		a, p = b[:8], b[8:]
	} else {
		a, p = aesUnwrapCore(block, c)
	}
	if subtle.ConstantTimeCompare(a[:4], prefix) != 1 {
		return nil, false
	}
	mli := int(binary.BigEndian.Uint32(a[4:]))
	if mli > len(p) || mli <= len(p)-8 {
		return nil, false
	}
	for _, z := range p[mli:] {
		if z != 0 {
			return nil, false
		}
	}
	return p[:mli], true
}

// ---- binding ----

func (b *cryptoBinding) installCipher() {
	b.fn("cipherInit", func(args []vm.Value) (vm.Value, error) {
		ivv := argAt(args, 2)
		hasIV := !ivv.IsNull() && !ivv.IsUndefined()
		c, err := b.cipherInit(strArg(args, 0), b.bytesArg(args, 1), b.bytesArg(args, 2), hasIV, argAt(args, 3).IsTruthy(), intArg(args, 4, -1))
		if err != nil {
			return vm.Undefined, err
		}
		return b.handle(c), nil
	})
	b.fn("cipherUpdate", func(args []vm.Value) (vm.Value, error) {
		c, ok := handleOf[*cipherCtx](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid cipher handle")
		}
		out, err := c.update(b.bytesArg(args, 1))
		if err != nil {
			return vm.Undefined, err
		}
		return b.buf(out), nil
	})
	b.fn("cipherFinal", func(args []vm.Value) (vm.Value, error) {
		c, ok := handleOf[*cipherCtx](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid cipher handle")
		}
		if c.finalized {
			return vm.Undefined, b.err("ERR_CRYPTO_INVALID_STATE", "Invalid state")
		}
		out, fe, err := c.final()
		if fe != nil {
			return vm.Undefined, b.osslErr(fe.hex, "Provider routines", fe.reason)
		}
		if err != nil {
			return vm.Undefined, err
		}
		return b.buf(out), nil
	})
	invalidState := func(op string) error {
		return b.err("ERR_CRYPTO_INVALID_STATE", "Invalid state for operation "+op)
	}
	b.fn("cipherSetAutoPadding", func(args []vm.Value) (vm.Value, error) {
		c, _ := handleOf[*cipherCtx](args, 0)
		if c == nil || c.finalized {
			return vm.Undefined, invalidState("setAutoPadding")
		}
		c.padding = argAt(args, 1).IsTruthy()
		return vm.Undefined, nil
	})
	b.fn("cipherSetAAD", func(args []vm.Value) (vm.Value, error) {
		c, _ := handleOf[*cipherCtx](args, 0)
		if c == nil || !c.aead || c.started || c.finalized {
			return vm.Undefined, invalidState("setAAD")
		}
		plen := intArg(args, 2, -1)
		aad := b.bytesArg(args, 1)
		if c.spec.impl == "ccm" {
			if plen < 0 {
				return vm.Undefined, b.typeErr("ERR_MISSING_ARGS", "options.plaintextLength required for CCM mode with AAD")
			}
			c.ccmLen = plen
		}
		c.aad = append(c.aad, aad...)
		return vm.Undefined, nil
	})
	b.fn("cipherSetAuthTag", func(args []vm.Value) (vm.Value, error) {
		c, _ := handleOf[*cipherCtx](args, 0)
		if c == nil || !c.aead || !c.decrypt || c.tagSet || c.finalized {
			return vm.Undefined, invalidState("setAuthTag")
		}
		tag := b.bytesArg(args, 1)
		n := len(tag)
		valid := false
		switch c.spec.impl {
		case "gcm":
			// node 26 accepts only full 16-byte tags unless authTagLength
			// was given up front.
			valid = (c.tagLen < 0 && n == 16) || n == c.tagLen
		case "ccm":
			valid = n == c.tagLen
		case "chachapoly":
			valid = n >= 1 && n <= 16 && (c.tagLen == 16 || n == c.tagLen)
		}
		if !valid {
			return vm.Undefined, b.typeErr("ERR_CRYPTO_INVALID_AUTH_TAG", "Invalid authentication tag length: "+itoa(n))
		}
		c.tag = tag
		c.tagSet = true
		return vm.Undefined, nil
	})
	b.fn("cipherGetAuthTag", func(args []vm.Value) (vm.Value, error) {
		c, _ := handleOf[*cipherCtx](args, 0)
		if c == nil || !c.aead || c.decrypt || !c.finalized || c.tag == nil {
			return vm.Undefined, invalidState("getAuthTag")
		}
		return b.buf(c.tag), nil
	})
	b.fn("cipherInfo", func(args []vm.Value) (vm.Value, error) {
		var spec *cipherSpec
		v := argAt(args, 0)
		if v.IsNumber() {
			nid := int(v.ToFloat())
			for _, n := range cipherNames {
				if s := cipherTable[strings.ToLower(n)]; s.nid == nid {
					spec = s
					break
				}
			}
		} else {
			spec = lookupCipher(v.ToString())
		}
		if spec == nil {
			return vm.Undefined, nil
		}
		info := map[string]any{"mode": spec.mode, "name": spec.name, "nid": spec.nid, "keyLength": spec.keyLen}
		if spec.blockSize > 0 {
			info["blockSize"] = spec.blockSize
		}
		if spec.ivLen > 0 {
			info["ivLength"] = spec.ivLen
		}
		if kl := intArg(args, 1, -1); kl >= 0 && kl != spec.keyLen {
			return vm.Undefined, nil
		}
		if il := intArg(args, 2, -1); il >= 0 {
			if spec.ivLen == 0 || !spec.validIVLen(il) {
				return vm.Undefined, nil
			}
			info["ivLength"] = il
		}
		return b.jsonValue(info)
	})
	b.fn("getCiphers", func(args []vm.Value) (vm.Value, error) {
		arr := vm.NewArray()
		for _, n := range cipherNames {
			arr.AsArray().Append(vm.NewString(n))
		}
		return arr, nil
	})
}
