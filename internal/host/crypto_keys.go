package host

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/mldsa"
	"crypto/mlkem"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"os"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/nooga/paserati/pkg/vm"
)

// node (OpenSSL) works with RSA keys down to 512 bits; Go's crypto/rsa
// refuses anything under 1024 unless rsa1024min=0. The runtime re-reads
// GODEBUG when it is set through os.Setenv.
func init() {
	cur := os.Getenv("GODEBUG")
	if !strings.Contains(cur, "rsa1024min") {
		if cur != "" {
			cur += ","
		}
		os.Setenv("GODEBUG", cur+"rsa1024min=0")
	}
}

// ---- curves -----------------------------------------------------------------

type ecCurve struct {
	name    string // OpenSSL / node name (asymmetricKeyDetails.namedCurve)
	jwk     string
	oid     asn1.ObjectIdentifier
	curve   elliptic.Curve
	ecdh    ecdh.Curve // nil for secp256k1
	size    int
	aliases []string
}

var ecCurveList = []*ecCurve{
	{name: "prime256v1", jwk: "P-256", oid: asn1.ObjectIdentifier{1, 2, 840, 10045, 3, 1, 7}, curve: elliptic.P256(), ecdh: ecdh.P256(), size: 32, aliases: []string{"p-256", "secp256r1"}},
	{name: "secp384r1", jwk: "P-384", oid: asn1.ObjectIdentifier{1, 3, 132, 0, 34}, curve: elliptic.P384(), ecdh: ecdh.P384(), size: 48, aliases: []string{"p-384"}},
	{name: "secp521r1", jwk: "P-521", oid: asn1.ObjectIdentifier{1, 3, 132, 0, 35}, curve: elliptic.P521(), ecdh: ecdh.P521(), size: 66, aliases: []string{"p-521"}},
	{name: "secp256k1", jwk: "secp256k1", oid: asn1.ObjectIdentifier{1, 3, 132, 0, 10}, curve: secp256k1.S256(), size: 32},
}

func lookupCurve(name string) *ecCurve {
	n := strings.ToLower(name)
	for _, c := range ecCurveList {
		if n == c.name || n == strings.ToLower(c.jwk) {
			return c
		}
		for _, a := range c.aliases {
			if n == a {
				return c
			}
		}
	}
	return nil
}

func curveByOID(oid asn1.ObjectIdentifier) *ecCurve {
	for _, c := range ecCurveList {
		if c.oid.Equal(oid) {
			return c
		}
	}
	return nil
}

// ecKey holds raw key material: d is the fixed-width private scalar (nil
// for public keys), pub the uncompressed point.
type ecKey struct {
	curve *ecCurve
	d     []byte
	pub   []byte
}

func (k *ecKey) xy() (*big.Int, *big.Int) {
	n := k.curve.size
	return new(big.Int).SetBytes(k.pub[1 : 1+n]), new(big.Int).SetBytes(k.pub[1+n:])
}

func newECPrivate(c *ecCurve, d []byte) (*ecKey, error) {
	if len(d) > c.size {
		return nil, errors.New("invalid private key")
	}
	d = leftPad(d, c.size)
	if c.ecdh == nil {
		priv := secp256k1.PrivKeyFromBytes(d)
		if priv.Key.IsZero() {
			return nil, errors.New("invalid private key")
		}
		return &ecKey{curve: c, d: d, pub: priv.PubKey().SerializeUncompressed()}, nil
	}
	priv, err := ecdsa.ParseRawPrivateKey(c.curve, d)
	if err != nil {
		return nil, err
	}
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	return &ecKey{curve: c, d: d, pub: pub}, nil
}

// newECPublic validates and normalizes a SEC1 point (compressed,
// uncompressed or hybrid) to uncompressed form.
func newECPublic(c *ecCurve, point []byte) (*ecKey, error) {
	if len(point) == 0 {
		return nil, errors.New("invalid point")
	}
	n := c.size
	var x, y *big.Int
	switch {
	case (point[0] == 2 || point[0] == 3) && len(point) == 1+n:
		x = new(big.Int).SetBytes(point[1:])
		var err error
		y, err = decompressY(c, x, point[0] == 3)
		if err != nil {
			return nil, err
		}
	case (point[0] == 4 || point[0] == 6 || point[0] == 7) && len(point) == 1+2*n:
		x = new(big.Int).SetBytes(point[1 : 1+n])
		y = new(big.Int).SetBytes(point[1+n:])
	default:
		return nil, errors.New("invalid point")
	}
	if !c.curve.IsOnCurve(x, y) {
		return nil, errors.New("point not on curve")
	}
	pub := make([]byte, 1+2*n)
	pub[0] = 4
	x.FillBytes(pub[1 : 1+n])
	y.FillBytes(pub[1+n:])
	return &ecKey{curve: c, pub: pub}, nil
}

func decompressY(c *ecCurve, x *big.Int, odd bool) (*big.Int, error) {
	p := c.curve.Params().P
	// y^2 = x^3 + a*x + b, with a = -3 for the NIST curves and 0 for secp256k1
	y2 := new(big.Int).Exp(x, big.NewInt(3), p)
	if c.ecdh != nil {
		y2.Sub(y2, new(big.Int).Mul(big.NewInt(3), x))
	}
	y2.Add(y2, c.curve.Params().B)
	y2.Mod(y2, p)
	y := new(big.Int).ModSqrt(y2, p)
	if y == nil {
		return nil, errors.New("invalid point")
	}
	if y.Bit(0) == 1 != odd {
		y.Sub(p, y)
	}
	return y, nil
}

func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}

// ---- asymmetric keys ----------------------------------------------------------

type pssParams struct {
	hash    string
	mgf1    string
	saltLen int
}

type asymKey struct {
	typ     string // asymmetricKeyType
	private bool

	rsaPriv *rsa.PrivateKey
	rsaPub  *rsa.PublicKey
	pss     *pssParams

	ec *ecKey

	edPriv ed25519.PrivateKey
	edPub  ed25519.PublicKey

	xPriv *ecdh.PrivateKey
	xPub  *ecdh.PublicKey

	mldsaPriv *mldsa.PrivateKey
	mldsaPub  *mldsa.PublicKey

	mlkemSeed []byte
	mlkemPub  []byte
}

func (k *asymKey) public() *asymKey {
	if !k.private {
		return k
	}
	p := &asymKey{typ: k.typ, pss: k.pss}
	switch {
	case k.rsaPriv != nil:
		p.rsaPub = &k.rsaPriv.PublicKey
	case k.ec != nil:
		p.ec = &ecKey{curve: k.ec.curve, pub: k.ec.pub}
	case k.edPriv != nil:
		p.edPub = k.edPriv.Public().(ed25519.PublicKey)
	case k.xPriv != nil:
		p.xPub = k.xPriv.PublicKey()
	case k.mldsaPriv != nil:
		p.mldsaPub = k.mldsaPriv.PublicKey()
	case k.mlkemSeed != nil:
		p.mlkemPub = k.mlkemPub
	}
	return p
}

func (k *asymKey) rsaPublic() *rsa.PublicKey {
	if k.rsaPriv != nil {
		return &k.rsaPriv.PublicKey
	}
	return k.rsaPub
}

