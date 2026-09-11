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

// TestResolveMainEntryIgnoresModuleFieldWithoutExports guards a real
// deviation from Node found chasing the Bedrock investigation
// (docs/real-node-plan.md, round 96): real, unmodified
// @aws-sdk/client-bedrock-runtime's own package.json has both "main"
// and "module" fields and no "exports" map at all - and real Node's own
// resolution (confirmed directly against a synthetic equivalent
// package, not assumed) uses "main" for *both* require() and import in
// that shape, completely ignoring "module" - a bundler-only convention,
// not part of Node's own algorithm. This resolver used to prefer
// "module" for ESM imports specifically (present since this resolver's
// very first commit, apparently never verified against real Node), so
// `import` picked a different file (dist-es) than `require()` did
// (dist-cjs) for the exact same package - a different file with a
// different import graph and, in the real package that surfaced this,
// a different crash than real Node's own resolution would ever hit.
func TestResolveMainEntryIgnoresModuleFieldWithoutExports(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{
		"name": "pkg",
		"main": "./main.js",
		"module": "./module.js"
	}`)
	writeFile(t, filepath.Join(root, "main.js"), `module.exports = "from-main";`)
	writeFile(t, filepath.Join(root, "module.js"), `module.exports = "from-module";`)

	for _, cond := range []exportsCondition{exportsConditionRequire, exportsConditionImport} {
		entry, err := resolveMainEntry(root, cond)
		if err != nil {
			t.Fatalf("resolveMainEntry(%v): %v", cond, err)
		}
		want, err := canonicalPath(filepath.Join(root, "main.js"))
		if err != nil {
			t.Fatalf("canonicalPath: %v", err)
		}
		gotAbs, err := canonicalPath(entry)
		if err != nil {
			t.Fatalf("canonicalPath(entry): %v", err)
		}
		if gotAbs != want {
			t.Errorf("resolveMainEntry(cond=%v) = %q, want %q (the \"main\" field, matching real Node - not \"module\")", cond, entry, want)
		}
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

	// canonicalPath (nodemodules.go) now resolves symlinks in the final
	// path - t.TempDir() itself sits under a symlink on macOS (/var ->
	// /private/var), so "want" needs the same canonicalization applied
	// here, or this compares a resolved, symlink-free path against a
	// raw one that still has the symlink in it. Real Node does the same
	// realpath-ing on every resolved module path (see canonicalPath's
	// own doc comment for why), so this is the correct comparison, not
	// a workaround for the test.
	want, err := canonicalPath(filepath.Join(root, "node_modules", "@scope", "pkg", "index.js"))
	if err != nil {
		t.Fatalf("canonicalPath(want): %v", err)
	}
	if resolved.ResolvedPath != want {
		t.Errorf("ResolvedPath = %q, want %q", resolved.ResolvedPath, want)
	}
}

// TestShouldWrapCJSIgnoresDynamicImportCall guards the exact bug found
// chasing the Bedrock "@smithy/core/protocols" blocker: a real CJS file
// (@smithy/core's own dist-cjs submodules/protocols/index.js) doing
// `const { X } = await import('@smithy/core/event-streams')` among
// otherwise unambiguous require()/module.exports CJS. A dynamic
// `import(...)` call is legal in CommonJS too - it must not, by itself,
// flip shouldWrapCJS's ESM-or-not verdict the way a static `import ...
// from`/bare `export ...` declaration does. Genuine ESM (with a static
// import) must still be detected as ESM - not wrapped - so both
// directions are asserted here, not just the false-positive fix.
func TestShouldWrapCJSIgnoresDynamicImportCall(t *testing.T) {
	cjsWithDynamicImport := `'use strict';
var fs = require('fs');
async function loadExtra() {
  const { X } = await import('./extra.js');
  return X;
}
module.exports = { loadExtra };
`
	if !shouldWrapCJS("/virtual/protocols.js", cjsWithDynamicImport) {
		t.Error("a CJS file using dynamic import() should still be wrapped as CJS, not treated as ESM")
	}

	genuineESM := `import fs from "fs";
export const value = 42;
`
	if shouldWrapCJS("/virtual/esm.js", genuineESM) {
		t.Error("a genuine ESM file (static import/export) should not be wrapped as CJS")
	}
}

// TestNodeModulesResolverCircularRequireThroughSelfSymlink guards the
// second bug found in the same investigation: a real npm/homebrew global
// install (@earendil-works/pi-coding-agent's own node_modules) had a
// stray self-referential `node_modules/node_modules -> node_modules`
// symlink sitting inside it. findPackageDir's ancestor walk matches an
// ancestor as soon as `<ancestor>/node_modules/<pkg>` exists on disk -
// and that self-symlink makes the check succeed one level too early,
// prepending a spurious extra "/node_modules" segment onto the resolved
// path. A circular CJS require (a real, unavoidable shape - @smithy/
// core's own dist-cjs protocols/serde modules require each other) that
// crosses this shortcut resolved to a *different* absolute path string
// each time, defeating execFile's cache (keyed by that string) and
// breaking the shared, in-progress module.exports circular CJS require
// depends on. canonicalPath's EvalSymlinks collapses the shortcut back
// to one real path regardless of which route reached it - this
// reproduces the self-symlink directly (no external package needed) and
// asserts a value set by one side of the cycle is visible, synchronously
// and via the *same* cached module object, to the other side.
func TestNodeModulesResolverCircularRequireThroughSelfSymlink(t *testing.T) {
	root := t.TempDir()

	// Bare package specifiers, not relative ones: findPackageDir's
	// ancestor walk (the thing the self-symlink below trips up) only
	// runs for a bare "pkg-a"/"pkg-b" require, not a "./..." one. Each
	// package's own require() runs from *inside* node_modules/pkg-*, so
	// its very first ancestor-walk step lands on "root/node_modules" -
	// exactly the directory the self-symlink sits in - before it would
	// ever reach "root" itself, one level further up, where the real,
	// correct match also exists.
	writeFile(t, filepath.Join(root, "node_modules", "pkg-a", "index.js"), `
		exports.mark = "unset";
		const b = require("pkg-b");
		b.setFromA();
	`)
	writeFile(t, filepath.Join(root, "node_modules", "pkg-b", "index.js"), `
		const a = require("pkg-a");
		exports.setFromA = function () {
			a.mark = "set-by-b";
		};
	`)

	// The stray self-referential symlink itself: node_modules/node_modules
	// pointing right back at node_modules, exactly like the real install
	// that surfaced this.
	if err := os.Symlink(
		filepath.Join(root, "node_modules"),
		filepath.Join(root, "node_modules", "node_modules"),
	); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	p := driver.NewPaserati()
	val, errs := RunCJS(p, `module.exports = require("pkg-a").mark;`, filepath.Join(root, "entry.js"))
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	if got := val.ToString(); got != "set-by-b" {
		t.Errorf("a.mark = %q, want %q (b's require(\"pkg-a\") should have hit the same cached module a's own require(\"pkg-b\") populated, not a fresh, differently-pathed reload)", got, "set-by-b")
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
