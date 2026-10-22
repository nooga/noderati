package host

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// tls.go is the native half of node:tls. The JS half (tls_shim.go) builds
// TLSSocket on top of net.Socket and tls.Server on top of net.Server,
// exactly like Node; this file only adds what OpenSSL's TLSWrap/
// SecureContext provide natively:
//
//   - secure contexts (key/cert/ca parsed into a tls.Certificate + pool),
//   - handle.startTLS(opts, cb): swaps the handle's live conn for a
//     *tls.Conn in place (client or server side) and runs the handshake,
//   - post-handshake introspection (protocol, cipher, ALPN, peer
//     certificate as a Node-shaped object, OpenSSL-style verify error).
//
// Certificate verification on the client is done by hand after the
// handshake (InsecureSkipVerify + x509.Verify), mirroring OpenSSL's
// SSL_get_verify_result: the handshake itself never fails on a bad
// chain; the JS side then decides, from rejectUnauthorized, whether to
// destroy the socket or just set authorized=false/authorizationError -
// which is the only way to reproduce Node's observable behavior.

var clientSessionCache = tls.NewLRUClientSessionCache(256)

type secureCtx struct {
	certs   []tls.Certificate
	roots   *x509.CertPool // nil = default CA store
	caPEMs  [][]byte
	ciphers []uint16
}

var (
	secureCtxRegistry sync.Map // id -> *secureCtx
	secureCtxSeq      atomic.Uint64
)

type tlsHandleState struct {
	conn       *tls.Conn
	isServer   bool
	verifyCode string
	verifyMsg  string
	servername string
	ctx        *secureCtx
}

func secureCtxFromValue(v vm.Value) *secureCtx {
	if v.Type() != vm.TypeObject {
		return nil
	}
	idv, ok := v.AsPlainObject().Get("__ctxId")
	if !ok || !idv.IsNumber() {
		return nil
	}
	if c, ok := secureCtxRegistry.Load(uint64(idv.ToFloat())); ok {
		return c.(*secureCtx)
	}
	return nil
}

// pemListOpt reads a key/cert/ca option: a string, a Buffer, or an array
// of either.
func pemListOpt(vmInst *vm.VM, o *vm.PlainObject, key string) [][]byte {
	if o == nil {
		return nil
	}
	v, ok := o.GetOwn(key)
	if !ok || v.IsUndefined() || v.Type() == vm.TypeNull {
		return nil
	}
	if v.IsArray() {
		arr := v.AsArray()
		out := make([][]byte, 0, arr.Length())
		for i := 0; i < arr.Length(); i++ {
			e := arr.Get(i)
			if e.Type() == vm.TypeObject && e.AsTypedArray() == nil {
				if pv, ok := e.AsPlainObject().Get("pem"); ok {
					e = pv
				}
			}
			out = append(out, append([]byte(nil), valueToBytes(vmInst, e)...))
		}
		return out
	}
	return [][]byte{append([]byte(nil), valueToBytes(vmInst, v)...)}
}

var (
	defaultCAMu   sync.Mutex
	defaultCAPEMs [][]byte // nil = system store
	defaultCAPool *x509.CertPool
)

func currentDefaultRoots() *x509.CertPool {
	defaultCAMu.Lock()
	defer defaultCAMu.Unlock()
	return defaultCAPool
}

func buildSecureContext(vmInst *vm.VM, o *vm.PlainObject) (*secureCtx, error) {
	ctx := &secureCtx{}
	keys := pemListOpt(vmInst, o, "key")
	certs := pemListOpt(vmInst, o, "cert")
	if len(certs) > 0 || len(keys) > 0 {
		if len(keys) == 0 {
			return nil, errors.New("error:0A0000BE:SSL routines::no private key assigned")
		}
		if len(certs) == 0 {
			return nil, errors.New("error:0A0000BE:SSL routines::no certificate assigned")
		}
		for i, k := range keys {
			c := certs[0]
			if i < len(certs) {
				c = certs[i]
			}
			pair, err := tls.X509KeyPair(c, k)
			if err != nil {
				return nil, fmt.Errorf("error:1E08010C:DECODER routines::unsupported (%v)", err)
			}
			ctx.certs = append(ctx.certs, pair)
		}
	}
	if cas := pemListOpt(vmInst, o, "ca"); len(cas) > 0 {
		pool := x509.NewCertPool()
		for _, c := range cas {
			pool.AppendCertsFromPEM(c)
			if blk, _ := pem.Decode(c); blk == nil {
				if cert, err := x509.ParseCertificate(c); err == nil {
					pool.AddCert(cert)
				}
			}
		}
		ctx.roots = pool
		ctx.caPEMs = cas
	}
	if cl := getStrOpt(o, "ciphers", ""); cl != "" {
		ctx.ciphers = parseCipherList(cl)
	}
	return ctx, nil
}

