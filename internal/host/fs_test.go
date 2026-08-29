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
	js := `
		import { writeFileSync, readFileSync } from "fs";
		writeFileSync("` + file + `", "hello world");
		readFileSync("` + file + `")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hello world" {
		t.Errorf("readFileSync = %q", val.ToString())
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
