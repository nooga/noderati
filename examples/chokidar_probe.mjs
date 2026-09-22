import chokidar from "chokidar";
import { mkdirSync, writeFileSync, rmSync, appendFileSync } from "fs";
import { join } from "path";

const root = "/tmp/chokidar_probe_" + process.pid;
rmSync(root, { recursive: true, force: true });
mkdirSync(root, { recursive: true });

const watcher = chokidar.watch(root, { persistent: true, ignoreInitial: true });

const events = [];
watcher.on("all", (event, path) => {
  events.push(event + ":" + path.slice(root.length + 1));
  console.log("event:", event, path.slice(root.length + 1));
});

watcher.on("ready", async () => {
  console.log("watcher ready");

  writeFileSync(join(root, "a.txt"), "hello");
  await new Promise((r) => setTimeout(r, 300));

  appendFileSync(join(root, "a.txt"), " world");
  await new Promise((r) => setTimeout(r, 300));

  mkdirSync(join(root, "subdir"));
  await new Promise((r) => setTimeout(r, 300));

  writeFileSync(join(root, "subdir", "b.txt"), "nested");
  await new Promise((r) => setTimeout(r, 300));

  rmSync(join(root, "a.txt"));
  await new Promise((r) => setTimeout(r, 300));

  console.log("total events:", events.length);
  await watcher.close();
  rmSync(root, { recursive: true, force: true });
  console.log("ALL OK");
  process.exit(0);
});

watcher.on("error", (e) => {
  console.log("watcher error:", e && e.stack || e);
  process.exit(1);
});
