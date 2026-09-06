package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestStreamPipelinePromises(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Readable, Writable } from "node:stream";
		import { pipeline } from "node:stream/promises";

		class Src extends Readable {}
		class Dst extends Writable {
			collected = "";
			write(chunk) { this.collected += chunk; return super.write(chunk); }
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
			write(chunk) { this.collected += chunk; return super.write(chunk); }
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
