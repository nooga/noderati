package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestQueueMicrotaskRunsBeforeMacrotask drives the real ordering guarantee
// queueMicrotask actually needs to provide - callbacks run before any
// later-queued timer, even though this implementation (built on
// vmInst.NewPromiseFromExecutor, the same primitive as
// Promise.withResolvers) takes one extra microtask tick versus V8's
// native fast path (see queue_microtask.go's own doc comment). Found
// missing while re-probing real undici's fetch() (round 75,
// docs/real-node-plan.md): node:events' real addAbortListener queues its
// callback this way when a signal is already aborted.
func TestQueueMicrotaskRunsBeforeMacrotask(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let order = [];
		queueMicrotask(() => order.push("microtask"));
		order.push("sync");
		await new Promise((resolve) => setTimeout(resolve, 20));
		order.join(",")
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "sync,microtask" {
		t.Errorf("got %q, want %q", val.ToString(), "sync,microtask")
	}
}
