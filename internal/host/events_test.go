package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestEventEmitterOnEmit(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { EventEmitter } from "node:events";
		const ee = new EventEmitter();
		let n = 0;
		ee.on("x", (v) => { n = v; });
		ee.emit("x", 42);
		n
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "42" {
		t.Errorf("emit = %q, want 42", val.ToString())
	}
}

func TestEventEmitterOnceAndOff(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { EventEmitter } from "node:events";
		const ee = new EventEmitter();
		let count = 0;
		const fn = () => { count++; };
		ee.once("a", fn);
		ee.emit("a");
		ee.emit("a");
		ee.on("b", fn);
		ee.off("b", fn);
		ee.emit("b");
		count
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "1" {
		t.Errorf("once/off = %q, want 1", val.ToString())
	}
}

// TestEventsGetSetMaxListeners drives the exact real call shape found
// while probing undici (round 74, docs/real-node-plan.md): its own
// lib/web/fetch/request.js calls getMaxListeners(new
// AbortController().signal) at module load time - on a real AbortSignal,
// not an instance of this module's own EventEmitter class, so the
// implementation must work generically rather than requiring
// instanceof. Also checks both the named-destructure and bare-default
// require() shapes still work side by side (real undici uses both,
// round 69 vs round 74).
func TestEventsGetSetMaxListeners(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import EventEmitter, { getMaxListeners, setMaxListeners } from "node:events";
		const notAnEmitter = {};
		const defaultCount = getMaxListeners(notAnEmitter);

		const ee = new EventEmitter();
		setMaxListeners(5, ee);
		const eeCount = getMaxListeners(ee);

		JSON.stringify({
			defaultCount,
			eeCount,
			bareDefaultIsClass: EventEmitter.name === "EventEmitter",
			staticAccessWorks: typeof EventEmitter.getMaxListeners === "function",
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"defaultCount":10,"eeCount":5,"bareDefaultIsClass":true,"staticAccessWorks":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestEventsAddAbortListener drives the exact real call shape found while
// re-probing real undici's fetch() after paserati#302 was fixed (round
// 75, docs/real-node-plan.md): undici's own lib/core/util.js destructures
// `addAbortListener` off `require("node:events")` at module load time and
// calls it unconditionally on every request that carries a signal.
//
// fired1 asserts *false*, not the spec-correct *true* - a known, already
// filed upstream gap (paserati#372: AbortController.abort() never
// dispatches 'abort' to addEventListener listeners at all, a real
// AbortSignal/EventTarget bug, not anything addAbortListener's own JS
// here gets wrong). This locks in current, honest behavior rather than
// asserting something that can't pass until #372 is fixed - flip this
// expectation the moment that issue closes.
func TestEventsAddAbortListener(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { addAbortListener } from "node:events";

		const ac1 = new AbortController();
		let fired1 = false;
		addAbortListener(ac1.signal, () => { fired1 = true; });
		ac1.abort();

		const ac2 = new AbortController();
		let fired2 = false;
		const disposer = addAbortListener(ac2.signal, () => { fired2 = true; });
		disposer[Symbol.dispose]();
		ac2.abort();

		JSON.stringify({ fired1, fired2, hasDispose: typeof disposer[Symbol.dispose] === "function" })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"fired1":false,"fired2":false,"hasDispose":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestEventsAddAbortListenerAlreadyAborted covers addAbortListener's other
// branch, which does NOT depend on paserati#372: a signal that is already
// aborted at call time queues the listener as a microtask instead of
// relying on addEventListener at all (real Node: abort listeners never
// run synchronously with the call that set .aborted).
func TestEventsAddAbortListenerAlreadyAborted(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { addAbortListener } from "node:events";

		const ac = new AbortController();
		ac.abort();
		let fired = false;
		addAbortListener(ac.signal, () => { fired = true; });
		const syncFired = fired;
		await new Promise((resolve) => setTimeout(resolve, 20));
		JSON.stringify({ syncFired, fired })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"syncFired":false,"fired":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}
