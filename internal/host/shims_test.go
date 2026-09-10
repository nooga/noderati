package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestPerfHooksShim(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { performance } from "node:perf_hooks";
		typeof performance.now === "function" ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("perf_hooks = %q, want ok", val.ToString())
	}
}

// StringDecoder's own tests moved to stringdecoder_test.go once it
// became a real implementation (docs/real-node-plan.md) instead of a
// JS-string shim that just did String(c) - see that file for coverage.
