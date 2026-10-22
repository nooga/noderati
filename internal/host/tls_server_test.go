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

// TestHTTPClientServerAndHTTPSMatchNode: expected output captured from real node v26 running the same
// script against an equivalent certificate.
func TestHTTPClientServerAndHTTPSMatchNode(t *testing.T) {
	dir := writeTestCertPEM(t)
	got := runScriptString(t, strings.ReplaceAll(`import http from "node:http";
import https from "node:https";
import net from "node:net";
import fs from "node:fs";
const dir = "__DIR__";
const key = fs.readFileSync(dir + "/key.pem"), cert = fs.readFileSync(dir + "/cert.pem");
const out = [];
const handler = (req, res) => {
  let body = "";
  req.on("data", (d) => { body += d; });
  req.on("end", () => {
    res.setHeader("X-Got", [req.method, req.url, req.headers["x-multi"], req.headers.host === undefined ? "nohost" : "host"].join(" "));
    res.end("body:" + body);
  });
};
const srv = http.createServer(handler);
out.push([srv instanceof http.Server, srv instanceof net.Server, srv.keepAliveTimeout, srv.headersTimeout, srv.requestTimeout].join(","));
await new Promise((r) => srv.listen(0, "127.0.0.1", r));
const port = srv.address().port;
const req = http.request({ port, host: "127.0.0.1", method: "POST", path: "/p?q=1", headers: { "X-Multi": ["a", "b"] } });
out.push([req.headersSent, req.getHeader("host") === "127.0.0.1:" + port].join(","));
req.write(Buffer.from("he"));
out.push(req.headersSent);
try { req.setHeader("x-late", "1"); } catch (e) { out.push(e.code); }
const res = await new Promise((r) => { req.on("response", r); req.end("llo"); });
let body = "";
res.setEncoding("utf8");
for await (const c of res) body += c;
out.push([res.statusCode, res.headers["x-got"], body, req.res === res, req.finished].join(","));
const closed = await new Promise((r) => (req._closed ? r(true) : req.on("close", () => r(true))));
out.push("req close " + closed);
// get with URL string
const body2 = await new Promise((r) => http.get("http://127.0.0.1:" + port + "/g", (res) => { let b = ""; res.on("data", (d) => (b += d)); res.on("end", () => r(b)); }));
out.push(body2);
await new Promise((r) => srv.getConnections((e, n) => { out.push("conns:" + (e === null) + ":" + (typeof n)); r(); }));
srv.closeAllConnections();
await new Promise((r) => srv.close(r));
// errors
const e1 = await new Promise((r) => http.get({ port: 1, host: "127.0.0.1" }).on("error", r));
out.push([e1.code, e1.message, e1.syscall, e1.address, e1.port].join(","));
const r2 = http.get({ port: 1, host: "127.0.0.1" });
const e2 = await new Promise((r) => { r2.on("error", r); r2.destroy(); });
out.push([e2.code, e2.message].join(","));
const r3 = http.get({ port: 1, host: "127.0.0.1" });
let abortEv = false;
r3.on("abort", () => { abortEv = true; });
r3.on("error", (e) => out.push("abort error " + e.code));
r3.abort();
await new Promise((r) => r3.on("close", r));
out.push(["aborted", r3.aborted, abortEv, r3.destroyed].join(","));
try { http.request({ path: "/a b" }); } catch (e) { out.push(e.code); }
try { http.request({ protocol: "https:" }); } catch (e) { out.push(e.code + ":" + e.message); }
// https
const hs = https.createServer({ key, cert }, (req, res) => res.end("secure:" + req.url));
out.push([hs instanceof https.Server, hs instanceof http.Server].join(","));
await new Promise((r) => hs.listen(0, "127.0.0.1", r));
const sbody = await new Promise((r, j) => https.get({ host: "127.0.0.1", port: hs.address().port, path: "/s", ca: cert, servername: "localhost" }, (res) => { let b = ""; res.on("data", (d) => (b += d)); res.on("end", () => r(b)); }).on("error", j));
out.push(sbody);
const se = await new Promise((r) => https.get({ host: "127.0.0.1", port: hs.address().port }).on("error", r));
out.push(se.code);
await new Promise((r) => hs.close(r));
// EADDRINUSE on http server
const a = http.createServer();
await new Promise((r) => a.listen(0, "127.0.0.1", r));
const ee = await new Promise((r) => http.createServer().on("error", r).listen(a.address().port, "127.0.0.1"));
out.push([ee.code, ee.syscall].join(","));
a.close();
out.join("\n")
`, "__DIR__", dir))
	want := `true,true,5000,60000,300000
false,true
true
ERR_HTTP_HEADERS_SENT
200,POST /p?q=1 a, b host,body:hello,true,true
req close true
body:
conns:true:number
ECONNREFUSED,connect ECONNREFUSED 127.0.0.1:1,connect,127.0.0.1,1
ECONNRESET,socket hang up
aborted,true,true,true
ERR_UNESCAPED_CHARACTERS
ERR_INVALID_PROTOCOL:Protocol "https:" not supported. Expected "http:"
true,false
secure:/s
DEPTH_ZERO_SELF_SIGNED_CERT
EADDRINUSE,listen`
	if got != want {
		t.Errorf("mismatch\n got: %s\nwant: %s", got, want)
	}
}
