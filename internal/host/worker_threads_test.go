package host

import (
	"strings"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestWorkerConstructorThrowsHonestly(t *testing.T) {
	// Worker was previously built with vm.NewNativeFunction, which defaults
	// IsConstructor to false - `new Worker(...)` never reached this file's
	// own code at all, throwing paserati's own generic "Worker is not a
	// constructor" instead. Confirmed as a real, distinct bug (not the
	// postMessage/terminate no-ops originally suspected) before fixing it -
	// this test guards the fix: a real Worker still isn't implemented, but
	// `new Worker(...)` must at least reach our own code and throw our own
	// specific, honest error.
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Worker } from "node:worker_threads";
		let message = "", code = "";
		try {
			new Worker("/some/path.js");
		} catch (e) {
			message = e.message;
			code = e.code;
		}
		JSON.stringify({ message, code })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	result := val.ToString()
	if strings.Contains(result, "is not a constructor") {
		t.Errorf("new Worker(...) hit paserati's generic constructor guard instead of our own error: %s", result)
	}
	if !strings.Contains(result, "ERR_WORKER_NOT_SUPPORTED") {
		t.Errorf("new Worker(...) did not throw the expected ERR_WORKER_NOT_SUPPORTED error: %s", result)
	}
}
