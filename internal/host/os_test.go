package host

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func newOSHost(t *testing.T) *driver.Paserati {
	t.Helper()
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	return p
}

// TestOSCpus verifies os.cpus() exists and matches real Node's shape: an
// array of objects with model (string), speed (number), and times (an
// object with user/nice/sys/idle/irq numeric fields) - see
// docs/real-node-plan.md's Sixtieth round entry.
func TestOSCpus(t *testing.T) {
	p := newOSHost(t)
	js := `
		import os from "os";
		const cpus = os.cpus();
		[
			Array.isArray(cpus),
			cpus.length > 0,
			typeof cpus[0].model,
			typeof cpus[0].speed,
			typeof cpus[0].times,
			typeof cpus[0].times.user,
			typeof cpus[0].times.nice,
			typeof cpus[0].times.sys,
			typeof cpus[0].times.idle,
			typeof cpus[0].times.irq,
			cpus.length,
		].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "true|true|string|number|object|number|number|number|number|number|" +
		strconv.Itoa(max(runtime.NumCPU(), 1))
	if val.ToString() != want {
		t.Errorf("os.cpus() shape = %q, want %q", val.ToString(), want)
	}
}

func TestOSCpusNodeAlias(t *testing.T) {
	p := newOSHost(t)
	js := `
		import os from "node:os";
		os.cpus().length > 0
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "true" {
		t.Errorf("node:os cpus() = %q, want true", val.ToString())
	}
}

// TestOSCpusNamedImport covers the `import { cpus } from "os"` named-import
// form (the house pattern used by url_test.go's TestURLParse-style tests),
// distinct from the default-import form the earlier tests use.
func TestOSCpusNamedImport(t *testing.T) {
	p := newOSHost(t)
	js := `
		import { cpus } from "os";
		cpus().length > 0
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "true" {
		t.Errorf("named import cpus() = %q, want true", val.ToString())
	}
}

// TestOSCpusRequire covers the CJS `require("os")` consumer path - the
// actual shape that motivated this fix (an npm package doing
// `const os = require('os'); os.cpus()`), not just ESM import.
func TestOSCpusRequire(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.js")
	if err := os.WriteFile(file, []byte(`
		const os = require("os");
		module.exports = os.cpus().length > 0 && typeof os.cpus()[0].times.user === "number";
	`), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati", file})
	p.SetSkipTypeCheck(true)
	src, _ := os.ReadFile(file)
	val, errs := RunCJS(p, string(src), file)
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	if val.ToString() != "true" {
		t.Errorf("require(\"os\").cpus() = %q, want true", val.ToString())
	}
}
