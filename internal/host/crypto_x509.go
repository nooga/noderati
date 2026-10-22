package host

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"

	"github.com/nooga/paserati/pkg/vm"
)

// X509Certificate support: crypto/x509 parses, and the string forms
// mirror what node builds from OpenSSL's printers.

type x509Cert struct {
	c *x509.Certificate
}

var x509AttrNames = map[string]string{
	"2.5.4.3": "CN", "2.5.4.6": "C", "2.5.4.7": "L", "2.5.4.8": "ST", "2.5.4.10": "O",
	"2.5.4.11": "OU", "2.5.4.5": "serialNumber", "2.5.4.9": "street", "2.5.4.12": "title",
	"2.5.4.4": "SN", "2.5.4.42": "GN", "2.5.4.43": "initials", "2.5.4.44": "generationQualifier",
	"2.5.4.46": "dnQualifier", "2.5.4.65": "pseudonym", "2.5.4.17": "postalCode",
	"2.5.4.15": "businessCategory", "2.5.4.97": "organizationIdentifier",
	"1.2.840.113549.1.9.1":         "emailAddress",
	"0.9.2342.19200300.100.1.25":   "DC",
	"0.9.2342.19200300.100.1.1":    "UID",
	"1.3.6.1.4.1.311.60.2.1.3":     "jurisdictionC",
	"1.3.6.1.4.1.311.60.2.1.2":     "jurisdictionST",
	"1.3.6.1.4.1.311.60.2.1.1":     "jurisdictionL",
}

var x509SigAlgNames = map[string]string{
	"1.2.840.113549.1.1.4":    "md5WithRSAEncryption",
	"1.2.840.113549.1.1.5":    "sha1WithRSAEncryption",
	"1.2.840.113549.1.1.10":   "rsassaPss",
	"1.2.840.113549.1.1.11":   "sha256WithRSAEncryption",
	"1.2.840.113549.1.1.12":   "sha384WithRSAEncryption",
	"1.2.840.113549.1.1.13":   "sha512WithRSAEncryption",
	"1.2.840.113549.1.1.14":   "sha224WithRSAEncryption",
	"1.2.840.113549.1.1.15":   "sha512-224WithRSAEncryption",
	"1.2.840.113549.1.1.16":   "sha512-256WithRSAEncryption",
	"1.2.840.10045.4.1":       "ecdsa-with-SHA1",
	"1.2.840.10045.4.3.1":     "ecdsa-with-SHA224",
	"1.2.840.10045.4.3.2":     "ecdsa-with-SHA256",
	"1.2.840.10045.4.3.3":     "ecdsa-with-SHA384",
	"1.2.840.10045.4.3.4":     "ecdsa-with-SHA512",
	"2.16.840.1.101.3.4.3.9":  "ecdsa_with_SHA3-224",
	"2.16.840.1.101.3.4.3.10": "ecdsa_with_SHA3-256",
	"2.16.840.1.101.3.4.3.11": "ecdsa_with_SHA3-384",
	"2.16.840.1.101.3.4.3.12": "ecdsa_with_SHA3-512",
	"2.16.840.1.101.3.4.3.13": "RSA-SHA3-224",
	"2.16.840.1.101.3.4.3.14": "RSA-SHA3-256",
	"2.16.840.1.101.3.4.3.15": "RSA-SHA3-384",
	"2.16.840.1.101.3.4.3.16": "RSA-SHA3-512",
	"2.16.840.1.101.3.4.3.17": "ML-DSA-44",
	"2.16.840.1.101.3.4.3.18": "ML-DSA-65",
	"2.16.840.1.101.3.4.3.19": "ML-DSA-87",
	"1.3.101.112":             "ED25519",
	"1.3.101.113":             "ED448",
	"1.2.840.10040.4.3":       "dsaWithSHA1",
	"2.16.840.1.101.3.4.3.2":  "dsa_with_SHA256",
}