func parseCipherList(list string) []uint16 {
	var out []uint16
	names := map[string]uint16{}
	for _, cs := range tls.CipherSuites() {
		names[strings.ToUpper(goToOpenSSLCipher(cs.Name))] = cs.ID
		names[strings.ToUpper(cs.Name)] = cs.ID
	}
	for _, part := range strings.Split(list, ":") {
		if id, ok := names[strings.ToUpper(strings.TrimSpace(part))]; ok {
			out = append(out, id)
		}
	}
	return out
}

var goToOpenSSLCipherNames = map[string]string{
	"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256":       "ECDHE-ECDSA-AES128-GCM-SHA256",
	"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256":         "ECDHE-RSA-AES128-GCM-SHA256",
	"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384":       "ECDHE-ECDSA-AES256-GCM-SHA384",
	"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384":         "ECDHE-RSA-AES256-GCM-SHA384",
	"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256": "ECDHE-ECDSA-CHACHA20-POLY1305",
	"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256":   "ECDHE-RSA-CHACHA20-POLY1305",
	"TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA":          "ECDHE-ECDSA-AES128-SHA",
	"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA":            "ECDHE-RSA-AES128-SHA",
	"TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA":          "ECDHE-ECDSA-AES256-SHA",
	"TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA":            "ECDHE-RSA-AES256-SHA",
	"TLS_RSA_WITH_AES_128_GCM_SHA256":               "AES128-GCM-SHA256",
	"TLS_RSA_WITH_AES_256_GCM_SHA384":               "AES256-GCM-SHA384",
	"TLS_RSA_WITH_AES_128_CBC_SHA":                  "AES128-SHA",
	"TLS_RSA_WITH_AES_256_CBC_SHA":                  "AES256-SHA",
	"TLS_RSA_WITH_AES_128_CBC_SHA256":               "AES128-SHA256",
	"TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256":       "ECDHE-ECDSA-AES128-SHA256",
	"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256":         "ECDHE-RSA-AES128-SHA256",
	"TLS_AES_128_GCM_SHA256":                        "TLS_AES_128_GCM_SHA256",
	"TLS_AES_256_GCM_SHA384":                        "TLS_AES_256_GCM_SHA384",
	"TLS_CHACHA20_POLY1305_SHA256":                  "TLS_CHACHA20_POLY1305_SHA256",
}

func goToOpenSSLCipher(name string) string {
	if n, ok := goToOpenSSLCipherNames[name]; ok {
		return n
	}
	return name
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLSv1"
	case tls.VersionTLS11:
		return "TLSv1.1"
	case tls.VersionTLS12:
		return "TLSv1.2"
	case tls.VersionTLS13:
		return "TLSv1.3"
	}
	return "unknown"
}

func tlsVersionFromName(s string) uint16 {
	switch s {
	case "TLSv1":
		return tls.VersionTLS10
	case "TLSv1.1":
		return tls.VersionTLS11
	case "TLSv1.2":
		return tls.VersionTLS12
	case "TLSv1.3":
		return tls.VersionTLS13
	}
	return 0
}

