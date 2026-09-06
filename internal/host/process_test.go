package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func newProcessHost(t *testing.T) *driver.Paserati {
	t.Helper()
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	return p
}

// TestProcessHrtimeShape verifies process.hrtime() returns a real Node
// [seconds, nanoseconds] tuple - see docs/real-node-plan.md's Sixtieth
// round entry.
func TestProcessHrtimeShape(t *testing.T) {
	p := newProcessHost(t)
	js := `
		const t = process.hrtime();
		[
			Array.isArray(t),
			t.length,
			typeof t[0],
			typeof t[1],
			t[0] >= 0,
			t[1] >= 0 && t[1] < 1e9,
		].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "true|2|number|number|true|true"
	if val.ToString() != want {
		t.Errorf("process.hrtime() shape = %q, want %q", val.ToString(), want)
	}
}

// TestProcessHrtimeDelta verifies process.hrtime(previous) returns a
// non-negative elapsed delta, and that the delta is nonzero across two
// readings separated by real work (a busy loop, since noderati has no
// sleep primitive available synchronously here).
func TestProcessHrtimeDelta(t *testing.T) {
	p := newProcessHost(t)
	js := `
		const start = process.hrtime();
		let x = 0;
		for (let i = 0; i < 1e7; i++) { x += i; }
		const diff = process.hrtime(start);
		[
			Array.isArray(diff),
			diff.length,
			diff[0] >= 0,
			diff[1] >= 0 && diff[1] < 1e9,
			(diff[0] > 0 || diff[1] > 0),
		].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "true|2|true|true|true"
	if val.ToString() != want {
		t.Errorf("process.hrtime(previous) = %q, want %q", val.ToString(), want)
	}
}
