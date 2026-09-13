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

// TestAsyncLocalStorageConstructible guards the exact real-world shape
// round 117 found breaking real, unmodified @aws-sdk/client-s3: a class
// field initializer (`static storage = new AsyncLocalStorage();`)
// evaluated unconditionally at module load, regardless of whether
// `.run()` is ever subsequently called - round 72's own scoping ("nothing
// reachable calls `.run()`") never considered that construction alone,
// with no usage at all, needs a real constructor to exist.
func TestAsyncLocalStorageConstructible(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { AsyncLocalStorage } from "node:async_hooks";
		class HasStaticField {
			static storage = new AsyncLocalStorage();
		}
		typeof HasStaticField.storage.run
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "function" {
		t.Errorf("AsyncLocalStorage should be constructible with a real .run method, got typeof %q", val.ToString())
	}
}

// TestAsyncLocalStorageSyncRunAndGetStore covers the case async_hooks.go's
// own doc comment says is actually correct: getStore() sees the active
// store while a *synchronous* run() callback (no internal await) is on
// the stack, nested run() calls correctly restore the outer store once
// the inner one returns, and getStore() is undefined both before the
// first run() and after the last one returns. This is the guarantee the
// stack-based implementation genuinely provides, not the cross-await case
// the doc comment says it explicitly does not.
func TestAsyncLocalStorageSyncRunAndGetStore(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { AsyncLocalStorage } from "node:async_hooks";
		const als = new AsyncLocalStorage();
		const before = als.getStore();
		const seen = [];
		als.run({ id: "outer" }, () => {
			seen.push(als.getStore()?.id);
			als.run({ id: "inner" }, () => {
				seen.push(als.getStore()?.id);
			});
			seen.push(als.getStore()?.id);
		});
		const after = als.getStore();
		JSON.stringify({ before, seen, after });
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"seen":["outer","inner","outer"]}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestAsyncLocalStorageDoesNotSurviveAcrossAwait pins down, as an actual
// checked assertion rather than just a doc comment's claim, the exact
// known limitation this implementation has: getStore() called from a
// run() callback's own continuation *after* an internal await no longer
// sees the store, because the stack-based run()'s own `finally` pop fires
// the instant the still-pending promise is returned, not when the
// callback's remaining code actually resumes. If this ever starts
// passing "req-1" for the post-await value, either the implementation
// changed to something genuinely correct (update this test to expect the
// real value and delete this comment) or something coincidental is
// masking the gap - either way this test existing means that change gets
// noticed instead of silently happening.
func TestAsyncLocalStorageDoesNotSurviveAcrossAwait(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { AsyncLocalStorage } from "node:async_hooks";
		const als = new AsyncLocalStorage();
		let beforeAwait, afterAwait;
		await als.run({ id: "req-1" }, async () => {
			beforeAwait = als.getStore()?.id;
			await Promise.resolve();
			afterAwait = als.getStore()?.id;
		});
		JSON.stringify({ beforeAwait, afterAwait });
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"beforeAwait":"req-1"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s (afterAwait should be the known-missing case - see this test's own doc comment)", val.ToString(), want)
	}
}
