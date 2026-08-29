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