// x509SigDigest maps signature OIDs to (digest, kind) for verify.
var x509SigDigest = map[string][2]string{
	"1.2.840.113549.1.1.4": {"md5", "rsa"}, "1.2.840.113549.1.1.5": {"sha1", "rsa"},
	"1.2.840.113549.1.1.11": {"sha256", "rsa"}, "1.2.840.113549.1.1.12": {"sha384", "rsa"},
	"1.2.840.113549.1.1.13": {"sha512", "rsa"}, "1.2.840.113549.1.1.14": {"sha224", "rsa"},
	"1.2.840.113549.1.1.15": {"sha512-224", "rsa"}, "1.2.840.113549.1.1.16": {"sha512-256", "rsa"},
	"2.16.840.1.101.3.4.3.13": {"sha3-224", "rsa"}, "2.16.840.1.101.3.4.3.14": {"sha3-256", "rsa"},
	"2.16.840.1.101.3.4.3.15": {"sha3-384", "rsa"}, "2.16.840.1.101.3.4.3.16": {"sha3-512", "rsa"},
	"1.2.840.10045.4.1": {"sha1", "ec"}, "1.2.840.10045.4.3.1": {"sha224", "ec"},
	"1.2.840.10045.4.3.2": {"sha256", "ec"}, "1.2.840.10045.4.3.3": {"sha384", "ec"},
	"1.2.840.10045.4.3.4": {"sha512", "ec"},
	"2.16.840.1.101.3.4.3.9": {"sha3-224", "ec"}, "2.16.840.1.101.3.4.3.10": {"sha3-256", "ec"},
	"2.16.840.1.101.3.4.3.11": {"sha3-384", "ec"}, "2.16.840.1.101.3.4.3.12": {"sha3-512", "ec"},
	"1.3.101.112":             {"", "ed25519"},
	"2.16.840.1.101.3.4.3.17": {"", "ml-dsa-44"}, "2.16.840.1.101.3.4.3.18": {"", "ml-dsa-65"},
	"2.16.840.1.101.3.4.3.19": {"", "ml-dsa-87"},
}

var (
	oidExtSAN = asn1.ObjectIdentifier{2, 5, 29, 17}
	oidExtEKU = asn1.ObjectIdentifier{2, 5, 29, 37}
	oidExtKU  = asn1.ObjectIdentifier{2, 5, 29, 15}
	oidExtAIA = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 1}
	oidEmail  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}
	oidCN     = asn1.ObjectIdentifier{2, 5, 4, 3}
)


func attrName(oid asn1.ObjectIdentifier) string {
	if n, ok := x509AttrNames[oid.String()]; ok {
		return n
	}
	return oid.String()
}

func attrValue(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case []byte:
		return string(s), true
	}
	return fmt.Sprint(v), false
}

// esc2253 escapes like OpenSSL's ASN1_STRFLGS_ESC_2253 | ESC_CTRL.
func esc2253(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case strings.IndexByte(",+\"\\<>;", c) >= 0,
			i == 0 && (c == '#' || c == ' '),
			i == len(s)-1 && c == ' ':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&sb, "\\%02X", c)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

func parseRDN(raw []byte) pkix.RDNSequence {
	var seq pkix.RDNSequence
	asn1.Unmarshal(raw, &seq)
	return seq
}

// nameMultiline renders a Name like node's subject/issuer getters.
func nameMultiline(raw []byte) string {
	var lines []string
	for _, rdn := range parseRDN(raw) {
		parts := make([]string, 0, len(rdn))
		for _, atv := range rdn {
			v, _ := attrValue(atv.Value)
			parts = append(parts, attrName(atv.Type)+"="+esc2253(v))
		}
		lines = append(lines, strings.Join(parts, " + "))
	}
	return strings.Join(lines, "\n")
}

// nameLegacy returns [key, value] pairs for toLegacyObject.
func nameLegacy(raw []byte) [][2]string {
	var out [][2]string
	for _, rdn := range parseRDN(raw) {
		for _, atv := range rdn {
			v, _ := attrValue(atv.Value)
			out = append(out, [2]string{attrName(atv.Type), v})
		}
	}
	return out
}

