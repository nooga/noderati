package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestMarkAsUncloneableActuallyBlocksClone drives the exact real
// requirement found while probing undici (round 74, docs/real-node-plan.md):
// real undici's webidl/index.js wires
// `webidl.util.markAsUncloneable = require('node:worker_threads').markAsUncloneable`,
// then calls it from CacheStorage/Cache/Request/Response constructors.
// A real implementation must actually make a later structuredClone()
// throw, not just exist as a callable no-op.
func TestMarkAsUncloneableActuallyBlocksClone(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { markAsUncloneable } from "node:worker_threads";
		const plain = { a: 1 };
		const marked = { b: 2 };
		markAsUncloneable(marked);

		const clonedPlain = structuredClone(plain);
		let threw = false;
		try {
			structuredClone(marked);
		} catch (e) {
			threw = true;
		}
		JSON.stringify({ clonedPlainOk: clonedPlain.a === 1, threw })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"clonedPlainOk":true,"threw":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}
