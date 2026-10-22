package host

import "testing"

// TestNetServerAndSocketMatchNode: expected output captured from real node v26 running the same script.
func TestNetServerAndSocketMatchNode(t *testing.T) {
	got := runScriptString(t, `import net from "node:net";
import os from "node:os";
import path from "node:path";
const out = [];
// 1. unconnected socket does nothing
const fresh = new net.Socket();
let freshErr = null;
fresh.on("error", (e) => { freshErr = e.code; });
await new Promise((r) => setTimeout(r, 50));
out.push(["fresh", freshErr, fresh.readyState, fresh.pending, fresh.connecting, fresh.destroyed].join(","));
fresh.destroy();

// 2. server + socket.connect method, half-open, events
const events = []; let srvConn = false;
const srv = net.createServer({ allowHalfOpen: true }, (s) => {
  srvConn = true;
  let got = "";
  s.on("data", (d) => { got += d; });
  s.on("end", () => {
    events.push("srv:end:" + got);
    srv.getConnections((e, n) => {
      events.push("conns:" + n);
      s.end("reply");
    });
  });
});
await new Promise((r) => srv.listen(0, "127.0.0.1", r));
const addr = srv.address();
out.push(["addr", addr.address, addr.family, typeof addr.port, srv.listening].join(","));
const c = new net.Socket();
await new Promise((r) => c.connect(addr.port, "127.0.0.1", r));
events.push("client:connect:" + c.remoteAddress + ":" + (c.remotePort === addr.port) + ":" + c.remoteFamily + ":" + c.localAddress);
let reply = "";
c.setEncoding("utf8");
c.on("data", (d) => { reply += d; });
c.end("ping");
await new Promise((r) => c.on("close", r));
events.push("client:close:" + reply + ":" + c.bytesRead + ":" + c.bytesWritten + ":" + c.readyState);
await new Promise((r) => srv.close(r));
out.push(srvConn + "|" + events.join("|"));

// 3. EADDRINUSE
const a = net.createServer();
await new Promise((r) => a.listen(0, "127.0.0.1", r));
const b = net.createServer();
const err = await new Promise((r) => { b.on("error", r); b.listen(a.address().port, "127.0.0.1"); });
out.push([err.code, err.syscall, err.address, err.port === a.address().port, err.message.replace(/\d+$/, "N")].join(","));
a.close();

// 4. connect refused
const refused = await new Promise((r) => net.connect(1, "127.0.0.1").on("error", r));
out.push([refused.code, refused.syscall, refused.address, refused.port, refused.message].join(","));

// 5. unix socket
const sockPath = path.join(os.tmpdir(), "noderati-net-" + process.pid + ".sock");
const u = net.createServer((s) => s.end("unix-hi"));
await new Promise((r) => u.listen(sockPath, r));
const uaddr = u.address() === sockPath;
const uc = net.connect({ path: sockPath });
let ud = "";
uc.on("data", (d) => { ud += d; });
await new Promise((r) => uc.on("end", r));
await new Promise((r) => u.close(r));
out.push(["unix", uaddr, ud].join(","));

// 6. maxConnections + drop
const m = net.createServer((s) => {});
m.maxConnections = 1;
let drops = 0;
m.on("drop", (d) => { drops++; });
await new Promise((r) => m.listen(0, "127.0.0.1", r));
const m1 = net.connect(m.address().port, "127.0.0.1");
await new Promise((r) => m1.on("connect", r));
await new Promise((r) => setTimeout(r, 50));
const m2 = net.connect(m.address().port, "127.0.0.1");
await new Promise((r) => { m2.on("close", r); m2.on("error", () => {}); });
out.push(["drops", drops].join(","));
m1.destroy();
await new Promise((r) => m.close(r));

// 7. timeout event
const t = net.createServer(() => {});
await new Promise((r) => t.listen(0, "127.0.0.1", r));
const tc = net.connect(t.address().port, "127.0.0.1");
tc.setTimeout(30);
await new Promise((r) => tc.on("timeout", r));
out.push(["timeout", tc.timeout].join(","));
tc.destroy();
t.close();

// 8. listen arg errors
try { net.createServer().listen({}); } catch (e) { out.push(e.code); }
try { net.connect({ port: 70000 }); } catch (e) { out.push(e.code); }

// 9. close not running
const nr = await new Promise((r) => net.createServer().close(r));
out.push(nr.code);
out.join("\n")
`)
	want := `fresh,,open,true,false,false
addr,127.0.0.1,IPv4,number,true
true|client:connect:127.0.0.1:true:IPv4:127.0.0.1|srv:end:ping|conns:1|client:close:reply:5:4:closed
EADDRINUSE,listen,127.0.0.1,true,listen EADDRINUSE: address already in use 127.0.0.1:N
ECONNREFUSED,connect,127.0.0.1,1,connect ECONNREFUSED 127.0.0.1:1
unix,true,unix-hi
drops,1
timeout,30
ERR_INVALID_ARG_VALUE
ERR_SOCKET_BAD_PORT
ERR_SERVER_NOT_RUNNING`
	if got != want {
		t.Errorf("mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestNetBlockListSocketAddressAndDuplexMatchNode: expected output captured from real node v26 running the same script.
func TestNetBlockListSocketAddressAndDuplexMatchNode(t *testing.T) {
	got := runScriptString(t, `import net from "node:net";
import { Duplex, Readable, Writable } from "node:stream";
const out = [];
const bl = new net.BlockList();
bl.addAddress("1.2.3.4");
bl.addRange("10.0.0.1", "10.0.0.9");
bl.addSubnet("8592:757c:efae:4e45::", 64, "ipv6");
out.push(JSON.stringify(bl.rules));
out.push([bl.check("10.0.0.5"), bl.check("10.0.0.10"), bl.check("8592:757c:efae:4e45::f", "ipv6"), bl.check("::ffff:1.2.3.4", "ipv6"), bl.check("1.2.3.4", "ipv6"), net.BlockList.isBlockList(bl)].join(","));
const sa = new net.SocketAddress({ address: "::1", family: "ipv6", port: 80 });
out.push(JSON.stringify(sa) + "," + net.SocketAddress.isSocketAddress(sa) + "," + JSON.stringify(net.SocketAddress.parse("[::1]:8")) + "," + net.SocketAddress.parse("bad"));
try { bl.addAddress("nope"); } catch (e) { out.push(e.code); }
out.push([net.isIP("::1"), net.isIP("1.2.3.4"), net.isIP("01.2.3.4"), net.isIPv6("fe80::1%lo0"), net.getDefaultAutoSelectFamily(), net.getDefaultAutoSelectFamilyAttemptTimeout(), net.Stream === net.Socket].join(","));
const s = new net.Socket();
out.push([s instanceof Readable, typeof s.pipe, typeof s.unshift, typeof s[Symbol.asyncIterator], s.readableHighWaterMark, s.writableHighWaterMark, s.allowHalfOpen].join(","));
s.destroy();
// pipe + async iteration over a real connection
const srv = net.createServer((c) => c.pipe(c));
await new Promise((r) => srv.listen(0, "127.0.0.1", r));
const c = net.connect(srv.address().port, "127.0.0.1");
c.end("abc".repeat(1000));
let total = "";
for await (const chunk of c) total += chunk;
out.push(total.length);
await new Promise((r) => srv.close(r));
out.join("\n")
`)
	want := `["Subnet: IPv6 8592:757c:efae:4e45::/64","Range: IPv4 10.0.0.1-10.0.0.9","Address: IPv4 1.2.3.4"]
true,false,true,true,false,true
{"address":"::1","port":80,"family":"ipv6","flowlabel":0},true,{"address":"::1","port":8,"family":"ipv6","flowlabel":0},undefined
ERR_INVALID_ADDRESS
6,4,0,true,true,500,true
true,function,function,function,65536,65536,false
3000`
	if got != want {
		t.Errorf("mismatch\n got: %s\nwant: %s", got, want)
	}
}