func nameAttr(raw []byte, oid asn1.ObjectIdentifier) []string {
	var out []string
	for _, rdn := range parseRDN(raw) {
		for _, atv := range rdn {
			if atv.Type.Equal(oid) {
				v, _ := attrValue(atv.Value)
				out = append(out, v)
			}
		}
	}
	return out
}

// altNameSafe mirrors node's IsSafeAltName.
func altNameSafe(s string, utf8 bool) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\\', ',', '\'':
			return false
		}
		if utf8 {
			if c < ' ' || c == 0x7f {
				return false
			}
		} else if c < ' ' || c > '~' {
			return false
		}
	}
	return true
}

func altNameQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case c == ',' || c == '\'' || c < ' ' || c > '~':
			fmt.Fprintf(&sb, "\\u%04x", c)
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func altNameValue(s string, utf8 bool) string {
	if altNameSafe(s, utf8) {
		return s
	}
	return altNameQuote(s)
}

type generalName struct {
	tag   int
	bytes []byte
	full  []byte
}

func parseGeneralNames(der []byte) []generalName {
	var seq asn1.RawValue
	if _, err := asn1.Unmarshal(der, &seq); err != nil {
		return nil
	}
	var out []generalName
	rest := seq.Bytes
	for len(rest) > 0 {
		var v asn1.RawValue
		var err error
		rest, err = asn1.Unmarshal(rest, &v)
		if err != nil {
			break
		}
		out = append(out, generalName{tag: v.Tag, bytes: v.Bytes, full: v.FullBytes})
	}
	return out
}

func ipString(b []byte) string {
	switch len(b) {
	case 4:
		return net.IP(b).String()
	case 16:
		parts := make([]string, 8)
		for i := range parts {
			parts[i] = fmt.Sprintf("%X", uint16(b[2*i])<<8|uint16(b[2*i+1]))
		}
		return strings.Join(parts, ":")
	}
	return "<invalid>"
}

func printGeneralName(g generalName) string {
	switch g.tag {
	case 0:
		return "othername:<unsupported>"
	case 1:
		return "email:" + altNameValue(string(g.bytes), false)
	case 2:
		return "DNS:" + altNameValue(string(g.bytes), false)
	case 4:
		var parts []string
		seq := parseRDN(g.bytes)
		for i := len(seq) - 1; i >= 0; i-- {
			for _, atv := range seq[i] {
				v, _ := attrValue(atv.Value)
				parts = append(parts, attrName(atv.Type)+"="+esc2253(v))
			}
		}
		return "DirName:" + altNameValue(strings.Join(parts, ","), true)
	case 6:
		return "URI:" + altNameValue(string(g.bytes), false)
	case 7:
		return "IP Address:" + ipString(g.bytes)
	case 8:
		var oid asn1.ObjectIdentifier
		full := append([]byte{0x06}, g.full[1:]...)
		if _, err := asn1.Unmarshal(full, &oid); err == nil {
			return "Registered ID:" + oid.String()
		}
		return "Registered ID:<invalid>"
	}
	return "<unsupported>"
}

func findExt(c *x509.Certificate, oid asn1.ObjectIdentifier) []byte {
	for _, e := range c.Extensions {
		if e.Id.Equal(oid) {
			return e.Value
		}
	}
	return nil
}

func (x *x509Cert) altNames() []generalName {
	if v := findExt(x.c, oidExtSAN); v != nil {
		return parseGeneralNames(v)
	}
	return nil
}

func (x *x509Cert) subjectAltName() *string {
	v := findExt(x.c, oidExtSAN)
	if v == nil {
		return nil
	}
	var parts []string
	for _, g := range parseGeneralNames(v) {
		parts = append(parts, printGeneralName(g))
	}
	s := strings.Join(parts, ", ")
	return &s
}

type accessDescription struct {
	Method   asn1.ObjectIdentifier
	Location asn1.RawValue
}

