package host

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// captureExit swaps in a fake osExit that records the requested code
// instead of terminating the test binary, and restores the real one
// afterwards. Mirrors paserati's own identical helper
// (pkg/driver/host_idle_test.go, added alongside #484).
func captureExit(t *testing.T) *bool {
	t.Helper()
	exited := false
	old := osExit
	osExit = func(code int) {
		exited = true
		if code != 1 {
			t.Errorf("expected exit code 1, got %d", code)
		}
	}
	t.Cleanup(func() { osExit = old })
	return &exited
}

// captureStderr redirects os.Stderr for the duration of the test and
// returns a function that restores it and yields everything written.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = old })
	return func() string {
		w.Close()
		os.Stderr = old
		var buf bytes.Buffer
		io.Copy(&buf, r)
		return buf.String()
	}
}

// The three tests below guard a real noderati bug found this round
// (docs/real-node-plan.md): paserati#484 fixed its own bare
// setTimeout/nextTick to report and exit(1) on an exception thrown with
// no catching JS context above it, matching real Node's crash behavior -
// but noderati has three of its own call sites that reimplement (rather
// than delegate to) that same dispatch, so #484's fix never reached any
// of them on its own:
//
//   - timeout_object.go wraps paserati's real setTimeout to return a
//     Node-shaped Timeout object, and reschedules the user's callback
//     directly through the AsyncRuntime for the ref-counted path,
//     bypassing paserati's own (now-fixed) setTimeoutFn entirely.
//   - immediate_object.go's setImmediate doesn't exist in paserati at
//     all (it's a noderati-only global), so #484 never touched it.
//   - process.go's process.nextTick is noderati's own separate
//     implementation of real Node's process object, distinct from
//     paserati's bare global nextTick that #484 did patch.
//
// Confirmed as a real, current gap: a real webpack compile's own
// AsyncQueue schedules its queue-draining function via both
// `setImmediate(root._ensureProcessing)` and
// `process.nextTick(() => callback(...))`, and an exception thrown
// inside either one vanished with zero diagnostics even after pulling
// #484 - the crash never reached webpack because it never went through
// paserati's own fixed dispatch.

func TestSetTimeoutUncaughtExceptionReportsAndExits(t *testing.T) {
	exited := captureExit(t)
	stderr := captureStderr(t)
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)

	_, errs := p.RunCode(`setTimeout(() => { throw new Error("boom from timeout") }, 0)`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode failed: %v", errs[0])
	}
	if !*exited {
		t.Fatal("expected the uncaught exception to trigger a process exit")
	}
	if got := stderr(); !strings.Contains(got, "Uncaught exception: Error: boom from timeout") {
		t.Errorf("expected stderr to report the uncaught exception, got %q", got)
	}
}

func TestSetImmediateUncaughtExceptionReportsAndExits(t *testing.T) {
	exited := captureExit(t)
	stderr := captureStderr(t)
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)

	_, errs := p.RunCode(`setImmediate(() => { throw new Error("boom from immediate") })`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode failed: %v", errs[0])
	}
	if !*exited {
		t.Fatal("expected the uncaught exception to trigger a process exit")
	}
	if got := stderr(); !strings.Contains(got, "Uncaught exception: Error: boom from immediate") {
		t.Errorf("expected stderr to report the uncaught exception, got %q", got)
	}
}

func TestProcessNextTickUncaughtExceptionReportsAndExits(t *testing.T) {
	exited := captureExit(t)
	stderr := captureStderr(t)
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)

	_, errs := p.RunCode(`process.nextTick(() => { throw new Error("boom from nextTick") })`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode failed: %v", errs[0])
	}
	if !*exited {
		t.Fatal("expected the uncaught exception to trigger a process exit")
	}
	if got := stderr(); !strings.Contains(got, "Uncaught exception: Error: boom from nextTick") {
		t.Errorf("expected stderr to report the uncaught exception, got %q", got)
	}
}
