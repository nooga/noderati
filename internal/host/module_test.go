package host

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestCreateRequirePathJoin(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mod.mjs")
	if err := os.WriteFile(file, []byte(`export const tag = "ok";`), 0644); err != nil {
		t.Fatal(err)
	}
	fileURL := "file://" + filepath.ToSlash(file)

	p := New([]string{"noderati", file})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { createRequire } from "node:module";
		const require = createRequire(import.meta.url);
		require("path").join("a", "b")
	`, driver.RunOptions{ModuleName: file, Filename: file})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := filepath.Join("a", "b")
	if val.ToString() != want {
		t.Errorf("path.join = %q, want %q", val.ToString(), want)
	}

	val, errs = p.RunCode(`
		import { createRequire } from "node:module";
		const require = createRequire(`+strconv.Quote(fileURL)+`);
		require("path").join("a", "b")
	`, driver.RunOptions{ModuleName: file, Filename: file})
	if len(errs) > 0 {
		t.Fatalf("RunCode file URL: %v", errs[0])
	}
	if val.ToString() != want {
		t.Errorf("path.join via file URL = %q, want %q", val.ToString(), want)
	}
}

func TestCreateRequireFS(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mod.mjs")
	if err := os.WriteFile(file, []byte(`export const tag = "ok";`), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati", file})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { createRequire } from "node:module";
		const require = createRequire(import.meta.url);
		typeof require("fs").readFileSync
	`, driver.RunOptions{ModuleName: file, Filename: file})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "function" {
		t.Errorf("require(fs).readFileSync type = %q, want function", val.ToString())
	}
}

func TestRequireNativeNullDefault(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.js")
	if err := os.WriteFile(file, []byte(`
		const fs = require("fs");
		module.exports = typeof fs.readFileSync;
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
	if val.ToString() != "function" {
		t.Errorf("require(fs).readFileSync type = %q, want function", val.ToString())
	}
}

func TestWorkerThreadsImport(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { Worker, parentPort, isMainThread } from "node:worker_threads";
		[typeof Worker, parentPort === null, isMainThread === true].join(",")
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	got := val.ToString()
	if got != "function,true,true" {
		t.Errorf("worker_threads import = %q, want function,true,true", got)
	}
}