func (x *x509Cert) infoAccess() (*string, [][2]string) {
	v := findExt(x.c, oidExtAIA)
	if v == nil {
		return nil, nil
	}
	var ads []accessDescription
	asn1.Unmarshal(v, &ads)
	var lines []string
	var legacy [][2]string
	for _, ad := range ads {
		m := ad.Method.String()
		switch m {
		case "1.3.6.1.5.5.7.48.1":
			m = "OCSP"
		case "1.3.6.1.5.5.7.48.2":
			m = "CA Issuers"
		}
		gn := printGeneralName(generalName{tag: ad.Location.Tag, bytes: ad.Location.Bytes, full: ad.Location.FullBytes})
		line := m + " - " + gn
		lines = append(lines, line)
		if i := strings.IndexByte(line, ':'); i >= 0 {
			legacy = append(legacy, [2]string{line[:i], line[i+1:]})
		}
	}
	s := strings.Join(lines, "\n")
	return &s, legacy
}

func (x *x509Cert) extKeyUsage() []string {
	v := findExt(x.c, oidExtEKU)
	if v == nil {
		return nil
	}
	var oids []asn1.ObjectIdentifier
	asn1.Unmarshal(v, &oids)
	out := make([]string, 0, len(oids))
	for _, o := range oids {
		out = append(out, o.String())
	}
	return out
}

func colonHex(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = fmt.Sprintf("%02X", c)
	}
	return strings.Join(parts, ":")
}

func asn1TimeString(t interface{ Format(string) string }) string {
	return t.Format("Jan _2 15:04:05 2006 GMT")
}

type certOuter struct {
	TBS    asn1.RawValue
	SigAlg pkix.AlgorithmIdentifier
	Sig    asn1.BitString
}

func (x *x509Cert) sigAlg() pkix.AlgorithmIdentifier {
	var o certOuter
	asn1.Unmarshal(x.c.Raw, &o)
	return o.SigAlg
}

func (x *x509Cert) info() map[string]any {
	c := x.c
	m := map[string]any{}
	m["subject"] = nameMultiline(c.RawSubject)
	m["issuer"] = nameMultiline(c.RawIssuer)
	m["subjectLegacy"] = nameLegacy(c.RawSubject)
	m["issuerLegacy"] = nameLegacy(c.RawIssuer)
	m["subjectAltName"] = x.subjectAltName()
	ia, iaLegacy := x.infoAccess()
	m["infoAccess"] = ia
	m["infoAccessLegacy"] = iaLegacy
	nb, na := c.NotBefore.UTC(), c.NotAfter.UTC()
	m["validFrom"] = asn1TimeString(nb)
	m["validTo"] = asn1TimeString(na)
	m["validFromMs"] = nb.UnixMilli()
	m["validToMs"] = na.UnixMilli()
	s1 := sha1.Sum(c.Raw)
	s256 := sha256.Sum256(c.Raw)
	s512 := sha512.Sum512(c.Raw)
	m["fingerprint"] = colonHex(s1[:])
	m["fingerprint256"] = colonHex(s256[:])
	m["fingerprint512"] = colonHex(s512[:])
	if eku := x.extKeyUsage(); eku != nil {
		m["keyUsage"] = eku
	}
	serial := "0"
	if c.SerialNumber != nil && c.SerialNumber.Sign() != 0 {
		serial = fmt.Sprintf("%X", new(big.Int).Abs(c.SerialNumber).Bytes())
		if c.SerialNumber.Sign() < 0 {
			serial = "-" + serial
		}
	}
	m["serialNumber"] = serial
	m["ca"] = c.BasicConstraintsValid && c.IsCA
	alg := x.sigAlg()
	m["signatureAlgorithmOid"] = alg.Algorithm.String()
	if n, ok := x509SigAlgNames[alg.Algorithm.String()]; ok {
		m["signatureAlgorithm"] = n
	}
	m["pem"] = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
	if k, err := parseSPKI(c.RawSubjectPublicKeyInfo); err == nil {
		switch {
		case k.rsaPub != nil:
			n := k.rsaPub.N
			m["modulus"] = fmt.Sprintf("%X", n.Bytes())
			m["exponent"] = fmt.Sprintf("0x%x", k.rsaPub.E)
			m["bits"] = n.BitLen()
		case k.ec != nil:
			m["bits"] = k.ec.curve.curve.Params().BitSize
			m["asn1Curve"] = k.ec.curve.name
			if strings.HasPrefix(k.ec.curve.jwk, "P-") {
				m["nistCurve"] = k.ec.curve.jwk
			}
		}
	}
	return m
}