// verifyChain reproduces OpenSSL's verify result for a peer chain.
func verifyChain(chain []*x509.Certificate, roots *x509.CertPool, server bool) (code, msg string) {
	if len(chain) == 0 {
		if server {
			return "", ""
		}
		return "UNABLE_TO_GET_ISSUER_CERT", "unable to get issuer certificate"
	}
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	usage := x509.ExtKeyUsageServerAuth
	if server {
		usage = x509.ExtKeyUsageClientAuth
	}
	if roots == nil {
		roots = currentDefaultRoots()
	}
	_, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{usage}})
	if err == nil {
		return "", ""
	}
	var cie x509.CertificateInvalidError
	if errors.As(err, &cie) {
		switch cie.Reason {
		case x509.Expired:
			if time.Now().Before(cie.Cert.NotBefore) {
				return "CERT_NOT_YET_VALID", "certificate is not yet valid"
			}
			return "CERT_HAS_EXPIRED", "certificate has expired"
		case x509.IncompatibleUsage:
			return "UNSUPPORTED_CERTIFICATE_PURPOSE", "unsupported certificate purpose"
		}
	}
	selfSigned := func(c *x509.Certificate) bool {
		return c.CheckSignatureFrom(c) == nil && strings.EqualFold(string(c.RawIssuer), string(c.RawSubject))
	}
	if len(chain) == 1 && selfSigned(chain[0]) {
		return "DEPTH_ZERO_SELF_SIGNED_CERT", "self-signed certificate"
	}
	if len(chain) > 1 && selfSigned(chain[len(chain)-1]) {
		return "SELF_SIGNED_CERT_IN_CHAIN", "self-signed certificate in certificate chain"
	}
	if len(chain) == 1 {
		return "UNABLE_TO_VERIFY_LEAF_SIGNATURE", "unable to verify the first certificate"
	}
	return "UNABLE_TO_GET_ISSUER_CERT_LOCALLY", "unable to get local issuer certificate"
}

type tlsVerifyError struct{ code, msg string }

func (e *tlsVerifyError) Error() string { return e.msg }

func tlsHandshakeErrInfo(err error, isServer bool) *netErrorInfo {
	var ve *tlsVerifyError
	if errors.As(err, &ve) {
		return &netErrorInfo{code: ve.code, message: ve.msg, syscall: "verify"}
	}
	var ae0 tls.AlertError
	if isServer && (errors.As(err, &ae0) || strings.Contains(err.Error(), "remote error: tls:")) {
		return &netErrorInfo{code: "ECONNRESET", message: "socket hang up"}
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		msg := "Client network socket disconnected before secure TLS connection was established"
		if isServer {
			msg = "socket hang up"
		}
		return &netErrorInfo{code: "ECONNRESET", syscall: "", message: msg}
	}
	s := err.Error()
	var re tls.RecordHeaderError
	if errors.As(err, &re) {
		return &netErrorInfo{code: "ERR_SSL_WRONG_VERSION_NUMBER", message: "SSL routines:ssl3_get_record:wrong version number"}
	}
	var ae tls.AlertError
	if errors.As(err, &ae) {
		name := strings.ToUpper(strings.ReplaceAll(strings.TrimPrefix(ae.Error(), "tls: "), " ", "_"))
		return &netErrorInfo{code: "ERR_SSL_TLSV1_ALERT_" + name, message: "SSL alert: " + ae.Error()}
	}
	if strings.Contains(s, "no application protocol") {
		return &netErrorInfo{code: "ERR_SSL_TLSV1_ALERT_NO_APPLICATION_PROTOCOL", message: "SSL routines::tlsv1 alert no application protocol"}
	}
	if strings.Contains(s, "doesn't support any of the certificate's signature algorithms") || strings.Contains(s, "no cipher suite supported") || strings.Contains(s, "no mutually supported") {
		return &netErrorInfo{code: "ERR_SSL_NO_SHARED_CIPHER", message: "SSL routines::no shared cipher"}
	}
	if strings.Contains(s, "client didn't provide a certificate") {
		return &netErrorInfo{code: "ERR_SSL_PEER_DID_NOT_RETURN_A_CERTIFICATE", message: "SSL routines::peer did not return a certificate"}
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, os.ErrDeadlineExceeded) {
		return netErrInfo(err, "read")
	}
	return &netErrorInfo{code: "ERR_SSL_SSL_HANDSHAKE_FAILURE", message: s}
}

