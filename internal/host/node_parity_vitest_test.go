package host

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// Expected values in this file were captured from real Node (v26) running
// the identical script.

func TestV8SerializeMatchesNodeBytes(t *testing.T) {
	got := runScriptString(t, `
		import v8 from "node:v8";
		const circ = { name: "c" }; circ.self = circ;
		const shared = { s: 1 };
		const sparse = [1, , 3]; sparse.extra = "e";
		const cases = [undefined, null, true, 0, -1, 2147483647, 2147483648, 1.5, -0, NaN, 12345678901234567890n, -5n,
			"hello", "héllo", "日本", "😀", { a: 1, b: "x", 2: "int" }, [1, "two", null], sparse,
			new Date(1700000000123), /ab+c/gi, new Map([["k", 1], [2, { v: 3 }]]), new Set([1, "a", true]),
			[shared, shared], circ, Buffer.from([1, 2, 3]), new Uint8Array([4, 5]), new Int16Array([-1, 2]),
			new Float64Array([1.5]), new DataView(new ArrayBuffer(3)), new ArrayBuffer(4), Buffer.from([9, 8, 7, 6]).subarray(1, 3),
			new Number(3), new String("s"), new Boolean(false), new Array(5).fill(7), { a: undefined, b: 2 }];
		cases.map((v) => v8.serialize(v).toString("hex")).join(",")
	`)
	want := "ff0f5f,ff0f30,ff0f54,ff0f4900,ff0f4901,ff0f49feffffff0f,ff0f4e000000000000e041,ff0f4e000000000000f83f,ff0f4e0000000000000080,ff0f4e000000000000f87f,ff0f5a10d20a1feb8ca954ab,ff0f5a110500000000000000," +
		"ff0f220568656c6c6f,ff0f220568e96c6c6f,ff0f6304e5652c67,ff0f63043dd800de,ff0f6f49042203696e7422016149022201622201787b03,ff0f41034902220374776f30240003,ff0f6103490049024904490622056578747261220165400303," +
		"ff0f4400b08756febc7842,ff0f52220461622b6303,ff0f3b22016b490249046f22017649067b013a04,ff0f274902220161542c03," +
		"ff0f41026f22017349027b015e01240002,ff0f6f22046e616d65220163220473656c665e007b02,ff0f5c0a03010203,ff0f5c01020405,ff0f5c0304ffff0200," +
		"ff0f5c0808000000000000f83f,ff0f5c0903000000,ff0f420400000000,ff0f5c0a020807," +
		"ff0f6e0000000000000840,ff0f73220173,ff0f78,ff0f4105490e490e490e490e490e240005,ff0f6f2201615f22016249047b02"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestV8DeserializeRoundTripsAndRejectsLikeNode(t *testing.T) {
	got := runScriptString(t, `
		import v8 from "node:v8";
		const e1 = new RangeError("r", { cause: 42 }); e1.stack = "S";
		const d = v8.deserialize(v8.serialize(e1));
		const round = v8.deserialize(v8.serialize({ a: [1, , 3], m: new Map([[1, new Set([2])]]), d: new Date(5), r: /x/g, b: Buffer.from("hi"), u: new Uint16Array([7]), n: -0, big: 10n, s: "日本" }));
		const out = [d instanceof RangeError, d.message, d.stack, d.cause, round.a.length, 1 in round.a, round.m.get(1).has(2), round.d.getTime(), round.r.flags,
			Buffer.isBuffer(round.b), round.b.toString(), round.u[0], round.u.constructor.name, Object.is(round.n, -0), String(round.big), round.s];
		try { v8.deserialize(Buffer.from([0xff, 0x0f, 0x99])); } catch (e) { out.push(e.message); }
		try { v8.deserialize(Buffer.from([0x01])); } catch (e) { out.push(e.message); }
		try { v8.serialize(Promise.resolve()); } catch (e) { out.push(e.message); }
		JSON.stringify(out)
	`)
	want := `[true,"r","S",42,3,false,true,5,"g",true,"hi",7,"Uint16Array",true,"10","日本","Unable to deserialize cloned data.","Unable to deserialize cloned data due to invalid or unsupported version.","#<Promise> could not be cloned."]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestMessageChannelMatchesNode(t *testing.T) {
	got := runScriptString(t, `
		import { MessageChannel, MessagePort, receiveMessageOnPort } from "node:worker_threads";
		const log = [];
		try { new MessagePort(); } catch (e) { log.push("ctor:" + e.name + ":" + e.code); }
		const { port1, port2 } = new MessageChannel();
		log.push("inst:" + (port1 instanceof MessagePort) + ":" + (globalThis.MessageChannel === MessageChannel));
		port2.postMessage({ a: 1, nested: [1, { b: 2 }] });
		port2.postMessage("second");
		log.push("rmop:" + JSON.stringify(receiveMessageOnPort(port1)));
		const obj = { x: 1 };
		port1.on("message", (m) => log.push("p1 got:" + JSON.stringify(m) + ":" + (m === obj)));
		port2.postMessage(obj);
		await new Promise((r) => setTimeout(r, 20));
		let closes = 0; port1.on("close", () => closes++); port2.on("close", () => closes++);
		port2.close();
		await new Promise((r) => setTimeout(r, 20));
		log.push("closes:" + closes);
		try { port2.postMessage(() => 1); } catch (e) { log.push("fn:" + e.name); }
		const ev = new MessageChannel();
		ev.port1.addEventListener("message", (e) => log.push("event:" + e.data + ":" + (e instanceof Event)));
		ev.port1.start(); ev.port2.postMessage("hi");
		await new Promise((r) => setTimeout(r, 20));
		ev.port1.close();
		log.join(" | ")
	`)
	want := `ctor:TypeError:ERR_CONSTRUCT_CALL_INVALID | inst:true:true | rmop:{"message":{"a":1,"nested":[1,{"b":2}]}} | p1 got:"second":false | p1 got:{"x":1}:false | closes:2 | fn:DataCloneError | event:hi:true`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Both ends of one socketpair inside a single VM: the same framing and
// ordering fork()'s parent and child use.
func TestIPCChannelJSONFramingAndDisconnect(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	vmInst := p.GetVM()
	a := newEventEmitterObject(vmInst)
	b := newEventEmitterObject(vmInst)
	setupIPCChannel(vmInst, a, ipcFile(fds[0], "a"), fds[0], "json", false)
	setupIPCChannel(vmInst, b, ipcFile(fds[1], "b"), fds[1], "json", false)
	gt, _ := vmInst.GetGlobal("globalThis")
	gt.AsPlainObject().SetOwn("__ipcA", vm.NewValueFromPlainObject(a))
	gt.AsPlainObject().SetOwn("__ipcB", vm.NewValueFromPlainObject(b))
	val, errs := p.RunCode(`
		const a = globalThis.__ipcA, b = globalThis.__ipcB;
		const got = [];
		const done = new Promise((resolve) => {
			// The reply is sent immediately before disconnect(): it must
			// still reach a before a's 'disconnect'.
			b.on("message", (m) => { got.push(JSON.stringify(m)); if (m === "last") { b.send("reply"); b.disconnect(); } });
			a.on("message", (m) => got.push("a got:" + m));
			a.on("disconnect", () => { got.push("a disconnect:" + a.connected); resolve(); });
		});
		a.send({ n: 1, buf: Buffer.from("hé") });
		a.send([1, "two", null]);
		a.send("last", (err) => got.push("send cb:" + err));
		await done;
		a.on("error", (e) => got.push("error:" + e.code));
		got.push("after:" + a.send("x"));
		await new Promise((r) => setImmediate(r));
		got.join(" | ")
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `send cb:null | {"n":1,"buf":{"type":"Buffer","data":[104,195,169]}} | [1,"two",null] | "last" | a got:reply | a disconnect:false | after:false | error:ERR_IPC_CHANNEL_CLOSED`
	if val.ToString() != want {
		t.Errorf("got  %s\nwant %s", val.ToString(), want)
	}
}

func TestURLResolvesDotSegmentsAndObjectBase(t *testing.T) {
	got := runScriptString(t, `
		import { fileURLToPath } from "node:url";
		const out = ["file:///a/b/index.js/../entry/p.js", "http://x.com/a/./b/.", "http://x.com/a/b/..", "http://x.com/%2e%2E/a", "http://x.com/a/.%2e/b",
			"http://x.com/a/b/../../../..", "file:///C:/../x", "http://x.com/a//b/../c", "foo://h/a/../b"].map((u) => new URL(u).href);
		out.push(fileURLToPath("file:///a/./b/%20c/../d"), new URL("./d.js", new URL("file:///a/b/c.js")).href, new URL("?q=1", "http://h/p").href);
		JSON.stringify(out)
	`)
	want := `["file:///a/b/entry/p.js","http://x.com/a/b/","http://x.com/a/","http://x.com/a","http://x.com/b","http://x.com/","file:///C:/x","http://x.com/a//c","foo://h/b","/a/b/d","file:///a/b/d.js","http://h/p?q=1"]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Node matches export conditions in the exports object's own key order,
// with --conditions entries added to the active set.
func TestExportsConditionsFollowKeyOrderAndUserConditions(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "node_modules", "cond-pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"package.json": `{"name":"cond-pkg","exports":{".":{"custom":"./custom.mjs","import":"./import.mjs","node":"./node.mjs","default":"./default.mjs"}}}`,
		"custom.mjs":   `export default "custom";`,
		"import.mjs":   `export default "import";`,
		"node.mjs":     `export default "node";`,
		"default.mjs":  `export default "default";`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(pkg, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	main := filepath.Join(dir, "main.mjs")
	if err := os.WriteFile(main, []byte(`import v from "cond-pkg"; globalThis.__cond = v;`), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		p := New([]string{"noderati", main})
		p.SetSkipTypeCheck(true)
		src, _ := os.ReadFile(main)
		if _, errs := p.RunCode(string(src), driver.RunOptions{ModuleName: main}); len(errs) > 0 {
			t.Fatalf("RunCode: %v", errs[0])
		}
		v, _ := p.RunCode(`globalThis.__cond`, driver.RunOptions{})
		return v.ToString()
	}
	if got := run(); got != "import" {
		t.Errorf("without user conditions: got %q, want %q", got, "import")
	}
	UserConditions = []string{"custom"}
	defer func() { UserConditions = nil }()
	if got := run(); got != "custom" {
		t.Errorf("with --conditions custom: got %q, want %q", got, "custom")
	}
}

func TestBufferFromObjectShapes(t *testing.T) {
	got := runScriptString(t, `
		const back = Buffer.from(JSON.parse(JSON.stringify({ b: Buffer.from([1, 2, 255]) })).b);
		JSON.stringify([Buffer.isBuffer(back), [...back].join(), [...Buffer.from(new String("hi"))].join(), Buffer.from({ length: "x" }).length, [...Buffer.from({ length: 2, 0: 7, 1: 8 })].join()])
	`)
	if got != `[true,"1,2,255","104,105",0,"7,8"]` {
		t.Errorf("got %s", got)
	}
}

// A module exporting one mutable i32 global (42) and one immutable f64
// global (2.5), hand-assembled.
var wasmGlobalsModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x06, 0x12, 0x02,
	0x7f, 0x01, 0x41, 0x2a, 0x0b,
	0x7c, 0x00, 0x44, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x04, 0x40, 0x0b,
	0x07, 0x09, 0x02,
	0x01, 'g', 0x03, 0x00,
	0x01, 'h', 0x03, 0x01,
}

func TestWasmExportedGlobals(t *testing.T) {
	got := runScriptString(t, fmt.Sprintf(`
		const bytes = new Uint8Array(%s);
		const { exports } = new WebAssembly.Instance(new WebAssembly.Module(bytes), {});
		const out = [exports.g instanceof WebAssembly.Global, exports.g.value, exports.h.value, exports.g.valueOf()];
		exports.g.value = 7;
		out.push(exports.g.value);
		try { exports.h.value = 1; } catch (e) { out.push(e.name + ":" + e.message); }
		JSON.stringify(out)
	`, jsByteArray(wasmGlobalsModule)))
	if got != `[true,42,2.5,42,7,"TypeError:set WebAssembly.Global.value): Can't set the value of an immutable global."]` {
		t.Errorf("got %s", got)
	}
}

func jsByteArray(b []byte) string {
	s := "["
	for i, x := range b {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprint(x)
	}
	return s + "]"
}

func TestImportFileURLSpecifier(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "m.mjs")
	if err := os.WriteFile(mod, []byte(`export const x = 41;`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runScriptString(t, fmt.Sprintf(`
		const m = await import(%q);
		const n = await import(%q);
		String(m.x + 1) + "," + (n.x === m.x)
	`, "file://"+mod, mod))
	if got != "42,true" {
		t.Errorf("got %s", got)
	}
}

func TestProcessMemoryAndCPUUsageShape(t *testing.T) {
	got := runScriptString(t, `
		const m = process.memoryUsage(), c = process.cpuUsage();
		JSON.stringify([Object.keys(m).join(), typeof process.memoryUsage.rss(), m.rss > 0, Object.keys(c).join(), Object.keys(process.cpuUsage(c)).join(),
			typeof process.stdout.setMaxListeners, process.stdout.setMaxListeners(20) === process.stdout, process.stdout.getMaxListeners()])
	`)
	if got != `["rss,heapTotal,heapUsed,external,arrayBuffers","number",true,"user,system","user,system","function",true,20]` {
		t.Errorf("got %s", got)
	}
}
