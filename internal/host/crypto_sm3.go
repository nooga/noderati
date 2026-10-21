package host

import (
	"encoding/binary"
	"hash"
	"math/bits"
)

// sm3 is the Chinese national hash standard (GB/T 32905-2016), offered by
// node through OpenSSL and absent from Go's standard library.
type sm3 struct {
	v   [8]uint32
	buf [64]byte
	n   int
	len uint64
}

var sm3IV = [8]uint32{0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600, 0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e}

func newSM3() hash.Hash { h := &sm3{}; h.Reset(); return h }

func (h *sm3) Reset()         { h.v = sm3IV; h.n = 0; h.len = 0 }
func (h *sm3) Size() int      { return 32 }
func (h *sm3) BlockSize() int { return 64 }

func (h *sm3) Clone() (hash.Cloner, error) { c := *h; return &c, nil }

func (h *sm3) Write(p []byte) (int, error) {
	n := len(p)
	h.len += uint64(n)
	for len(p) > 0 {
		c := copy(h.buf[h.n:], p)
		h.n += c
		p = p[c:]
		if h.n == 64 {
			h.block(h.buf[:])
			h.n = 0
		}
	}
	return n, nil
}

func (h *sm3) Sum(b []byte) []byte {
	c := *h
	bitLen := c.len * 8
	c.Write([]byte{0x80})
	for c.n != 56 {
		c.Write([]byte{0})
	}
	var l [8]byte
	binary.BigEndian.PutUint64(l[:], bitLen)
	c.Write(l[:])
	var out [32]byte
	for i, x := range c.v {
		binary.BigEndian.PutUint32(out[i*4:], x)
	}
	return append(b, out[:]...)
}

func (h *sm3) block(p []byte) {
	var w [68]uint32
	var w1 [64]uint32
	for i := 0; i < 16; i++ {
		w[i] = binary.BigEndian.Uint32(p[i*4:])
	}
	p1 := func(x uint32) uint32 { return x ^ bits.RotateLeft32(x, 15) ^ bits.RotateLeft32(x, 23) }
	for i := 16; i < 68; i++ {
		w[i] = p1(w[i-16]^w[i-9]^bits.RotateLeft32(w[i-3], 15)) ^ bits.RotateLeft32(w[i-13], 7) ^ w[i-6]
	}
	for i := 0; i < 64; i++ {
		w1[i] = w[i] ^ w[i+4]
	}
	a, b, c, d, e, f, g, hh := h.v[0], h.v[1], h.v[2], h.v[3], h.v[4], h.v[5], h.v[6], h.v[7]
	for j := 0; j < 64; j++ {
		var t uint32 = 0x79cc4519
		if j >= 16 {
			t = 0x7a879d8a
		}
		ss1 := bits.RotateLeft32(bits.RotateLeft32(a, 12)+e+bits.RotateLeft32(t, j%32), 7)
		ss2 := ss1 ^ bits.RotateLeft32(a, 12)
		var ff, gg uint32
		if j < 16 {
			ff = a ^ b ^ c
			gg = e ^ f ^ g
		} else {
			ff = (a & b) | (a & c) | (b & c)
			gg = (e & f) | (^e & g)
		}
		tt1 := ff + d + ss2 + w1[j]
		tt2 := gg + hh + ss1 + w[j]
		d = c
		c = bits.RotateLeft32(b, 9)
		b = a
		a = tt1
		hh = g
		g = bits.RotateLeft32(f, 19)
		f = e
		e = tt2 ^ bits.RotateLeft32(tt2, 9) ^ bits.RotateLeft32(tt2, 17)
	}
	h.v[0] ^= a
	h.v[1] ^= b
	h.v[2] ^= c
	h.v[3] ^= d
	h.v[4] ^= e
	h.v[5] ^= f
	h.v[6] ^= g
	h.v[7] ^= hh
}
