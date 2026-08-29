package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestReadlineCreateInterface(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { createInterface, emitKeypressEvents } from "node:readline";
		import { Readable, Writable } from "node:stream";
		const input = new Readable();
		const output = new Writable();
		let wrote = "";
		output.on("data", (c) => { wrote += c; });
		emitKeypressEvents(input);
		const rl = createInterface({ input, output, terminal: false });
		typeof rl.question === "function" && typeof rl.close === "function" ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("createInterface = %q, want ok", val.ToString())
	}
}

func TestReadlineImportDoesNotThrow(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	_, errs := p.RunCode(`import { createInterface } from "node:readline";`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("import readline: %v", errs[0])
	}
}