var (
	oidRSA       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidRSAPSS    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidMGF1      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}
	oidEC        = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	oidEd25519   = asn1.ObjectIdentifier{1, 3, 101, 112}
	oidX25519    = asn1.ObjectIdentifier{1, 3, 101, 110}
	oidMLDSA44   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 17}
	oidMLDSA65   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 18}
	oidMLDSA87   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 19}
	oidMLKEM768  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 4, 2}
	oidMLKEM1024 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 4, 3}
	oidPBES2     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}
	oidPBKDF2    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 12}
)

var hashOIDs = map[string]asn1.ObjectIdentifier{
	"sha1":       {1, 3, 14, 3, 2, 26},
	"sha224":     {2, 16, 840, 1, 101, 3, 4, 2, 4},
	"sha256":     {2, 16, 840, 1, 101, 3, 4, 2, 1},
	"sha384":     {2, 16, 840, 1, 101, 3, 4, 2, 2},
	"sha512":     {2, 16, 840, 1, 101, 3, 4, 2, 3},
	"sha512-224": {2, 16, 840, 1, 101, 3, 4, 2, 5},
	"sha512-256": {2, 16, 840, 1, 101, 3, 4, 2, 6},
}

func hashByOID(oid asn1.ObjectIdentifier) string {
	for n, o := range hashOIDs {
		if o.Equal(oid) {
			return n
		}
	}
	return ""
}

var mldsaParams = map[string]func() mldsa.Parameters{
	"ml-dsa-44": mldsa.MLDSA44, "ml-dsa-65": mldsa.MLDSA65, "ml-dsa-87": mldsa.MLDSA87,
}

func mldsaOID(typ string) asn1.ObjectIdentifier {
	switch typ {
	case "ml-dsa-44":
		return oidMLDSA44
	case "ml-dsa-65":
		return oidMLDSA65
	}
	return oidMLDSA87
}

func mldsaTypeOf(p mldsa.Parameters) string {
	switch p {
	case mldsa.MLDSA44():
		return "ml-dsa-44"
	case mldsa.MLDSA65():
		return "ml-dsa-65"
	}
	return "ml-dsa-87"
}

func mlkemPubFromSeed(typ string, seed []byte) ([]byte, error) {
	if typ == "ml-kem-768" {
		dk, err := mlkem.NewDecapsulationKey768(seed)
		if err != nil {
			return nil, err
		}
		return dk.EncapsulationKey().Bytes(), nil
	}
	dk, err := mlkem.NewDecapsulationKey1024(seed)
	if err != nil {
		return nil, err
	}
	return dk.EncapsulationKey().Bytes(), nil
}

func checkMLKEMPub(typ string, pub []byte) error {
	var err error
	if typ == "ml-kem-768" {
		_, err = mlkem.NewEncapsulationKey768(pub)
	} else {
		_, err = mlkem.NewEncapsulationKey1024(pub)
	}
	return err
}

// ---- DER encoding -------------------------------------------------------------------

type spkiASN struct {
	Algo      pkix.AlgorithmIdentifier
	PublicKey asn1.BitString
}

type pkcs8ASN struct {
	Version    int
	Algo       pkix.AlgorithmIdentifier
	PrivateKey []byte
	Attributes asn1.RawValue `asn1:"optional,tag:0"`
}

type sec1ASN struct {
	Version       int
	PrivateKey    []byte
	NamedCurveOID asn1.ObjectIdentifier `asn1:"optional,explicit,tag:0"`
	PublicKey     asn1.BitString        `asn1:"optional,explicit,tag:1"`
}

type pssParamsASN struct {
	Hash       pkix.AlgorithmIdentifier `asn1:"optional,explicit,tag:0"`
	MGF        pkix.AlgorithmIdentifier `asn1:"optional,explicit,tag:1"`
	SaltLength int                      `asn1:"optional,explicit,tag:2,default:20"`
	Trailer    int                      `asn1:"optional,explicit,tag:3,default:1"`
}

func hashAlgID(name string) pkix.AlgorithmIdentifier {
	if name == "sha1" {
		return pkix.AlgorithmIdentifier{}
	}
	return pkix.AlgorithmIdentifier{Algorithm: hashOIDs[name], Parameters: asn1.NullRawValue}
}

func (k *asymKey) algID() pkix.AlgorithmIdentifier {
	switch k.typ {
	case "rsa":
		return pkix.AlgorithmIdentifier{Algorithm: oidRSA, Parameters: asn1.NullRawValue}
	case "rsa-pss":
		if k.pss == nil {
			return pkix.AlgorithmIdentifier{Algorithm: oidRSAPSS}
		}
		p := pssParamsASN{Hash: hashAlgID(k.pss.hash), SaltLength: k.pss.saltLen, Trailer: 1}
		if k.pss.mgf1 != "sha1" {
			inner, _ := asn1.Marshal(hashAlgID(k.pss.mgf1))
			p.MGF = pkix.AlgorithmIdentifier{Algorithm: oidMGF1, Parameters: asn1.RawValue{FullBytes: inner}}
		}
		der, _ := asn1.Marshal(p)
		return pkix.AlgorithmIdentifier{Algorithm: oidRSAPSS, Parameters: asn1.RawValue{FullBytes: der}}
	case "ec":
		der, _ := asn1.Marshal(k.ec.curve.oid)
		return pkix.AlgorithmIdentifier{Algorithm: oidEC, Parameters: asn1.RawValue{FullBytes: der}}
	case "ed25519":
		return pkix.AlgorithmIdentifier{Algorithm: oidEd25519}
	case "x25519":
		return pkix.AlgorithmIdentifier{Algorithm: oidX25519}
	case "ml-kem-768":
		return pkix.AlgorithmIdentifier{Algorithm: oidMLKEM768}
	case "ml-kem-1024":
		return pkix.AlgorithmIdentifier{Algorithm: oidMLKEM1024}
	}
	return pkix.AlgorithmIdentifier{Algorithm: mldsaOID(k.typ)}
}

func (k *asymKey) rawPublic() []byte {
	switch {
	case k.typ == "rsa" || k.typ == "rsa-pss":
		return x509.MarshalPKCS1PublicKey(k.rsaPublic())
	case k.ec != nil:
		return k.ec.pub
	case k.typ == "ed25519":
		return k.public().edPub
	case k.typ == "x25519":
		return k.public().xPub.Bytes()
	case k.typ == "ml-kem-768" || k.typ == "ml-kem-1024":
		return k.mlkemPub
	}
	return k.public().mldsaPub.Bytes()
}

func (k *asymKey) spkiDER() []byte {
	der, _ := asn1.Marshal(spkiASN{Algo: k.algID(), PublicKey: asn1.BitString{Bytes: k.rawPublic(), BitLength: 8 * len(k.rawPublic())}})
	return der
}

func (k *asymKey) sec1DER(withParams bool) []byte {
	s := sec1ASN{Version: 1, PrivateKey: k.ec.d, PublicKey: asn1.BitString{Bytes: k.ec.pub, BitLength: 8 * len(k.ec.pub)}}
	if withParams {
		s.NamedCurveOID = k.ec.curve.oid
	}
	der, _ := asn1.Marshal(s)
	return der
}

