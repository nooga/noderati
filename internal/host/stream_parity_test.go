package host

import "testing"

// Expected strings below were produced by running the same scripts under
// real node v26: event ordering of a Writable's write/final/finish/close,
// Duplex destroy(err), finished() premature close, the iterator helpers,
// pipeline with a generator stage, early break out of async iteration,
// compose, Writable.pipe.
func TestStreamNodeParityEventOrder(t *testing.T) {
	got := runScriptString(t, `
import { Readable, Writable, Duplex, Transform, PassThrough, pipeline, finished, compose } from "node:stream";
const log = [];
const w = new Writable({ write(c, e, cb) { log.push("write " + c); setTimeout(cb, 1); }, final(cb) { log.push("final"); cb(); } });
w.on("prefinish", () => log.push("prefinish"));
w.on("finish", () => log.push("finish"));
w.on("close", () => log.push("close"));
w.write("x");
w.end("y", () => log.push("end cb"));
await new Promise((r) => w.on("close", r));
const d = new Duplex({ read() {}, write(c, e, cb) { cb(); } });
d.on("error", (e) => log.push("error " + e.message));
d.on("close", () => log.push("dclose " + d.destroyed + " " + d.errored.message));
d.destroy(new Error("boom"));
log.push("sync " + d.destroyed);
await new Promise((r) => d.on("close", r));
log.push("readable side " + (typeof d.read) + " " + (typeof d.setEncoding) + " " + (typeof d.cork) + " " + d.allowHalfOpen);
const r = new Readable({ read() {} });
finished(r, (err) => log.push("finished " + (err && err.code)));
r.destroy();
await new Promise((res) => setTimeout(res, 5));
log.push(JSON.stringify(await Readable.from([1, 2, 3, 4]).map((x) => x * 2).filter((x) => x > 2).take(2).toArray()));
log.push(await Readable.from([1, 2, 3]).reduce((a, b) => a + b, 0));
const out = [];
await new Promise((res) => pipeline(Readable.from(["a", "b"]), async function* (s) { for await (const c of s) yield c.toUpperCase(); },
  new Writable({ objectMode: true, write(c, e, cb) { out.push(c); cb(); } }), (err) => { log.push("pipeline " + err + " " + out.join("")); res(); }));
const it = Readable.from([1, 2, 3]);
for await (const v of it) { log.push("iter " + v); break; }
log.push("destroyed after break " + it.destroyed);
const c = compose(new PassThrough(), async function* (s) { for await (const x of s) yield String(x) + "!"; });
c.end("z");
log.push(JSON.stringify(await c.toArray()));
const pw = new Writable({ write(c, e, cb) { cb(); } });
pw.on("error", (e) => log.push(e.code));
pw.pipe(new PassThrough());
await new Promise((res) => setTimeout(res, 5));
log.join("|")
`)
	want := `write x|write y|final|prefinish|end cb|finish|close|sync true|error boom|dclose true boom|readable side function function function true|finished ERR_STREAM_PREMATURE_CLOSE|[4,6]|6|pipeline undefined AB|iter 1|destroyed after break true|["z!"]|ERR_STREAM_CANNOT_PIPE`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// stream/consumers, stream/web (queuing strategies, Compression/
// DecompressionStream, TextEncoderStream), fromWeb adapters and
// stream/promises finished/pipeline with an AbortSignal.
func TestStreamWebConsumersPromises(t *testing.T) {
	got := runScriptString(t, `
import { Readable, Duplex, PassThrough } from "node:stream";
import { text, json, buffer } from "node:stream/consumers";
import { finished, pipeline } from "node:stream/promises";
import * as web from "node:stream/web";
const log = [];
log.push(await text(Readable.from(["a", "b"])));
log.push(JSON.stringify(await json(Readable.from(['{"x":', "1}"]))));
log.push((await buffer(Readable.from([Buffer.from("xy")]))).toString());
const cq = new web.CountQueuingStrategy({ highWaterMark: 3 });
log.push(cq.highWaterMark + " " + cq.size() + " " + new web.ByteLengthQueuingStrategy({ highWaterMark: 1 }).size(new Uint8Array(7)));
const cs = new web.CompressionStream("gzip");
const ds = new web.DecompressionStream("gzip");
const cw = cs.writable.getWriter(); cw.write(new TextEncoder().encode("hello")); cw.close();
const parts = []; const cr = cs.readable.getReader();
for (;;) { const { value, done } = await cr.read(); if (done) break; parts.push(Buffer.from(value)); }
const dw = ds.writable.getWriter(); dw.write(new Uint8Array(Buffer.concat(parts))); dw.close();
const dr = ds.readable.getReader(); let s = "";
for (;;) { const { value, done } = await dr.read(); if (done) break; s += Buffer.from(value).toString(); }
log.push(s);
const tes = new TextEncoderStream();
const tw = tes.writable.getWriter(); tw.write("hé"); tw.close();
const tr = tes.readable.getReader(); const bytes = [];
for (;;) { const { value, done } = await tr.read(); if (done) break; bytes.push(...value); }
log.push(bytes.join(","));
log.push(JSON.stringify(await Readable.fromWeb(new web.ReadableStream({ start(c) { c.enqueue("a"); c.close(); } }), { objectMode: true }).toArray()));
const ts = new web.TransformStream({ transform(c, ctl) { ctl.enqueue(String(c).toUpperCase()); } });
const dd = Duplex.fromWeb(ts, { objectMode: true });
dd.end("lower");
log.push(JSON.stringify(await dd.toArray()));
const r = new Readable({ read() {} });
const p = finished(r);
r.destroy(new Error("r4"));
await p.catch((e) => log.push("finished " + e.message));
const ac = new AbortController();
const pp = pipeline(new Readable({ read() {} }), new PassThrough(), { signal: ac.signal });
ac.abort();
await pp.catch((e) => log.push(e.name));
log.join("|")
`)
	want := `ab|{"x":1}|xy|3 1 7|hello|104,195,169|["a"]|["LOWER"]|finished r4|AbortError`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
