package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestPathJoin(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import { join } from "path";
		import path from "node:path";
		join("a", "b") === path.join("a", "b") ? join("a", "b") : "mismatch"
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "a/b" && val.ToString() != `a\b` {
		t.Errorf("path.join = %q", val.ToString())
	}
}

func TestProcessArgv(t *testing.T) {
	p := New([]string{"/usr/bin/noderati", "script.js", "x"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`process.argv.join(",")`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "/usr/bin/noderati,script.js,x" {
		t.Errorf("argv = %q", val.ToString())
	}
}
