package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestSetImmediateRunsCallbackWithArgsAndRespectsMicrotaskOrdering guards
// the real call site that motivated this file: real undici's
// client-h1.js calls the bare global setImmediate(...) directly after a
// response finishes parsing. Confirms the callback (with forwarded extra
// arguments) actually fires, that a microtask still beats every
// macrotask (the one ordering guarantee that's actually part of the
// contract - real Node explicitly does *not* guarantee setTimeout(fn,0)
// vs setImmediate() ordering from top-level/module scope, only from
// within an I/O callback, so this deliberately doesn't pin that), and
// that clearImmediate() genuinely prevents a cancelled callback from
// ever running.
func TestSetImmediateRunsCallbackWithArgsAndRespectsMicrotaskOrdering(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let order = [];
		await new Promise((resolve) => {
			setTimeout(() => order.push("timeout"), 0);
			setImmediate(() => order.push("immediate"));
			Promise.resolve().then(() => order.push("microtask"));
			setImmediate((a, b) => order.push("args:" + a + "," + b), "x", "y");
			const cancelled = setImmediate(() => order.push("SHOULD_NOT_RUN"));
			clearImmediate(cancelled);
			setTimeout(resolve, 20);
		});
		JSON.stringify({
			firstWasMicrotask: order[0] === "microtask",
			ranAll: ["timeout", "immediate", "args:x,y"].every((x) => order.includes(x)),
			neverRanCancelled: !order.includes("SHOULD_NOT_RUN"),
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"firstWasMicrotask":true,"ranAll":true,"neverRanCancelled":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestSetImmediateHasRefUnrefStubs guards the Immediate object's shape:
// real Node code (and this project's own precedent for Timeout objects
// in timeout_object.go) calls .unref()/.ref()/.hasRef() on a
// setImmediate() return value without checking it exists first.
func TestSetImmediateHasRefUnrefStubs(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const im = setImmediate(() => {});
		const shape = typeof im.ref === "function" && typeof im.unref === "function" && typeof im.hasRef === "function";
		const hadRefBefore = im.hasRef();
		im.unref();
		im.ref();
		JSON.stringify({ shape, hadRefBefore })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"shape":true,"hadRefBefore":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}
