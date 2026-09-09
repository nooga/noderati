package host

import (
	"strings"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestAsyncResourceSubclassAndRunInAsyncScope drives the exact real call
// pattern found in real undici's api-request.js and its four siblings:
// `class X extends AsyncResource { constructor() { super('TYPE') } }`
// then `this.runInAsyncScope(fn, thisArg, ...args)` - confirmed against
// the actual vendored source before writing async_hooks.go, not assumed.
func TestAsyncResourceSubclassAndRunInAsyncScope(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { AsyncResource } from "node:async_hooks";

		class Handler extends AsyncResource {
			constructor() {
				super("UNDICI_REQUEST");
			}
		}

		const h = new Handler();
		let seenThis, seenArgs;
		const result = h.runInAsyncScope(function (a, b) {
			seenThis = this;
			seenArgs = [a, b];
			return a + b;
		}, { tag: "recv" }, 2, 3);

		JSON.stringify({
			result,
			seenThisTag: seenThis.tag,
			seenArgs,
			asyncIdIsNumber: typeof h.asyncId() === "number",
			triggerAsyncIdIsNumber: typeof h.triggerAsyncId() === "number",
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"result":5,"seenThisTag":"recv","seenArgs":[2,3],"asyncIdIsNumber":true,"triggerAsyncIdIsNumber":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestAsyncResourceRunInAsyncScopePropagatesThrow guards that
// runInAsyncScope is a real forwarding call, not a swallowing wrapper -
// undici's own handlers rely on a callback's thrown error surfacing to
// their caller (it's the mechanism their try/catch-around-dispatch uses).
func TestAsyncResourceRunInAsyncScopePropagatesThrow(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	_, errs := p.RunCode(`
		import { AsyncResource } from "node:async_hooks";
		const r = new AsyncResource("TEST");
		r.runInAsyncScope(() => { throw new Error("boom"); }, null);
	`, driver.RunOptions{})
	if len(errs) == 0 {
		t.Fatal("expected the thrown error to propagate out of runInAsyncScope")
	}
	if !strings.Contains(errs[0].Error(), "boom") {
		t.Errorf("error = %q, want it to mention 'boom'", errs[0].Error())
	}
}

// TestAsyncHooksCJSRequire mirrors the real shape real undici actually
// uses - `const { AsyncResource } = require('node:async_hooks')` from a
// CJS module - not just the ESM import path. Guards the same class of
// require()-vs-import gap net/tls/http/https hit (nativeRequireNames).
func TestAsyncHooksCJSRequire(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := RunCJS(p, `
		const { AsyncResource } = require('node:async_hooks');
		class H extends AsyncResource {
			constructor() { super('T'); }
		}
		module.exports = new H().runInAsyncScope(() => 42, null);
	`, "/virtual/test.js")
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	if val.ToFloat() != 42 {
		t.Errorf("got %v, want 42", val)
	}
}

// TestAsyncLocalStorageNotExported documents the deliberate omission (see
// async_hooks.go's own doc comment) as an actual, checked assertion
// rather than an unstated absence - a future accidental re-add of a
// broken stack-based AsyncLocalStorage would need to touch this test.
func TestAsyncLocalStorageNotExported(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import * as ah from "node:async_hooks";
		typeof ah.AsyncLocalStorage
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "undefined" {
		t.Errorf("AsyncLocalStorage should not be exported (see async_hooks.go), got typeof %q", val.ToString())
	}
}
