package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestPromiseWithResolvers drives the exact real call pattern found
// while probing undici: real undici's own lib/web/fetch/index.js does
// `let p = Promise.withResolvers()` at the top of its exported fetch(),
// so every real fetch() call needs this to actually work - not just
// exist as a shape-only stub. Checks both the resolve and reject paths.
func TestPromiseWithResolvers(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const { promise, resolve } = Promise.withResolvers();
		resolve(42);
		const resolvedValue = await promise;

		const { promise: p2, reject } = Promise.withResolvers();
		reject(new Error("nope"));
		let caught = "";
		try { await p2; } catch (e) { caught = e.message; }

		JSON.stringify({ resolvedValue, caught, isPromise: promise instanceof Promise })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"resolvedValue":42,"caught":"nope","isPromise":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}
