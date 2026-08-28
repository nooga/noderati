import { join } from "path";
import { tmpdir } from "os";
import {
	writeFileSync,
	appendFileSync,
	readFileSync,
	existsSync,
	statSync,
	readdirSync,
	mkdirSync,
	unlinkSync,
	rmSync,
} from "fs";

const dir = join(tmpdir(), "noderati-fs-demo");
mkdirSync(dir);
const file = join(dir, "hello.txt");

writeFileSync(file, "hello");
appendFileSync(file, " noderati");
console.log("read:", readFileSync(file));
console.log("exists:", existsSync(file));

const st = statSync(file);
console.log("stat size:", st.size, "isFile:", st.isFile, "isDirectory:", st.isDirectory);
console.log("readdir:", readdirSync(dir).join(","));

unlinkSync(file);
rmSync(dir);
console.log("cleaned:", !existsSync(dir));
