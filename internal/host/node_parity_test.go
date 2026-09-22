package host

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// runScriptString runs an ES module under noderati and returns its
// completion value as a string. Every expected value in this file was
// captured from real Node (v26) running the identical script.
func runScriptString(t *testing.T, script string) string {
	t.Helper()
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(script, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	return val.ToString()
}

func TestZlibSyncRoundTripsEveryFormat(t *testing.T) {
	got := runScriptString(t, `
		import zlib from "node:zlib";
		const text = "hello zlib ".repeat(500);
		const pairs = [["gzipSync","gunzipSync"],["deflateSync","inflateSync"],["deflateRawSync","inflateRawSync"],["gzipSync","unzipSync"],["deflateSync","unzipSync"]];
		JSON.stringify(pairs.map(([c, d]) => zlib[d](zlib[c](text)).toString() === text))
	`)
	if got != "[true,true,true,true,true]" {
		t.Errorf("got %s", got)
	}
}

// minizlib (tar's zlib layer) drives zlib classes only through the
// synchronous _processChunk, never as a stream.
func TestZlibClassProcessChunkIsSynchronous(t *testing.T) {
	got := runScriptString(t, `
		import zlib from "node:zlib";
		const g = new zlib.Gzip({});
		const parts = [g._processChunk(Buffer.from("abc"), zlib.constants.Z_NO_FLUSH),
		               g._processChunk(Buffer.from("def"), zlib.constants.Z_FINISH)];
		const u = new zlib.Unzip({});
		const whole = Buffer.concat(parts);
		const a = u._processChunk(whole.subarray(0, 7), zlib.constants.Z_NO_FLUSH);
		const b = u._processChunk(whole.subarray(7), zlib.constants.Z_FINISH);
		[typeof g._handle.close, Buffer.concat([a, b]).toString()].join(",")
	`)
	if got != "function,abcdef" {
		t.Errorf("got %s", got)
	}
}

func TestZlibErrorsMatchNode(t *testing.T) {
	got := runScriptString(t, `
		import zlib from "node:zlib";
		const g = (s) => zlib.gzipSync(s);
		const cases = [
			Buffer.concat([g("ab"), Buffer.from("zzzz")]),
			Buffer.concat([g("ab"), Buffer.alloc(8)]),
			Buffer.concat([g("ab"), Buffer.from([0x1f])]),
			Buffer.from("not gzip"),
			Buffer.alloc(0),
			Buffer.concat([g("ab"), g("cd")]),
		];
		JSON.stringify(cases.map((c) => { try { return "ok:" + zlib.gunzipSync(c); } catch (e) { return e.code + " " + e.errno + " " + e.message; } }))
	`)
	want := `["Z_DATA_ERROR -3 incorrect header check","ok:ab","Z_BUF_ERROR -5 unexpected end of file","Z_DATA_ERROR -3 incorrect header check","Z_BUF_ERROR -5 unexpected end of file","ok:abcd"]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestEventEmitterMatchesNodeListenerSemantics(t *testing.T) {
	got := runScriptString(t, `
		import { EventEmitter } from "node:events";
		const e = new EventEmitter();
		const log = [];
		e.on("newListener", (ev) => { if (ev !== "newListener") log.push("new:" + String(ev)); });
		e.on("removeListener", (ev) => log.push("rm:" + String(ev)));
		const f = () => log.push("f");
		e.once("x", f);
		e.removeListener("x", f);
		e.emit("x");
		log.push("afterOnceRemove:" + e.listenerCount("x"));
		e.on("a", () => log.push("a1"));
		e.prependListener("a", () => log.push("a0"));
		e.prependOnceListener("a", () => log.push("aOnce"));
		e.emit("a"); e.emit("a");
		const g = () => {};
		e.once("b", g);
		log.push("listeners:" + (e.listeners("b")[0] === g) + ",raw:" + (e.rawListeners("b")[0] !== g));
		const s = Symbol("s");
		e.on(s, () => {});
		log.push("names:" + e.eventNames().map(String).join("|"));
		e.removeAllListeners("a");
		log.push("chain:" + (e.addListener("c", g) === e) + (e.removeAllListeners() === e));
		log.push("names2:" + e.eventNames().length);
		log.join(" ")
	`)
	want := "new:removeListener new:x rm:x afterOnceRemove:0 new:a new:a new:a rm:a aOnce a0 a1 a0 a1 new:b listeners:true,raw:true new:Symbol(s) names:newListener|removeListener|a|b|Symbol(s) rm:a rm:a new:c rm:newListener rm:b rm:c rm:Symbol(s) chain:truetrue names2:0"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestFsFdReadWriteMatchesNode(t *testing.T) {
	dir := t.TempDir()
	got := runScriptString(t, fmt.Sprintf(`
		import fs from "node:fs";
		const f = %q + "/file.bin";
		const out = [];
		const fd = fs.openSync(f, "w", 0o640);
		out.push(fd > 2);
		out.push(fs.writeSync(fd, Buffer.from("hello world"), 0, 5));
		out.push(fs.writeSync(fd, " there"));
		out.push(fs.writeSync(fd, Buffer.from("J"), 0, 1, 0));
		await new Promise((r) => fs.write(fd, Buffer.from("ABCDEF"), 2, 3, null, (e, n) => { out.push(n); r(); }));
		await new Promise((r) => fs.writev(fd, [Buffer.from("<"), Buffer.from(">")], (e, n) => { out.push(n); r(); }));
		const st = fs.fstatSync(fd);
		out.push(st.size, st.isFile(), (st.mode & 0o777).toString(8));
		fs.closeSync(fd);
		out.push(fs.readFileSync(f, "utf8"));
		try { fs.closeSync(fd); } catch (e) { out.push(e.code, e.message); }
		const rfd = fs.openSync(f, "r");
		const buf = Buffer.alloc(8);
		out.push(fs.readSync(rfd, buf, 0, 4, null), buf.toString("utf8", 0, 4));
		out.push(fs.readSync(rfd, buf, 0, 3, 6), buf.toString("utf8", 0, 3));
		fs.closeSync(rfd);
		JSON.stringify(out)
	`, dir))
	want := `[true,5,6,1,3,2,16,true,"640","Jello thereCDE<>","EBADF","EBADF: bad file descriptor, close",4,"Jell",3,"the"]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestFsStatsHaveNodeShape(t *testing.T) {
	dir := t.TempDir()
	got := runScriptString(t, fmt.Sprintf(`
		import fs from "node:fs";
		const s = fs.statSync(%q);
		const keys = Object.keys(s).join(",");
		const lazy = s.mtime === s.mtime && s.mtime instanceof Date && Object.keys(s).includes("mtime");
		JSON.stringify([keys, lazy, s.isDirectory(), s.isFile(), fs.statSync(%q + "/nope", { throwIfNoEntry: false })])
	`, dir, dir))
	want := `["dev,mode,nlink,uid,gid,rdev,blksize,ino,size,blocks,atimeMs,mtimeMs,ctimeMs,birthtimeMs",true,true,false,null]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Real Node treats a numeric utimes argument as seconds, a Date as ms.
func TestFsUtimesNumberIsSeconds(t *testing.T) {
	f := filepath.Join(t.TempDir(), "u.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runScriptString(t, fmt.Sprintf(`
		import fs from "node:fs";
		const f = %q;
		fs.utimesSync(f, 1000, 2000);
		const a = fs.statSync(f).mtimeMs;
		fs.utimesSync(f, new Date(5000), new Date(6000));
		[a, fs.statSync(f).mtimeMs].join(",")
	`, f))
	if got != "2000000,6000" {
		t.Errorf("got %s", got)
	}
}

func TestFsMkdirRecursiveReturnsFirstCreated(t *testing.T) {
	dir := t.TempDir()
	got := runScriptString(t, fmt.Sprintf(`
		import fs from "node:fs";
		import fsp from "node:fs/promises";
		const root = %q;
		const a = fs.mkdirSync(root + "/a/b", { recursive: true }) === root + "/a";
		const b = fs.mkdirSync(root + "/a/b", { recursive: true }) === undefined;
		const c = (await fsp.mkdir(root + "/q/r", { recursive: true })) === root + "/q";
		const d = await new Promise((r) => fs.mkdir(root + "/x/y", 0o755, (e) => r(e.code)));
		JSON.stringify([a, b, c, d])
	`, dir))
	if got != `[true,true,true,"ENOENT"]` {
		t.Errorf("got %s", got)
	}
}

func TestProcessIdsAndUmask(t *testing.T) {
	got := runScriptString(t, `
		const before = process.umask();
		const prev = process.umask(0o027);
		const now = process.umask();
		process.umask(before);
		JSON.stringify([process.getuid(), process.getgid(), process.geteuid(), process.getegid(), prev === before, now])
	`)
	want := fmt.Sprintf(`[%d,%d,%d,%d,true,%d]`, os.Getuid(), os.Getgid(), os.Geteuid(), os.Getegid(), 0o027)
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
