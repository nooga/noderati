package host

import (
	"net"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// An in-process DNS server with canned records keeps the resolver tests
// hermetic; the expected output was captured from real node v26 querying
// this same server.

func fakeDNSAnswer(q dnsmessage.Question) ([]dnsmessage.Resource, dnsmessage.RCode) {
	name := q.Name.String()
	h := func(t dnsmessage.Type) dnsmessage.ResourceHeader {
		return dnsmessage.ResourceHeader{Name: q.Name, Type: t, Class: dnsmessage.ClassINET, TTL: 300}
	}
	mustName := func(s string) dnsmessage.Name { return dnsmessage.MustNewName(s) }
	if name == "missing.test." {
		return nil, dnsmessage.RCodeNameError
	}
	if name == "fail.test." {
		return nil, dnsmessage.RCodeServerFailure
	}
	if name != "rec.test." && name != "4.3.2.1.in-addr.arpa." {
		return nil, dnsmessage.RCodeSuccess
	}
	var all []dnsmessage.Resource
	add := func(t dnsmessage.Type, body dnsmessage.ResourceBody) {
		if q.Type == t || q.Type == dnsmessage.TypeALL {
			all = append(all, dnsmessage.Resource{Header: h(t), Body: body})
		}
	}
	if name == "4.3.2.1.in-addr.arpa." {
		add(dnsmessage.TypePTR, &dnsmessage.PTRResource{PTR: mustName("host.rec.test.")})
		return all, dnsmessage.RCodeSuccess
	}
	add(dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{1, 2, 3, 4}})
	add(dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{5, 6, 7, 8}})
	add(dnsmessage.TypeAAAA, &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 0x01, 0x0d, 0xb8, 15: 1}})
	add(dnsmessage.TypeMX, &dnsmessage.MXResource{Pref: 10, MX: mustName("mx.rec.test.")})
	add(dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"v=spf1", "-all"}})
	add(dnsmessage.TypeNS, &dnsmessage.NSResource{NS: mustName("ns1.rec.test.")})
	add(dnsmessage.TypeSRV, &dnsmessage.SRVResource{Priority: 1, Weight: 2, Port: 5060, Target: mustName("sip.rec.test.")})
	add(dnsmessage.TypeSOA, &dnsmessage.SOAResource{NS: mustName("ns1.rec.test."), MBox: mustName("admin.rec.test."), Serial: 2024, Refresh: 3600, Retry: 600, Expire: 86400, MinTTL: 60})
	add(dnsmessage.Type(257), &dnsmessage.UnknownResource{Type: 257, Data: append([]byte{0, 5}, "issueletsencrypt.org"...)})
	naptr := []byte{0, 100, 0, 10, 1, 'S', 7}
	naptr = append(naptr, "SIP+D2U"...)
	naptr = append(naptr, 0, 4)
	naptr = append(naptr, "_sip"...)
	naptr = append(naptr, 4)
	naptr = append(naptr, "_udp"...)
	naptr = append(naptr, 3)
	naptr = append(naptr, "rec"...)
	naptr = append(naptr, 4)
	naptr = append(naptr, "test"...)
	naptr = append(naptr, 0)
	add(dnsmessage.Type(35), &dnsmessage.UnknownResource{Type: 35, Data: naptr})
	add(dnsmessage.Type(52), &dnsmessage.UnknownResource{Type: 52, Data: []byte{3, 1, 1, 0xde, 0xad, 0xbe, 0xef}})
	if q.Type == dnsmessage.TypeCNAME {
		all = append(all, dnsmessage.Resource{Header: h(dnsmessage.TypeCNAME), Body: &dnsmessage.CNAMEResource{CNAME: mustName("alias.rec.test.")}})
	}
	return all, dnsmessage.RCodeSuccess
}

func serveFakeDNS(pc net.PacketConn) {
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		var m dnsmessage.Message
		if err := m.Unpack(buf[:n]); err != nil || len(m.Questions) == 0 {
			continue
		}
		ans, rcode := fakeDNSAnswer(m.Questions[0])
		resp := dnsmessage.Message{
			Header:    dnsmessage.Header{ID: m.Header.ID, Response: true, RecursionDesired: true, RecursionAvailable: true, RCode: rcode},
			Questions: m.Questions,
			Answers:   ans,
		}
		out, err := resp.Pack()
		if err != nil {
			continue
		}
		_, _ = pc.WriteTo(out, addr)
	}
}

