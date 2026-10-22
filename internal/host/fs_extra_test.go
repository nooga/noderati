package host

import (
	"runtime"
	"strings"
	"testing"
)

// Expected values in this file come from running the same scripts under
// real Node v26.

func fsTestScript(dir, body string) string {
	return `
		import fs from "node:fs";
		import fsp from "node:fs/promises";
		import path from "node:path";
		const dir = ` + "`" + dir + "`" + `;
		const D = (v) => JSON.stringify(v, (k, x) => typeof x === "string" ? x.split(dir).join("D") : typeof x === "bigint" ? "big" : x);
		const wait = (ms) => new Promise((r) => setTimeout(r, ms));
	` + body
}

func TestFSConstantsMatchNode(t *testing.T) {
	got := runScriptString(t, `
		import fs from "node:fs";
		import fsp from "node:fs/promises";
		const c = fs.constants;
		JSON.stringify([Object.keys(c).slice(0, 5), c.S_IFMT, c.S_IFLNK, c.S_IRWXU, c.COPYFILE_FICLONE_FORCE, c.UV_DIRENT_BLOCK,
			c.O_CREAT === require_("O_CREAT"), fsp.constants === c, Object.keys(c).length]);
		function require_(n) { return c[n]; }
	`)
	n := "55"
	if runtime.GOOS == "linux" {
		n = "56"
	}
	want := `[["UV_FS_SYMLINK_DIR","UV_FS_SYMLINK_JUNCTION","O_RDONLY","O_WRONLY","O_RDWR"],61440,40960,448,4,7,true,true,` + n + `]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestFSErrorsCarryDestAndLibuvText(t *testing.T) {
	dir := t.TempDir()
	got := runScriptString(t, fsTestScript(dir, `
		fs.writeFileSync(dir + "/f", "x");
		fs.mkdirSync(dir + "/sub");
		const out = [];
		const tryit = (fn) => { try { fn(); out.push("ok"); } catch (e) { out.push([e.code, e.message, e.syscall, e.path, e.dest, e.errno]); } };
		tryit(() => fs.copyFileSync(dir + "/f", dir + "/f2", fs.constants.COPYFILE_EXCL));
		tryit(() => fs.copyFileSync(dir + "/f", dir + "/f2", fs.constants.COPYFILE_EXCL));
		tryit(() => fs.renameSync(dir + "/nope", dir + "/x"));
		tryit(() => fs.symlinkSync("t", dir + "/f"));
		tryit(() => fs.rmSync(dir + "/nope"));
		tryit(() => fs.rmSync(dir + "/nope", { force: true }));
		tryit(() => fs.rmSync(dir + "/sub"));
		tryit(() => fs.mkdtempSync("/nonexistent-dir/x"));
		tryit(() => fs.truncateSync(dir + "/nope"));
		D(out)
	`))
	want := `["ok",["EEXIST","EEXIST: file already exists, copyfile 'D/f' -> 'D/f2'","copyfile","D/f","D/f2",-17],` +
		`["ENOENT","ENOENT: no such file or directory, rename 'D/nope' -> 'D/x'","rename","D/nope","D/x",-2],` +
		`["EEXIST","EEXIST: file already exists, symlink 't' -> 'D/f'","symlink","t","D/f",-17],` +
		`["ENOENT","ENOENT: no such file or directory, lstat 'D/nope'","lstat","D/nope",null,-2],"ok",` +
		`["ERR_FS_EISDIR","Path is a directory: rm returned EISDIR (is a directory) D/sub","rm","D/sub",null,21],` +
		`["ENOENT","ENOENT: no such file or directory, mkdtemp '/nonexistent-dir/xXXXXXX'","mkdtemp","/nonexistent-dir/xXXXXXX",null,-2],` +
		`["ENOENT","ENOENT: no such file or directory, open 'D/nope'","open","D/nope",null,-2]]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestFSStatsDirentClasses(t *testing.T) {
	dir := t.TempDir()
	got := runScriptString(t, fsTestScript(dir, `
		fs.mkdirSync(dir + "/sub/deep", { recursive: true });
		fs.writeFileSync(dir + "/sub/deep/g.js", "x");
		fs.writeFileSync(dir + "/sub/h.js", "x");
		const s = fs.statSync(dir);
		const big = fs.statSync(dir, { bigint: true });
		const legacy = new fs.Stats(1, 33188, 1, 2, 3, 4, 4096, 5, 6, 7, 1000, 2000, 3000, 4000);
		const d = new fs.Dirent("n", fs.constants.UV_DIRENT_DIR, "/p");
		const ents = fs.readdirSync(dir + "/sub", { recursive: true, withFileTypes: true }).map((e) => [e.name, e.parentPath, e.isFile()]).sort();
		D([s instanceof fs.Stats, s.constructor.name, big instanceof fs.Stats, big.constructor.name, typeof big.mtimeNs, big.isDirectory(),
		   legacy.atimeMs, legacy.mtime.toISOString(), legacy.isFile(),
		   d.name, d.parentPath, d.isDirectory(), Object.keys(d), d instanceof fs.Dirent,
		   fs.readdirSync(dir, { recursive: true }).sort(), ents,
		   typeof globalThis.Temporal === "undefined" || s.mtimeInstant.epochMilliseconds === Math.trunc(s.mtimeMs)])
	`))
	want := `[true,"Stats",false,"BigIntStats","bigint",true,1000000.002,"1970-01-01T00:50:00.000Z",true,"n","/p",true,["name","parentPath"],true,` +
		`["sub","sub/deep","sub/deep/g.js","sub/h.js"],[["deep","D/sub",false],["g.js","D/sub/deep",true],["h.js","D/sub",true]],true]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestFSGlobSync(t *testing.T) {
	dir := t.TempDir()
	got := runScriptString(t, fsTestScript(dir, `
		for (const f of [".hidden", "a.js", "b.ts", ".dot/x.js", "src/c.js", "src/.d.js", "src/lib/e.js", "src/lib/f.md", "node_modules/m/i.js"]) {
			fs.mkdirSync(path.dirname(path.join(dir, f)), { recursive: true });
			fs.writeFileSync(path.join(dir, f), "");
		}
		fs.symlinkSync(path.join(dir, "src"), path.join(dir, "link"));
		const g = (p, o = {}) => fs.globSync(p, { cwd: dir, ...o }).sort();
		D([g("*"), g("**"), g("**/*.js"), g(".*"), g("*.{js,ts}"), g("src/[a-c].js"), g("link/*"), g("src/*/"), g("src/lib/../c.js"),
		   g(["a.js", "a.js", "*.js"]), g("**/*.js", { exclude: ["node_modules/**"] }), g("@(a|b).js"),
		   fs.globSync("src/*", { cwd: dir, withFileTypes: true }).map((e) => [e.name, e.parentPath]).sort(),
		   fs.globSync(dir + "/s*/*.js")])
	`))
	want := `[["a.js","b.ts","link","node_modules","src"],` +
		`[".","a.js","b.ts","link","node_modules","node_modules/m","node_modules/m/i.js","src","src/c.js","src/lib","src/lib/e.js","src/lib/f.md"],` +
		`["a.js","node_modules/m/i.js","src/c.js","src/lib/e.js"],[".dot",".hidden"],["a.js","b.ts"],["src/c.js"],["link/c.js","link/lib"],` +
		`["src/lib"],["src/c.js"],["a.js"],["a.js","src/c.js","src/lib/e.js"],["a.js"],[["c.js","D/src"],["lib","D/src"]],["D/src/c.js"]]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestFSReadWriteStreamEventOrder(t *testing.T) {
	dir := t.TempDir()
	got := runScriptString(t, fsTestScript(dir, `
		const f = dir + "/a.txt";
		fs.writeFileSync(f, "hello world, this is a test file\n".repeat(3));
		const log = [];
		const rs = fs.createReadStream(f, { highWaterMark: 16, start: 2, end: 40, encoding: "utf8" });
		log.push(["pending", rs.pending, rs.bytesRead, rs.fd, rs.flags, rs.mode, rs.pos]);
		for (const ev of ["open", "ready", "end", "close"]) rs.on(ev, (a) => log.push(["r:" + ev, typeof a, rs.pending, rs.bytesRead]));
		rs.on("data", (d) => log.push(["r:data", d, rs.bytesRead]));
		await new Promise((r) => rs.on("close", r));
		const ws = fs.createWriteStream(dir + "/b.txt");
		for (const ev of ["open", "ready", "finish", "close"]) ws.on(ev, (a) => log.push(["w:" + ev, typeof a, ws.bytesWritten]));
		log.push(["write", ws.write("abc"), ws.write(Buffer.from("def"))]);
		ws.end("ghi");
		await new Promise((r) => ws.on("close", r));
		log.push(["content", fs.readFileSync(dir + "/b.txt", "utf8")]);
		const bad = fs.createReadStream(dir + "/nope");
		const evs = [];
		bad.on("error", (e) => evs.push(e.code));
		await new Promise((r) => bad.on("close", r));
		log.push(["bad", evs, bad.destroyed]);
		const { pipeline } = await import("node:stream");
		await new Promise((res) => pipeline(fs.createReadStream(f, { start: 0, end: 4 }), fs.createWriteStream(dir + "/d.txt"), res));
		log.push(["pipeline", fs.readFileSync(dir + "/d.txt", "utf8")]);
		try { fs.createReadStream(f, { start: 5, end: 2 }); } catch (e) { log.push([e.code, e.message]); }
		D(log)
	`))
	want := `[["pending",true,0,null,"r",438,2],["r:open","number",false,0],["r:ready","undefined",false,0],` +
		`["r:data","llo world, this ",16],["r:data","is a test file\nh",32],["r:data","ello wo",39],` +
		`["r:end","undefined",false,39],["r:close","undefined",true,39],["write",true,true],` +
		`["w:open","number",0],["w:ready","undefined",0],["w:finish","undefined",9],["w:close","undefined",9],` +
		`["content","abcdefghi"],["bad",["ENOENT"],true],["pipeline","hello"],` +
		`["ERR_OUT_OF_RANGE","The value of \"start\" is out of range. It must be <= \"end\" (here: 2). Received 5"]]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestFSPromisesFileHandle(t *testing.T) {
	dir := t.TempDir()
	got := runScriptString(t, fsTestScript(dir, `
		const f = dir + "/a.txt";
		await fsp.writeFile(f, "hello\nworld\n");
		await fsp.appendFile(f, "more\n");
		const h = await fsp.open(f, "r+");
		const r = [typeof h.fd];
		const b = Buffer.alloc(5);
		const rr = await h.read(b, 0, 5, 0); r.push(rr.bytesRead, rr.buffer === b, b.toString());
		const rr2 = await h.read({ buffer: Buffer.alloc(3), position: 6 }); r.push(rr2.bytesRead, rr2.buffer.toString());
		const w = await h.write("HE", 0); r.push(w.bytesWritten, w.buffer);
		r.push(await h.readFile({ encoding: "utf8" }));
		const wv = await h.writev([Buffer.from("a"), Buffer.from("b")], 0); r.push(wv.bytesWritten);
		const a = Buffer.alloc(2), c = Buffer.alloc(2); const rv = await h.readv([a, c], 0); r.push(rv.bytesRead, a.toString() + c.toString());
		await h.truncate(4); r.push((await h.stat()).size);
		await h.utimes(5, 6); r.push((await h.stat()).mtimeMs);
		let closeEv = 0; h.on("close", () => closeEv++);
		await h.close(); r.push(h.fd, closeEv);
		try { await h.stat(); } catch (e) { r.push(e.code, e.message, e.syscall); }
		const h2 = await fsp.open(f);
		const lines = []; for await (const l of h2.readLines()) lines.push(l);
		r.push(lines);
		const h3 = await fsp.open(f);
		let sd = ""; for await (const ch of h3.createReadStream({ encoding: "utf8" })) sd += ch;
		r.push(sd, h3.fd);
		try { await fsp.open(dir + "/nope"); } catch (e) { r.push(e.code, e.syscall); }
		D(r)
	`))
	want := `["number",5,true,"hello",3,"wor",2,"HE","HEllo\nworld\nmore\n",2,4,"abll",4,6000,-1,1,"EBADF","file closed","fstat",["abll"],"abll",-1,"ENOENT","open"]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestFSPromisesPathFunctions(t *testing.T) {
	dir := t.TempDir()
	got := runScriptString(t, fsTestScript(dir, `
		const f = dir + "/a.txt";
		const r = [];
		r.push(await fsp.writeFile(f, "hello"));
		await fsp.copyFile(f, dir + "/b.txt");
		try { await fsp.copyFile(f, dir + "/b.txt", fs.constants.COPYFILE_EXCL); } catch (e) { r.push(e.code); }
		await fsp.chmod(f, 0o600); r.push((await fsp.stat(f)).mode & 0o777);
		await fsp.rename(dir + "/b.txt", dir + "/c.txt");
		await fsp.symlink("c.txt", dir + "/l"); r.push(await fsp.readlink(dir + "/l"));
		await fsp.lutimes(dir + "/l", 1000, 2000); r.push((await fsp.lstat(dir + "/l")).mtimeMs);
		await fsp.utimes(f, 1000, 3000); r.push((await fsp.stat(f)).mtimeMs);
		await fsp.link(f, dir + "/hard"); r.push((await fsp.stat(f)).nlink);
		await fsp.truncate(dir + "/c.txt", 3); r.push(await fsp.readFile(dir + "/c.txt", "utf8"));
		await fsp.mkdir(dir + "/d"); await fsp.rmdir(dir + "/d"); r.push(fs.existsSync(dir + "/d"));
		r.push(Object.keys(await fsp.statfs(dir)));
		const g = []; for await (const x of fsp.glob("*.txt", { cwd: dir })) g.push(x); r.push(g.sort());
		const od = await fsp.opendir(dir); const names = []; for await (const e of od) names.push(e.name); r.push(names.sort());
		const t = await fsp.mkdtempDisposable(dir + "/x-"); const tp = t.path; await t[Symbol.asyncDispose](); r.push(fs.existsSync(tp));
		const m = await fsp.mkdtemp(dir + "/y-"); r.push(m.length - (dir + "/y-").length);
		await fsp.cp(dir, dir + "-cp", { recursive: true, filter: async (s) => !s.endsWith("/l") });
		r.push(fs.readdirSync(dir + "-cp").sort()); fs.rmSync(dir + "-cp", { recursive: true });
		try { await fsp.rm(dir + "/nope"); } catch (e) { r.push(e.code); }
		r.push(fs.promises === fsp);
		D(r)
	`))
	want := `[null,"EEXIST",384,"c.txt",2000000,3000000,2,"hel",false,["type","bsize","frsize","blocks","bfree","bavail","files","ffree"],` +
		`["a.txt","c.txt"],["a.txt","c.txt","hard","l"],false,6,["a.txt","c.txt","hard","y-` // y-XXXXXX dir name is random
	// writeFile resolves to undefined, which JSON renders as null in an array.
	if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, `"],"ENOENT",true]`) {
		t.Errorf("got  %s\nwant %s...", got, want)
	}
}

func TestFSCallbackExtras(t *testing.T) {
	dir := t.TempDir()
	got := runScriptString(t, fsTestScript(dir, `
		const f = dir + "/a.txt";
		fs.writeFileSync(f, "abc");
		const cb = (fn) => new Promise((res) => fn((...a) => res(a)));
		const r = [];
		r.push(await cb((k) => fs.appendFile(f, "d", k)));
		r.push(await cb((k) => fs.exists(f, k)), await cb((k) => fs.exists(dir + "/nope", k)));
		const [e1, p] = await cb((k) => fs.mkdtemp(dir + "/q-", k)); r.push(e1, typeof p);
		r.push(await cb((k) => fs.cp(f, dir + "/cp.txt", k)), fs.readFileSync(dir + "/cp.txt", "utf8"));
		const [e2, d] = await cb((k) => fs.opendir(dir, k)); const [e3, ent] = await cb((k) => d.read(k)); r.push(e2, e3, typeof ent.name, (await cb((k) => d.close(k)))[0]);
		try { d.closeSync(); } catch (e) { r.push(e.code, e.message); }
		const [e4] = await cb((k) => fs.rm(dir + "/nope", k)); r.push(e4.code, e4.syscall);
		const [e5, m] = await cb((k) => fs.glob("*.txt", { cwd: dir }, k)); r.push(e5, m.sort());
		const [e6, sf] = await cb((k) => fs.statfs(dir, k)); r.push(e6, sf.constructor.name);
		const fd = fs.openSync(f); const [e7, n, bufs] = await cb((k) => fs.readv(fd, [Buffer.alloc(2)], 0, k)); fs.closeSync(fd); r.push(e7, n, bufs[0].toString());
		r.push(await cb((k) => fs.truncate(f, k)), fs.statSync(f).size);
		const ds = fs.mkdtempDisposableSync(dir + "/z-"); r.push(Object.keys(ds), fs.existsSync(ds.path)); ds[Symbol.dispose](); r.push(fs.existsSync(ds.path));
		try { fs.cpSync(dir, dir + "/inner", { recursive: true }); } catch (e) { r.push(e.code); }
		try { fs.cpSync(dir, dir + "-2"); } catch (e) { r.push(e.code); }
		const blob = await fs.openAsBlob(dir + "/cp.txt", { type: "text/plain" }); r.push(blob.size, blob.type, await blob.text());
		D(r)
	`))
	want := `[[null],[true],[false],null,"string",[null,null],"abcd",null,null,"string",null,"ERR_DIR_CLOSED","Directory handle was closed",` +
		`"ENOENT","lstat",null,["a.txt","cp.txt"],null,"StatFs",null,2,"ab",[null],0,["path","remove"],true,false,"ERR_FS_CP_EINVAL","ERR_FS_EISDIR",4,"text/plain","abcd"]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
