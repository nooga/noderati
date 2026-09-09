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
