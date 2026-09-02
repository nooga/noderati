package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestV8Require(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import v8 from "node:v8";
		const stats = v8.getHeapStatistics();
		v8.setFlagsFromString("--foo"); // must not throw
		[
			typeof stats.used_heap_size,
			stats.used_heap_size > 0,
			stats.heap_size_limit >= stats.used_heap_size,
			v8.startupSnapshot.isBuildingSnapshot(),
		].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "number|true|true|false"
	if val.ToString() != want {
		t.Errorf("v8 module = %q, want %q", val.ToString(), want)
	}
}

// TestV8RequireViaCJS guards cjs.go's nativeRequireNames entry: `import`
// and `require()` of "node:v8" go through genuinely different lookup
// paths (see nativeRequireNames's doc comment) and both need to work.
func TestV8RequireViaCJS(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `module.exports = require("node:v8").startupSnapshot.isBuildingSnapshot();`
	val, errs := RunCJS(p, js, "/virtual/entry.cjs")
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	if val.IsTruthy() {
		t.Errorf("isBuildingSnapshot() = true, want false")
	}
}