// startTLS takes the handle's conn over for TLS. Any in-flight raw Read
// is interrupted first (and whatever it already got is replayed into the
// TLS layer) so exactly one reader ever touches the raw conn.
func (h *netHandle) startTLS(o *vm.PlainObject, cb vm.Value) {
	isServer := getBoolOpt(o, "isServer", false)
	ctx := (*secureCtx)(nil)
	if v, ok := o.GetOwn("secureContext"); ok {
		ctx = secureCtxFromValue(v)
	}
	if ctx == nil {
		ctx = &secureCtx{}
	}
	conf := &tls.Config{
		InsecureSkipVerify: true,
		ClientSessionCache: clientSessionCache,
		Certificates:       ctx.certs,
		CipherSuites:       ctx.ciphers,
	}
	if v := tlsVersionFromName(getStrOpt(o, "minVersion", "")); v != 0 {
		conf.MinVersion = v
	}
	if v := tlsVersionFromName(getStrOpt(o, "maxVersion", "")); v != 0 {
		conf.MaxVersion = v
	}
	if alpn := stringArrayOpt(o, "ALPNProtocols"); len(alpn) > 0 {
		conf.NextProtos = alpn
	}
	servername := getStrOpt(o, "servername", "")
	if isServer {
		conf.InsecureSkipVerify = false
		if getBoolOpt(o, "requestCert", false) {
			roots := ctx.roots
			if roots == nil {
				roots = currentDefaultRoots()
			}
			if getBoolOpt(o, "rejectUnauthorized", true) {
				conf.ClientAuth = tls.RequireAndVerifyClientCert
				if roots == nil {
					if sys, err := x509.SystemCertPool(); err == nil {
						roots = sys
					}
				}
				conf.ClientCAs = roots
			} else {
				conf.ClientAuth = tls.RequestClientCert
			}
		}
		if getBoolOpt(o, "hasSNICallback", false) {
			base := conf
			conf = conf.Clone()
			conf.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				sctx, err := h.askSNI(hello.ServerName)
				if err != nil {
					return nil, err
				}
				if sctx == nil || len(sctx.certs) == 0 {
					return nil, nil
				}
				c := base.Clone()
				c.Certificates = sctx.certs
				return c, nil
			}
		}
	} else {
		if servername != "" && isIPString(servername) == 0 {
			conf.ServerName = servername
		}
		// With rejectUnauthorized, OpenSSL-backed Node aborts during the
		// handshake (the server then sees ECONNRESET, never
		// 'secureConnection'); do the same here. checkHost is set only
		// when the default checkServerIdentity is in use.
		if getBoolOpt(o, "verifyInHandshake", false) {
			checkHost := getStrOpt(o, "checkHost", "")
			conf.VerifyConnection = func(cs tls.ConnectionState) error {
				if code, msg := verifyChain(cs.PeerCertificates, ctx.roots, false); code != "" {
					return &tlsVerifyError{code: code, msg: msg}
				}
				if checkHost != "" && len(cs.PeerCertificates) > 0 {
					if cs.PeerCertificates[0].VerifyHostname(checkHost) != nil {
						return &tlsVerifyError{code: "ERR_TLS_CERT_ALTNAME_INVALID"}
					}
				}
				return nil
			}
		}
	}

	h.mu.Lock()
	if h.closed || h.conn == nil {
		h.mu.Unlock()
		info := &netErrorInfo{code: "EBADF", syscall: "", message: "socket is not connected"}
		h.schedule(func() { callJSCallback(h.vmInst, cb, info.toValue(h.vmInst)) })
		return
	}
	h.ready = false
	raw := h.conn
	if h.inRead {
		_ = raw.SetReadDeadline(time.Unix(1, 0))
		for h.inRead {
			h.cond.Wait()
		}
		_ = raw.SetReadDeadline(time.Time{})
	}
	if v, ok := o.GetOwn("prefix"); ok && v.AsTypedArray() != nil {
		h.prefix = append(append([]byte(nil), valueToBytes(h.vmInst, v)...), h.prefix...)
	}
	if len(h.prefix) > 0 {
		raw = &prefixConn{Conn: raw, prefix: h.prefix}
		h.prefix = nil
	}
	var tc *tls.Conn
	if isServer {
		tc = tls.Server(raw, conf)
	} else {
		tc = tls.Client(raw, conf)
	}
	h.conn = tc
	state := &tlsHandleState{conn: tc, isServer: isServer, servername: servername, ctx: ctx}
	h.tlsState = state
	h.mu.Unlock()

	go func() {
		err := tc.HandshakeContext(context.Background())
		if err != nil {
			info := tlsHandshakeErrInfo(err, isServer)
			h.schedule(func() {
				if !h.isClosed() {
					callJSCallback(h.vmInst, cb, info.toValue(h.vmInst))
				}
			})
			return
		}
		cs := tc.ConnectionState()
		if isServer {
			state.servername = cs.ServerName
			if len(cs.PeerCertificates) > 0 {
				state.verifyCode, state.verifyMsg = verifyChain(cs.PeerCertificates, ctx.roots, true)
			} else if getBoolOpt(o, "requestCert", false) {
				state.verifyCode, state.verifyMsg = "UNABLE_TO_GET_ISSUER_CERT", "unable to get issuer certificate"
			}
		} else {
			state.verifyCode, state.verifyMsg = verifyChain(cs.PeerCertificates, ctx.roots, false)
		}
		h.mu.Lock()
		h.ready = true
		h.mu.Unlock()
		h.cond.Broadcast()
		h.schedule(func() {
			if !h.isClosed() {
				callJSCallback(h.vmInst, cb, vm.Null)
			}
		})
	}()
}

