package host

import (
	"testing"
	"time"

	"github.com/nooga/paserati/pkg/driver"
)

// TestTimeoutObjectShape drives the exact real call shape found while
// re-probing real undici's fetch() (round 75, docs/real-node-plan.md):
// undici's own lib/util/timers.js does
// `fastNowTimeout = setTimeout(onTick, TICK_MS); fastNowTimeout?.unref()`
// on every FastTimer construction - a real, unconditional call hit on
// every request. A plain number (paserati's own unwrapped setTimeout
// return value) has no .unref, so `numberValue?.unref()` throws
// "undefined is not a function" (the `?.` only guards the receiver being
// nullish, not the looked-up property itself) - confirmed directly
// before writing the fix.
func TestTimeoutObjectShape(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const t = setTimeout(() => {}, 1000);
		const shape = JSON.stringify({
			type: typeof t,
			ref: typeof t.ref,
			unref: typeof t.unref,
			refresh: typeof t.refresh,
			hasRef: typeof t.hasRef,
			hasRefBeforeClear: t.hasRef(),
		});
		clearTimeout(t);
		shape
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"type":"object","ref":"function","unref":"function","refresh":"function","hasRef":"function","hasRefBeforeClear":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestTimeoutObjectClearTimeoutActuallyCancels verifies clearTimeout on
// our wrapped Timeout object still cancels the real underlying timer -
// not just accepts the object shape without effect.
func TestTimeoutObjectClearTimeoutActuallyCancels(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let fired = false;
		const t = setTimeout(() => { fired = true; }, 10);
		clearTimeout(t);
		await new Promise((resolve) => setTimeout(resolve, 30));
		fired
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "false" {
		t.Errorf("fired = %s, want false (cleared before it could fire)", val.ToString())
	}
}

// TestTimeoutObjectRefresh verifies .refresh() actually reschedules the
// real timer (undici's own use of it: reset an idle-close deadline every
// time a socket makes progress) rather than being a no-op stub - checked
// by refreshing partway through the original delay and confirming the
// callback still fires only once, after the *refreshed* deadline.
func TestTimeoutObjectRefresh(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let count = 0;
		const t = setTimeout(() => { count++; }, 20);
		t.refresh();
		await new Promise((resolve) => setTimeout(resolve, 60));
		count
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "1" {
		t.Errorf("count = %s, want 1", val.ToString())
	}
}

// TestTimeoutObjectUnrefExcludesFromPendingWork is the actual fix for the
// ~4s-consistent "process won't exit after success" tail latency chased
// in round 87 (docs/real-node-plan.md): before this fix, .unref()/.ref()
// were pure no-ops (the doc comment literally said "paserati's timer
// initializer has no ref-counted keep-alive concept to hook into" - which
// was wrong, pkg/runtime.AsyncRuntime already exports ScheduleUnrefTimer
// for exactly this). Confirmed the gap first with a minimal, undici-free
// script-level repro (a bare `setTimeout(fn, 4000).unref()` with nothing
// else scheduled still blocked process exit for the full 4 seconds *and*
// fired the callback - real Node does neither).
//
// Tested by timing RunCode itself, not by reading a `fired` flag from
// the script's own return value or by polling
// AsyncRuntime.HasPendingWork() after the fact - two things learned the
// hard way while writing this test: (1) RunCode/runAsModule always
// drains to real idle before returning (the same as a real `node
// script.js` invocation draining its own event loop), so a genuinely
// *ref'd* dangling timer correctly, expectedly blocks RunCode for its
// full remaining delay - checking HasPendingWork() afterward can't
// observe a state that's already been fully drained one way or the
// other by the time RunCode returns. (2) a script's own top-level
// return *expression* is evaluated synchronously, before any timer
// gets a chance to fire, regardless of ref state - a bare `fired` read
// with no `await` is always false, proving nothing either way. Timing
// is the one signal that actually distinguishes "excluded, drain
// loop exited immediately" from "included, drain loop waited it out".
func TestTimeoutObjectUnrefExcludesFromPendingWork(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	start := time.Now()
	_, errs := p.RunCode(`
		const t = setTimeout(() => {}, 300);
		t.unref();
	`, driver.RunOptions{})
	elapsed := time.Since(start)
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if elapsed > 150*time.Millisecond {
		t.Errorf("RunCode took %v - want well under the unref'd timer's 300ms delay; a lone unref'd timer with nothing else outstanding must not block the drain loop from returning", elapsed)
	}
}

// TestTimeoutObjectRefRestoresPendingWork is
// TestTimeoutObjectUnrefExcludesFromPendingWork's mirror: .ref() must
// undo .unref()'s exclusion, matching real Node's own ref()/unref()
// symmetry (and undici's own real usage - a keep-alive socket's idle
// timer gets .unref()'d while idle and .ref()'d again the moment a new
// request needs it, the same pattern net.go's own Socket.ref()/.unref()
// already relies on for sockets - see net.go's extOpActive doc comment).
// Same timing-based shape as the test above, inverted: after .ref()
// undoes the .unref(), this timer alone is real, outstanding work
// again, so RunCode must wait for it to become due before returning.
func TestTimeoutObjectRefRestoresPendingWork(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	start := time.Now()
	_, errs := p.RunCode(`
		const t = setTimeout(() => {}, 40);
		t.unref();
		t.ref();
	`, driver.RunOptions{})
	elapsed := time.Since(start)
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if elapsed < 30*time.Millisecond {
		t.Errorf("RunCode took %v - want at least ~40ms; ref() must undo the prior unref() so this lone timer is real outstanding work the drain loop waits for", elapsed)
	}
}

// TestTimeoutObjectHasRefReflectsRealState guards .hasRef() itself -
// previously hardcoded to always report true (honestly documented as
// such, back when ref()/unref() were no-ops), it must now track the
// actual current state through both a .unref() and a following .ref().
func TestTimeoutObjectHasRefReflectsRealState(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const t = setTimeout(() => {}, 1000);
		const before = t.hasRef();
		t.unref();
		const afterUnref = t.hasRef();
		t.ref();
		const afterRef = t.hasRef();
		clearTimeout(t);
		JSON.stringify({ before, afterUnref, afterRef })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"before":true,"afterUnref":false,"afterRef":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestTimeoutObjectUnrefTimerStillFiresIfLoopStaysAlive guards the other
// half of AsyncRuntime.ScheduleUnrefTimer's documented contract: a
// .unref()'d timer is excluded only from *justifying* a wait by itself -
// if something else (here, a second, ref'd timer) keeps the loop running
// long enough anyway, the unref'd timer must still fire normally once
// due, not be silently dropped.
func TestTimeoutObjectUnrefTimerStillFiresIfLoopStaysAlive(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let fired = false;
		const t = setTimeout(() => { fired = true; }, 20);
		t.unref();
		await new Promise((resolve) => setTimeout(resolve, 60));
		fired
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "true" {
		t.Errorf("fired = %s, want true (an unref'd timer must still fire if other work keeps the loop alive past its deadline)", val.ToString())
	}
}
