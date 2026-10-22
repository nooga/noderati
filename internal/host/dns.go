package host

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
	"golang.org/x/net/dns/dnsmessage"
)

// dns.go is node:dns's native half.
//
//   - lookup()/lookupService() go through the system's resolution path
//     (Go's resolver honors /etc/hosts and nsswitch, like getaddrinfo).
//   - Every resolve*() goes over the wire to the configured name servers
//     with a small RFC 1035 client built on x/net/dns/dnsmessage, the way
//     c-ares does for Node: that's what makes setServers()/Resolver real
//     and covers the record types Go's net.Resolver can't return (CAA,
//     NAPTR, SOA, TLSA, ANY) plus TTLs.
//
// The JS half (dns_shim.go) owns argument validation, result shapes and
// error objects.

func declareDNS() {
	registerJSShim("dns", dnsShim)
	registerJSShim("dns/promises", dnsPromisesShim)
}

// systemNameServers reads nameserver lines from /etc/resolv.conf.
func systemNameServers() []string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return []string{"127.0.0.1"}
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	if len(out) == 0 {
		out = []string{"127.0.0.1"}
	}
	return out
}

var dnsTypeByName = map[string]dnsmessage.Type{
	"A": dnsmessage.TypeA, "AAAA": dnsmessage.TypeAAAA, "ANY": dnsmessage.TypeALL,
	"CAA": dnsmessage.Type(257), "CNAME": dnsmessage.TypeCNAME, "MX": dnsmessage.TypeMX,
	"NAPTR": dnsmessage.Type(35), "NS": dnsmessage.TypeNS, "PTR": dnsmessage.TypePTR,
	"SOA": dnsmessage.TypeSOA, "SRV": dnsmessage.TypeSRV, "TLSA": dnsmessage.Type(52),
	"TXT": dnsmessage.TypeTXT,
}

type dnsQueryError struct{ code string }

func (e *dnsQueryError) Error() string { return e.code }

// dnsExchange sends one query to one server, over UDP and (on truncation)
// TCP.
func dnsExchange(ctx context.Context, server string, q []byte) ([]byte, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := conn.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		if n < 12 || buf[0] != q[0] || buf[1] != q[1] {
			continue
		}
		resp := append([]byte(nil), buf[:n]...)
		if resp[2]&0x02 != 0 { // TC: retry over TCP
			return dnsExchangeTCP(ctx, server, q)
		}
		return resp, nil
	}
}

