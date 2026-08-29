package host

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCJSModuleExportsGetter(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "getter.js")
	if err := os.WriteFile(file, []byte(`
		Object.defineProperty(module, 'exports', {
			enumerable: true,
			get: function () { return { tag: "ok" }; }
		});
	`), 0644); err != nil {
		t.Fatal(err)
	}
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := RunCJS(p, string(mustRead(t, file)), file)
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	obj := val.AsPlainObject()
	if obj == nil {
		t.Fatalf("exports = %v", val.Inspect())
	}
	tag, ok := obj.Get("tag")
	if !ok || tag.ToString() != "ok" {
		t.Errorf("module.exports getter = %v", val.Inspect())
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
