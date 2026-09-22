import { readFileSync, writeFileSync } from "node:fs";
import * as acorn from "acorn";

const path = process.argv[2];
const src = readFileSync(path, "utf8");

const ast = acorn.parse(src, {
  ecmaVersion: "latest",
  sourceType: "module",
});

function lineOf(offset) {
  let line = 1;
  for (let i = 0; i < offset; i++) {
    if (src[i] === "\n") line++;
  }
  return line;
}

const insertOffsets = [];

function visit(node) {
  if (node == null || typeof node !== "object") return;
  if (Array.isArray(node)) {
    for (const item of node) visit(item);
    return;
  }
  if (node.type === "BlockStatement" || node.type === "Program") {
    for (const stmt of node.body) {
      insertOffsets.push(stmt.start);
    }
  }
  for (const key of Object.keys(node)) {
    if (key === "start" || key === "end" || key === "loc" || key === "range") continue;
    const val = node[key];
    if (val && typeof val === "object") visit(val);
  }
}
visit(ast);

insertOffsets.sort((a, b) => a - b);
console.error("total insertion points:", insertOffsets.length);

let out = "";
let cursor = 0;
let probeId = 0;
for (const offset of insertOffsets) {
  if (offset < cursor) continue;
  out += src.slice(cursor, offset);
  out += `console.error("PROBE_${probeId}_line_${lineOf(offset)}");\n`;
  probeId++;
  cursor = offset;
}
out += src.slice(cursor);

writeFileSync(path + ".instrumented.js", out);
console.error("inserted", probeId, "probes ->", path + ".instrumented.js");
