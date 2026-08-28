import { join, dirname, basename, extname } from "path";
import { platform, arch, homedir, tmpdir } from "os";

console.log("argv:", process.argv.join(" "));
console.log("platform:", process.platform, process.arch, "| os:", platform(), arch());
console.log("cwd:", process.cwd());
console.log("home:", homedir());
console.log("tmp:", tmpdir());
console.log("join:", join("a", "b", "c.ts"));
console.log("dirname/base/ext:", dirname("/tmp/demo.ts"), basename("/tmp/demo.ts"), extname("/tmp/demo.ts"));