func (k *asymKey) pkcs8DER() []byte {
	var inner []byte
	switch k.typ {
	case "rsa", "rsa-pss":
		inner = x509.MarshalPKCS1PrivateKey(k.rsaPriv)
	case "ec":
		inner = k.sec1DER(false)
	case "ed25519":
		inner, _ = asn1.Marshal(k.edPriv.Seed())
	case "x25519":
		inner, _ = asn1.Marshal(k.xPriv.Bytes())
	case "ml-kem-768", "ml-kem-1024":
		inner = append([]byte{0x80, byte(len(k.mlkemSeed))}, k.mlkemSeed...)
	default:
		seed := k.mldsaPriv.Bytes()
		inner = append([]byte{0x80, byte(len(seed))}, seed...)
	}
	der, _ := asn1.Marshal(pkcs8ASN{Algo: k.algID(), PrivateKey: inner})
	return der
}

// ---- DER parsing ----------------------------------------------------------------------

var errDecoderUnsupported = errors.New("decoder unsupported")

func parsePSSParams(raw asn1.RawValue) (*pssParams, error) {
	if len(raw.FullBytes) == 0 || bytes.Equal(raw.FullBytes, asn1.NullRawValue.FullBytes) {
		return nil, nil
	}
	var p pssParamsASN
	if _, err := asn1.Unmarshal(raw.FullBytes, &p); err != nil {
		return nil, err
	}
	out := &pssParams{hash: "sha1", mgf1: "sha1", saltLen: p.SaltLength}
	if p.SaltLength == 0 && !bytes.Contains(raw.FullBytes, []byte{0xa2}) {
		out.saltLen = 20
	}
	if len(p.Hash.Algorithm) > 0 {
		out.hash = hashByOID(p.Hash.Algorithm)
	}
	if len(p.MGF.Algorithm) > 0 {
		var h pkix.AlgorithmIdentifier
		if _, err := asn1.Unmarshal(p.MGF.Parameters.FullBytes, &h); err == nil {
			out.mgf1 = hashByOID(h.Algorithm)
		}
	}
	if out.hash == "" || out.mgf1 == "" {
		return nil, errors.New("unsupported pss hash")
	}
	return out, nil
}

func parseSPKI(der []byte) (*asymKey, error) {
	var s spkiASN
	if rest, err := asn1.Unmarshal(der, &s); err != nil || len(rest) != 0 {
		return nil, errDecoderUnsupported
	}
	raw := s.PublicKey.RightAlign()
	alg := s.Algo.Algorithm
	switch {
	case alg.Equal(oidRSA), alg.Equal(oidRSAPSS):
		pub, err := x509.ParsePKCS1PublicKey(raw)
		if err != nil {
			return nil, err
		}
		k := &asymKey{typ: "rsa", rsaPub: pub}
		if alg.Equal(oidRSAPSS) {
			k.typ = "rsa-pss"
			if k.pss, err = parsePSSParams(s.Algo.Parameters); err != nil {
				return nil, err
			}
		}
		return k, nil
	case alg.Equal(oidEC):
		var coid asn1.ObjectIdentifier
		if _, err := asn1.Unmarshal(s.Algo.Parameters.FullBytes, &coid); err != nil {
			return nil, errDecoderUnsupported
		}
		c := curveByOID(coid)
		if c == nil {
			return nil, errDecoderUnsupported
		}
		ek, err := newECPublic(c, raw)
		if err != nil {
			return nil, err
		}
		return &asymKey{typ: "ec", ec: ek}, nil
	case alg.Equal(oidEd25519):
		if len(raw) != ed25519.PublicKeySize {
			return nil, errDecoderUnsupported
		}
		return &asymKey{typ: "ed25519", edPub: ed25519.PublicKey(append([]byte(nil), raw...))}, nil
	case alg.Equal(oidX25519):
		pub, err := ecdh.X25519().NewPublicKey(raw)
		if err != nil {
			return nil, err
		}
		return &asymKey{typ: "x25519", xPub: pub}, nil
	case alg.Equal(oidMLKEM768), alg.Equal(oidMLKEM1024):
		typ := "ml-kem-768"
		if alg.Equal(oidMLKEM1024) {
			typ = "ml-kem-1024"
		}
		if err := checkMLKEMPub(typ, raw); err != nil {
			return nil, err
		}
		return &asymKey{typ: typ, mlkemPub: raw}, nil
	case alg.Equal(oidMLDSA44), alg.Equal(oidMLDSA65), alg.Equal(oidMLDSA87):
		pk, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			return nil, err
		}
		mp := pk.(*mldsa.PublicKey)
		return &asymKey{typ: mldsaTypeOf(mp.Parameters()), mldsaPub: mp}, nil
	}
	return nil, errDecoderUnsupported
}

func parsePKCS8(der []byte) (*asymKey, error) {
	var p pkcs8ASN
	if rest, err := asn1.Unmarshal(der, &p); err != nil || len(rest) != 0 {
		return nil, errDecoderUnsupported
	}
	alg := p.Algo.Algorithm
	switch {
	case alg.Equal(oidRSA), alg.Equal(oidRSAPSS):
		priv, err := x509.ParsePKCS1PrivateKey(p.PrivateKey)
		if err != nil {
			return nil, err
		}
		k := &asymKey{typ: "rsa", private: true, rsaPriv: priv}
		if alg.Equal(oidRSAPSS) {
			k.typ = "rsa-pss"
			if k.pss, err = parsePSSParams(p.Algo.Parameters); err != nil {
				return nil, err
			}
		}
		return k, nil
	case alg.Equal(oidEC):
		var coid asn1.ObjectIdentifier
		if _, err := asn1.Unmarshal(p.Algo.Parameters.FullBytes, &coid); err != nil {
			return nil, errDecoderUnsupported
		}
		return parseSEC1(p.PrivateKey, curveByOID(coid))
	case alg.Equal(oidEd25519):
		var seed []byte
		if _, err := asn1.Unmarshal(p.PrivateKey, &seed); err != nil || len(seed) != ed25519.SeedSize {
			return nil, errDecoderUnsupported
		}
		return &asymKey{typ: "ed25519", private: true, edPriv: ed25519.NewKeyFromSeed(seed)}, nil
	case alg.Equal(oidX25519):
		var d []byte
		if _, err := asn1.Unmarshal(p.PrivateKey, &d); err != nil {
			return nil, errDecoderUnsupported
		}
		priv, err := ecdh.X25519().NewPrivateKey(d)
		if err != nil {
			return nil, err
		}
		return &asymKey{typ: "x25519", private: true, xPriv: priv}, nil
	case alg.Equal(oidMLKEM768), alg.Equal(oidMLKEM1024):
		typ := "ml-kem-768"
		if alg.Equal(oidMLKEM1024) {
			typ = "ml-kem-1024"
		}
		seed := mlSeed(p.PrivateKey, 64)
		if seed == nil {
			return nil, errDecoderUnsupported
		}
		pub, err := mlkemPubFromSeed(typ, seed)
		if err != nil {
			return nil, err
		}
		return &asymKey{typ: typ, private: true, mlkemSeed: seed, mlkemPub: pub}, nil
	case alg.Equal(oidMLDSA44), alg.Equal(oidMLDSA65), alg.Equal(oidMLDSA87):
		seed := mlSeed(p.PrivateKey, 32)
		if seed == nil {
			return nil, errDecoderUnsupported
		}
		typ := "ml-dsa-44"
		if alg.Equal(oidMLDSA65) {
			typ = "ml-dsa-65"
		} else if alg.Equal(oidMLDSA87) {
			typ = "ml-dsa-87"
		}
		sk, err := mldsa.NewPrivateKey(mldsaParams[typ](), seed)
		if err != nil {
			return nil, err
		}
		return &asymKey{typ: typ, private: true, mldsaPriv: sk}, nil
	}
	return nil, errDecoderUnsupported
}

