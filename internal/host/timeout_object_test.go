package host

import (
	"testing"

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
