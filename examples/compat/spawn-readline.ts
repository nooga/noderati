import { spawnSync, spawn } from "node:child_process";
import { createInterface, emitKeypressEvents } from "node:readline";
import { Readable, Writable } from "node:stream";

const sync = spawnSync("echo", ["spawn-sync-ok"]);
if (sync.status !== 0 || !sync.stdout.includes("spawn-sync-ok")) {
  throw new Error(`spawnSync failed: ${JSON.stringify(sync)}`);
}

const child = spawn("echo", ["spawn-async-ok"]);
let asyncOut = "";
child.stdout.on("data", (chunk) => {
  asyncOut += chunk;
});
await new Promise<void>((resolve, reject) => {
  child.on("error", reject);
  child.on("close", (code) => {
    if (code !== 0) reject(new Error(`exit ${code}`));
    else resolve();
  });
});
if (!asyncOut.includes("spawn-async-ok")) {
  throw new Error(`spawn async stdout: ${asyncOut}`);
}

const input = new Readable();
const output = new Writable();
emitKeypressEvents(input);
const rl = createInterface({ input, output, terminal: false });
rl.close();

console.log("ok spawn-readline");