// mlSeed extracts the seed from an ML-DSA/ML-KEM PKCS#8 private key in
// either "seed-only" ([0] IMPLICIT OCTET STRING) or "both" (SEQUENCE {
// seed, expanded }) form.
func mlSeed(b []byte, n int) []byte {
	if len(b) == 2+n && b[0] == 0x80 && int(b[1]) == n {
		return b[2:]
	}
	var both struct {
		Seed     []byte
		Expanded []byte
	}
	if _, err := asn1.Unmarshal(b, &both); err == nil && len(both.Seed) == n {
		return both.Seed
	}
	return nil
}

func parseSEC1(der []byte, c *ecCurve) (*asymKey, error) {
	var s sec1ASN
	if _, err := asn1.Unmarshal(der, &s); err != nil || s.Version != 1 {
		return nil, errDecoderUnsupported
	}
	if len(s.NamedCurveOID) > 0 {
		c = curveByOID(s.NamedCurveOID)
	}
	if c == nil {
		return nil, errDecoderUnsupported
	}
	ek, err := newECPrivate(c, s.PrivateKey)
	if err != nil {
		return nil, err
	}
	return &asymKey{typ: "ec", private: true, ec: ek}, nil
}

// ---- PKCS#8 encryption (PBES2) and legacy PEM encryption ------------------------

type pbes2Params struct {
	KDF pkix.AlgorithmIdentifier
	Enc pkix.AlgorithmIdentifier
}

type pbkdf2Params struct {
	Salt       []byte
	Iterations int
	KeyLength  int                      `asn1:"optional"`
	PRF        pkix.AlgorithmIdentifier `asn1:"optional"`
}

type encryptedPKCS8 struct {
	Algo pkix.AlgorithmIdentifier
	Data []byte
}

var pbeCiphers = map[string]struct {
	oid    asn1.ObjectIdentifier
	keyLen int
	des    bool
}{
	"aes-128-cbc":  {asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 2}, 16, false},
	"aes-192-cbc":  {asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 22}, 24, false},
	"aes-256-cbc":  {asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}, 32, false},
	"des-ede3-cbc": {asn1.ObjectIdentifier{1, 2, 840, 113549, 3, 7}, 24, true},
}

var prfOIDs = map[string]asn1.ObjectIdentifier{
	"sha1":   {1, 2, 840, 113549, 2, 7},
	"sha224": {1, 2, 840, 113549, 2, 8},
	"sha256": {1, 2, 840, 113549, 2, 9},
	"sha384": {1, 2, 840, 113549, 2, 10},
	"sha512": {1, 2, 840, 113549, 2, 11},
}

func normalizePBECipher(name string) string {
	n := strings.ToLower(name)
	switch n {
	case "aes128":
		return "aes-128-cbc"
	case "aes192":
		return "aes-192-cbc"
	case "aes256":
		return "aes-256-cbc"
	case "des3":
		return "des-ede3-cbc"
	}
	return n
}

func cbcEncrypt(block cipher.Block, iv, data []byte) []byte {
	bs := block.BlockSize()
	pad := bs - len(data)%bs
	p := append(append([]byte(nil), data...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(p))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, p)
	return out
}

