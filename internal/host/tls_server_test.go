package host

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTestCertPEM writes a self-signed RSA-2048 cert/key pair shaped like
// "openssl req -x509 -subj /CN=localhost/O=Test -addext
// subjectAltName=DNS:localhost,IP:127.0.0.1" (the cert the expected
// output below was captured against with real node).
func writeTestCertPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject: pkix.Name{ExtraNames: []pkix.AttributeTypeAndValue{
			{Type: []int{2, 5, 4, 3}, Value: "localhost"}, {Type: []int{2, 5, 4, 10}, Value: "Test"},
		}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "key.pem"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestTLSServerAndClientMatchNode: expected output captured from real
// node v26 running the same script against an equivalent certificate.
func TestTLSServerAndClientMatchNode(t *testing.T) {
	dir := writeTestCertPEM(t)
	got := runScriptString(t, strings.ReplaceAll(`import tls from "node:tls";
import fs from "node:fs";
const dir = "__DIR__";
const key = fs.readFileSync(dir + "/key.pem"), cert = fs.readFileSync(dir + "/cert.pem");
const out = [];
const srvEvents = [];
const srv = tls.createServer({ key, cert, ALPNProtocols: ["h2", "http/1.1"] }, (s) => {
  srvEvents.push(["secureConnection", s.encrypted, s.authorized, s.alpnProtocol, s.servername, s.getProtocol(), JSON.stringify(s.getPeerCertificate())].join(":"));
  s.on("data", (d) => s.write("echo:" + d));
  s.on("end", () => s.end());
});
srv.on("tlsClientError", (e) => srvEvents.push("tlsClientError:" + e.code));
await new Promise((r) => srv.listen(0, "127.0.0.1", r));
const port = srv.address().port;
const c = tls.connect({ port, host: "127.0.0.1", ca: cert, servername: "localhost", ALPNProtocols: ["http/1.1"] });
await new Promise((r) => c.on("secureConnect", r));
const pc = c.getPeerCertificate(true);
out.push([c.authorized, c.authorizationError, c.alpnProtocol, c.encrypted, c.servername, c.getProtocol(), typeof c.getCipher().name].join(","));
out.push([JSON.stringify(pc.subject), pc.subjectaltname, pc.bits, pc.ca, typeof pc.fingerprint256, Buffer.isBuffer(pc.raw), pc.issuerCertificate === pc].join(","));
c.end("hi");
let data = "";
c.on("data", (d) => { data += d; });
await new Promise((r) => c.on("close", r));
out.push(data);
const e2 = await new Promise((r) => tls.connect({ port, host: "127.0.0.1" }).on("error", r));
out.push(e2.code);
const c3 = tls.connect({ port, host: "127.0.0.1", rejectUnauthorized: false });
await new Promise((r) => c3.on("secureConnect", r));
out.push([c3.authorized, c3.authorizationError].join(","));
c3.end();
await new Promise((r) => c3.on("close", r));
const e4 = await new Promise((r) => tls.connect({ port, host: "localhost", ca: cert, servername: "example.com" }).on("error", r));
out.push([e4.code, e4.reason, e4.host].join(","));
await new Promise((r) => setTimeout(r, 50));
await new Promise((r) => srv.close(r));
out.push(srvEvents.join("|"));
out.push([tls.checkServerIdentity("a.example.com", { subject: { CN: "*.example.com" } }) === undefined, tls.checkServerIdentity("x.y.example.com", { subjectaltname: "DNS:*.example.com" }).code].join(","));
const alpn = {};
tls.convertALPNProtocols(["h2", "http/1.1"], alpn);
out.push(alpn.ALPNProtocols.toString("hex"));
out.push([tls.DEFAULT_MIN_VERSION, tls.DEFAULT_MAX_VERSION, tls.DEFAULT_ECDH_CURVE, Array.isArray(tls.rootCertificates), Array.isArray(tls.getCiphers()), tls.createSecureContext({ key, cert }) instanceof tls.SecureContext].join(","));
out.join("\n")
`, "__DIR__", dir))
	want := `true,,http/1.1,true,localhost,TLSv1.3,string
{"CN":"localhost","O":"Test"},DNS:localhost, IP Address:127.0.0.1,2048,true,string,true,true
echo:hi
DEPTH_ZERO_SELF_SIGNED_CERT
false,DEPTH_ZERO_SELF_SIGNED_CERT
ERR_TLS_CERT_ALTNAME_INVALID,Host: example.com. is not in the cert's altnames: DNS:localhost, IP Address:127.0.0.1,example.com
secureConnection:true:false:http/1.1:localhost:TLSv1.3:{}|tlsClientError:ECONNRESET|secureConnection:true:false:false:false:TLSv1.3:{}|tlsClientError:ECONNRESET
true,ERR_TLS_CERT_ALTNAME_INVALID
02683208687474702f312e31
TLSv1.2,TLSv1.3,auto,true,true,true`
	if got != want {
		t.Errorf("mismatch\n got: %s\nwant: %s", got, want)
	}
}
