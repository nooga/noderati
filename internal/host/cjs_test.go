package host

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequirePathJoin(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.js")
	if err := os.WriteFile(file, []byte(`
		const path = require("path");
		module.exports = path.join("a", "b");
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
	if val.ToString() != "a/b" && val.ToString() != `a\b` {
		t.Errorf("path.join via require = %q", val.ToString())
	}
}

func TestRequireRelativeCJS(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib.js")
	app := filepath.Join(dir, "app.js")
	if err := os.WriteFile(lib, []byte(`exports.n = 7;`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app, []byte(`module.exports = require("./lib").n;`), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati", app})
	p.SetSkipTypeCheck(true)
	src, _ := os.ReadFile(app)
	val, errs := RunCJS(p, string(src), app)
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	if val.ToString() != "7" {
		t.Errorf("relative require = %q", val.ToString())
	}
}

func TestRequireMissingThrows(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.js")
	if err := os.WriteFile(file, []byte(`
		try { require("no-such-noderati-pkg"); module.exports = "no"; } catch (e) { module.exports = "yes"; }
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
	if val.ToString() != "yes" {
		t.Errorf("missing require = %q", val.ToString())
	}
}