// legacyPubkey is toLegacyObject().pubkey: the SPKI for RSA keys, the
// uncompressed point for EC keys.
func (x *x509Cert) legacyPubkey() []byte {
	k, err := parseSPKI(x.c.RawSubjectPublicKeyInfo)
	if err != nil {
		return nil
	}
	switch {
	case k.rsaPub != nil:
		return k.spkiDER()
	case k.ec != nil:
		return k.ec.pub
	}
	return nil
}

func hostLabelsMatch(pattern, host string, wildcards, partial, multi bool) bool {
	pattern = strings.ToLower(strings.TrimSuffix(pattern, "."))
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if pattern == host {
		return true
	}
	if !wildcards {
		return false
	}
	star := strings.IndexByte(pattern, '*')
	if star < 0 || strings.Count(pattern, "*") != 1 {
		return false
	}
	pdot := strings.IndexByte(pattern, '.')
	if pdot < 0 || star > pdot {
		return false // wildcard only in the leftmost label
	}
	suffix := pattern[pdot:]
	if strings.Count(suffix, ".") < 2 {
		return false // need at least two labels after the wildcard
	}
	label := pattern[:pdot]
	if label != "*" {
		if !partial || strings.HasPrefix(label, "xn--") {
			return false
		}
	}
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	hl := host[:len(host)-len(suffix)]
	if hl == "" || (!multi && strings.Contains(hl, ".")) {
		return false
	}
	pre, post := label[:star], label[star+1:]
	return len(hl) >= len(pre)+len(post) && strings.HasPrefix(hl, pre) && strings.HasSuffix(hl, post)
}

func (x *x509Cert) checkHost(name, subject string, wildcards, partial, multi bool) *string {
	sawDNS := false
	for _, g := range x.altNames() {
		if g.tag != 2 {
			continue
		}
		sawDNS = true
		if hostLabelsMatch(string(g.bytes), name, wildcards, partial, multi) {
			s := string(g.bytes)
			return &s
		}
	}
	if subject == "always" || (subject == "default" && !sawDNS) {
		for _, cn := range nameAttr(x.c.RawSubject, oidCN) {
			if hostLabelsMatch(cn, name, wildcards, partial, multi) {
				return &cn
			}
		}
	}
	return nil
}

func emailMatch(a, b string) bool {
	ia, ib := strings.LastIndexByte(a, '@'), strings.LastIndexByte(b, '@')
	if ia < 0 || ib < 0 {
		return strings.EqualFold(a, b)
	}
	return a[:ia] == b[:ib] && strings.EqualFold(a[ia:], b[ib:])
}

func (x *x509Cert) checkEmail(email, subject string) bool {
	saw := false
	for _, g := range x.altNames() {
		if g.tag != 1 {
			continue
		}
		saw = true
		if emailMatch(string(g.bytes), email) {
			return true
		}
	}
	if subject == "always" || (subject == "default" && !saw) {
		for _, e := range nameAttr(x.c.RawSubject, oidEmail) {
			if emailMatch(e, email) {
				return true
			}
		}
	}
	return false
}

func (x *x509Cert) checkIP(ip net.IP) bool {
	for _, g := range x.altNames() {
		if g.tag == 7 && (len(g.bytes) == 4 || len(g.bytes) == 16) && net.IP(g.bytes).Equal(ip) {
			if (len(g.bytes) == 4) == (ip.To4() != nil) {
				return true
			}
		}
	}
	return false
}

