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

// TestRequireJSONParsesNotExecutes guards the exact real gap found
// chasing the Bedrock investigation (docs/real-node-plan.md, round 96):
// real, unmodified @aws-sdk/client-bedrock-runtime's own dist-cjs
// runtimeConfig.js does `require("../package.json")` for its own
// package-version metadata - a real, common CJS pattern, not synthetic.
// require() used to wrap *any* resolved file's raw text in the CJS
// function template unconditionally, including .json files, which threw
// a JS syntax error parsing `{ "name": ..., ... }` as a function body
// instead of real Node's own behavior: a required .json file's content
// is parsed as JSON, never executed as JS.
func TestRequireJSONParsesNotExecutes(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.json")
	appPath := filepath.Join(dir, "app.js")
	if err := os.WriteFile(dataPath, []byte(`{"name": "widget", "count": 3, "tags": ["a", "b"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appPath, []byte(`
		const data = require("./data.json");
		module.exports = JSON.stringify({ name: data.name, count: data.count, tagsLen: data.tags.length });
	`), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati", appPath})
	p.SetSkipTypeCheck(true)
	src, _ := os.ReadFile(appPath)
	val, errs := RunCJS(p, string(src), appPath)
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	want := `{"name":"widget","count":3,"tagsLen":2}`
	if val.ToString() != want {
		t.Errorf("require(json) = %q, want %q", val.ToString(), want)
	}
}

// TestRequireJSONCached guards that a second require() of the same
// .json file returns the same cached module (not a silent re-parse each
// time), matching require()'s own module-caching contract for every
// other extension.
func TestRequireJSONCached(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.json")
	appPath := filepath.Join(dir, "app.js")
	if err := os.WriteFile(dataPath, []byte(`{"n": 1}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appPath, []byte(`
		const a = require("./data.json");
		const b = require("./data.json");
		module.exports = a === b;
	`), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati", appPath})
	p.SetSkipTypeCheck(true)
	src, _ := os.ReadFile(appPath)
	val, errs := RunCJS(p, string(src), appPath)
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Error("expected two require()s of the same .json file to return the identical cached object")
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
