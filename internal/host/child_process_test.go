package host

import (
	"strings"
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

func TestSpawnCwdOption(t *testing.T) {
	// pi-agent-core's real shell-exec harness (nodejs.js's
	// AgentEnvironment.exec) always passes cwd as a spawn() option, not via
	// a separate API - if this option is dropped, every real tool call
	// silently runs in noderati's own working directory instead of the
	// one the agent actually asked for.
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { spawn } from "node:child_process";
		const child = spawn("pwd", [], { cwd: "/tmp" });
		let out = "";
		child.stdout.on("data", (c) => { out += c; });
		await new Promise((resolve) => child.on("close", resolve));
		out.trim()
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	// macOS's /tmp is itself a symlink to /private/tmp - pwd -P (or just
	// comparing against both) avoids a false failure from that, not from
	// anything this test is actually checking.
	got := val.ToString()
	if got != "/tmp" && got != "/private/tmp" {
		t.Errorf("spawn with cwd option: pwd = %q, want /tmp (or its real path)", got)
	}
}

func TestSpawnEnvOption(t *testing.T) {
	// Node's spawn() semantics: passing an env option *replaces* the
	// child's environment entirely, it doesn't merge with the parent's.
	// Uses a marker var set on the test process's own environment rather
	// than $PATH, since POSIX sh synthesizes its own default PATH when the
	// environment has none at all - that's shell behavior, not something
	// this test is trying to check.
	t.Setenv("NODERATI_TEST_MARKER_VAR", "should-not-be-inherited")
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { spawn } from "node:child_process";
		const child = spawn("sh", ["-c", "echo $ONLY_VAR-$NODERATI_TEST_MARKER_VAR"], {
			env: { ONLY_VAR: "hi" },
		});
		let out = "";
		child.stdout.on("data", (c) => { out += c; });
		await new Promise((resolve) => child.on("close", resolve));
		out.trim()
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hi-" {
		t.Errorf("spawn with env option = %q, want %q (ONLY_VAR set, marker var absent - replaced, not merged)", val.ToString(), "hi-")
	}
}

func TestSpawnDetachedGetsOwnProcessGroup(t *testing.T) {
	// pi-agent-core's own killProcessTree (used for both its per-call
	// timeout and Escape-key cancellation) relies on process.kill(-pid,
	// sig) reaching the whole subprocess tree - which only works if
	// detached:true actually put the child in its own new process group
	// (POSIX setpgid(0,0): new group id == the child's own pid).
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { spawn } from "node:child_process";
		const child = spawn("sh", ["-c", "ps -o pid=,pgid= -p $$"], { detached: true });
		let out = "";
		child.stdout.on("data", (c) => { out += c; });
		await new Promise((resolve) => child.on("close", resolve));
		out.trim()
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	fields := strings.Fields(val.ToString())
	if len(fields) != 2 {
		t.Fatalf("unexpected ps output %q", val.ToString())
	}
	if fields[0] != fields[1] {
		t.Errorf("detached child's pid=%s pgid=%s, want them equal (own process group)", fields[0], fields[1])
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