// checkIssued mirrors X509_check_issued: names, key identifiers and the
// issuer's keyUsage.
func checkIssued(child, issuer *x509.Certificate) bool {
	if !bytes.Equal(child.RawIssuer, issuer.RawSubject) {
		a, b := nameMultiline(child.RawIssuer), nameMultiline(issuer.RawSubject)
		if !strings.EqualFold(a, b) {
			return false
		}
	}
	if len(child.AuthorityKeyId) > 0 && len(issuer.SubjectKeyId) > 0 && !bytes.Equal(child.AuthorityKeyId, issuer.SubjectKeyId) {
		return false
	}
	if findExt(issuer, oidExtKU) != nil && issuer.KeyUsage&x509.KeyUsageCertSign == 0 {
		return false
	}
	return true
}

func (b *cryptoBinding) x509Verify(x *x509Cert, k *asymKey) bool {
	var o certOuter
	if _, err := asn1.Unmarshal(x.c.Raw, &o); err != nil {
		return false
	}
	return b.verifySigAlg(k, o.SigAlg, x.c.RawTBSCertificate, o.Sig.RightAlign())
}

// verifySigAlg checks an X.509-style signature (AlgorithmIdentifier +
// signature over tbs) with k.
func (b *cryptoBinding) verifySigAlg(k *asymKey, alg pkix.AlgorithmIdentifier, tbs, sig []byte) bool {
	oid := alg.Algorithm.String()
	if oid == "1.2.840.113549.1.1.10" {
		if k.rsaPublic() == nil {
			return false
		}
		p, err := parsePSSParams(alg.Parameters)
		if err != nil {
			return false
		}
		if p == nil {
			p = &pssParams{hash: "sha1", mgf1: "sha1", saltLen: 20}
		}
		info := lookupDigest(p.hash)
		if info == nil {
			return false
		}
		return rsaPSSVerify(k.rsaPublic(), info, p.mgf1, digestOf(info, tbs), sig, p.saltLen)
	}
	d, ok := x509SigDigest[oid]
	if !ok {
		return false
	}
	switch d[1] {
	case "rsa":
		if k.typ != "rsa" && k.typ != "rsa-pss" {
			return false
		}
		k2 := *k
		k2.typ, k2.pss = "rsa", nil
		k = &k2
	default:
		if k.typ != d[1] {
			return false
		}
	}
	ok, err := b.verifyWith(k.public(), tbs, sig, &signOpts{digest: d[0], padding: rsaPKCS1Padding})
	return err == nil && ok
}

var errX509NoStartLine = errors.New("no start line")

// placeholderSPKI is a valid P-256 SubjectPublicKeyInfo (the generator).
var placeholderSPKI = func() []byte {
	k := &asymKey{typ: "ec", ec: &ecKey{curve: lookupCurve("prime256v1")}}
	x, y := k.ec.curve.curve.Params().Gx, k.ec.curve.curve.Params().Gy
	k.ec.pub = append(append([]byte{4}, leftPad(x.Bytes(), 32)...), leftPad(y.Bytes(), 32)...)
	return k.spkiDER()
}()

