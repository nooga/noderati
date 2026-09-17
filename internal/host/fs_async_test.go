package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestFSAsyncWriteFileReadFile guards the exact real-world shape that
// surfaced the gap (docs/real-node-plan.md, Round 122): real, unmodified
// eslint@9.36.0's own lib/eslint/legacy-eslint.js does
// `const writeFile = util.promisify(fs.writeFile);` at module top level -
// a real, unconditional call, not behind any guard - so a missing
// callback-style fs.writeFile threw `util.promisify`'s own "original"
// argument TypeError before any of that module's real logic ran, the
// exact same "one real callsite reaches an entirely-missing style of an
// otherwise-real module" shape fs_async.go's own mkdir/stat/etc. were
// added for. Exercises both the no-options and with-options-and-encoding
// call shapes, and the real string-or-Buffer default readFile carries
// when no encoding is given.
func TestFSAsyncWriteFileReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	script := `
		import fs from "fs";
		import util from "util";
		const writeFile = util.promisify(fs.writeFile);
		const readFile = util.promisify(fs.readFile);
		await writeFile("` + path + `", "hello async fs");
		const text = await readFile("` + path + `", "utf8");
		const buf = await readFile("` + path + `");
		JSON.stringify({ text, isBuffer: Buffer.isBuffer(buf), bufText: buf.toString() });
	`
	val, errs := p.RunCode(script, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"text":"hello async fs","isBuffer":true,"bufText":"hello async fs"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "hello async fs" {
		t.Errorf("file on disk = %q, %v; want %q, nil", data, err, "hello async fs")
	}
}

// TestFSAsyncWriteFileError guards that a real failure (writing into a
// directory that doesn't exist) rejects the promisified call with a real
// error, rather than the callback never firing at all - the same
// correctness bar fs_async.go's own pre-existing mkdir/stat tests hold
// their error paths to.
func TestFSAsyncWriteFileError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist", "out.txt")

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	script := `
		import fs from "fs";
		import util from "util";
		const writeFile = util.promisify(fs.writeFile);
		let threw = false;
		try {
			await writeFile("` + path + `", "data");
		} catch (e) {
			threw = true;
		}
		threw;
	`
	val, errs := p.RunCode(script, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "true" {
		t.Errorf("expected writeFile to a nonexistent directory to reject, got threw=%s", val.ToString())
	}
}

// TestFSAsyncLstatAndAccess guards the two other real callback-style
// gaps found in the same round chasing eslint's real transitive
// dependencies (docs/real-node-plan.md, Round 122): real `locate-path`
// does `promisify(fs.lstat)` and real `path-exists` does
// `promisify(fs.access)`, both at module top level, right next to the
// already-implemented `fs.stat` this file's own comment describes -
// lstat/access's own callback-style variants were simply never added
// alongside it.
func TestFSAsyncLstatAndAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "present.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.txt")

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	script := `
		import fs from "fs";
		import util from "util";
		const lstat = util.promisify(fs.lstat);
		const access = util.promisify(fs.access);

		const stats = await lstat("` + path + `");

		let accessOk = false;
		try {
			await access("` + path + `");
			accessOk = true;
		} catch {}

		let accessMissingFailed = false;
		try {
			await access("` + missing + `");
		} catch {
			accessMissingFailed = true;
		}

		JSON.stringify({ isFile: stats.isFile(), accessOk, accessMissingFailed });
	`
	val, errs := p.RunCode(script, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isFile":true,"accessOk":true,"accessMissingFailed":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}
