// Diffs the Node API surface of real node against noderati.
//
//   node scripts/apigap/report.mjs [path/to/noderati] [--json out.json] [--only mod1,mod2]
//
// Prints, per builtin module, how many of node's callable/class exports
// noderati has (constants counted separately) and which are missing,
// then missing globals, process members, and instance members.
import { execFileSync } from "node:child_process";
import { builtinModules } from "node:module";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
const flag = (name) => { const i = args.indexOf(name); return i < 0 ? undefined : args.splice(i, 2)[1]; };
const jsonOut = flag("--json");
const only = flag("--only")?.split(",");
const noderati = resolve(args[0] ?? join(here, "../../noderati"));

const mods = builtinModules.filter((m) => !m.startsWith("_")).map((m) => m.replace(/^node:/, ""));
const dir = mkdtempSync(join(tmpdir(), "apigap-"));
const run = (bin, out) => {
  try {
    execFileSync(bin, [join(here, "surface.cjs")], {
      env: { ...process.env, MODS: JSON.stringify(mods), OUT: out, NODE_NO_WARNINGS: "1" },
      stdio: ["ignore", "ignore", "inherit"], timeout: 60000,
    });
  } catch (e) {
    console.error(`${bin} exited with ${e.status}`);
  }
  return JSON.parse(readFileSync(out, "utf8"));
};
const N = run(process.execPath, join(dir, "node.json"));
const R = run(noderati, join(dir, "noderati.json"));

const isConst = (k) => /(^|\.)[A-Z][A-Z0-9_]+$/.test(k) || /(^|\.)constants\./.test(k);
const report = { missingModules: [], requireFails: R.esmOnly ?? [], modules: {}, globals: [], process: [], instances: {} };
let have = 0, total = 0;
for (const [m, e] of Object.entries(N.modules)) {
  if (only && !only.includes(m)) continue;
  const r = R.modules[m];
  const all = Object.keys(e.keys ?? {});
  const api = all.filter((k) => !isConst(k)), consts = all.filter(isConst);
  if (!r || r.error) {
    report.missingModules.push(m);
    total += api.length;
    continue;
  }
  const missing = api.filter((k) => !(k in r.keys));
  const missingConsts = consts.filter((k) => !(k in r.keys));
  report.modules[m] = { api: api.length, have: api.length - missing.length, missing, consts: consts.length, missingConsts: missingConsts.length };
  have += api.length - missing.length;
  total += api.length;
}
if (!only) {
  report.globals = N.globals.filter((k) => !R.globals.includes(k));
  report.process = N.process.filter((k) => !R.process.includes(k));
}
for (const [k, v] of Object.entries(N.instances)) {
  if (!Array.isArray(v)) continue;
  const r = R.instances[k];
  report.instances[k] = Array.isArray(r) ? { total: v.length, missing: v.filter((x) => !r.includes(x)) } : { total: v.length, error: r?.error ?? "missing" };
}

console.log(`Builtin module API (excluding constants): ${have}/${total} (${((100 * have) / total).toFixed(1)}%)\n`);
if (report.missingModules.length) console.log(`Missing modules: ${report.missingModules.join(" ")}`);
if (report.requireFails.length) console.log(`require() fails, import works: ${report.requireFails.join(" ")}`);
console.log();
for (const [m, d] of Object.entries(report.modules)) {
  const c = d.consts ? ` (constants ${d.consts - d.missingConsts}/${d.consts})` : "";
  console.log(`${d.missing.length ? " " : "✓"} ${m}: ${d.have}/${d.api}${c}${d.missing.length ? "\n    missing: " + d.missing.join(" ") : ""}`);
}
if (!only) {
  console.log(`\nMissing globals: ${report.globals.join(" ") || "none"}`);
  console.log(`\nMissing process members: ${report.process.join(" ") || "none"}`);
}
console.log("\nInstances:");
for (const [k, d] of Object.entries(report.instances)) {
  if (d.error) console.log(`  ${k}: ERROR ${d.error}`);
  else console.log(`  ${d.missing.length ? " " : "✓"} ${k}: ${d.total - d.missing.length}/${d.total}${d.missing.length ? " missing: " + d.missing.join(" ") : ""}`);
}
if (jsonOut) writeFileSync(jsonOut, JSON.stringify(report, null, 1));
