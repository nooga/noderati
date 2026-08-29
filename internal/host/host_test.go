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

func TestExportedClassStaticCreate(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "cls.js")
	if err := os.WriteFile(mod, []byte(`
		export class Foo {
			static create() { return new Foo(); }
		}
	`), 0644); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "app.js")
	if err := os.WriteFile(app, []byte(`
		import { Foo } from "./cls.js";
		const f = Foo.create();
		f ? "ok" : "no"
	`), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati", app})
	p.SetSkipTypeCheck(true)
	src, _ := os.ReadFile(app)
	val, errs := p.RunCode(string(src), driver.RunOptions{ModuleName: app, Filename: app})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("export class static create = %q, want ok", val.ToString())
	}
}

func TestStructuredClone(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const src = { a: 1, b: ["x", { y: true }] };
		const copy = structuredClone(src);
		copy.b[1].y = false;
		src.b[1].y === true && copy.a === 1 && copy.b[0] === "x" ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("structuredClone = %q, want ok", val.ToString())
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

func TestProcessStdinIsTTY(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`typeof process.stdin.isTTY + "," + typeof process.stdin.on`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "boolean,function" {
		t.Errorf("stdin = %q, want boolean,function", val.ToString())
	}
}

func TestProcessStdoutWriteCallback(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let called = false;
		process.stdout.write("", () => { called = true; });
		await new Promise((resolve) => process.nextTick(resolve));
		called && typeof process.on === "function" ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("stdout write callback = %q, want ok", val.ToString())
	}
}

func TestProcessStdinPipeEnd(t *testing.T) {
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
	})
	go func() {
		_, _ = w.Write([]byte("hi"))
		_ = w.Close()
	}()

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let data = "";
		await new Promise((resolve) => {
			process.stdin.on("data", (c) => { data += c; });
			process.stdin.on("end", resolve);
			process.stdin.resume();
		});
		data
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hi" {
		t.Errorf("stdin pipe = %q, want hi", val.ToString())
	}
}

func TestNamedReExport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "uuid.js"), `
		export function uuidv7() { return "id-1"; }
	`)
	writeFile(t, filepath.Join(dir, "index.js"), `
		export { uuidv7 } from "./uuid.js";
	`)
	app := filepath.Join(dir, "app.js")
	writeFile(t, app, `
		import { uuidv7 } from "./index.js";
		uuidv7()
	`)

	p := New([]string{"noderati", app})
	p.SetSkipTypeCheck(true)
	src, err := os.ReadFile(app)
	if err != nil {
		t.Fatal(err)
	}
	val, errs := p.RunCode(string(src), driver.RunOptions{ModuleName: app, Filename: app})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "id-1" {
		t.Errorf("named re-export = %q, want id-1", val.ToString())
	}
}

func TestNamedClassReExport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "agent.js"), `
		export class Agent {
			constructor() { this.ok = true; }
		}
	`)
	writeFile(t, filepath.Join(dir, "index.js"), `
		export { Agent } from "./agent.js";
	`)
	app := filepath.Join(dir, "app.js")
	writeFile(t, app, `
		import { Agent } from "./index.js";
		const a = new Agent();
		a && a.ok ? "ok" : "no"
	`)

	p := New([]string{"noderati", app})
	p.SetSkipTypeCheck(true)
	src, err := os.ReadFile(app)
	if err != nil {
		t.Fatal(err)
	}
	val, errs := p.RunCode(string(src), driver.RunOptions{ModuleName: app, Filename: app})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("named class re-export = %q, want ok", val.ToString())
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
