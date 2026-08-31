console.log("columns", process.stdout.columns, typeof process.stdout.columns);
console.log("isTTY", process.stdout.isTTY, typeof process.stdout.isTTY);
const msg = "tsc: The TypeScript Compiler - Version 5.8.3";
console.log("msg.len", msg.length);
const padded = msg.padEnd(75);
console.log("padEnd75.len", padded.length);
console.log("padEnd10", JSON.stringify("hi".padEnd(10)));
console.log("padStart5", JSON.stringify("TS ".padStart(5)));
if (typeof process.stdout.write === "function") {
	process.stdout.write("write-ok\n");
}
