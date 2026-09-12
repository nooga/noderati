package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestFSPromisesReadWrite(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "async.txt")

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	// Real Node's fs.promises.readFile only returns a decoded string
	// when an explicit encoding is given - see
	// TestFSPromisesReadFileDefaultsToBuffer below for the
	// no-encoding-argument default.
	js := `
		import { writeFile, readFile } from "node:fs/promises";
		await writeFile("` + file + `", "async-data");
		await readFile("` + file + `", "utf8")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "async-data" {
		t.Errorf("readFile = %q", val.ToString())
	}
}

// TestFSPromisesReadFileDefaultsToBuffer mirrors
// TestFSReadFileSyncDefaultsToBuffer (fs_test.go) for the async API: real
// Node's fs.promises.readFile(path), with no encoding argument, resolves
// to a real Buffer of the raw bytes, not a string. Round-trips all 256
// byte values, not just ASCII, so a signedness/truncation bug in the byte
// path can't hide behind a small sample.
func TestFSPromisesReadFileDefaultsToBuffer(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "async-binary.bin")
	want := make([]byte, 256)
	for i := range want {
		want[i] = byte(i)
	}
	if err := os.WriteFile(file, want, 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import { readFile } from "node:fs/promises";
		const b = await readFile("` + file + `");
		const matches = b.length === 256 && Array.from(b).every((v, i) => v === i);
		JSON.stringify({
			isBuffer: Buffer.isBuffer(b),
			isU8: b instanceof Uint8Array,
			length: b.length,
			matches,
		})
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want2 := `{"isBuffer":true,"isU8":true,"length":256,"matches":true}`
	if val.ToString() != want2 {
		t.Errorf("readFile (no encoding) = %s, want %s", val.ToString(), want2)
	}
}

// TestFSPromisesWriteFileAcceptsBuffer covers the mirror-image bug:
// fs.promises.writeFile must accept a real Buffer/Uint8Array `data`
// argument (not just a JS string) and write its raw bytes, matching real
// Node - mirrors TestFSWriteAppendFileSyncAcceptBuffer (fs_test.go) for
// the async API.
func TestFSPromisesWriteFileAcceptsBuffer(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "async-buf.bin")

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import { writeFile, readFile } from "node:fs/promises";
		const bytes = Array.from({ length: 256 }, (_, i) => i);
		await writeFile("` + file + `", Buffer.from(bytes));
		const b = await readFile("` + file + `");
		const matches = b.length === 256 && Array.from(b).every((v, i) => v === i);
		JSON.stringify({ length: b.length, matches })
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"length":256,"matches":true}`
	if val.ToString() != want {
		t.Errorf("writeFile with Buffer = %s, want %s", val.ToString(), want)
	}
}

func TestFSPromisesMkdirReaddirStat(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "promised")

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import { mkdir, readdir, stat } from "node:fs/promises";
		await mkdir("` + sub + `");
		const names = await readdir("` + dir + `");
		const s = await stat("` + sub + `");
		names.join(",") + ":" + (s.isDirectory() ? "dir" : "file")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "promised:dir" {
		t.Errorf("mkdir/readdir/stat = %q", val.ToString())
	}
}

func TestFSPromisesAccessUnlinkRm(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "rm.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import { access, unlink } from "node:fs/promises";
		await access("` + file + `");
		await unlink("` + file + `");
		let missing = false;
		await access("` + file + `").catch(() => { missing = true; });
		missing ? "ok" : "no"
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("access/unlink/rm = %q", val.ToString())
	}
}

// TestFSPromisesAliasOnFS guards a real Node alias found chasing the
// Bedrock investigation (docs/real-node-plan.md, round 98):
// `require('fs').promises === require('fs/promises')` is `true` in real
// Node, and real code still reaches for it this way -
// @aws-sdk/token-providers' own dist-cjs/index.js does
// `const { writeFile } = node_fs.promises` (`node_fs` being
// `require('node:fs')`) at module top level. Checks both the ESM
// (`import fs from "node:fs"`) and CJS (`require("fs")`) shapes, since
// they're built from independently-snapshotted "default" objects here
// (see installFSPromisesAlias's own comment for why) - fixing one
// without the other would leave a real, silent gap.
func TestFSPromisesAliasOnFS(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "via-fs-promises.txt")

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import fs from "node:fs";
		await fs.promises.writeFile("` + file + `", "via-fs.promises");
		await fs.promises.readFile("` + file + `", "utf8")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode (ESM): %v", errs[0])
	}
	if val.ToString() != "via-fs.promises" {
		t.Errorf("fs.promises.readFile (ESM) = %q", val.ToString())
	}

	cjsVal, errs := RunCJS(p, `
		const fs = require("fs");
		module.exports = JSON.stringify({
			sameAsRequireFsPromises: fs.promises === require("fs/promises"),
			writeFileIsFunction: typeof fs.promises.writeFile === "function",
		});
	`, filepath.Join(dir, "app.js"))
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	want := `{"sameAsRequireFsPromises":true,"writeFileIsFunction":true}`
	if cjsVal.ToString() != want {
		t.Errorf("CJS fs.promises = %s, want %s", cjsVal.ToString(), want)
	}
}
