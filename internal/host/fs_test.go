package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func newWithFS() *driver.Paserati {
	p := New([]string{"noderati"})
	declareFS(p)
	p.SetSkipTypeCheck(true)
	return p
}

func TestFSWriteReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")

	p := newWithFS()
	// Real Node's readFileSync only returns a decoded string when an
	// explicit encoding is given - see TestFSReadFileSyncDefaultsToBuffer
	// below for the no-encoding-argument default.
	js := `
		import { writeFileSync, readFileSync } from "fs";
		writeFileSync("` + file + `", "hello world");
		readFileSync("` + file + `", "utf8")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hello world" {
		t.Errorf("readFileSync = %q", val.ToString())
	}
}

// TestFSReadFileSyncDefaultsToBuffer confirms the fix for the readFileSync
// bug found while probing real WebAssembly loading (paserati#375): real
// Node's fs.readFileSync(path), with no encoding argument, returns a real
// Buffer of the raw bytes - not a string. The previous implementation ran
// every read through Go's string(b) unconditionally, which silently
// corrupts any binary file (confirmed directly against a real 52042-byte
// .wasm file - see docs/real-node-plan.md). This round-trips every byte
// value 0-255, not just ASCII, so a signedness/truncation bug in the
// string round-trip can't hide behind a small, ASCII-only sample the way
// "hello world" would.
func TestFSReadFileSyncDefaultsToBuffer(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "binary.bin")
	want := make([]byte, 256)
	for i := range want {
		want[i] = byte(i)
	}
	if err := os.WriteFile(file, want, 0644); err != nil {
		t.Fatal(err)
	}

	p := newWithFS()
	js := `
		import { readFileSync } from "fs";
		const b = readFileSync("` + file + `");
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
		t.Errorf("readFileSync (no encoding) = %s, want %s", val.ToString(), want2)
	}
}

// TestFSReadFileSyncUtf8ReturnsString confirms readFileSync(path, "utf8")
// still returns a decoded JS string, matching the string-shorthand form of
// real Node's encoding argument (the {encoding: "utf8"} object form is
// exercised by TestFSReadFileSyncEncodingObject below).
func TestFSReadFileSyncUtf8ReturnsString(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "text.txt")
	if err := os.WriteFile(file, []byte("héllo"), 0644); err != nil {
		t.Fatal(err)
	}

	p := newWithFS()
	js := `
		import { readFileSync } from "fs";
		const s = readFileSync("` + file + `", "utf8");
		JSON.stringify({ isString: typeof s === "string", value: s })
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isString":true,"value":"héllo"}`
	if val.ToString() != want {
		t.Errorf("readFileSync utf8 = %s, want %s", val.ToString(), want)
	}
}

// TestFSReadFileSyncEncodingObject covers the {encoding: "..."} options
// object form of readFileSync's second argument (real Node accepts both
// that and a bare string shorthand).
func TestFSReadFileSyncEncodingObject(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "text2.txt")
	if err := os.WriteFile(file, []byte("hi there"), 0644); err != nil {
		t.Fatal(err)
	}

	p := newWithFS()
	js := `
		import { readFileSync } from "fs";
		const s = readFileSync("` + file + `", { encoding: "utf8" });
		JSON.stringify({ isString: typeof s === "string", value: s })
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isString":true,"value":"hi there"}`
	if val.ToString() != want {
		t.Errorf("readFileSync {encoding} = %s, want %s", val.ToString(), want)
	}
}

// TestFSWriteAppendFileSyncAcceptBuffer covers the mirror-image bug:
// writeFileSync/appendFileSync must accept a real Buffer/Uint8Array `data`
// argument (not just a JS string) and write its raw bytes, matching real
// Node. Round-trips the full 0-255 byte range through both calls, then
// reads the result back with readFileSync's own (now-fixed) Buffer
// default so the whole path is exercised with real bytes end to end.
func TestFSWriteAppendFileSyncAcceptBuffer(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "buf.bin")

	p := newWithFS()
	js := `
		import { writeFileSync, appendFileSync, readFileSync } from "fs";
		const bytes = Array.from({ length: 256 }, (_, i) => i);
		const first = Buffer.from(bytes.slice(0, 128));
		const second = Buffer.from(bytes.slice(128));
		writeFileSync("` + file + `", first);
		appendFileSync("` + file + `", second);
		const b = readFileSync("` + file + `");
		const matches = b.length === 256 && Array.from(b).every((v, i) => v === i);
		JSON.stringify({ length: b.length, matches })
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"length":256,"matches":true}`
	if val.ToString() != want {
		t.Errorf("writeFileSync/appendFileSync with Buffer = %s, want %s", val.ToString(), want)
	}
}