// parseCertificate wraps x509.ParseCertificate. crypto/x509 refuses
// certificates whose key it cannot represent (secp256k1, Ed448, ...);
// OpenSSL takes them, so on failure the SPKI is swapped for a placeholder
// to parse everything else, and the raw fields are put back.
func parseCertificate(der []byte) (*x509.Certificate, error) {
	c, err := x509.ParseCertificate(der)
	if err == nil {
		return c, nil
	}
	var outer struct {
		TBS    asn1.RawValue
		SigAlg asn1.RawValue
		Sig    asn1.RawValue
	}
	if rest, e := asn1.Unmarshal(der, &outer); e != nil || len(rest) > 0 {
		return nil, err
	}
	var fields []asn1.RawValue
	rest := outer.TBS.Bytes
	for len(rest) > 0 {
		var v asn1.RawValue
		var e error
		if rest, e = asn1.Unmarshal(rest, &v); e != nil {
			return nil, err
		}
		fields = append(fields, v)
	}
	idx := 5 // serial, sigalg, issuer, validity, subject, spki
	if len(fields) > 0 && fields[0].Class == asn1.ClassContextSpecific && fields[0].Tag == 0 {
		idx = 6
	}
	if len(fields) <= idx {
		return nil, err
	}
	origSPKI := fields[idx].FullBytes
	var tbs []byte
	for i, f := range fields {
		if i == idx {
			tbs = append(tbs, placeholderSPKI...)
		} else {
			tbs = append(tbs, f.FullBytes...)
		}
	}
	tbsDER, e := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: tbs})
	if e != nil {
		return nil, err
	}
	body := append(append(tbsDER, outer.SigAlg.FullBytes...), outer.Sig.FullBytes...)
	full, e := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: body})
	if e != nil {
		return nil, err
	}
	c, e = x509.ParseCertificate(full)
	if e != nil {
		return nil, err
	}
	c.Raw = der
	c.RawTBSCertificate = outer.TBS.FullBytes
	c.RawSubjectPublicKeyInfo = origSPKI
	c.PublicKey, c.PublicKeyAlgorithm = nil, x509.UnknownPublicKeyAlgorithm
	return c, nil
}

func parseX509(data []byte) (*x509.Certificate, error) {
	if bytes.Contains(data, []byte("-----BEGIN")) {
		rest := data
		for {
			var blk *pem.Block
			blk, rest = pem.Decode(rest)
			if blk == nil {
				return nil, errX509NoStartLine
			}
			switch blk.Type {
			case "CERTIFICATE", "X509 CERTIFICATE", "TRUSTED CERTIFICATE":
				return parseCertificate(blk.Bytes)
			}
		}
	}
	c, err := parseCertificate(data)
	if err != nil {
		return nil, errX509NoStartLine
	}
	return c, nil
}

type spkacASN struct {
	PKAC   asn1.RawValue
	SigAlg pkix.AlgorithmIdentifier
	Sig    asn1.BitString
}

type pkacASN struct {
	SPKI      asn1.RawValue
	Challenge string `asn1:"ia5"`
}

// parseSPKAC decodes a base64 Netscape SPKAC.
func parseSPKAC(data []byte) (*spkacASN, *pkacASN, bool) {
	clean := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, string(data))
	der, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		if der, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "=")); err != nil {
			return nil, nil, false
		}
	}
	var s spkacASN
	if rest, err := asn1.Unmarshal(der, &s); err != nil || len(rest) > 0 {
		return nil, nil, false
	}
	var p pkacASN
	if _, err := asn1.Unmarshal(s.PKAC.FullBytes, &p); err != nil {
		return nil, nil, false
	}
	return &s, &p, true
}