// askSNI runs on the handshake goroutine: it hops to the VM thread to run
// the JS SNICallback and blocks until it answers.
func (h *netHandle) askSNI(servername string) (*secureCtx, error) {
	type answer struct {
		ctx *secureCtx
		err error
	}
	ch := make(chan answer, 1)
	var once sync.Once
	h.schedule(func() {
		done := vm.NewNativeFunction(2, false, "done", func(args []vm.Value) (vm.Value, error) {
			once.Do(func() {
				if len(args) > 0 && !args[0].IsUndefined() && args[0].Type() != vm.TypeNull {
					ch <- answer{err: errors.New(args[0].ToString())}
					return
				}
				var c *secureCtx
				if len(args) > 1 {
					c = secureCtxFromValue(args[1])
				}
				ch <- answer{ctx: c}
			})
			return vm.Undefined, nil
		})
		fn, ok := h.obj.Get("onsni")
		if !ok || !fn.IsCallable() {
			once.Do(func() { ch <- answer{} })
			return
		}
		if _, err := h.vmInst.Call(fn, vm.NewValueFromPlainObject(h.obj), []vm.Value{vm.NewString(servername), done}); err != nil {
			once.Do(func() { ch <- answer{err: err} })
		}
	})
	select {
	case a := <-ch:
		return a.ctx, a.err
	case <-time.After(30 * time.Second):
		return nil, errors.New("SNICallback timed out")
	}
}

