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
	js := `
		import { writeFile, readFile } from "node:fs/promises";
		await writeFile("` + file + `", "async-data");
		await readFile("` + file + `")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "async-data" {
		t.Errorf("readFile = %q", val.ToString())
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
