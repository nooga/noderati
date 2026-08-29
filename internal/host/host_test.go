package host

import (
	"os"
	"path/filepath"
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

func TestNestedNativeImport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib.js"), `
		import { join } from "path";
		export const p = join("a", "b");
	`)
	appPath := filepath.Join(root, "app.js")
	writeFile(t, appPath, `import { p } from "./lib.js"; p`)

	p := New([]string{"noderati", appPath})
	p.SetSkipTypeCheck(true)
	source, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	val, errs := p.RunCode(string(source), driver.RunOptions{ModuleName: appPath, Filename: appPath})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "a/b" && val.ToString() != `a\b` {
		t.Errorf("nested native import = %q", val.ToString())
	}
}

func TestGlobalAlias(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`global === globalThis`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsBoolean() || !val.AsBoolean() {
		t.Errorf("global === globalThis = %v", val)
	}
}

func TestRelativeImportFromAbsoluteModulePath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib.js"), `export const msg = "from-lib";`)
	appPath := filepath.Join(root, "app.js")
	writeFile(t, appPath, `import { msg } from "./lib.js"; msg`)

	p := New([]string{"noderati", appPath})
	p.SetSkipTypeCheck(true)

	source, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	val, errs := p.RunCode(string(source), driver.RunOptions{ModuleName: appPath, Filename: appPath})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "from-lib" {
		t.Errorf("relative import = %q, want %q", val.ToString(), "from-lib")
	}
}

func TestNestedRelativeImport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sub", "inner.js"), `export const n = 7;`)
	writeFile(t, filepath.Join(root, "mid.js"), `import { n } from "./sub/inner.js"; export const doubled = n * 2;`)
	appPath := filepath.Join(root, "app.js")
	writeFile(t, appPath, `import { doubled } from "./mid.js"; doubled`)

	p := New([]string{"noderati", appPath})
	p.SetSkipTypeCheck(true)
	source, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	val, errs := p.RunCode(string(source), driver.RunOptions{ModuleName: appPath, Filename: appPath})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "14" {
		t.Errorf("nested import = %q, want 14", val.ToString())
	}
}

func TestPathRelativeAndWin32(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { relative, win32 } from "path";
		relative("/a/b", "/a/c") + ":" + typeof win32
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "../c:object" && val.ToString() != "..\\c:object" {
		t.Errorf("path extras = %q", val.ToString())
	}
}
