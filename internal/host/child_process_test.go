package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestSpawnEcho(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { spawn } from "node:child_process";
		const child = spawn("echo", ["hello"]);
		let out = "";
		child.stdout.on("data", (c) => { out += c; });
		await new Promise((resolve) => child.on("close", resolve));
		out.trim()
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hello" {
		t.Errorf("spawn stdout = %q, want hello", val.ToString())
	}
}

func TestSpawnTrue(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { spawn } from "node:child_process";
		const child = spawn("true");
		await new Promise((resolve) => child.on("close", (code) => resolve(code)));
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "0" {
		t.Errorf("spawn close code = %q, want 0", val.ToString())
	}
}

func TestSpawnSyncStillWorks(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { spawnSync } from "node:child_process";
		spawnSync("true").status
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "0" {
		t.Errorf("spawnSync status = %q, want 0", val.ToString())
	}
}
