import { ok, equal, notEqual, fail } from "assert";

ok(true);
equal("noderati", "noderati");
notEqual("a", "b");

let failed = false;
try {
	fail("boom");
} catch (e) {
	failed = true;
	console.log("fail() threw as expected");
}
ok(failed);

console.log("assert checks passed");