func TestFSExistsSync(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	p := newWithFS()
	js := `
		import { existsSync } from "fs";
		existsSync("` + file + `") && !existsSync("` + filepath.Join(dir, "missing.txt") + `")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsBoolean() || !val.AsBoolean() {
		t.Errorf("existsSync = %v", val)
	}
}

func TestFSMkdirReaddir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")

	p := newWithFS()
	js := `
		import { mkdirSync, readdirSync } from "fs";
		mkdirSync("` + sub + `");
		readdirSync("` + dir + `").join(",")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "sub" {
		t.Errorf("readdirSync = %q", val.ToString())
	}
}

func TestFSNodeAlias(t *testing.T) {
	p := newWithFS()
	js := `
		import { writeFileSync } from "fs";
		import fs from "node:fs";
		writeFileSync === fs.writeFileSync
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsBoolean() || !val.AsBoolean() {
		t.Errorf("node:fs alias mismatch: %v", val)
	}
}

func TestFSReadMissingThrows(t *testing.T) {
	p := newWithFS()
	js := `
		import { readFileSync } from "fs";
		try {
			readFileSync("/nonexistent-noderati-fs-test");
			"no throw";
		} catch (e) {
			"threw";
		}
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "threw" {
		t.Errorf("expected throw, got %q", val.ToString())
	}
}

func TestFSStatSync(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "stat.txt")
	if err := os.WriteFile(file, []byte("abcd"), 0644); err != nil {
		t.Fatal(err)
	}

	p := newWithFS()
	js := `
		import { statSync } from "fs";
		const s = statSync("` + file + `");
		s.size === 4 && s.isFile() && !s.isDirectory()
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsBoolean() || !val.AsBoolean() {
		t.Errorf("statSync = %v", val)
	}
}

func TestFSAppendCopyRenameRm(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	copyDst := filepath.Join(dir, "b.txt")
	renamed := filepath.Join(dir, "c.txt")

	p := newWithFS()
	js := `
		import { writeFileSync, appendFileSync, copyFileSync, renameSync, rmSync, readFileSync } from "fs";
		writeFileSync("` + file + `", "ab");
		appendFileSync("` + file + `", "cd");
		copyFileSync("` + file + `", "` + copyDst + `");
		renameSync("` + copyDst + `", "` + renamed + `");
		const content = readFileSync("` + file + `") + readFileSync("` + renamed + `");
		rmSync("` + renamed + `");
		content
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "abcdabcd" {
		t.Errorf("append/copy/rename = %q", val.ToString())
	}
	if _, err := os.Stat(renamed); err == nil {
		t.Error("rmSync did not remove file")
	}
}

func TestFSUnlinkRmdir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "unlink.txt")
	sub := filepath.Join(dir, "empty")

	p := newWithFS()
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}

	js := `
		import { unlinkSync, rmdirSync, existsSync } from "fs";
		unlinkSync("` + file + `");
		rmdirSync("` + sub + `");
		!existsSync("` + file + `") && !existsSync("` + sub + `")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsBoolean() || !val.AsBoolean() {
		t.Errorf("unlink/rmdir = %v", val)
	}
}

func TestFSAccessSyncAndConstants(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	p := newWithFS()
	js := `
		import { accessSync, constants, existsSync } from "fs";
		accessSync("` + file + `", constants.F_OK);
		existsSync("` + file + `")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsBoolean() || !val.AsBoolean() {
		t.Errorf("accessSync = %v", val)
	}
}
