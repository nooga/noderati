package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// Both pipeline tests below used to override write() directly instead
// of _write() (real Node's own actual extension point), calling
// super.write(chunk) - confirmed directly against real Node (round 101,
// docs/real-node-plan.md) that this exact shape throws
// ERR_METHOD_NOT_IMPLEMENTED there too (super.write() -> the base
// Writable's own _write(), never overridden here, throws). Both were
// coincidentally passing only because stream.go's own Writable._write()
// default used to silently succeed instead of throwing like real Node -
// fixed the same round these are corrected in.
func TestStreamPipelinePromises(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Readable, Writable } from "node:stream";
		import { pipeline } from "node:stream/promises";

		class Src extends Readable {}
		class Dst extends Writable {
			collected = "";
			_write(chunk, _encoding, callback) { this.collected += chunk; callback(); }
		}
		const src = new Src();
		const dst = new Dst();
		const done = pipeline(src, dst);
		src.emit("data", "a");
		src.emit("data", "b");
		src.emit("end");
		await done;
		dst.collected
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ab" {
		t.Errorf("pipeline collected = %q, want %q", val.ToString(), "ab")
	}
}

func TestStreamPipelineCallback(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Readable, Writable, pipeline } from "node:stream";

		class Src extends Readable {}
		class Dst extends Writable {
			collected = "";
			_write(chunk, _encoding, callback) { this.collected += chunk; callback(); }
		}
		const src = new Src();
		const dst = new Dst();
		const result = await new Promise((resolve, reject) => {
			pipeline(src, dst, (err) => err ? reject(err) : resolve(dst.collected));
			src.emit("data", "x");
			src.emit("end");
		});
		result
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "x" {
		t.Errorf("callback pipeline collected = %q, want %q", val.ToString(), "x")
	}
}

func TestStreamPipelineRejectsOnError(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Readable, Writable } from "node:stream";
		import { pipeline } from "node:stream/promises";

		const src = new Readable();
		const dst = new Writable();
		const done = pipeline(src, dst);
		src.emit("error", new Error("boom"));
		let caught = "";
		try {
			await done;
		} catch (e) {
			caught = e.message;
		}
		caught
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "boom" {
		t.Errorf("pipeline error = %q, want %q", val.ToString(), "boom")
	}
}

// TestReadableDestroyIsReentrancySafe guards the exact real shape
// found while stress-testing real undici's fetch() past its first
// success (round 81, docs/real-node-plan.md): real undici's own
// lib/web/fetch/index.js registers a body's own error handler as
// "this.body.on('error', onError)", where onError itself calls
// "this.body.destroy(error)" - so destroy(err) is expected to be
// called again from within a listener that its own first call to
// destroy() invoked. Without a re-entrancy guard, destroy(err)
// unconditionally re-emits 'error' every time it's called, which
// re-invokes the same listener, which calls destroy(err) again -
// genuine infinite recursion (a VM stack overflow that, unlike a
// caught JS exception, never stopped script execution on its own).
func TestReadableDestroyIsReentrancySafe(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Readable } from "node:stream";

		const r = new Readable();
		let errorCount = 0;
		let closeCount = 0;
		r.on("error", (err) => {
			errorCount++;
			// Real undici's own pattern: the 'error' listener itself
			// calls destroy() again on the same stream.
			r.destroy(err);
		});
		r.on("close", () => { closeCount++; });
		r.destroy(new Error("boom"));
		JSON.stringify({ errorCount, closeCount, destroyed: r.destroyed })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"errorCount":1,"closeCount":1,"destroyed":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestStreamEventEmitterIsRealEventsClass confirms stream.go's
