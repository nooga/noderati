package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestPiAiReal exercises the real, unmodified @earendil-works/pi-ai
// package (its fake — a made-up /compat and /oauth subpath surface that
// didn't exist in the real package at all — was deleted 2026-09-05, round
// 47/48 of docs/real-node-plan.md, once a real Fireworks end-to-end test
// via `--provider fireworks -p "..."` with the fake off returned correct
// completions 3/3 runs). This only smoke-tests network-free real exports
// (models.js's modelsAreEqual); the actual live-completion path is
// verified separately against a real backend, not by a unit test here.
func TestPiAiReal(t *testing.T) {
	pkgRoot := findPiAgentCorePackage(t)
	if pkgRoot == "" {
		t.Skip("pi-coding-agent not installed")
	}
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(pkgRoot); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { modelsAreEqual } from "@earendil-works/pi-ai";
		modelsAreEqual({ id: "a", provider: "p" }, { id: "a", provider: "p" }) ? "eq" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "eq" {
		t.Errorf("pi-ai modelsAreEqual = %q, want eq", val.ToString())
	}
}

// TestHostedGitInfoReal exercises the real, unmodified hosted-git-info
// package (its fake was deleted 2026-09-01 once paserati#159/#160/#163/
// #168 — destructuring, re-exporting an import, and Object.assign
// enumerability — were all fixed and this was confirmed working end to
// end, matching pi-coding-agent's own real usage in dist/utils/git.js:
// fromUrl() plus reading plain properties off the result, never any of
// the #fill-based template methods).
func TestHostedGitInfoReal(t *testing.T) {
	pkgRoot := findPiAgentCorePackage(t)
	if pkgRoot == "" {
		t.Skip("pi-coding-agent not installed")
	}
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(pkgRoot); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import hostedGitInfo from "hosted-git-info";
		const info = hostedGitInfo.fromUrl("git@github.com:foo/bar.git");
		[info.domain, info.user, info.project].join(",")
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "github.com,foo,bar" {
		t.Errorf("hosted-git-info fromUrl = %q, want github.com,foo,bar", val.ToString())
	}
}

// TestPiAgentCoreReal exercises the real, unmodified
// @earendil-works/pi-agent-core package (its fake was deleted 2026-09-05,
// round 47/48 — see TestPiAiReal above for the same verification this
// piggybacks on). Only checks that the real Agent class and uuidv7 import
// and construct correctly; the live agent-loop/streaming path is verified
// separately against a real backend, not by a unit test here.
func TestPiAgentCoreReal(t *testing.T) {
	pkgRoot := findPiAgentCorePackage(t)
	if pkgRoot == "" {
		t.Skip("pi-agent-core not installed")
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(pkgRoot); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)

	val, errs := p.RunCode(`
		import { Agent, uuidv7 } from "@earendil-works/pi-agent-core";
		typeof Agent === "function" && typeof uuidv7() === "string" ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("pi-agent-core = %q, want ok", val.ToString())
	}
}

func findPiAgentCorePackage(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core",
		"/usr/local/lib/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core",
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, "dist", "index.js")); err == nil {
			return filepath.Dir(filepath.Dir(filepath.Dir(candidate)))
		}
	}
	return ""
}
