package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestSignalBridgeActivatesOnListenerAndReactivates guards the fix for a
// real, user-visible bug (docs/real-node-plan.md, Round 102/103):
// startSignalBridge used to call signal.Notify for every bridgeable signal
// unconditionally at startup, overriding the OS's own default disposition
// (e.g. SIGINT's default terminate) even when nothing in JS was listening
// for it - a running noderati process with zero signal listeners simply
// ignored Ctrl-C forever. The fix gates bridging on listener count
// (wireProcessSignalListeners/signalBridge, signals.go), matching real
// Node's own libuv-backed behavior.
//
// Uses SIGWINCH deliberately, not SIGINT/SIGTERM: SIGWINCH's own OS default
// disposition is "ignore", so this test can safely self-signal
// (process.kill(process.pid, ...)) from inside the test binary itself at
// every stage - including the deactivated stage - without any risk of the
// OS default actually terminating this `go test` process if the bridge
// were somehow still (or no longer) wired correctly. Verifying the
// SIGINT/SIGTERM default-terminates-when-unhandled half specifically was
// done by hand against the compiled binary in a real subprocess (see the
// Round 102/103 doc entry) rather than here, for exactly that reason.
func TestSignalBridgeActivatesOnListenerAndReactivates(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let fired1 = 0;
		function cb1() { fired1++; }

		// 1. Registering the first listener must activate real OS-level
		// bridging - a real self-signal must reach cb1. The timeout timer
		// is cleared on success - left running, a script-wide event loop
		// (matching real Node) keeps this test alive until it fires
		// regardless of the promise having long since resolved.
		await new Promise((resolve, reject) => {
			const timer = setTimeout(() => reject(new Error("timeout waiting for first SIGWINCH")), 3000);
			process.once("SIGWINCH", () => { cb1(); clearTimeout(timer); resolve(); });
			process.kill(process.pid, "SIGWINCH");
		});

		// 2. Removing the only listener must deactivate bridging (and
		// leave listenerCount at 0) - this must not throw or hang.
		process.removeAllListeners("SIGWINCH");
		const countAfterRemove = process.listenerCount("SIGWINCH");

		// 3. Registering a second listener afterwards must re-activate
		// bridging cleanly (proves deactivate->reactivate doesn't leave
		// signal.Stop/Notify in a broken state) - another real self-signal
		// must reach the new listener.
		let fired2 = 0;
		await new Promise((resolve, reject) => {
			const timer = setTimeout(() => reject(new Error("timeout waiting for second SIGWINCH")), 3000);
			process.once("SIGWINCH", () => { fired2++; clearTimeout(timer); resolve(); });
			process.kill(process.pid, "SIGWINCH");
		});

		JSON.stringify({ fired1, countAfterRemove, fired2 });
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"fired1":1,"countAfterRemove":0,"fired2":1}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestSignalBridgeRemoveListenerDeactivates guards the off()/removeListener
// path specifically (as opposed to removeAllListeners, covered above) -
// both are wired independently in wireProcessSignalListeners.
func TestSignalBridgeRemoveListenerDeactivates(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		function handler() {}
		process.on("SIGWINCH", handler);
		const before = process.listenerCount("SIGWINCH");
		process.removeListener("SIGWINCH", handler);
		const after = process.listenerCount("SIGWINCH");
		JSON.stringify({ before, after });
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"before":1,"after":0}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestSignalBridgeOnceSelfRemovalDeactivates guards a real bug found by
// review, not by either test above: addListener's once() wrapper
// (emitter.go) self-removes by calling the bare removeListener helper
// directly, never through wireProcessSignalListeners' own on/off
// overrides - so a plain process.once("SIGINT", cleanup) shutdown hook (a
// very common real shape) used to fire once, self-remove, and leave
// bridge.deactivate uncalled: listenerCount correctly dropped to 0, but
// the signal stayed intercepted forever. TestSignalBridgeActivatesOnListenerAndReactivates
// couldn't have caught this - it deactivates via an explicit
// removeAllListeners() call, never via once()'s own self-removal path.
// The real fix lives in the relay goroutine's tick closure (signals.go),
// which now checks listenerCount itself after every delivery, regardless
// of which removal path emptied it.
func TestSignalBridgeOnceSelfRemovalDeactivates(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let fired = 0;
		await new Promise((resolve, reject) => {
			const timer = setTimeout(() => reject(new Error("timeout waiting for SIGWINCH")), 3000);
			process.once("SIGWINCH", () => { fired++; clearTimeout(timer); resolve(); });
			process.kill(process.pid, "SIGWINCH");
		});
		JSON.stringify({ fired, countAfterOnce: process.listenerCount("SIGWINCH") });
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"fired":1,"countAfterOnce":0}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}