func installTLSHandleMethods(h *netHandle, obj *vm.PlainObject, fn func(string, int, func([]vm.Value) (vm.Value, error)), arg func([]vm.Value, int) vm.Value) {
	vmInst := h.vmInst
	state := func() *tlsHandleState {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.tlsState
	}
	fn("startTLS", 2, func(args []vm.Value) (vm.Value, error) {
		var o *vm.PlainObject
		if v := arg(args, 0); v.Type() == vm.TypeObject {
			o = v.AsPlainObject()
		}
		h.startTLS(o, arg(args, 1))
		return vm.Undefined, nil
	})
	fn("getProtocol", 0, func(_ []vm.Value) (vm.Value, error) {
		st := state()
		if st == nil || !st.conn.ConnectionState().HandshakeComplete {
			return vm.Null, nil
		}
		return vm.NewString(tlsVersionName(st.conn.ConnectionState().Version)), nil
	})
	fn("getCipher", 0, func(_ []vm.Value) (vm.Value, error) {
		st := state()
		if st == nil || !st.conn.ConnectionState().HandshakeComplete {
			return vm.Undefined, nil
		}
		cs := st.conn.ConnectionState()
		std := tls.CipherSuiteName(cs.CipherSuite)
		o := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		o.SetOwn("name", vm.NewString(goToOpenSSLCipher(std)))
		o.SetOwn("standardName", vm.NewString(std))
		ver := "TLSv1/SSLv3"
		if cs.Version == tls.VersionTLS13 {
			ver = "TLSv1.3"
		}
		o.SetOwn("version", vm.NewString(ver))
		return vm.NewValueFromPlainObject(o), nil
	})
	fn("getALPN", 0, func(_ []vm.Value) (vm.Value, error) {
		st := state()
		if st == nil {
			return vm.False, nil
		}
		if p := st.conn.ConnectionState().NegotiatedProtocol; p != "" {
			return vm.NewString(p), nil
		}
		return vm.False, nil
	})
	fn("getServername", 0, func(_ []vm.Value) (vm.Value, error) {
		st := state()
		if st == nil || st.servername == "" {
			return vm.False, nil
		}
		return vm.NewString(st.servername), nil
	})
	fn("verifyError", 0, func(_ []vm.Value) (vm.Value, error) {
		st := state()
		if st == nil || st.verifyCode == "" {
			return vm.Null, nil
		}
		info := &netErrorInfo{code: st.verifyCode, message: st.verifyMsg}
		return info.toValue(vmInst), nil
	})
	fn("isSessionReused", 0, func(_ []vm.Value) (vm.Value, error) {
		st := state()
		return vm.BooleanValue(st != nil && st.conn.ConnectionState().DidResume), nil
	})
	fn("getPeerCertificate", 1, func(args []vm.Value) (vm.Value, error) {
		st := state()
		if st == nil {
			return vm.Null, nil
		}
		chain := st.conn.ConnectionState().PeerCertificates
		if len(chain) == 0 {
			return vm.Null, nil
		}
		return certChainToValue(vmInst, chain, arg(args, 0).IsTruthy()), nil
	})
	fn("getCertificate", 0, func(_ []vm.Value) (vm.Value, error) {
		st := state()
		if st == nil || len(st.ctx.certs) == 0 {
			return vm.Null, nil
		}
		leaf := st.ctx.certs[0]
		c, err := x509.ParseCertificate(leaf.Certificate[0])
		if err != nil {
			return vm.Null, nil
		}
		return certToValue(vmInst, c), nil
	})
	fn("getPeerX509", 0, func(_ []vm.Value) (vm.Value, error) {
		st := state()
		if st == nil {
			return vm.Undefined, nil
		}
		chain := st.conn.ConnectionState().PeerCertificates
		if len(chain) == 0 {
			return vm.Undefined, nil
		}
		return wrapBuffer(vmInst, chain[0].Raw), nil
	})
	fn("exportKeyingMaterial", 3, func(args []vm.Value) (vm.Value, error) {
		st := state()
		if st == nil {
			return vm.Undefined, nil
		}
		var context []byte
		if c := arg(args, 2); !c.IsUndefined() {
			context = valueToBytes(vmInst, c)
		}
		cs := st.conn.ConnectionState()
		b, err := cs.ExportKeyingMaterial(arg(args, 1).ToString(), context, int(arg(args, 0).ToFloat()))
		if err != nil {
			return vm.Undefined, err
		}
		return wrapBuffer(vmInst, b), nil
	})
}

// ---- certificate objects ----

var oidShortNames = map[string]string{
	"2.5.4.3": "CN", "2.5.4.6": "C", "2.5.4.7": "L", "2.5.4.8": "ST", "2.5.4.10": "O",
	"2.5.4.11": "OU", "1.2.840.113549.1.9.1": "emailAddress", "2.5.4.5": "serialNumber",
	"2.5.4.4": "SN", "2.5.4.42": "GN", "2.5.4.12": "title", "0.9.2342.19200300.100.1.25": "DC",
}

func nameToValue(vmInst *vm.VM, n pkix.Name) vm.Value {
	o := vm.NewObject(vm.Null).AsPlainObject()
	for _, atv := range n.Names {
		key := oidShortNames[atv.Type.String()]
		if key == "" {
			key = atv.Type.String()
		}
		val := vm.NewString(fmt.Sprint(atv.Value))
		if prev, ok := o.GetOwn(key); ok {
			if prev.IsArray() {
				prev.AsArray().Append(val)
			} else {
				o.SetOwn(key, vm.NewArrayWithArgs([]vm.Value{prev, val}))
			}
			continue
		}
		o.SetOwn(key, val)
	}
	return vm.NewValueFromPlainObject(o)
}

func opensslTime(t time.Time) string {
	return t.UTC().Format("Jan _2 15:04:05 2006 GMT")
}

func colonHex(b []byte) string {
	h := strings.ToUpper(hex.EncodeToString(b))
	var sb strings.Builder
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			sb.WriteByte(':')
		}
		sb.WriteString(h[i : i+2])
	}
	return sb.String()
}

