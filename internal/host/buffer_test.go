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
			// Was carefully NOT asserted here through paserati#377: a
			// minimal, noderati-free repro (new Uint8Array(4).buffer
			// instanceof ArrayBuffer) confirmed that false even for a
			// completely vanilla paserati Uint8Array - filed as #377,
			// fixed upstream and pulled (paserati@c6a66eda), so this now
			// asserts the real, correct behavior instead of routing
			// around it.
			bufIsArrayBuffer: b.buffer instanceof ArrayBuffer,
			byteLength: b.buffer.byteLength,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isU8":true,"len":4,"b0":0,"b1":97,"b2":115,"b3":109,"bufIsArrayBuffer":true,"byteLength":4}`
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

// TestBufferBase64FullByteRange is the test the base64 bridge actually
// needs to survive: real wasm binaries (real undici's own vendored
// llhttp-wasm.js included - confirmed directly by reading a real
// installed copy's source, which does exactly `wasmBuffer =
// Buffer.from(wasmBase64, 'base64')`) contain every byte value 0-255,
// not just the small integers TestBufferBase64RoundTrip happens to use.
// A signedness or truncation bug in the base64 decode path would still
// pass that test while corrupting real wasm bytes.
func TestBufferBase64FullByteRange(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Buffer } from "node:buffer";
		const bytes = Array.from({ length: 256 }, (_, i) => i);
		const b64 = Buffer.from(bytes).toString("base64");
		const decoded = Buffer.from(b64, "base64");
		const matches = decoded.length === 256 && Array.from(decoded).every((v, i) => v === bytes[i]);
		JSON.stringify({ length: decoded.length, matches })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"length":256,"matches":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestBufferSubarrayDoesNotMutateReceiver guards the reparenting done by
// wrapInheritedTypedArrayMethod: it must only touch the *returned* view,
// never the receiver it was called on - otherwise calling
// Buffer.from(existingUint8Array).subarray() would reach back and turn
// the caller's own, unrelated Uint8Array into something
// Buffer.isBuffer() reports true for.
func TestBufferSubarrayDoesNotMutateReceiver(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Buffer } from "node:buffer";
		const u = new Uint8Array([1, 2, 3]);
		const b = Buffer.from(u);
		b.subarray();
		JSON.stringify({ uIsU8: u instanceof Uint8Array, uIsBuffer: Buffer.isBuffer(u) })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"uIsU8":true,"uIsBuffer":false}`
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