func cbcDecrypt(block cipher.Block, iv, data []byte) ([]byte, bool) {
	bs := block.BlockSize()
	if len(data) == 0 || len(data)%bs != 0 {
		return nil, false
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > bs {
		return nil, false
	}
	for _, x := range out[len(out)-pad:] {
		if int(x) != pad {
			return nil, false
		}
	}
	return out[:len(out)-pad], true
}

func encryptPKCS8(der []byte, cipherName string, pass []byte) ([]byte, error) {
	spec, ok := pbeCiphers[normalizePBECipher(cipherName)]
	if !ok {
		return nil, errors.New("unknown cipher")
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, string(pass), salt, 2048, spec.keyLen)
	if err != nil {
		return nil, err
	}
	var block cipher.Block
	if spec.des {
		block, _ = des.NewTripleDESCipher(key)
	} else {
		block, _ = aes.NewCipher(key)
	}
	iv := make([]byte, block.BlockSize())
	rand.Read(iv)
	kdfParams, _ := asn1.Marshal(pbkdf2Params{Salt: salt, Iterations: 2048, PRF: pkix.AlgorithmIdentifier{Algorithm: prfOIDs["sha256"], Parameters: asn1.NullRawValue}})
	ivDER, _ := asn1.Marshal(iv)
	params, _ := asn1.Marshal(pbes2Params{
		KDF: pkix.AlgorithmIdentifier{Algorithm: oidPBKDF2, Parameters: asn1.RawValue{FullBytes: kdfParams}},
		Enc: pkix.AlgorithmIdentifier{Algorithm: spec.oid, Parameters: asn1.RawValue{FullBytes: ivDER}},
	})
	return asn1.Marshal(encryptedPKCS8{
		Algo: pkix.AlgorithmIdentifier{Algorithm: oidPBES2, Parameters: asn1.RawValue{FullBytes: params}},
		Data: cbcEncrypt(block, iv, der),
	})
}

var errBadDecrypt = errors.New("bad decrypt")
var errNeedPassphrase = errors.New("passphrase required")

// decryptPKCS8 returns (der, true, nil) for an EncryptedPrivateKeyInfo,
// (nil, false, nil) when der isn't one.
func decryptPKCS8(der []byte, pass []byte) ([]byte, bool, error) {
	var e encryptedPKCS8
	if rest, err := asn1.Unmarshal(der, &e); err != nil || len(rest) != 0 || !e.Algo.Algorithm.Equal(oidPBES2) {
		return nil, false, nil
	}
	if pass == nil {
		return nil, true, errNeedPassphrase
	}
	var p pbes2Params
	if _, err := asn1.Unmarshal(e.Algo.Parameters.FullBytes, &p); err != nil || !p.KDF.Algorithm.Equal(oidPBKDF2) {
		return nil, true, errDecoderUnsupported
	}
	var kp pbkdf2Params
	if _, err := asn1.Unmarshal(p.KDF.Parameters.FullBytes, &kp); err != nil {
		return nil, true, errDecoderUnsupported
	}
	prf := func() hash.Hash { return sha1.New() }
	if len(kp.PRF.Algorithm) > 0 {
		found := false
		for name, oid := range prfOIDs {
			if oid.Equal(kp.PRF.Algorithm) {
				prf = digestTable[name].newHash
				found = true
			}
		}
		if !found {
			return nil, true, errDecoderUnsupported
		}
	}
	var spec struct {
		keyLen int
		des    bool
	}
	found := false
	for _, s := range pbeCiphers {
		if s.oid.Equal(p.Enc.Algorithm) {
			spec.keyLen, spec.des, found = s.keyLen, s.des, true
		}
	}
	if !found {
		return nil, true, errDecoderUnsupported
	}
	var iv []byte
	if _, err := asn1.Unmarshal(p.Enc.Parameters.FullBytes, &iv); err != nil {
		return nil, true, errDecoderUnsupported
	}
	key, err := pbkdf2.Key(prf, string(pass), kp.Salt, kp.Iterations, spec.keyLen)
	if err != nil {
		return nil, true, err
	}
	var block cipher.Block
	if spec.des {
		block, _ = des.NewTripleDESCipher(key)
	} else {
		block, _ = aes.NewCipher(key)
	}
	if len(iv) != block.BlockSize() {
		return nil, true, errDecoderUnsupported
	}
	out, ok := cbcDecrypt(block, iv, e.Data)
	if !ok {
		return nil, true, errBadDecrypt
	}
	return out, true, nil
}

var legacyPEMCiphers = map[string]x509.PEMCipher{
	"aes-128-cbc": x509.PEMCipherAES128, "aes-192-cbc": x509.PEMCipherAES192, "aes-256-cbc": x509.PEMCipherAES256,
	"des-ede3-cbc": x509.PEMCipher3DES, "des-cbc": x509.PEMCipherDES,
}

// ---- key input ------------------------------------------------------------------------

func (b *cryptoBinding) keyDecodeErr(err error) error {
	switch {
	case err == errNeedPassphrase:
		return b.osslErr("07880109", "common libcrypto routines", "interrupted or cancelled")
	case err == errBadDecrypt:
		return b.osslErr("1C800064", "Provider routines", "bad decrypt")
	}
	return b.osslErr("1E08010C", "DECODER routines", "unsupported")
}

func parseKeyDER(der []byte, typ, kind string, pass []byte) (*asymKey, error) {
	switch typ {
	case "spki":
		return parseSPKI(der)
	case "pkcs1":
		if kind == "public" {
			if pub, err := x509.ParsePKCS1PublicKey(der); err == nil {
				return &asymKey{typ: "rsa", rsaPub: pub}, nil
			}
		}
		priv, err := x509.ParsePKCS1PrivateKey(der)
		if err != nil {
			return nil, errDecoderUnsupported
		}
		return &asymKey{typ: "rsa", private: true, rsaPriv: priv}, nil
	case "sec1":
		return parseSEC1(der, nil)
	case "pkcs8":
		if plain, enc, err := decryptPKCS8(der, pass); enc {
			if err != nil {
				return nil, err
			}
			der = plain
		}
		return parsePKCS8(der)
	}
	// Unknown container: try everything, as OpenSSL's decoders do.
	if kind == "public" {
		if k, err := parseSPKI(der); err == nil {
			return k, nil
		}
	}
	for _, t := range []string{"pkcs8", "pkcs1", "sec1"} {
		if k, err := parseKeyDER(der, t, kind, pass); err == nil {
			return k, nil
		} else if err == errNeedPassphrase || err == errBadDecrypt {
			return nil, err
		}
	}
	if kind == "public" {
		if pub, err := x509.ParsePKCS1PublicKey(der); err == nil {
			return &asymKey{typ: "rsa", rsaPub: pub}, nil
		}
	}
	return nil, errDecoderUnsupported
}

func parseKeyPEM(data []byte, kind string, pass []byte) (*asymKey, error) {
	rest := data
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			return nil, errDecoderUnsupported
		}
		der := blk.Bytes
		if x509.IsEncryptedPEMBlock(blk) { //nolint:staticcheck // legacy OpenSSL PEM encryption
			if pass == nil {
				return nil, errNeedPassphrase
			}
			d, err := x509.DecryptPEMBlock(blk, pass) //nolint:staticcheck
			if err != nil {
				return nil, errBadDecrypt
			}
			der = d
		}
		switch blk.Type {
		case "PUBLIC KEY":
			if kind == "private" {
				return nil, errDecoderUnsupported
			}
			return parseSPKI(der)
		case "RSA PUBLIC KEY":
			if kind == "private" {
				return nil, errDecoderUnsupported
			}
			pub, err := x509.ParsePKCS1PublicKey(der)
			if err != nil {
				return nil, errDecoderUnsupported
			}
			return &asymKey{typ: "rsa", rsaPub: pub}, nil
		case "CERTIFICATE":
			if kind == "private" {
				return nil, errDecoderUnsupported
			}
			cert, err := x509.ParseCertificate(der)
			if err != nil {
				return nil, errDecoderUnsupported
			}
			return parseSPKI(cert.RawSubjectPublicKeyInfo)
		case "PRIVATE KEY":
			return parsePKCS8(der)
		case "ENCRYPTED PRIVATE KEY":
			plain, _, err := decryptPKCS8(der, pass)
			if err != nil {
				return nil, err
			}
			return parsePKCS8(plain)
		case "RSA PRIVATE KEY":
			priv, err := x509.ParsePKCS1PrivateKey(der)
			if err != nil {
				return nil, errDecoderUnsupported
			}
			return &asymKey{typ: "rsa", private: true, rsaPriv: priv}, nil
		case "EC PRIVATE KEY":
			return parseSEC1(der, nil)
		}
		// other block types (EC PARAMETERS, ...) are skipped
	}
}

// ---- JWK ---------------------------------------------------------------------

type orderedJSON struct{ keys, vals []string }

func (o *orderedJSON) add(k, v string) { o.keys = append(o.keys, k); o.vals = append(o.vals, v) }

func (o *orderedJSON) String() string {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(o.vals[i])
		sb.Write(kb)
		sb.WriteByte(':')
		sb.Write(vb)
	}
	sb.WriteByte('}')
	return sb.String()
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func b64uInt(n *big.Int) string { return b64u(n.Bytes()) }