func TestDNSResolverMatchesNode(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go serveFakeDNS(pc)
	addr := pc.LocalAddr().String()
	got := runScriptString(t, strings.ReplaceAll(`import dns from "node:dns";
import dnsp from "node:dns/promises";
const server = "__DNS__";
const out = [];
const r = new dns.Resolver({ timeout: 500, tries: 1 });
r.setServers([server]);
out.push(JSON.stringify(r.getServers()));
const cb = (fn, ...args) => new Promise((res) => fn.call(r, ...args, (err, v) => res(err ? "ERR " + err.code + " " + err.syscall + " " + err.hostname + " " + err.message : v)));
out.push(JSON.stringify(await cb(r.resolve4, "rec.test")));
out.push(JSON.stringify(await cb(r.resolve4, "rec.test", { ttl: true })));
out.push(JSON.stringify(await cb(r.resolve6, "rec.test")));
out.push(JSON.stringify(await cb(r.resolveMx, "rec.test")));
out.push(JSON.stringify(await cb(r.resolveTxt, "rec.test")));
out.push(JSON.stringify(await cb(r.resolveNs, "rec.test")));
out.push(JSON.stringify(await cb(r.resolveSrv, "rec.test")));
out.push(JSON.stringify(await cb(r.resolveSoa, "rec.test")));
out.push(JSON.stringify(await cb(r.resolveCaa, "rec.test")));
out.push(JSON.stringify(await cb(r.resolveNaptr, "rec.test")));
const tlsa = await cb(r.resolveTlsa, "rec.test");
out.push(JSON.stringify(tlsa.map((t) => ({ ...t, data: Buffer.from(t.data).toString("hex"), isAB: t.data instanceof ArrayBuffer }))));
out.push(JSON.stringify(await cb(r.resolveCname, "rec.test")));
out.push(JSON.stringify(await cb(r.resolve, "rec.test", "MX")));
out.push(JSON.stringify(await cb(r.reverse, "1.2.3.4")));
out.push(JSON.stringify(await cb(r.resolvePtr, "4.3.2.1.in-addr.arpa")));
out.push(await cb(r.resolve4, "missing.test"));
out.push(await cb(r.resolve4, "empty.test"));
out.push(await cb(r.resolve4, "fail.test"));
const any = await cb(r.resolveAny, "rec.test");
out.push(JSON.stringify(any.map((x) => x.type).filter(Boolean).sort()));
out.push(JSON.stringify(any.filter((x) => x.type === "MX" || x.type === "A")));
const pr = new dnsp.Resolver();
pr.setServers([server]);
out.push(JSON.stringify(await pr.resolveMx("rec.test")));
out.push(await pr.resolveTxt("missing.test").catch((e) => e.code + " " + e.syscall));
out.push([typeof dns.promises.resolve4, dns.promises === dnsp, typeof dnsp.lookup].join(","));
out.push(JSON.stringify(await dnsp.lookup("127.0.0.1", { all: true })));
try { r.setServers(["nope"]); } catch (e) { out.push(e.code); }
try { dns.setDefaultResultOrder("bad"); } catch (e) { out.push(e.code); }
out.push(dns.getDefaultResultOrder());
out.join("\n")
`, "__DNS__", addr))
	want := strings.ReplaceAll(`["__DNS__"]
["1.2.3.4","5.6.7.8"]
[{"address":"1.2.3.4","ttl":300},{"address":"5.6.7.8","ttl":300}]
["2001:db8::1"]
[{"exchange":"mx.rec.test","priority":10,"type":"MX"}]
[["v=spf1","-all"]]
["ns1.rec.test"]
[{"name":"sip.rec.test","port":5060,"priority":1,"weight":2,"type":"SRV"}]
{"nsname":"ns1.rec.test","hostmaster":"admin.rec.test","serial":2024,"refresh":3600,"retry":600,"expire":86400,"minttl":60}
[{"critical":0,"type":"CAA","issue":"letsencrypt.org"}]
[{"flags":"S","service":"SIP+D2U","regexp":"","replacement":"_sip._udp.rec.test","order":100,"preference":10}]
[{"certUsage":3,"selector":1,"match":1,"data":"deadbeef","isAB":true}]
["alias.rec.test"]
[{"exchange":"mx.rec.test","priority":10,"type":"MX"}]
["host.rec.test"]
["host.rec.test"]
ERR ENOTFOUND queryA missing.test queryA ENOTFOUND missing.test
ERR ENODATA queryA empty.test queryA ENODATA empty.test
ERR ESERVFAIL queryA fail.test queryA ESERVFAIL fail.test
["A","A","AAAA","CAA","MX","NAPTR","NS","SOA","SRV","TXT"]
[{"address":"1.2.3.4","ttl":300,"type":"A"},{"address":"5.6.7.8","ttl":300,"type":"A"},{"exchange":"mx.rec.test","priority":10,"type":"MX"}]
[{"exchange":"mx.rec.test","priority":10,"type":"MX"}]
ENOTFOUND queryTxt
function,true,function
[{"address":"127.0.0.1","family":4}]
ERR_INVALID_IP_ADDRESS
ERR_INVALID_ARG_VALUE
verbatim`, "__DNS__", addr)
	if got != want {
		t.Errorf("mismatch\n got: %s\nwant: %s", got, want)
	}
}
