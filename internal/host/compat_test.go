package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestTTYIsatty(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { isatty } from "node:tty";
		typeof isatty(1)
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "boolean" {
		t.Errorf("isatty type = %q, want boolean", val.ToString())
	}
}

func TestProcessModuleDefault(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import process from "node:process";
		import { release } from "node:os";
		import { isatty } from "node:tty";
		typeof process.env === "object" && Array.isArray(process.argv) && typeof release === "function" && typeof isatty === "function" ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("process module combo = %q", val.ToString())
	}
}

func TestTwoHashDefaultImports(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "package.json"), `{
		"name": "pkg",
		"type": "module",
		"imports": {
			"#a": "./a.js",
			"#b": "./b.js"
		},
		"exports": "./index.js"
	}`)
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "a.js"), `export default { tag: "a" };`)
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "b.js"), `export default { tag: "b" };`)
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "index.js"), `
		import a from "#a";
		import b from "#b";
		export default (a && a.tag) + (b && b.tag);
	`)

	appPath := filepath.Join(root, "app.ts")
	writeFile(t, appPath, `import pkg from "pkg"; pkg`)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

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
	if val.ToString() != "ab" {
		t.Errorf("two hash defaults = %q, want ab", val.ToString())
	}
}

func TestPackageImportsResolver(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "package.json"), `{
		"name": "pkg",
		"type": "module",
		"imports": { "#util": "./util.js" },
		"exports": "./index.js"
	}`)
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "util.js"), `export const answer = 42;`)
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "index.js"), `
		import { answer } from "#util";
		export default answer;
	`)

	appPath := filepath.Join(root, "app.ts")
	writeFile(t, appPath, `import pkg from "pkg"; pkg`)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

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
	if val.ToString() != "42" {
		t.Errorf("package import = %q, want 42", val.ToString())
	}
}

func TestOSRelease(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`import { release } from "node:os"; typeof release()`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "string" {
		t.Errorf("os.release type = %q", val.ToString())
	}
}

func TestNestedExportsResolution(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "node_modules", "nested-exp", "package.json"), `{
		"name": "nested-exp",
		"type": "module",
		"exports": {
			".": {
				"import": { "default": "./entry.js" }
			}
		}
	}`)
	writeFile(t, filepath.Join(root, "node_modules", "nested-exp", "entry.js"), `export default "nested";`)

	appPath := filepath.Join(root, "app.ts")
	writeFile(t, appPath, `import v from "nested-exp"; v`)

	origWD, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	p := New([]string{"noderati", appPath})
	p.SetSkipTypeCheck(true)
	source, _ := os.ReadFile(appPath)
	val, errs := p.RunCode(string(source), driver.RunOptions{ModuleName: appPath, Filename: appPath})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "nested" {
		t.Errorf("nested exports = %q", val.ToString())
	}
}

func TestCJSNamedExports(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "node_modules", "cjs-named", "package.json"), `{
		"name": "cjs-named",
		"main": "index.js"
	}`)
	writeFile(t, filepath.Join(root, "node_modules", "cjs-named", "index.js"), `
		exports.parse = function (s) { return s.toUpperCase(); };
		exports.defaultTag = "d";
	`)
	appPath := filepath.Join(root, "app.ts")
	writeFile(t, appPath, `import { parse } from "cjs-named"; parse("hi")`)

	origWD, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	p := New([]string{"noderati", appPath})
	p.SetSkipTypeCheck(true)
	source, _ := os.ReadFile(appPath)
	val, errs := p.RunCode(string(source), driver.RunOptions{ModuleName: appPath, Filename: appPath})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "HI" {
		t.Errorf("named cjs export = %q, want HI", val.ToString())
	}
}

func TestCJSNamedExportValid(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "node_modules", "cjs-valid", "package.json"), `{
		"name": "cjs-valid",
		"main": "index.js"
	}`)
	writeFile(t, filepath.Join(root, "node_modules", "cjs-valid", "index.js"), `
		exports.valid = function (s) { return s === "ok"; };
		exports.parse = function (s) { return s; };
	`)
	appPath := filepath.Join(root, "app.ts")
	writeFile(t, appPath, `
		import { valid, parse } from "cjs-valid";
		[String(typeof parse), String(valid("ok")), String(parse("x"))].join(",")
	`)

	origWD, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	p := New([]string{"noderati", appPath})
	p.SetSkipTypeCheck(true)
	source, _ := os.ReadFile(appPath)
	val, errs := p.RunCode(string(source), driver.RunOptions{ModuleName: appPath, Filename: appPath})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "function,true,x" {
		t.Errorf("named valid export = %q, want function,true,x", val.ToString())
	}
}

func TestNodeMissingResolver(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	_, errs := p.RunCode(`import "node:net"`, driver.RunOptions{})
	if len(errs) == 0 {
		t.Fatal("expected error for missing node:net")
	}
	if !strings.Contains(errs[0].Error(), "Cannot find module 'node:net'") {
		t.Errorf("error = %q", errs[0].Error())
	}
}

func TestMissingNodeBuiltinNamedError(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	_, errs := p.RunCode(`import "node:net"`, driver.RunOptions{})
	if len(errs) == 0 {
		t.Fatal("expected error for missing node:net")
	}
	msg := errs[0].Error()
	if !strings.Contains(msg, "Cannot find module 'node:net'") {
		t.Errorf("error = %q, want named cannot-find-module", msg)
	}
}

func TestBufferModule(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Buffer } from "node:buffer";
		typeof Buffer.from === "function" ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("buffer = %q", val.ToString())
	}
}

func TestSpawnSyncArgs(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { spawnSync } from "node:child_process";
		const r = spawnSync("echo", "hello", "world");
		r.stdout.trim()
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hello world" {
		t.Errorf("spawnSync stdout = %q, want %q", val.ToString(), "hello world")
	}
}
