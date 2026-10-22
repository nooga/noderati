package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestConsoleShimCustomStream drives the exact real call pattern from
// undici's pending-interceptors-formatter.js: construct a Console bound
// to a custom writable stream and confirm output actually reaches it,
// rather than only checking the class is constructible.
func TestConsoleShimCustomStream(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Console } from "node:console";

		let written = "";
		const fakeStream = {
			write(s) { written += s; return true; },
		};
		const logger = new Console({ stdout: fakeStream, inspectOptions: { colors: false } });
		logger.log("hello", 42);
		written
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if want := "hello 42\n"; val.ToString() != want {
		t.Errorf("got %q, want %q", val.ToString(), want)
	}
}

// As in Node, a Console needs a writable stream.
func TestConsoleRequiresWritableStream(t *testing.T) {
	got := runScriptString(t, `
		import { Console } from "node:console";
		let msg;
		try { new Console({}); } catch (e) { msg = e.code + ": " + e.message; }
		msg
	`)
	if want := "ERR_CONSOLE_WRITABLE_STREAM: Console expects a writable stream instance for stdout"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTimersShim(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { setTimeout, clearTimeout } from "node:timers";
		let fired = false;
		await new Promise((resolve) => {
			const id = setTimeout(() => { fired = true; resolve(); }, 5);
			typeof id;
		});
		fired
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Error("expected node:timers' setTimeout to actually fire")
	}
}