func (b *cryptoBinding) keyJWK(k *asymKey) (string, error) {
	o := &orderedJSON{}
	switch k.typ {
	case "rsa":
		pub := k.rsaPublic()
		o.add("kty", "RSA")
		o.add("n", b64uInt(pub.N))
		o.add("e", b64uInt(big.NewInt(int64(pub.E))))
		if k.private {
			p := k.rsaPriv
			p.Precompute()
			o.add("d", b64uInt(p.D))
			o.add("p", b64uInt(p.Primes[0]))
			o.add("q", b64uInt(p.Primes[1]))
			o.add("dp", b64uInt(p.Precomputed.Dp))
			o.add("dq", b64uInt(p.Precomputed.Dq))
			o.add("qi", b64uInt(p.Precomputed.Qinv))
		}
	case "ec":
		n := k.ec.curve.size
		o.add("kty", "EC")
		o.add("x", b64u(k.ec.pub[1:1+n]))
		o.add("y", b64u(k.ec.pub[1+n:]))
		o.add("crv", k.ec.curve.jwk)
		if k.private {
			o.add("d", b64u(k.ec.d))
		}
	case "ed25519", "x25519":
		crv := "Ed25519"
		if k.typ == "x25519" {
			crv = "X25519"
		}
		o.add("crv", crv)
		if k.private {
			if k.edPriv != nil {
				o.add("d", b64u(k.edPriv.Seed()))
			} else {
				o.add("d", b64u(k.xPriv.Bytes()))
			}
		}
		o.add("x", b64u(k.rawPublic()))
		o.add("kty", "OKP")
	case "ml-dsa-44", "ml-dsa-65", "ml-dsa-87", "ml-kem-768", "ml-kem-1024":
		if k.private {
			if k.mldsaPriv != nil {
				o.add("priv", b64u(k.mldsaPriv.Bytes()))
			} else {
				o.add("priv", b64u(k.mlkemSeed))
			}
		}
		o.add("kty", "AKP")
		o.add("alg", strings.ToUpper(k.typ))
		o.add("pub", b64u(k.rawPublic()))
	default:
		return "", b.err("ERR_CRYPTO_JWK_UNSUPPORTED_KEY_TYPE", "Unsupported JWK Key Type.")
	}
	return o.String(), nil
}

func (b *cryptoBinding) parseJWK(src string, kind string) (*asymKey, error) {
	var j map[string]any
	if err := json.Unmarshal([]byte(src), &j); err != nil {
		return nil, b.typeErr("ERR_CRYPTO_INVALID_JWK", "Invalid JWK format")
	}
	str := func(k string) (string, bool) { s, ok := j[k].(string); return s, ok }
	dec := func(k string) ([]byte, bool) {
		s, ok := str(k)
		if !ok {
			return nil, false
		}
		v, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
		return v, err == nil
	}
	kty, ok := str("kty")
	if !ok {
		return nil, b.typeErr("ERR_CRYPTO_INVALID_JWK", "Invalid JWK format")
	}
	_, hasD := j["d"]
	wantPriv := kind == "private"
	switch kty {
	case "RSA":
		bad := b.typeErr("ERR_CRYPTO_INVALID_JWK", "Invalid JWK RSA key")
		n, ok1 := dec("n")
		e, ok2 := dec("e")
		if !ok1 || !ok2 || len(e) > 4 {
			return nil, bad
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if !wantPriv {
			return &asymKey{typ: "rsa", rsaPub: pub}, nil
		}
		d, okd := dec("d")
		p, okp := dec("p")
		q, okq := dec("q")
		if !okd || !okp || !okq {
			return nil, bad
		}
		priv := &rsa.PrivateKey{PublicKey: *pub, D: new(big.Int).SetBytes(d), Primes: []*big.Int{new(big.Int).SetBytes(p), new(big.Int).SetBytes(q)}}
		if err := priv.Validate(); err != nil {
			return nil, bad
		}
		priv.Precompute()
		return &asymKey{typ: "rsa", private: true, rsaPriv: priv}, nil
	case "EC":
		bad := b.typeErr("ERR_CRYPTO_INVALID_JWK", "Invalid JWK EC key")
		crv, _ := str("crv")
		c := lookupCurve(crv)
		x, okx := dec("x")
		y, oky := dec("y")
		if c == nil || c.jwk != crv || !okx || !oky || len(x) != c.size || len(y) != c.size {
			return nil, bad
		}
		if wantPriv {
			if !hasD {
				return nil, bad
			}
			d, okd := dec("d")
			if !okd {
				return nil, bad
			}
			ek, err := newECPrivate(c, d)
			if err != nil || !bytes.Equal(ek.pub[1:], append(append([]byte{}, x...), y...)) {
				return nil, bad
			}
			return &asymKey{typ: "ec", private: true, ec: ek}, nil
		}
		ek, err := newECPublic(c, append(append([]byte{4}, x...), y...))
		if err != nil {
			return nil, bad
		}
		return &asymKey{typ: "ec", ec: ek}, nil
	case "OKP":
		crv, _ := str("crv")
		bad := b.typeErr("ERR_CRYPTO_INVALID_JWK", "Invalid JWK OKP key")
		x, okx := dec("x")
		if !okx {
			return nil, bad
		}
		switch crv {
		case "Ed25519":
			if wantPriv {
				d, okd := dec("d")
				if !okd || len(d) != 32 {
					return nil, bad
				}
				return &asymKey{typ: "ed25519", private: true, edPriv: ed25519.NewKeyFromSeed(d)}, nil
			}
			if len(x) != 32 {
				return nil, bad
			}
			return &asymKey{typ: "ed25519", edPub: ed25519.PublicKey(x)}, nil
		case "X25519":
			if wantPriv {
				d, okd := dec("d")
				if !okd {
					return nil, bad
				}
				pk, err := ecdh.X25519().NewPrivateKey(d)
				if err != nil {
					return nil, bad
				}
				return &asymKey{typ: "x25519", private: true, xPriv: pk}, nil
			}
			pk, err := ecdh.X25519().NewPublicKey(x)
			if err != nil {
				return nil, bad
			}
			return &asymKey{typ: "x25519", xPub: pk}, nil
		}
		return nil, b.typeErr("ERR_CRYPTO_INVALID_JWK", "Unsupported JWK OKP curve: "+crv)
	case "AKP":
		alg, _ := str("alg")
		typ := strings.ToLower(alg)
		bad := b.typeErr("ERR_CRYPTO_INVALID_JWK", "Invalid JWK AKP key")
		pub, okp := dec("pub")
		if !okp {
			return nil, bad
		}
		if params, ok := mldsaParams[typ]; ok {
			if wantPriv {
				seed, ok := dec("priv")
				if !ok {
					return nil, bad
				}
				sk, err := mldsa.NewPrivateKey(params(), seed)
				if err != nil || !bytes.Equal(sk.PublicKey().Bytes(), pub) {
					return nil, bad
				}
				return &asymKey{typ: typ, private: true, mldsaPriv: sk}, nil
			}
			pk, err := mldsa.NewPublicKey(params(), pub)
			if err != nil {
				return nil, bad
			}
			return &asymKey{typ: typ, mldsaPub: pk}, nil
		}
		if typ == "ml-kem-768" || typ == "ml-kem-1024" {
			if wantPriv {
				seed, ok := dec("priv")
				if !ok {
					return nil, bad
				}
				p, err := mlkemPubFromSeed(typ, seed)
				if err != nil || !bytes.Equal(p, pub) {
					return nil, bad
				}
				return &asymKey{typ: typ, private: true, mlkemSeed: seed, mlkemPub: p}, nil
			}
			if checkMLKEMPub(typ, pub) != nil {
				return nil, bad
			}
			return &asymKey{typ: typ, mlkemPub: pub}, nil
		}
		return nil, bad
	}
	return nil, b.typeErr("ERR_CRYPTO_INVALID_JWK", kty+" is not a supported JWK key type")
}

// ---- key info / export ------------------------------------------------------------

func (k *asymKey) info() map[string]any {
	details := map[string]any{}
	switch k.typ {
	case "rsa", "rsa-pss":
		pub := k.rsaPublic()
		details["modulusLength"] = pub.N.BitLen()
		details["publicExponent"] = fmt.Sprint(pub.E)
		if k.pss != nil {
			details["hashAlgorithm"] = k.pss.hash
			details["mgf1HashAlgorithm"] = k.pss.mgf1
			details["saltLength"] = k.pss.saltLen
		}
	case "ec":
		details["namedCurve"] = k.ec.curve.name
	}
	return map[string]any{"asymmetricKeyType": k.typ, "details": details}
}

func (b *cryptoBinding) exportKey(k *asymKey, format, typ, cipherName string, pass []byte) (vm.Value, error) {
	if format == "jwk" {
		s, err := b.keyJWK(k)
		if err != nil {
			return vm.Undefined, err
		}
		return vm.NewString(s), nil
	}
	var der []byte
	pemType := ""
	switch {
	case !k.private && typ == "spki":
		der, pemType = k.spkiDER(), "PUBLIC KEY"
	case !k.private && typ == "pkcs1":
		der, pemType = x509.MarshalPKCS1PublicKey(k.rsaPublic()), "RSA PUBLIC KEY"
	case k.private && typ == "pkcs8":
		der, pemType = k.pkcs8DER(), "PRIVATE KEY"
		if cipherName != "" {
			enc, err := encryptPKCS8(der, cipherName, pass)
			if err != nil {
				return vm.Undefined, b.typeErr("ERR_CRYPTO_UNKNOWN_CIPHER", "Unknown cipher")
			}
			der, pemType = enc, "ENCRYPTED PRIVATE KEY"
		}
	case k.private && typ == "pkcs1":
		der, pemType = x509.MarshalPKCS1PrivateKey(k.rsaPriv), "RSA PRIVATE KEY"
	case k.private && typ == "sec1":
		der, pemType = k.sec1DER(true), "EC PRIVATE KEY"
	default:
		return vm.Undefined, b.typeErr("ERR_INVALID_ARG_VALUE", "The property 'options.type' is invalid. Received '"+typ+"'")
	}
	if format == "der" {
		return b.buf(der), nil
	}
	blk := &pem.Block{Type: pemType, Bytes: der}
	if cipherName != "" && (typ == "pkcs1" || typ == "sec1") {
		c, ok := legacyPEMCiphers[normalizePBECipher(cipherName)]
		if !ok {
			return vm.Undefined, b.typeErr("ERR_CRYPTO_UNKNOWN_CIPHER", "Unknown cipher")
		}
		var err error
		blk, err = x509.EncryptPEMBlock(rand.Reader, pemType, der, pass, c) //nolint:staticcheck // node still writes these
		if err != nil {
			return vm.Undefined, err
		}
	}
	return vm.NewString(string(pem.EncodeToMemory(blk))), nil
}

func keysEqual(a, b *asymKey) bool {
	if a.private != b.private || a.typ != b.typ {
		return false
	}
	if a.private {
		return bytes.Equal(a.pkcs8DER(), b.pkcs8DER())
	}
	return bytes.Equal(a.spkiDER(), b.spkiDER())
}

// ---- generation ------------------------------------------------------------------

type keyGenOptions struct {
	ModulusLength     int    `json:"modulusLength"`
	PublicExponent    int64  `json:"publicExponent"`
	HashAlgorithm     string `json:"hashAlgorithm"`
	MGF1HashAlgorithm string `json:"mgf1HashAlgorithm"`
	SaltLength        *int   `json:"saltLength"`
	NamedCurve        string `json:"namedCurve"`
}

func generateRSA(bits int, e int64) (*rsa.PrivateKey, error) {
	if e == 0 || e == 65537 {
		return rsa.GenerateKey(rand.Reader, bits)
	}
	E := big.NewInt(e)
	one := big.NewInt(1)
	for {
		p, err := rand.Prime(rand.Reader, bits-bits/2)
		if err != nil {
			return nil, err
		}
		q, err := rand.Prime(rand.Reader, bits/2)
		if err != nil {
			return nil, err
		}
		if p.Cmp(q) == 0 {
			continue
		}
		n := new(big.Int).Mul(p, q)
		if n.BitLen() != bits {
			continue
		}
		pm, qm := new(big.Int).Sub(p, one), new(big.Int).Sub(q, one)
		phi := new(big.Int).Mul(pm, qm)
		d := new(big.Int).ModInverse(E, phi)
		if d == nil {
			continue
		}
		priv := &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: n, E: int(e)}, D: d, Primes: []*big.Int{p, q}}
		priv.Precompute()
		return priv, nil
	}
}

