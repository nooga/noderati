import * as tar from "tar";
import { mkdirSync, writeFileSync, rmSync, readFileSync, existsSync } from "fs";
import { join } from "path";

const root = "/tmp/tar_probe_" + process.pid;
const srcDir = join(root, "src");
const outDir = join(root, "out");
const archivePath = join(root, "archive.tar.gz");

rmSync(root, { recursive: true, force: true });
mkdirSync(srcDir, { recursive: true });
mkdirSync(outDir, { recursive: true });

writeFileSync(join(srcDir, "hello.txt"), "hello from tar probe\n");
mkdirSync(join(srcDir, "nested"));
writeFileSync(join(srcDir, "nested", "deep.txt"), "deep file content\n".repeat(50));

console.log("creating archive...");
await tar.c(
  {
    gzip: true,
    file: archivePath,
    cwd: srcDir,
  },
  ["hello.txt", "nested"]
);
console.log("archive created:", existsSync(archivePath), "size:", readFileSync(archivePath).length);

console.log("extracting archive...");
await tar.x({
  file: archivePath,
  cwd: outDir,
});

const helloContent = readFileSync(join(outDir, "hello.txt"), "utf8");
const deepContent = readFileSync(join(outDir, "nested", "deep.txt"), "utf8");

console.log("hello.txt matches:", helloContent === "hello from tar probe\n");
console.log("deep.txt matches:", deepContent === "deep file content\n".repeat(50));

console.log("listing archive entries...");
const entries = [];
await tar.t({
  file: archivePath,
  onentry: (entry) => entries.push(entry.path),
});
console.log("entries:", JSON.stringify(entries.sort()));

rmSync(root, { recursive: true, force: true });
console.log("ALL OK");
