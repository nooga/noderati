import bcrypt from "bcryptjs";

// Cost factor kept at the real, valid minimum (4, not the usual 10-12)
// purely so this probe finishes in a reasonable time under an
// interpreted (non-JIT) engine - correctness, not performance, is what
// this probe checks; the same real Blowfish/bcrypt code path runs
// either way.
const COST = 4;
const password = "correct horse battery staple";

console.log("=== sync ===");
const salt = bcrypt.genSaltSync(COST);
console.log("salt looks right:", new RegExp("^\\$2[aby]\\$0" + COST + "\\$").test(salt));

const hash = bcrypt.hashSync(password, salt);
console.log("hash looks right:", new RegExp("^\\$2[aby]\\$0" + COST + "\\$").test(hash));

console.log("correct password matches:", bcrypt.compareSync(password, hash));
console.log("wrong password rejected:", !bcrypt.compareSync("wrong password", hash));

console.log("=== async ===");
const asyncHash = await bcrypt.hash(password, COST);
console.log("async hash looks right:", new RegExp("^\\$2[aby]\\$0" + COST + "\\$").test(asyncHash));
console.log("async compare matches:", await bcrypt.compare(password, asyncHash));
console.log("async compare rejects wrong:", !(await bcrypt.compare("nope", asyncHash)));

console.log("=== fixed salt, exact hash string ===");
// A fixed salt makes hashSync's output fully deterministic - if this
// exact string matches real Node's own output for the same inputs, the
// actual bcrypt algorithm (base64 alphabet, cost factor, blowfish core)
// matches bit for bit, not just "looks like the right shape".
const fixedSalt = "$2b$04$CwTycUXWue0Thq9StjUM0u";
console.log("hash with fixed salt:", bcrypt.hashSync("secret", fixedSalt));

console.log("ALL OK");