// Readable/Writable/Transform now inherit from node:events' own real
// EventEmitter (via `import EventEmitter from "events"`) rather than a
// separate, hand-rolled duplicate class - the fix for
// docs/real-node-plan.md's ledger note ("stream.go also hand-rolls its
// own EventEmitter instead of reusing events.go's - pick one").
// instanceof across the two modules only holds if they're genuinely
// the same class object, not two separately-defined ones that happen
// to share a method set.
func TestStreamEventEmitterIsRealEventsClass(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { EventEmitter } from "node:events";
		import { Readable, Writable, Transform } from "node:stream";

		JSON.stringify({
			readable: new Readable() instanceof EventEmitter,
			writable: new Writable() instanceof EventEmitter,
			transform: new Transform() instanceof EventEmitter,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"readable":true,"writable":true,"transform":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestStreamEmitBindsThisToEmitter guards the specific behavioral
// divergence the stream.go/events.go EventEmitter duplication caused
// silently: events.go's own emit() was fixed to invoke listeners with
// the emitter bound as `this` (real Node's own behavior, needed by real
// undici's socket connect handler - see emitter.go's emitOnObject), but
// that fix only ever touched events.go's copy of the class - stream.go
// kept its own separate emit() that called listeners with a plain
// function call, `this` left undefined. Now that stream.go imports the
// real EventEmitter instead of duplicating it, a plain-function
// listener registered on a Readable must see the stream itself as
// `this`, exactly like a listener registered directly via
// node:events' own EventEmitter would.
func TestStreamEmitBindsThisToEmitter(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Readable } from "node:stream";

		const r = new Readable();
		let sawSelf = false;
		r.on("data", function (chunk) {
			sawSelf = this === r;
		});
		r.push("x");
		sawSelf
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsBoolean() || !val.AsBoolean() {
		t.Errorf("listener's this !== the Readable emitter: %v", val)
	}
}

// TestStreamTransformSubclassable drives the exact real requirement
// found while probing undici: real undici's own
// lib/web/eventsource/eventsource-stream.js does
// "class EventSourceStream extends Transform", so a missing Transform
// throws "Class extends value undefined is not a constructor or null"
// at require() time. A subclass overriding _transform() must have its
// override actually drive write()'s output, not the base class's
// identity default.
func TestStreamTransformSubclassable(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Transform } from "node:stream";

		class Upper extends Transform {
			_transform(chunk, _encoding, callback) {
				callback(null, String(chunk).toUpperCase());
			}
		}

		const t = new Upper();
		let result = "";
		let ended = false;
		t.on("data", (chunk) => { result += chunk; });
		t.on("end", () => { ended = true; });
		t.write("hello ");
		t.end("world");
		JSON.stringify({ result, ended })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"result":"HELLO WORLD","ended":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestStreamTransformDefaultIsPassthrough checks the base class's own
// (unoverridden) _transform default - an honest identity pass-through,
// not a stub that drops data.
func TestStreamTransformDefaultIsPassthrough(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Transform } from "node:stream";
		const t = new Transform();
		let result = "";
		t.on("data", (chunk) => { result += chunk; });
		t.write("abc");
		t.end("def");
		result
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "abcdef" {
		t.Errorf("got %q, want %q", val.ToString(), "abcdef")
	}
}

// TestStreamTransformPipesToDest checks Transform's own .pipe() (used
// by real undici's pipeline() to chain a Transform into the next stage)
// actually forwards transformed output - to a real Writable subclass
// overriding _write(), matching real Node's own actual contract. This
// used to pipe into a bare `new Writable()` and read the result via a
// "data" listener on the *destination* - confirmed directly against
// real Node (round 101, docs/real-node-plan.md) that this was never a
// real, valid shape at all: a plain Writable never emits "data" (that's
// Readable-only), and writing to one with no _write() override throws
// ERR_METHOD_NOT_IMPLEMENTED in real Node. The test's own premise was
// wrong from the start, coincidentally passing only because
// stream.go's Writable.write() had the exact matching bug (emitting
// "data" instead of calling _write()) - fixed the same round this test
// is corrected in.
func TestStreamTransformPipesToDest(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Transform, Writable } from "node:stream";

		class Double extends Transform {
			_transform(chunk, _encoding, callback) {
				callback(null, chunk + chunk);
			}
		}
		class Collector extends Writable {
			result = "";
			_write(chunk, _encoding, callback) {
				this.result += chunk;
				callback();
			}
		}

		const t = new Double();
		const w = new Collector();
		t.pipe(w);
		t.write("ab");
		t.end();
		w.result
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "abab" {
		t.Errorf("got %q, want %q", val.ToString(), "abab")
	}
}

// TestStreamWritableDefaultWriteThrows guards real Node's own exact
// behavior for a Writable whose _write() is never overridden: a real
// ERR_METHOD_NOT_IMPLEMENTED error, not a silent success - confirmed
// directly against real Node before fixing (round 101,
// docs/real-node-plan.md).
func TestStreamWritableDefaultWriteThrows(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Writable } from "node:stream";
		const w = new Writable();
		let code = "";
		w.on("error", (e) => { code = e.code; });
		w.write("x");
		code
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ERR_METHOD_NOT_IMPLEMENTED" {
		t.Errorf("got %q, want %q", val.ToString(), "ERR_METHOD_NOT_IMPLEMENTED")
	}
}

// TestStreamDuplexSubclassable drives the exact real shape that
// surfaced Duplex's absence in the first place (docs/real-node-plan.md,
// Round 93): real, unmodified @smithy/core's own dist-cjs
// submodules/serde/index.js's ChecksumStream `extends Duplex`, is
// .pipe()'d into from an upstream source (driving _write independently
// of anything reading from it), and separately pushes its own output
// via this.push() for whatever reads it as a Readable - the two sides
// genuinely independent, unlike Transform's write-drives-read pipeline.
func TestStreamDuplexSubclassable(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Duplex, Readable } from "node:stream";

		class Upper extends Duplex {
			_write(chunk, _encoding, callback) {
				this.push(String(chunk).toUpperCase());
				callback();
			}
			// A real Duplex's two sides are independent - ending the
			// writable half doesn't end the readable half on its own
			// (real Node's own default allowHalfOpen behavior); a
			// subclass that wants "end write -> end read" has to say so
			// itself, exactly like real ChecksumStream's own _final does.
			_final(callback) {
				this.push(null);
				callback();
			}
		}

		const source = new Readable();
		const t = new Upper();
		let result = "";
		let ended = false;
		t.on("data", (chunk) => { result += chunk; });
		t.on("end", () => { ended = true; });
		source.pipe(t);
		source.push("hello ");
		source.push("world");
		source.push(null);
		JSON.stringify({ result, ended })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"result":"HELLO WORLD","ended":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestStreamPassThroughIsIdentity checks real Node's own PassThrough
// contract: a Transform with no overrides at all, passing data through
// unchanged - not a separate implementation of its own.
func TestStreamPassThroughIsIdentity(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { PassThrough } from "node:stream";
		const t = new PassThrough();
		let result = "";
		t.on("data", (chunk) => { result += chunk; });
		t.write("abc");
		t.end("def");
		result
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "abcdef" {
		t.Errorf("got %q, want %q", val.ToString(), "abcdef")
	}
}
