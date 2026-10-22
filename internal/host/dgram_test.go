package host

import "testing"

// TestDgramMatchesNode: expected output captured from real node v26 running the same script.
func TestDgramMatchesNode(t *testing.T) {
	got := runScriptString(t, `import dgram from "node:dgram";
import { once } from "node:events";
import dns from "node:dns";
const out = [];
const log = (...a) => out.push(a.map((x) => typeof x === "string" ? x : JSON.stringify(x)).join(" "));
const err = (e) => [e.name, e.code, e.message].join("|");
const t = (fn) => { try { return fn(); } catch (e) { return err(e); } };

log(Object.keys(dgram).join(","));
log(t(() => dgram.createSocket("udp5")));
log(t(() => dgram.createSocket({ type: "x" })));
log(t(() => dgram.createSocket()));

// unbound socket errors
const u = dgram.createSocket("udp4");
log(t(() => u.address()));
log(t(() => u.remoteAddress()));
log(t(() => u.disconnect()));
log(t(() => u.setBroadcast(true)));
log(t(() => u.setTTL(5)));
log(t(() => u.setTTL("x")));
log(t(() => u.getRecvBufferSize()));
log(t(() => u.addMembership()));
log(u.getSendQueueSize(), u.getSendQueueCount());
log(u.ref() === u, u.unref() === u);
log(u instanceof dgram.Socket, typeof u.on, u.constructor.name);
u.close();
log(t(() => u.close()));
log(t(() => u.send("x", 1, "127.0.0.1")));

// server/client roundtrip
const server = dgram.createSocket("udp4");
const events = [];
server.on("listening", () => events.push("listening"));
server.bind(0, "127.0.0.1");
log(t(() => server.bind(0)));
await once(server, "listening");
const sa = server.address();
log(sa.address, sa.family, typeof sa.port);
const client = dgram.createSocket({ type: "udp4" });
const got = [];
server.on("message", (msg, rinfo) => {
  got.push([msg.toString(), Buffer.isBuffer(msg), rinfo.address, rinfo.family, rinfo.size, typeof rinfo.port]);
  if (got.length === 1) server.send(Buffer.from("pong"), rinfo.port, rinfo.address);
});
const reply = once(client, "message");
await new Promise((res) => client.send("ping", sa.port, "127.0.0.1", (e, n) => { log("sent", e, n); res(); }));
const [rmsg, rinfo] = await reply;
log("reply", rmsg.toString(), rinfo.port === sa.port, rinfo.address);
const ca = client.address();
log(ca.address, ca.family);

// array, offset/length, typed array, default address
await new Promise((res) => client.send(["a", Buffer.from("b"), new Uint8Array([99])], sa.port, (e, n) => { log("arr", e, n); res(); }));
await new Promise((res) => client.send(Buffer.from("hello world"), 6, 5, sa.port, "127.0.0.1", (e, n) => { log("off", e, n); res(); }));
await new Promise((res) => client.send("abcdef", 1, 3, sa.port, "localhost", (e, n) => { log("stroff", e, n); res(); }));
await new Promise((r) => setTimeout(r, 50));
log(got);
log(t(() => client.send(123, sa.port)));
log(t(() => client.send("x", 0)));
log(t(() => client.send("x", 70000)));
log(t(() => client.send("x", "abc")));
log(t(() => client.send(Buffer.from("x"), 5, 1, sa.port)));

// connect
const c2 = dgram.createSocket("udp4");
c2.on("connect", () => events.push("connect"));
await new Promise((res) => c2.connect(sa.port, "127.0.0.1", res));
const ra = c2.remoteAddress();
log("remote", ra.address, ra.family, ra.port === sa.port);
log(t(() => c2.connect(sa.port)));
log(t(() => c2.send("x", sa.port, "127.0.0.1")));
await new Promise((res) => c2.send("conn", (e, n) => { log("csent", e, n); res(); }));
await new Promise((r) => setTimeout(r, 50));
log(got[got.length - 1][0]);
c2.disconnect();
log(t(() => c2.remoteAddress()));
log(t(() => c2.disconnect()));
await new Promise((res) => c2.send("after", sa.port, "127.0.0.1", (e, n) => { log("dsent", e, n); res(); }));

// connect on unbound socket to a missing host errors
const fakeLookup = (host, family, cb) => host.endsWith(".invalid")
  ? process.nextTick(cb, Object.assign(new Error("getaddrinfo ENOTFOUND " + host), { code: "ENOTFOUND", syscall: "getaddrinfo" }))
  : dns.lookup(host, family, cb);
const c3 = dgram.createSocket({ type: "udp4", lookup: fakeLookup });
c3.on("error", (e) => log("c3 error", e.code, e.syscall));
c3.connect(1, "this-host-does-not-exist.invalid");
await new Promise((r) => setTimeout(r, 50));
c3.close();
log(t(() => dgram.createSocket({ type: "udp4", lookup: 5 })));

// options
log(server.setBroadcast(true), server.setTTL(64), server.setMulticastTTL(2), server.setMulticastLoopback(true));
log(t(() => server.setTTL(0)));
log(t(() => server.setMulticastInterface(5)));
server.setRecvBufferSize(65536);
server.setSendBufferSize(65536);
log(typeof server.getRecvBufferSize(), server.getSendBufferSize() >= 65536);
log(t(() => server.setRecvBufferSize(-1)));
log(t(() => server.addMembership("224.0.0.114")));
log(t(() => server.dropMembership("224.0.0.114")));
log(t(() => server.addMembership("1.2.3.4")));

// bind errors: EADDRINUSE
const dup = dgram.createSocket("udp4");
const dupErr = once(dup, "error");
dup.bind(sa.port, "127.0.0.1");
const [de] = await dupErr;
log("dup", de.code, de.syscall, de.address, de.port === sa.port, de.message.replace(String(sa.port), "PORT"));

// bind options & exclusive/reuseAddr
const r1 = dgram.createSocket({ type: "udp4", reuseAddr: true });
const r2 = dgram.createSocket({ type: "udp4", reuseAddr: true });
await new Promise((res) => r1.bind({ port: 0, address: "127.0.0.1" }, res));
await new Promise((res) => r2.bind({ port: r1.address().port, address: "127.0.0.1" }, res));
log("reuse", r1.address().port === r2.address().port);
r1.close(); r2.close();

// udp6
const s6 = dgram.createSocket("udp6");
await new Promise((res) => s6.bind(0, "::1", res));
const a6 = s6.address();
log(a6.address, a6.family);
const m6 = once(s6, "message");
const c6 = dgram.createSocket("udp6");
c6.send("six", a6.port, "::1");
const [msg6, ri6] = await m6;
log(msg6.toString(), ri6.address, ri6.family);
c6.close(); s6.close();

// close: callback and event
const closeEvents = [];
client.on("close", () => closeEvents.push("event"));
client.close(() => closeEvents.push("cb"));
log(t(() => client.address()));
await new Promise((r) => setImmediate(r));
log(closeEvents);
c2.close(); dup.close();
server.close();
log(events);

// async dispose
const ad = dgram.createSocket("udp4");
log(typeof ad[Symbol.asyncDispose]);
await ad[Symbol.asyncDispose]();
log(t(() => ad.close()));

// abort signal
const ac = new AbortController();
const as = dgram.createSocket({ type: "udp4", signal: ac.signal });
const asClose = once(as, "close");
ac.abort();
await asClose;
log("aborted closed");
out.join("\n")
`)
	want := `createSocket,Socket
TypeError|ERR_SOCKET_BAD_TYPE|Bad socket type specified. Valid types are: udp4, udp6
TypeError|ERR_SOCKET_BAD_TYPE|Bad socket type specified. Valid types are: udp4, udp6
TypeError|ERR_SOCKET_BAD_TYPE|Bad socket type specified. Valid types are: udp4, udp6
Error|EBADF|getsockname EBADF
Error|ERR_SOCKET_DGRAM_NOT_CONNECTED|Not connected
Error|ERR_SOCKET_DGRAM_NOT_CONNECTED|Not connected
Error|EBADF|setBroadcast EBADF
Error|EBADF|setTTL EBADF
TypeError|ERR_INVALID_ARG_TYPE|The "ttl" argument must be of type number. Received type string ('x')
SystemError|ERR_SOCKET_BUFFER_SIZE|Could not get or set buffer size: uv_recv_buffer_size returned EBADF (bad file descriptor)
TypeError|ERR_MISSING_ARGS|The "multicastAddress" argument must be specified
0 0
true true
true function Socket
Error|ERR_SOCKET_DGRAM_NOT_RUNNING|Not running
Error|ERR_SOCKET_DGRAM_NOT_RUNNING|Not running
Error|ERR_SOCKET_ALREADY_BOUND|Socket is already bound
127.0.0.1 IPv4 number
sent null 4
reply pong true 127.0.0.1
0.0.0.0 IPv4
arr null 3
off null 5
stroff null 3
[["ping",true,"127.0.0.1","IPv4",4,"number"],["abc",true,"127.0.0.1","IPv4",3,"number"],["world",true,"127.0.0.1","IPv4",5,"number"],["bcd",true,"127.0.0.1","IPv4",3,"number"]]
TypeError|ERR_INVALID_ARG_TYPE|The "buffer" argument must be of type string or an instance of Buffer, TypedArray, or DataView. Received type number (123)
RangeError|ERR_SOCKET_BAD_PORT|Port should be > 0 and < 65536. Received type number (0).
RangeError|ERR_SOCKET_BAD_PORT|Port should be > 0 and < 65536. Received type number (70000).
RangeError|ERR_SOCKET_BAD_PORT|Port should be > 0 and < 65536. Received type string ('abc').
RangeError|ERR_BUFFER_OUT_OF_BOUNDS|"offset" is outside of buffer bounds
remote 127.0.0.1 IPv4 true
Error|ERR_SOCKET_DGRAM_IS_CONNECTED|Already connected

csent null 4
conn
Error|ERR_SOCKET_DGRAM_NOT_CONNECTED|Not connected
Error|ERR_SOCKET_DGRAM_NOT_CONNECTED|Not connected
dsent null 5
c3 error ENOTFOUND getaddrinfo
TypeError|ERR_INVALID_ARG_TYPE|The "lookup" argument must be of type function. Received type number (5)
 64 2 true
Error|EINVAL|setTTL EINVAL
TypeError|ERR_INVALID_ARG_TYPE|The "interfaceAddress" argument must be of type string. Received type number (5)
number true
TypeError|ERR_SOCKET_BAD_BUFFER_SIZE|Buffer size must be a positive integer


Error|EINVAL|addMembership EINVAL
dup EADDRINUSE bind 127.0.0.1 true bind EADDRINUSE 127.0.0.1:PORT
reuse true
::1 IPv6
six ::1 IPv6
Error|ERR_SOCKET_DGRAM_NOT_RUNNING|Not running
["event","cb"]
["listening","connect"]
function
Error|ERR_SOCKET_DGRAM_NOT_RUNNING|Not running
aborted closed`
	if got != want {
		t.Errorf("mismatch\n got: %s\nwant: %s", got, want)
	}
}
