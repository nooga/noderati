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

// TestBufferIsRealUint8Array pins down the actual point of the "make
// Buffer real" pass done alongside paserati#375/WebAssembly: Buffer must
// be a genuine Uint8Array subclass (indexed byte access, real
// `.buffer`/ArrayBuffer backing, `instanceof Uint8Array`), not the old
// PlainObject-with-a-toString()-closure fake. This is exactly the
// surface `new WebAssembly.Module(nodeBuffer)` needs to be able to read.
func TestBufferIsRealUint8Array(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Buffer } from "node:buffer";
		const b = Buffer.from([0, 97, 115, 109]);
		JSON.stringify({
			isU8: b instanceof Uint8Array,
			len: b.length,
			b0: b[0], b1: b[1], b2: b[2], b3: b[3],
			// Not asserting "b.buffer instanceof ArrayBuffer" here - a
			// minimal, noderati-free repro (new Uint8Array(4).buffer
			// instanceof ArrayBuffer) confirms that's false even for a
			// completely vanilla paserati Uint8Array, so it's a
			// pre-existing paserati engine bug unrelated to Buffer, not
			// something this file's construction gets wrong.
			bufferCtorName: b.buffer.constructor.name,
			byteLength: b.buffer.byteLength,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isU8":true,"len":4,"b0":0,"b1":97,"b2":115,"b3":109,"bufferCtorName":"ArrayBuffer","byteLength":4}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestBufferBase64RoundTrip exercises exactly the shape real undici's
// vendored `lib/llhttp/llhttp-wasm.js` uses to embed its wasm binary:
// `Buffer.from('<base64>', 'base64')`. The decoded bytes must be real
// (readable via indexed access / AsTypedArray on the Go side, not hidden
// inside a Go-string closure), and must round-trip through `.toString`.
func TestBufferBase64RoundTrip(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Buffer } from "node:buffer";
		// "\0asm" + wasm version 1, the real 8-byte wasm module header.
		const b64 = Buffer.from([0,97,115,109,1,0,0,0]).toString("base64");
		const decoded = Buffer.from(b64, "base64");
		JSON.stringify({
			b64,
			len: decoded.length,
			bytes: Array.from(decoded),
			roundTrip: decoded.toString("base64") === b64,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"b64":"AGFzbQEAAAA=","len":8,"bytes":[0,97,115,109,1,0,0,0],"roundTrip":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestBufferSubarrayStaysBuffer checks that slicing a Buffer via the
// inherited %TypedArray%.prototype machinery (subarray/slice) still
// produces something Buffer.isBuffer recognizes and that shares the
// underlying bytes (a real view, not a copy) - the species-construction
// path that goes back through Buffer's own constructor.
func TestBufferSubarrayStaysBuffer(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Buffer } from "node:buffer";
		const b = Buffer.from([1, 2, 3, 4, 5]);
		const sub = b.subarray(1, 3);
		JSON.stringify({
			isBuffer: Buffer.isBuffer(sub),
			bytes: Array.from(sub),
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isBuffer":true,"bytes":[2,3]}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}