func dnsExchangeTCP(ctx context.Context, server string, q []byte) ([]byte, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	msg := make([]byte, 2+len(q))
	binary.BigEndian.PutUint16(msg, uint16(len(q)))
	copy(msg[2:], q)
	if _, err := conn.Write(msg); err != nil {
		return nil, err
	}
	var lb [2]byte
	if _, err := ioReadFull(conn, lb[:]); err != nil {
		return nil, err
	}
	resp := make([]byte, binary.BigEndian.Uint16(lb[:]))
	if _, err := ioReadFull(conn, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func ioReadFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func normalizeDNSServer(s string) string {
	if strings.HasPrefix(s, "[") {
		if _, _, err := net.SplitHostPort(s); err == nil {
			return s
		}
		return net.JoinHostPort(strings.Trim(s, "[]"), "53")
	}
	if ip := net.ParseIP(s); ip != nil {
		return net.JoinHostPort(s, "53")
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	return net.JoinHostPort(s, "53")
}

// dnsQuery resolves name/qtype against servers, c-ares style: each try
// walks the server list; the first authoritative answer (including an
// error rcode) wins.
func dnsQuery(servers []string, name string, qtype dnsmessage.Type, timeout time.Duration, tries int) (*dnsmessage.Message, error) {
	fqdn := name
	if !strings.HasSuffix(fqdn, ".") {
		fqdn += "."
	}
	qn, err := dnsmessage.NewName(fqdn)
	if err != nil {
		return nil, &dnsQueryError{"EBADNAME"}
	}
	if len(servers) == 0 {
		return nil, &dnsQueryError{"ECONNREFUSED"}
	}
	var lastErr error = &dnsQueryError{"ETIMEOUT"}
	for try := 0; try < tries; try++ {
		for _, srv := range servers {
			id := uint16(rand.IntN(65536))
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
			b.EnableCompression()
			_ = b.StartQuestions()
			_ = b.Question(dnsmessage.Question{Name: qn, Type: qtype, Class: dnsmessage.ClassINET})
			q, err := b.Finish()
			if err != nil {
				return nil, &dnsQueryError{"EBADQUERY"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			resp, err := dnsExchange(ctx, normalizeDNSServer(srv), q)
			cancel()
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					lastErr = &dnsQueryError{"ETIMEOUT"}
				} else {
					lastErr = &dnsQueryError{"ECONNREFUSED"}
				}
				continue
			}
			var m dnsmessage.Message
			if err := m.Unpack(resp); err != nil {
				lastErr = &dnsQueryError{"EBADRESP"}
				continue
			}
			switch m.Header.RCode {
			case dnsmessage.RCodeSuccess:
				return &m, nil
			case dnsmessage.RCodeNameError:
				return nil, &dnsQueryError{"ENOTFOUND"}
			case dnsmessage.RCodeServerFailure:
				lastErr = &dnsQueryError{"ESERVFAIL"}
				continue
			case dnsmessage.RCodeRefused:
				lastErr = &dnsQueryError{"EREFUSED"}
				continue
			case dnsmessage.RCodeFormatError:
				return nil, &dnsQueryError{"EFORMERR"}
			case dnsmessage.RCodeNotImplemented:
				return nil, &dnsQueryError{"ENOTIMP"}
			default:
				lastErr = &dnsQueryError{"ESERVFAIL"}
			}
		}
	}
	return nil, lastErr
}

func trimDot(s string) string { return strings.TrimSuffix(s, ".") }

// dnsRecordValue turns one answer RR into the plain object the shim
// formats (every record carries its type name and ttl).
func dnsRecordValue(vmInst *vm.VM, rr dnsmessage.Resource) (vm.Value, bool) {
	o := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	set := func(k string, v vm.Value) { o.SetOwn(k, v) }
	str := func(s string) vm.Value { return vm.NewString(s) }
	num := func(n float64) vm.Value { return vm.NumberValue(n) }
	set("ttl", num(float64(rr.Header.TTL)))
	switch b := rr.Body.(type) {
	case *dnsmessage.AResource:
		set("type", str("A"))
		set("address", str(net.IP(b.A[:]).String()))
	case *dnsmessage.AAAAResource:
		set("type", str("AAAA"))
		set("address", str(net.IP(b.AAAA[:]).String()))
	case *dnsmessage.CNAMEResource:
		set("type", str("CNAME"))
		set("value", str(trimDot(b.CNAME.String())))
	case *dnsmessage.NSResource:
		set("type", str("NS"))
		set("value", str(trimDot(b.NS.String())))
	case *dnsmessage.PTRResource:
		set("type", str("PTR"))
		set("value", str(trimDot(b.PTR.String())))
	case *dnsmessage.MXResource:
		set("type", str("MX"))
		set("exchange", str(trimDot(b.MX.String())))
		set("priority", num(float64(b.Pref)))
	case *dnsmessage.TXTResource:
		set("type", str("TXT"))
		arr := vm.NewArray()
		for _, t := range b.TXT {
			arr.AsArray().Append(str(t))
		}
		set("entries", arr)
	case *dnsmessage.SRVResource:
		set("type", str("SRV"))
		set("priority", num(float64(b.Priority)))
		set("weight", num(float64(b.Weight)))
		set("port", num(float64(b.Port)))
		set("name", str(trimDot(b.Target.String())))
	case *dnsmessage.SOAResource:
		set("type", str("SOA"))
		set("nsname", str(trimDot(b.NS.String())))
		set("hostmaster", str(trimDot(b.MBox.String())))
		set("serial", num(float64(b.Serial)))
		set("refresh", num(float64(b.Refresh)))
		set("retry", num(float64(b.Retry)))
		set("expire", num(float64(b.Expire)))
		set("minttl", num(float64(b.MinTTL)))
	case *dnsmessage.UnknownResource:
		d := b.Data
		switch rr.Header.Type {
		case 257: // CAA
			if len(d) < 2 || int(d[1])+2 > len(d) {
				return vm.Undefined, false
			}
			set("type", str("CAA"))
			set("critical", num(float64(d[0])))
			set("tag", str(string(d[2:2+int(d[1])])))
			set("value", str(string(d[2+int(d[1]):])))
		case 35: // NAPTR
			if len(d) < 4 {
				return vm.Undefined, false
			}
			set("type", str("NAPTR"))
			set("order", num(float64(binary.BigEndian.Uint16(d[0:]))))
			set("preference", num(float64(binary.BigEndian.Uint16(d[2:]))))
			p := 4
			readStr := func() string {
				if p >= len(d) {
					return ""
				}
				n := int(d[p])
				p++
				if p+n > len(d) {
					n = len(d) - p
				}
				s := string(d[p : p+n])
				p += n
				return s
			}
			set("flags", str(readStr()))
			set("service", str(readStr()))
			set("regexp", str(readStr()))
			// replacement is an uncompressed domain name (RFC 3403)
			var labels []string
			for p < len(d) {
				n := int(d[p])
				p++
				if n == 0 || p+n > len(d) {
					break
				}
				labels = append(labels, string(d[p:p+n]))
				p += n
			}
			set("replacement", str(strings.Join(labels, ".")))
		case 52: // TLSA
			if len(d) < 3 {
				return vm.Undefined, false
			}
			set("type", str("TLSA"))
			set("certUsage", num(float64(d[0])))
			set("selector", num(float64(d[1])))
			set("match", num(float64(d[2])))
			set("data", wrapBuffer(vmInst, append([]byte(nil), d[3:]...)))
		default:
			return vm.Undefined, false
		}
	default:
		return vm.Undefined, false
	}
	return vm.NewValueFromPlainObject(o), true
}

func reverseName(ip string) (string, bool) {
	p := net.ParseIP(ip)
	if p == nil {
		return "", false
	}
	if v4 := p.To4(); v4 != nil {
		return strconv.Itoa(int(v4[3])) + "." + strconv.Itoa(int(v4[2])) + "." + strconv.Itoa(int(v4[1])) + "." + strconv.Itoa(int(v4[0])) + ".in-addr.arpa", true
	}
	const hexd = "0123456789abcdef"
	var sb strings.Builder
	for i := len(p) - 1; i >= 0; i-- {
		sb.WriteByte(hexd[p[i]&0xf])
		sb.WriteByte('.')
		sb.WriteByte(hexd[p[i]>>4])
		sb.WriteByte('.')
	}
	sb.WriteString("ip6.arpa")
	return sb.String(), true
}

// serviceName maps a port to its /etc/services name (getnameinfo's
// NI_NAMEREQD-less behavior falls back to the number).
func serviceName(port int, proto string) string {
	f, err := os.Open("/etc/services")
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		want := strconv.Itoa(port) + "/" + proto
		for sc.Scan() {
			line := sc.Text()
			if i := strings.IndexByte(line, '#'); i >= 0 {
				line = line[:i]
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] == want {
				return fields[0]
			}
		}
	}
	return strconv.Itoa(port)
}

func installDNSNatives(p *driver.Paserati) {
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
	rt := vmInst.GetAsyncRuntime()
	b := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	arg := func(args []vm.Value, i int) vm.Value {
		if i < len(args) {
			return args[i]
		}
		return vm.Undefined
	}

	// lookup(hostname, family, cb(errCode|null, [{address,family}]))
	b.SetOwn("lookup", vm.NewNativeFunction(3, false, "lookup", func(args []vm.Value) (vm.Value, error) {
		hostname := arg(args, 0).ToString()
		family := int(arg(args, 1).ToFloat())
		cb := arg(args, 2)
		rt.BeginExternalOp()
		go func() {
			network := "ip"
			if family == 4 {
				network = "ip4"
			} else if family == 6 {
				network = "ip6"
			}
			ips, err := net.DefaultResolver.LookupIP(context.Background(), network, hostname)
			scheduleIO(rt, func() {
				defer rt.EndExternalOp()
				if err != nil || len(ips) == 0 {
					code := "ENOTFOUND"
					var de *net.DNSError
					if errors.As(err, &de) && de.IsTemporary && !de.IsNotFound {
						code = "EAI_AGAIN"
					}
					callJSCallback(vmInst, cb, vm.NewString(code))
					return
				}
				arr := vm.NewArray()
				for _, ip := range ips {
					o := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
					fam := 6
					if ip.To4() != nil {
						fam = 4
					}
					o.SetOwn("address", vm.NewString(ip.String()))
					o.SetOwn("family", vm.NumberValue(float64(fam)))
					arr.AsArray().Append(vm.NewValueFromPlainObject(o))
				}
				callJSCallback(vmInst, cb, vm.Null, arr)
			})
		}()
		return vm.Undefined, nil
	}))

	// lookupService(address, port, cb(errCode|null, hostname, service))
	b.SetOwn("lookupService", vm.NewNativeFunction(3, false, "lookupService", func(args []vm.Value) (vm.Value, error) {
		addr := arg(args, 0).ToString()
		port := int(arg(args, 1).ToFloat())
		cb := arg(args, 2)
		rt.BeginExternalOp()
		go func() {
			host := addr
			names, err := net.DefaultResolver.LookupAddr(context.Background(), addr)
			if err == nil && len(names) > 0 {
				host = trimDot(names[0])
			}
			svc := serviceName(port, "tcp")
			scheduleIO(rt, func() {
				defer rt.EndExternalOp()
				callJSCallback(vmInst, cb, vm.Null, vm.NewString(host), vm.NewString(svc))
			})
		}()
		return vm.Undefined, nil
	}))

	// query(servers[], name, rrtype, timeoutMs, tries, cb(errCode|null, records[]))
	b.SetOwn("query", vm.NewNativeFunction(6, false, "query", func(args []vm.Value) (vm.Value, error) {
		var servers []string
		if sv := arg(args, 0); sv.IsArray() {
			for i := 0; i < sv.AsArray().Length(); i++ {
				servers = append(servers, sv.AsArray().Get(i).ToString())
			}
		}
		name := arg(args, 1).ToString()
		rrtype := arg(args, 2).ToString()
		timeout := time.Duration(arg(args, 3).ToFloat()) * time.Millisecond
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		tries := int(arg(args, 4).ToFloat())
		if tries <= 0 {
			tries = 4
		}
		cb := arg(args, 5)
		qtype, ok := dnsTypeByName[rrtype]
		if !ok {
			return vm.Undefined, errors.New("unknown rrtype " + rrtype)
		}
		if rrtype == "PTR" {
			if rn, ok := reverseName(name); ok && net.ParseIP(name) != nil {
				name = rn
			}
		}
		rt.BeginExternalOp()
		go func() {
			msg, err := dnsQuery(servers, name, qtype, timeout, tries)
			scheduleIO(rt, func() {
				defer rt.EndExternalOp()
				if err != nil {
					code := "ESERVFAIL"
					var qe *dnsQueryError
					if errors.As(err, &qe) {
						code = qe.code
					}
					callJSCallback(vmInst, cb, vm.NewString(code))
					return
				}
				arr := vm.NewArray()
				for _, rr := range msg.Answers {
					if qtype != dnsmessage.TypeALL && rr.Header.Type != qtype {
						continue
					}
					if v, ok := dnsRecordValue(vmInst, rr); ok {
						arr.AsArray().Append(v)
					}
				}
				callJSCallback(vmInst, cb, vm.Null, arr)
			})
		}()
		return vm.Undefined, nil
	}))

	b.SetOwn("systemServers", vm.NewNativeFunction(0, false, "systemServers", func(_ []vm.Value) (vm.Value, error) {
		arr := vm.NewArray()
		for _, s := range systemNameServers() {
			arr.AsArray().Append(vm.NewString(s))
		}
		return arr, nil
	}))
	g.SetOwn("__noderatiDNSBinding", vm.NewValueFromPlainObject(b))
}
