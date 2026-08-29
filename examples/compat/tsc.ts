import { spawnSync } from "node:child_process";
import path from "node:path";

const tscPath = path.resolve("examples/node_modules/typescript/lib/tsc.js");
const noderati = process.argv[0];

function runTsc(...args: string[]): string {
  const result = spawnSync(noderati, tscPath, ...args);
  if (result.status !== 0) {
    throw new Error(`tsc ${args.join(" ")} failed (status ${result.status}): ${result.stderr}`);
  }
  return result.stdout;
}

const version = runTsc("--version");
if (!version.includes("Version")) {
  throw new Error(`unexpected --version output: ${version}`);
}
console.log(version.trim());

const help = runTsc("--help");
if (!help.includes("tsc: The TypeScript Compiler")) {
  throw new Error("unexpected --help output");
}
console.log("ok tsc");
