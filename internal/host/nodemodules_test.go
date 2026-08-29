package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestNodeModulesResolverSearchesEntryScriptDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "node_modules", "demo-pkg", "package.json"), `{
		"name": "demo-pkg",
		"type": "module",
		"exports": "./index.js"
	}`)
	writeFile(t, filepath.Join(root, "node_modules", "demo-pkg", "index.js"), `export function greet(name) { return "hello " + name; }`)

	appPath := filepath.Join(root, "app.ts")
	writeFile(t, appPath, `import { greet } from "demo-pkg"; greet("world")`)

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
	if val.ToString() != "hello world" {
		t.Errorf("greet result = %q, want %q", val.ToString(), "hello world")
	}
}

func TestNodeModulesResolverCanResolve(t *testing.T) {
	r := NewNodeModulesResolver()

	cases := []struct {
		spec string
		want bool
	}{
		{"demo-pkg", true},
		{"@scope/pkg", true},
		{"./relative", false},
		{"../parent", false},
		{"/absolute", false},
		{"node:fs", false},
		{"https://example.com/pkg", false},
	}

	for _, tc := range cases {
		if got := r.CanResolve(tc.spec); got != tc.want {
			t.Errorf("CanResolve(%q) = %v, want %v", tc.spec, got, tc.want)
		}
	}
}

func TestNodeModulesResolverResolveDemoPkg(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "node_modules", "demo-pkg", "package.json"), `{
		"name": "demo-pkg",
		"type": "module",
		"exports": "./index.js"
	}`)
	writeFile(t, filepath.Join(root, "node_modules", "demo-pkg", "index.js"), `export function greet(name) { return "hello " + name; }`)

	appPath := filepath.Join(root, "app.ts")
	writeFile(t, appPath, `import { greet } from "demo-pkg"; greet("world")`)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	p := New([]string{"noderati"})
	p.AddResolver(NewNodeModulesResolver())
	p.SetSkipTypeCheck(true)

	source, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	val, errs := p.RunCode(string(source), driver.RunOptions{ModuleName: appPath, Filename: appPath})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hello world" {
		t.Errorf("greet result = %q, want %q", val.ToString(), "hello world")
	}
}

func TestNodeModulesResolverResolveScopedPkg(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "node_modules", "@scope", "pkg", "package.json"), `{
		"name": "@scope/pkg",
		"type": "module",
		"exports": { ".": "./index.js" }
	}`)
	writeFile(t, filepath.Join(root, "node_modules", "@scope", "pkg", "index.js"), `export const value = 42;`)

	appPath := filepath.Join(root, "nested", "app.ts")
	writeFile(t, appPath, `import { value } from "@scope/pkg"; value`)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	r := NewNodeModulesResolver()
	resolved, err := r.Resolve("@scope/pkg", appPath)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer resolved.Source.Close()

	want := filepath.Join(root, "node_modules", "@scope", "pkg", "index.js")
	if resolved.ResolvedPath != want {
		t.Errorf("ResolvedPath = %q, want %q", resolved.ResolvedPath, want)
	}
}

func TestNodeModulesResolverResolveSubpath(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "node_modules", "demo-pkg", "package.json"), `{
		"name": "demo-pkg",
		"type": "module",
		"main": "index.js"
	}`)
	writeFile(t, filepath.Join(root, "node_modules", "demo-pkg", "sub", "index.js"), `export const sub = "ok";`)

	appPath := filepath.Join(root, "app.ts")
	writeFile(t, appPath, `import { sub } from "demo-pkg/sub"; sub`)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	p := New([]string{"noderati"})
	p.AddResolver(NewNodeModulesResolver())
	p.SetSkipTypeCheck(true)

	source, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	val, errs := p.RunCode(string(source), driver.RunOptions{ModuleName: appPath, Filename: appPath})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("subpath result = %q, want %q", val.ToString(), "ok")
	}
}

func TestNodeModulesCJSDefaultImport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "node_modules", "cjs-pkg", "package.json"), `{
		"name": "cjs-pkg",
		"main": "index.js"
	}`)
	writeFile(t, filepath.Join(root, "node_modules", "cjs-pkg", "index.js"), `
		module.exports = function greet(name) { return "hello " + name; };
	`)
	appPath := filepath.Join(root, "app.js")
	writeFile(t, appPath, `import greet from "cjs-pkg"; greet("world")`)

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
	if val.ToString() != "hello world" {
		t.Errorf("cjs default import = %q, want %q", val.ToString(), "hello world")
	}
}
