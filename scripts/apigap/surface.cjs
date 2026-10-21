// Dumps the Node API surface visible to a script: every builtin module's
// keys, globalThis, process, and the members of real instances of the
// main classes. Runs unchanged under node and noderati; report.mjs diffs
// the two. Usage: MODS='["fs",...]' OUT=out.json <runtime> surface.cjs
const out = { modules: {}, globals: [], process: [], instances: {} };
const MODS = JSON.parse(process.env.MODS);
const NESTED = ["promises", "posix", "win32", "constants", "webcrypto", "subtle", "performance"];

function keys(o) {
  const s = new Set();
  try { for (const k of Object.getOwnPropertyNames(o)) s.add(k); } catch {}
  try { for (const k in o) s.add(k); } catch {}
  return [...s].filter((k) => !k.startsWith("_")).sort();
}

// Own and inherited members of an instance, up to Object.prototype.
function members(o) {
  const s = new Set();
  for (let p = o; p && p !== Object.prototype && p !== Function.prototype; p = Object.getPrototypeOf(p)) {
    try { for (const k of Object.getOwnPropertyNames(p)) if (!k.startsWith("_") && k !== "constructor") s.add(k); } catch {}
  }
  try { for (const k in o) if (!k.startsWith("_")) s.add(k); } catch {}
  return [...s].sort();
}

const instances = {
  "Buffer": () => Buffer.alloc(8),
  "EventEmitter": () => new (require("events"))(),
  "stream.Readable": () => new (require("stream").Readable)({ read() {} }),
  "stream.Writable": () => new (require("stream").Writable)({ write(c, e, cb) { cb(); } }),
  "stream.Duplex": () => new (require("stream").Duplex)({ read() {}, write(c, e, cb) { cb(); } }),
  "stream.Transform": () => new (require("stream").Transform)({ transform(c, e, cb) { cb(null, c); } }),
  "net.Socket": () => { const s = new (require("net").Socket)(); s.on("error", () => {}); return s; },
  "http.Server": () => require("http").createServer(() => {}),
  "http.ClientRequest": () => {
    const r = require("http").request("http://127.0.0.1:9/");
    r.on("error", () => {});
    setTimeout(() => r.destroy && r.destroy(), 0);
    return r;
  },
  "ChildProcess": () => require("child_process").spawn("true"),
  "console.Console": () => new (require("console").Console)(process.stdout),
  "console": () => console,
  "readline.Interface": () => {
    const r = require("readline").createInterface({ input: new (require("stream").Readable)({ read() {} }) });
    setTimeout(() => r.close(), 0);
    return r;
  },
  "URL": () => new URL("http://a/b?c"),
  "process.stdout": () => process.stdout,
  "process.stdin": () => process.stdin,
  "Blob": () => new Blob(["x"]),
  "TextEncoder": () => new TextEncoder(),
  "zlib.Gzip": () => require("zlib").createGzip(),
  "fs.Stats": () => require("fs").statSync("."),
  "fs.Dirent": () => require("fs").readdirSync(".", { withFileTypes: true })[0],
  "fs.FSWatcher": () => { const w = require("fs").watch("."); setTimeout(() => w.close(), 0); return w; },
  "crypto.Hash": () => require("crypto").createHash("sha256"),
  "crypto.Hmac": () => require("crypto").createHmac("sha256", "k"),
};

(async () => {
  for (const m of MODS) {
    let mod;
    try {
      mod = require("node:" + m);
    } catch {
      try {
        const ns = await import("node:" + m);
        mod = ns.default ?? ns;
        (out.esmOnly ||= []).push(m);
      } catch (e) {
        out.modules[m] = { error: String(e && e.message).slice(0, 120) };
        continue;
      }
    }
    const entry = { keys: {} };
    for (const k of keys(mod)) {
      let v;
      try { v = mod[k]; } catch { entry.keys[k] = "throws"; continue; }
      entry.keys[k] = typeof v;
      if (v && typeof v === "object" && NESTED.includes(k)) {
        for (const k2 of keys(v)) entry.keys[k + "." + k2] = typeof v[k2];
      }
    }
    out.modules[m] = entry;
  }
  out.globals = keys(globalThis);
  out.process = keys(process);
  for (const [name, make] of Object.entries(instances)) {
    try { out.instances[name] = members(make()); } catch (e) { out.instances[name] = { error: String(e && e.message).slice(0, 120) }; }
  }
  setTimeout(() => {
    require("fs").writeFileSync(process.env.OUT, JSON.stringify(out, null, 1));
    process.exit(0);
  }, 50);
})();
