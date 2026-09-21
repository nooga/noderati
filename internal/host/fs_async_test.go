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

// TestFSAsyncStatAccessTrailingOptions guards a real noderati bug found
// this round (docs/real-node-plan.md): fs.stat/fs.lstat/fs.access all
// took a fixed-position `cb vm.Value` parameter, so a real caller passing
// real Node's own documented `(path, options, callback)` 3-argument form
// silently landed the options value in the callback slot instead -- never
// callable, so the callback simply never fired at all (no error, no
// success, nothing). This is exactly the shape real, unmodified
// enhanced-resolve's own `CachedInputFileSystem` uses when calling
// `fs.stat(path, undefined, callback)` internally, and it hung a real
// webpack compile forever with zero diagnostics.
func TestFSAsyncStatAccessTrailingOptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "present.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	script := `
		import fs from "fs";
		function callbackStyle(fn, ...args) {
			return new Promise((resolve, reject) => {
				fn(...args, (err, result) => err ? reject(err) : resolve(result));
			});
		}
		const stats = await callbackStyle(fs.stat, "` + path + `", {});
		const lstats = await callbackStyle(fs.lstat, "` + path + `", {});
		let accessOk = false;
		await callbackStyle(fs.access, "` + path + `", 0);
		accessOk = true;
		JSON.stringify({ isFile: stats.isFile(), lstatIsFile: lstats.isFile(), accessOk });
	`
	val, errs := p.RunCode(script, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isFile":true,"lstatIsFile":true,"accessOk":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestFSAsyncReadFileNonObjectOptionsDoesNotPanic guards a real noderati
// panic found this round (docs/real-node-plan.md): fsAsyncEncodingFromValue
// called v.AsPlainObject() unconditionally on any non-string trailing
// argument, which panics on any value whose type isn't exactly
// vm.TypeObject. Real, unmodified graceful-fs's own fs.readFile wrapper
// passes exactly this shape -- `fs$readFile(path, options, callback)`
// where `options` is `null` in the common case -- so this crashed a real
// webpack compile with an internal VM panic before the .json extension
// fix (a separate, earlier bug this same round) even let it get this far.
func TestFSAsyncReadFileNonObjectOptionsDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "present.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	script := `
		import fs from "fs";
		await new Promise((resolve, reject) => {
			fs.readFile("` + path + `", null, (err, data) => err ? reject(err) : resolve(data.toString()));
		});
	`
	val, errs := p.RunCode(script, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hello" {
		t.Errorf("got %q, want %q", val.ToString(), "hello")
	}
}

// TestFSAsyncReadlink guards a real noderati gap found this round
// (docs/real-node-plan.md): fs.readlink was entirely missing, and real,
// unmodified enhanced-resolve (webpack's own resolver, via
// SymlinkPlugin.js) calls it unconditionally on every path segment of
// every resolve attempt. Missing entirely wasn't a plain "undefined is
// not a function" -- enhanced-resolve's own CachedInputFileSystem wraps a
// missing async provider as a literal `null`, so the real, observed
// failure was a webpack compile hanging forever on a `null is not a
// function` exception thrown deep inside a scheduled callback.
func TestFSAsyncReadlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
	notALink := filepath.Join(dir, "notalink.txt")
	if err := os.WriteFile(notALink, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	script := `
		import fs from "fs";
		import util from "util";
		const readlink = util.promisify(fs.readlink);
		const resolved = await readlink("` + link + `");
		let notALinkFailed = false;
		try {
			await readlink("` + notALink + `");
		} catch {
			notALinkFailed = true;
		}
		JSON.stringify({ resolved, notALinkFailed });
	`
	val, errs := p.RunCode(script, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"resolved":"` + target + `","notALinkFailed":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}
