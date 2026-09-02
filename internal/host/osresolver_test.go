package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestImportRelativeCJSFile guards the OSPathResolver fix: a static ESM
// `import` of a relative `.cjs` file (or a CJS-shaped `.js` file) must be
// CJS-wrapped — require/module/exports injected — the same way
// NodeModulesResolver already wraps a `.cjs` file reached via a bare
// package specifier. Before this fix, OSPathResolver read the file as
// plain source with no wrapping at all, so any top-level `require(...)`
// call inside it threw "require is not defined" — found via jiti's real
// lib/jiti-static.mjs, which does exactly this
// (`import _createJiti from "../dist/jiti.cjs"`, and dist/jiti.cjs itself
// calls require("node:os") etc. at module scope).
func TestImportRelativeCJSFile(t *testing.T) {
	dir := t.TempDir()
	cjsFile := filepath.Join(dir, "foo.cjs")
	mainFile := filepath.Join(dir, "main.mjs")
	if err := os.WriteFile(cjsFile, []byte(`
		const path = require("path");
		module.exports = function greet() {
			return "hi from cjs, join=" + path.join("a", "b");
		};
	`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainFile, []byte(`
		import greet from "./foo.cjs";
		greet()
	`), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati", mainFile})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(readFile(t, mainFile), driver.RunOptions{ModuleName: mainFile, Filename: mainFile})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "hi from cjs, join=" + filepath.Join("a", "b")
	if val.ToString() != want {
		t.Errorf("import of relative .cjs = %q, want %q", val.ToString(), want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