func subjectAltName(c *x509.Certificate) string {
	var parts []string
	for _, d := range c.DNSNames {
		parts = append(parts, "DNS:"+d)
	}
	for _, ip := range c.IPAddresses {
		parts = append(parts, "IP Address:"+ip.String())
	}
	for _, e := range c.EmailAddresses {
		parts = append(parts, "email:"+e)
	}
	for _, u := range c.URIs {
		parts = append(parts, "URI:"+u.String())
	}
	return strings.Join(parts, ", ")
}

func certToValue(vmInst *vm.VM, c *x509.Certificate) vm.Value {
	o := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	o.SetOwn("subject", nameToValue(vmInst, c.Subject))
	o.SetOwn("issuer", nameToValue(vmInst, c.Issuer))
	if san := subjectAltName(c); san != "" {
		o.SetOwn("subjectaltname", vm.NewString(san))
	}
	o.SetOwn("ca", vm.BooleanValue(c.IsCA))
	switch pk := c.PublicKey.(type) {
	case *rsa.PublicKey:
		o.SetOwn("modulus", vm.NewString(strings.ToUpper(pk.N.Text(16))))
		o.SetOwn("bits", vm.NumberValue(float64(pk.N.BitLen())))
		o.SetOwn("exponent", vm.NewString(fmt.Sprintf("0x%x", pk.E)))
		o.SetOwn("pubkey", wrapBuffer(vmInst, x509.MarshalPKCS1PublicKey(pk)))
	case *ecdsa.PublicKey:
		o.SetOwn("bits", vm.NumberValue(float64(pk.Curve.Params().BitSize)))
		if b, err := pk.Bytes(); err == nil {
			o.SetOwn("pubkey", wrapBuffer(vmInst, b))
		}
		curve := pk.Curve.Params().Name
		asn1Names := map[string]string{"P-256": "prime256v1", "P-384": "secp384r1", "P-521": "secp521r1"}
		o.SetOwn("asn1Curve", vm.NewString(asn1Names[curve]))
		o.SetOwn("nistCurve", vm.NewString(curve))
	case ed25519.PublicKey:
		o.SetOwn("bits", vm.NumberValue(256))
		o.SetOwn("pubkey", wrapBuffer(vmInst, pk))
	}
	o.SetOwn("valid_from", vm.NewString(opensslTime(c.NotBefore)))
	o.SetOwn("valid_to", vm.NewString(opensslTime(c.NotAfter)))
	s1 := sha1.Sum(c.Raw)
	s256 := sha256.Sum256(c.Raw)
	s512 := sha512.Sum512(c.Raw)
	o.SetOwn("fingerprint", vm.NewString(colonHex(s1[:])))
	o.SetOwn("fingerprint256", vm.NewString(colonHex(s256[:])))
	o.SetOwn("fingerprint512", vm.NewString(colonHex(s512[:])))
	if len(c.ExtKeyUsage) > 0 || len(c.UnknownExtKeyUsage) > 0 {
		eku := vm.NewArray()
		arr := eku.AsArray()
		ekuOIDs := map[x509.ExtKeyUsage]string{
			x509.ExtKeyUsageServerAuth: "1.3.6.1.5.5.7.3.1", x509.ExtKeyUsageClientAuth: "1.3.6.1.5.5.7.3.2",
			x509.ExtKeyUsageCodeSigning: "1.3.6.1.5.5.7.3.3", x509.ExtKeyUsageEmailProtection: "1.3.6.1.5.5.7.3.4",
			x509.ExtKeyUsageTimeStamping: "1.3.6.1.5.5.7.3.8", x509.ExtKeyUsageOCSPSigning: "1.3.6.1.5.5.7.3.9",
		}
		for _, u := range c.ExtKeyUsage {
			if s, ok := ekuOIDs[u]; ok {
				arr.Append(vm.NewString(s))
			}
		}
		for _, u := range c.UnknownExtKeyUsage {
			arr.Append(vm.NewString(asn1.ObjectIdentifier(u).String()))
		}
		o.SetOwn("ext_key_usage", eku)
	}
	o.SetOwn("serialNumber", vm.NewString(strings.ToUpper(c.SerialNumber.Text(16))))
	o.SetOwn("raw", wrapBuffer(vmInst, c.Raw))
	return vm.NewValueFromPlainObject(o)
}

