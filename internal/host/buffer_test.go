package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestBufferIsFunction(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Buffer } from "node:buffer";
		typeof Buffer === "function" && typeof Buffer.from === "function" && typeof Buffer.alloc === "function" && typeof Buffer.isBuffer === "function" ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("Buffer API = %q", val.ToString())
	}
}

func TestBufferGlobal(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		typeof globalThis.Buffer === "function" && globalThis.Buffer === Buffer
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsBoolean() || !val.AsBoolean() {
		t.Errorf("globalThis.Buffer = %v", val)
	}
}

func TestBufferFromAndIsBuffer(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Buffer } from "node:buffer";
		const b = Buffer.from("hi");
		Buffer.isBuffer(b) && b.toString() === "hi" ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("Buffer.from = %q", val.ToString())
	}
}

func TestBufferAlloc(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Buffer } from "node:buffer";
		Buffer.alloc(4).length === 4 ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("Buffer.alloc = %q", val.ToString())
	}
}

// TestBufferAllocUnsafe guards against the real requirement found while
// probing undici (round 74, docs/real-node-plan.md): its own
// websocket/constants.js calls Buffer.allocUnsafe(0) at module load time.
func TestBufferAllocUnsafe(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Buffer } from "node:buffer";
		const b = Buffer.allocUnsafe(4);
		JSON.stringify({ length: b.length, isBuffer: Buffer.isBuffer(b) })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"length":4,"isBuffer":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}