func generateAsymKey(typ string, o keyGenOptions) (*asymKey, error) {
	switch typ {
	case "rsa", "rsa-pss":
		priv, err := generateRSA(o.ModulusLength, o.PublicExponent)
		if err != nil {
			return nil, err
		}
		k := &asymKey{typ: typ, private: true, rsaPriv: priv}
		if typ == "rsa-pss" && (o.HashAlgorithm != "" || o.MGF1HashAlgorithm != "" || o.SaltLength != nil) {
			p := &pssParams{hash: "sha1", saltLen: -1}
			if o.HashAlgorithm != "" {
				p.hash = digestTable[strings.ToLower(o.HashAlgorithm)].name
			}
			p.mgf1 = p.hash
			if o.MGF1HashAlgorithm != "" {
				p.mgf1 = digestTable[strings.ToLower(o.MGF1HashAlgorithm)].name
			}
			if o.SaltLength != nil {
				p.saltLen = *o.SaltLength
			} else {
				p.saltLen = digestTable[p.hash].size
			}
			k.pss = p
		}
		return k, nil
	case "ec":
		c := lookupCurve(o.NamedCurve)
		var d []byte
		if c.ecdh == nil {
			pk, err := secp256k1.GeneratePrivateKey()
			if err != nil {
				return nil, err
			}
			d = pk.Serialize()
		} else {
			pk, err := ecdsa.GenerateKey(c.curve, rand.Reader)
			if err != nil {
				return nil, err
			}
			if d, err = pk.Bytes(); err != nil {
				return nil, err
			}
		}
		ek, err := newECPrivate(c, d)
		if err != nil {
			return nil, err
		}
		return &asymKey{typ: "ec", private: true, ec: ek}, nil
	case "ed25519":
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		return &asymKey{typ: typ, private: true, edPriv: priv}, nil
	case "x25519":
		priv, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		return &asymKey{typ: typ, private: true, xPriv: priv}, nil
	case "ml-dsa-44", "ml-dsa-65", "ml-dsa-87":
		sk, err := mldsa.GenerateKey(mldsaParams[typ]())
		if err != nil {
			return nil, err
		}
		return &asymKey{typ: typ, private: true, mldsaPriv: sk}, nil
	case "ml-kem-768", "ml-kem-1024":
		seed := make([]byte, 64)
		rand.Read(seed)
		pub, err := mlkemPubFromSeed(typ, seed)
		if err != nil {
			return nil, err
		}
		return &asymKey{typ: typ, private: true, mlkemSeed: seed, mlkemPub: pub}, nil
	}
	return nil, fmt.Errorf("unsupported key type %s", typ)
}

// ---- binding ----------------------------------------------------------------------