func (b *cryptoBinding) installX509() {
	// spkac(op, bytes): op is "verify", "challenge" or "pubkey".
	b.fn("spkac", func(args []vm.Value) (vm.Value, error) {
		data := b.bytesArg(args, 1)
		op := strArg(args, 0)
		if len(data) == 0 {
			return vm.NewString(""), nil
		}
		s, p, ok := parseSPKAC(data)
		switch op {
		case "verify":
			if !ok {
				return vm.False, nil
			}
			k, err := parseSPKI(p.SPKI.FullBytes)
			return vm.BooleanValue(err == nil && b.verifySigAlg(k, s.SigAlg, s.PKAC.FullBytes, s.Sig.RightAlign())), nil
		case "challenge":
			if !ok {
				return vm.NewString(""), nil
			}
			return b.buf([]byte(p.Challenge)), nil
		case "pubkey":
			if !ok {
				return vm.NewString(""), nil
			}
			if _, err := parseSPKI(p.SPKI.FullBytes); err != nil {
				return vm.NewString(""), nil
			}
			return b.buf(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: p.SPKI.FullBytes})), nil
		}
		return vm.Undefined, nil
	})
	b.fn("isIPv6", func(args []vm.Value) (vm.Value, error) {
		s := strArg(args, 0)
		ip := net.ParseIP(s)
		return vm.BooleanValue(ip != nil && strings.Contains(s, ":")), nil
	})
	b.fn("x509Parse", func(args []vm.Value) (vm.Value, error) {
		c, err := parseX509(b.bytesArg(args, 0))
		if err == errX509NoStartLine {
			return vm.Undefined, b.osslErr("0480006C", "PEM routines", "no start line")
		}
		if err != nil {
			return vm.Undefined, b.osslErr("068000A8", "asn1 encoding routines", "wrong tag")
		}
		return b.handle(&x509Cert{c: c}), nil
	})
	b.fn("x509Info", func(args []vm.Value) (vm.Value, error) {
		x, ok := handleOf[*x509Cert](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid certificate handle")
		}
		return b.jsonValue(x.info())
	})
	b.fn("x509Raw", func(args []vm.Value) (vm.Value, error) {
		x, ok := handleOf[*x509Cert](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid certificate handle")
		}
		if strArg(args, 1) == "pubkey" {
			if pk := x.legacyPubkey(); pk != nil {
				return b.buf(pk), nil
			}
			return vm.Undefined, nil
		}
		return b.buf(append([]byte(nil), x.c.Raw...)), nil
	})
	b.fn("x509PublicKey", func(args []vm.Value) (vm.Value, error) {
		x, ok := handleOf[*x509Cert](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid certificate handle")
		}
		k, err := parseSPKI(x.c.RawSubjectPublicKeyInfo)
		if err != nil {
			return vm.Undefined, b.keyDecodeErr(err)
		}
		return b.keyResult(k), nil
	})
	// x509Check(handle, what, value, subject, wildcards, partial, multi)
	b.fn("x509Check", func(args []vm.Value) (vm.Value, error) {
		x, ok := handleOf[*x509Cert](args, 0)
		if !ok {
			return vm.Undefined, errors.New("invalid certificate handle")
		}
		val, subject := strArg(args, 2), strArg(args, 3)
		switch strArg(args, 1) {
		case "host":
			if r := x.checkHost(val, subject, argAt(args, 4).IsTruthy(), argAt(args, 5).IsTruthy(), argAt(args, 6).IsTruthy()); r != nil {
				return vm.NewString(*r), nil
			}
		case "email":
			if x.checkEmail(val, subject) {
				return vm.NewString(val), nil
			}
		case "ip":
			if ip := net.ParseIP(val); ip != nil && x.checkIP(ip) {
				return vm.NewString(val), nil
			}
		}
		return vm.Undefined, nil
	})
	b.fn("x509CheckIssued", func(args []vm.Value) (vm.Value, error) {
		x, ok1 := handleOf[*x509Cert](args, 0)
		y, ok2 := handleOf[*x509Cert](args, 1)
		return vm.BooleanValue(ok1 && ok2 && checkIssued(x.c, y.c)), nil
	})
	b.fn("x509Verify", func(args []vm.Value) (vm.Value, error) {
		x, ok1 := handleOf[*x509Cert](args, 0)
		k, ok2 := handleOf[*asymKey](args, 1)
		return vm.BooleanValue(ok1 && ok2 && b.x509Verify(x, k)), nil
	})
	b.fn("x509CheckPrivateKey", func(args []vm.Value) (vm.Value, error) {
		x, ok1 := handleOf[*x509Cert](args, 0)
		k, ok2 := handleOf[*asymKey](args, 1)
		if !ok1 || !ok2 {
			return vm.False, nil
		}
		pub, err := parseSPKI(x.c.RawSubjectPublicKeyInfo)
		return vm.BooleanValue(err == nil && keysEqual(pub, k.public())), nil
	})
}