func certChainToValue(vmInst *vm.VM, chain []*x509.Certificate, detailed bool) vm.Value {
	first := certToValue(vmInst, chain[0])
	if !detailed {
		return first
	}
	prev := first
	for i := 1; i <= len(chain); i++ {
		var next vm.Value
		if i < len(chain) {
			next = certToValue(vmInst, chain[i])
		} else {
			// The last cert's issuer is itself when self-signed (as
			// node reports), otherwise left pointing at itself too:
			// node fills in the trust-store root, which we don't have
			// as an object here.
			next = prev
		}
		prev.AsPlainObject().SetOwn("issuerCertificate", next)
		prev = next
		if i == len(chain) {
			break
		}
	}
	return first
}

// ---- root certificates ----

var (
	rootPEMOnce sync.Once
	rootPEMs    []string
)

func loadRootPEMs() []string {
	rootPEMOnce.Do(func() {
		for _, f := range []string{
			"/etc/ssl/cert.pem", "/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt",
			"/etc/ssl/ca-bundle.pem", "/etc/pki/tls/cacert.pem", "/usr/local/etc/openssl/cert.pem",
		} {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for {
				var blk *pem.Block
				blk, data = pem.Decode(data)
				if blk == nil {
					break
				}
				if blk.Type != "CERTIFICATE" {
					continue
				}
				rootPEMs = append(rootPEMs, strings.TrimRight(string(pem.EncodeToMemory(blk)), "\n"))
			}
			if len(rootPEMs) > 0 {
				break
			}
		}
	})
	return rootPEMs
}

func stringArrayOpt(opts *vm.PlainObject, key string) []string {
	if opts == nil {
		return nil
	}
	v, ok := opts.GetOwn(key)
	if !ok || !v.IsArray() {
		return nil
	}
	arr := v.AsArray()
	out := make([]string, arr.Length())
	for i := 0; i < arr.Length(); i++ {
		out[i] = arr.Get(i).ToString()
	}
	return out
}

func declareTLS() {
	registerJSShim("tls", tlsShim)
}

func installTLSNatives(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	g := gt.AsPlainObject()
	if g == nil {
		return
	}
	b := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	b.SetOwn("createSecureContext", vm.NewNativeFunction(1, false, "createSecureContext", func(args []vm.Value) (vm.Value, error) {
		var o *vm.PlainObject
		if len(args) > 0 && args[0].Type() == vm.TypeObject {
			o = args[0].AsPlainObject()
		}
		ctx, err := buildSecureContext(vmInst, o)
		if err != nil {
			return vm.Undefined, err
		}
		id := secureCtxSeq.Add(1)
		secureCtxRegistry.Store(id, ctx)
		out := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		out.SetOwn("__ctxId", vm.NumberValue(float64(id)))
		return vm.NewValueFromPlainObject(out), nil
	}))
	b.SetOwn("rootCertificates", vm.NewNativeFunction(0, false, "rootCertificates", func(_ []vm.Value) (vm.Value, error) {
		arr := vm.NewArray()
		for _, s := range loadRootPEMs() {
			arr.AsArray().Append(vm.NewString(s))
		}
		return arr, nil
	}))
	b.SetOwn("setDefaultCA", vm.NewNativeFunction(1, false, "setDefaultCA", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsArray() {
			return vm.Undefined, nil
		}
		arr := args[0].AsArray()
		pool := x509.NewCertPool()
		pems := make([][]byte, 0, arr.Length())
		for i := 0; i < arr.Length(); i++ {
			b := []byte(arr.Get(i).ToString())
			pems = append(pems, b)
			pool.AppendCertsFromPEM(b)
		}
		defaultCAMu.Lock()
		defaultCAPEMs = pems
		defaultCAPool = pool
		defaultCAMu.Unlock()
		return vm.Undefined, nil
	}))
	b.SetOwn("getCiphers", vm.NewNativeFunction(0, false, "getCiphers", func(_ []vm.Value) (vm.Value, error) {
		seen := map[string]bool{}
		var names []string
		for _, cs := range append(tls.CipherSuites(), tls.InsecureCipherSuites()...) {
			n := strings.ToLower(goToOpenSSLCipher(cs.Name))
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
		sort.Strings(names)
		arr := vm.NewArray()
		for _, n := range names {
			arr.AsArray().Append(vm.NewString(n))
		}
		return arr, nil
	}))
	g.SetOwn("__noderatiTLSBinding", vm.NewValueFromPlainObject(b))
}