func (b *cryptoBinding) keyResult(k *asymKey) vm.Value {
	obj := vm.NewObject(b.vm.ObjectPrototype).AsPlainObject()
	t := "public"
	if k.private {
		t = "private"
	}
	obj.SetOwn("type", vm.NewString(t))
	obj.SetOwn("handle", b.handle(k))
	return vm.NewValueFromPlainObject(obj)
}

func (b *cryptoBinding) installKeys() {
	b.fn("keyParse", func(args []vm.Value) (vm.Value, error) {
		data := b.bytesArg(args, 0)
		format, typ, kind := strArg(args, 1), strArg(args, 2), strArg(args, 3)
		var pass []byte
		if v := argAt(args, 4); !v.IsUndefined() && !v.IsNull() {
			pass = b.bytesArg(args, 4)
		}
		var k *asymKey
		var err error
		if format == "pem" {
			k, err = parseKeyPEM(data, kind, pass)
			if err == errDecoderUnsupported && !bytes.Contains(data, []byte("-----BEGIN")) {
				// node also accepts DER handed in with the default format
				k, err = parseKeyDER(data, "", kind, pass)
			}
		} else {
			k, err = parseKeyDER(data, typ, kind, pass)
		}
		if err != nil {
			return vm.Undefined, b.keyDecodeErr(err)
		}
		if kind == "private" && !k.private {
			return vm.Undefined, b.keyDecodeErr(errDecoderUnsupported)
		}
		return b.keyResult(k), nil
	})
	b.fn("keyParseJWK", func(args []vm.Value) (vm.Value, error) {
		k, err := b.parseJWK(strArg(args, 0), strArg(args, 1))
		if err != nil {
			return vm.Undefined, err
		}
		return b.keyResult(k), nil
	})
	b.fn("keyInfo", func(args []vm.Value) (vm.Value, error) {
		k, ok := handleOf[*asymKey](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid key handle")
		}
		return b.jsonValue(k.info())
	})
	b.fn("keyExport", func(args []vm.Value) (vm.Value, error) {
		k, ok := handleOf[*asymKey](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid key handle")
		}
		var pass []byte
		if v := argAt(args, 4); !v.IsUndefined() && !v.IsNull() {
			pass = b.bytesArg(args, 4)
		}
		return b.exportKey(k, strArg(args, 1), strArg(args, 2), strArg(args, 3), pass)
	})
	b.fn("keyToPublic", func(args []vm.Value) (vm.Value, error) {
		k, ok := handleOf[*asymKey](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid key handle")
		}
		return b.handle(k.public()), nil
	})
	b.fn("keyEquals", func(args []vm.Value) (vm.Value, error) {
		a, ok1 := handleOf[*asymKey](args, 0)
		c, ok2 := handleOf[*asymKey](args, 1)
		return vm.BooleanValue(ok1 && ok2 && keysEqual(a, c)), nil
	})
	// keyRaw(handle, what) exposes raw material webcrypto needs:
	// "public" (raw public key / uncompressed point), "seed".
	b.fn("keyRaw", func(args []vm.Value) (vm.Value, error) {
		k, ok := handleOf[*asymKey](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid key handle")
		}
		switch strArg(args, 1) {
		case "public":
			if k.typ == "rsa" || k.typ == "rsa-pss" {
				return vm.Undefined, errors.New("no raw form")
			}
			return b.buf(k.rawPublic()), nil
		case "seed":
			switch {
			case k.mldsaPriv != nil:
				return b.buf(k.mldsaPriv.Bytes()), nil
			case k.mlkemSeed != nil:
				return b.buf(k.mlkemSeed), nil
			}
		}
		return vm.Undefined, nil
	})
	// keyFromRaw(type, kind, bytes, namedCurve) builds keys from raw
	// material: EC points/scalars, OKP and ML-* public keys or seeds.
	b.fn("keyFromRaw", func(args []vm.Value) (vm.Value, error) {
		typ, kind := strArg(args, 0), strArg(args, 1)
		data := b.bytesArg(args, 2)
		var k *asymKey
		var err error
		switch typ {
		case "ec":
			c := lookupCurve(strArg(args, 3))
			if c == nil {
				return vm.Undefined, errors.New("bad curve")
			}
			var ek *ecKey
			if kind == "private" {
				ek, err = newECPrivate(c, data)
			} else {
				ek, err = newECPublic(c, data)
			}
			k = &asymKey{typ: "ec", private: kind == "private", ec: ek}
		case "ed25519":
			if kind == "private" && len(data) == 32 {
				k = &asymKey{typ: typ, private: true, edPriv: ed25519.NewKeyFromSeed(data)}
			} else if len(data) == 32 {
				k = &asymKey{typ: typ, edPub: ed25519.PublicKey(data)}
			} else {
				err = errors.New("bad key")
			}
		case "x25519":
			if kind == "private" {
				var p *ecdh.PrivateKey
				p, err = ecdh.X25519().NewPrivateKey(data)
				k = &asymKey{typ: typ, private: true, xPriv: p}
			} else {
				var p *ecdh.PublicKey
				p, err = ecdh.X25519().NewPublicKey(data)
				k = &asymKey{typ: typ, xPub: p}
			}
		case "ml-dsa-44", "ml-dsa-65", "ml-dsa-87":
			if kind == "private" {
				var sk *mldsa.PrivateKey
				sk, err = mldsa.NewPrivateKey(mldsaParams[typ](), data)
				k = &asymKey{typ: typ, private: true, mldsaPriv: sk}
			} else {
				var pk *mldsa.PublicKey
				pk, err = mldsa.NewPublicKey(mldsaParams[typ](), data)
				k = &asymKey{typ: typ, mldsaPub: pk}
			}
		case "ml-kem-768", "ml-kem-1024":
			if kind == "private" {
				var pub []byte
				pub, err = mlkemPubFromSeed(typ, data)
				k = &asymKey{typ: typ, private: true, mlkemSeed: data, mlkemPub: pub}
			} else {
				err = checkMLKEMPub(typ, data)
				k = &asymKey{typ: typ, mlkemPub: data}
			}
		default:
			err = errors.New("unsupported")
		}
		if err != nil {
			return vm.Undefined, err
		}
		return b.keyResult(k), nil
	})
	b.fn("keyGenerate", func(args []vm.Value) (vm.Value, error) {
		typ := strArg(args, 0)
		var o keyGenOptions
		if s := strArg(args, 1); s != "" {
			_ = json.Unmarshal([]byte(s), &o)
		}
		return b.async(argAt(args, 2), func() (func() vm.Value, error) {
			k, err := generateAsymKey(typ, o)
			if err != nil {
				return nil, err
			}
			return func() vm.Value {
				arr := vm.NewArray()
				arr.AsArray().Append(b.keyResult(k.public()))
				arr.AsArray().Append(b.keyResult(k))
				return arr
			}, nil
		})
	})
	b.fn("getCurves", func(args []vm.Value) (vm.Value, error) {
		arr := vm.NewArray()
		for _, n := range []string{"prime256v1", "secp256k1", "secp384r1", "secp521r1"} {
			arr.AsArray().Append(vm.NewString(n))
		}
		return arr, nil
	})
}
