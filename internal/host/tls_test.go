package host

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/nooga/paserati/pkg/driver"
)

// selfSignedTLSConfig builds a real, freshly-generated self-signed
// certificate for 127.0.0.1/localhost - a real TLS handshake needs a real
// certificate on the server side even in a test; rejectUnauthorized:false
// on the client side (below) is what lets our own throwaway CA through,
// exactly like a real Node test against a self-signed endpoint would.
func selfSignedTLSConfig(t *testing.T, alpnProtos []string) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: alpnProtos}
}

func newTLSEchoServer(t *testing.T, alpnProtos []string) (addr string, closeFn func()) {
	t.Helper()
	conf := selfSignedTLSConfig(t, alpnProtos)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", conf)
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func TestTLSConnectEchoByteExact(t *testing.T) {
	addr, closeFn := newTLSEchoServer(t, []string{"http/1.1"})
	defer closeFn()
	host, port := splitHostPort(t, addr)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:tls";
		let result = "";
		await new Promise((resolve, reject) => {
			const socket = connect({
				host: %q, port: %s, servername: "localhost",
				rejectUnauthorized: false, ALPNProtocols: ["http/1.1"],
			}, () => {
				socket.write("hello ");
				socket.end("tls world");
			});
			socket.on("data", (chunk) => { result += chunk.toString(); });
			socket.on("end", resolve);
			socket.on("error", reject);
		});
		result
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if want := "hello tls world"; val.ToString() != want {
		t.Errorf("got %q, want %q", val.ToString(), want)
	}
}

// TestTLSConnectSecureConnectAndALPN drives the exact shape undici's own
// connector relies on (lib/core/connect.js): `.once('secureConnect', cb)`
// with `this` as the socket, and a real negotiated ALPN protocol exposed
// afterward - not the plain 'connect' event, and not a synthesized value.
func TestTLSConnectSecureConnectAndALPN(t *testing.T) {
	addr, closeFn := newTLSEchoServer(t, []string{"http/1.1"})
	defer closeFn()
	host, port := splitHostPort(t, addr)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:tls";
		let alpn, thisIsSocket;
		await new Promise((resolve, reject) => {
			const socket = connect({
				host: %q, port: %s, servername: "localhost",
				rejectUnauthorized: false, ALPNProtocols: ["http/1.1"],
			});
			socket.once("secureConnect", function () {
				alpn = this.alpnProtocol;
				thisIsSocket = this === socket;
				socket.destroy();
				resolve();
			});
			socket.on("error", reject);
		});
		JSON.stringify({ alpn, thisIsSocket })
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"alpn":"http/1.1","thisIsSocket":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestTLSConnectRejectsBadCertByDefault checks the honest failure path:
// without rejectUnauthorized:false, a self-signed cert genuinely fails
// verification (a real x509 error from Go's own crypto/tls), not a
// silently-accepted connection.
func TestTLSConnectRejectsBadCertByDefault(t *testing.T) {
	addr, closeFn := newTLSEchoServer(t, []string{"http/1.1"})
	defer closeFn()
	host, port := splitHostPort(t, addr)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:tls";
		let gotError = false;
		await new Promise((resolve) => {
			const socket = connect({ host: %q, port: %s, servername: "localhost" });
			socket.on("error", (err) => { gotError = err instanceof Error; resolve(); });
			socket.on("secureConnect", () => resolve());
		});
		gotError
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Errorf("expected a real certificate-verification error, got %v", val)
	}
}
