package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestPiAiCompatShim(t *testing.T) {
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
		import { getProviders, getModels } from "@earendil-works/pi-ai/compat";
		import { getOAuthProviders } from "@earendil-works/pi-ai/oauth";
		const providers = getProviders();
		const models = getModels();
		const oauth = getOAuthProviders();
		[
			modelsAreEqual({ id: "a" }, { id: "a" }) ? "eq" : "no",
			Array.isArray(providers) ? "p" : "np",
			Array.isArray(models) ? "m" : "nm",
			Array.isArray(oauth) ? "o" : "no",
		].join(",")
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "eq,p,m,o" {
		t.Errorf("pi-ai shims = %q, want eq,p,m,o", val.ToString())
	}
}

func TestPiAiStreamSimpleFetchError(t *testing.T) {
	// Briefly broken by a paserati regression (dbf6d62d, #205's streaming
	// fetch() fix): doFetchRequestWithContext's own internal cleanup called
	// cancel() on any error return (including a plain connection-refused
	// dial failure, nothing to do with aborting), which made fetch()'s
	// ctx.Err()==context.Canceled check — intended to detect a real
	// AbortSignal-triggered abort — fire on every network failure,
	// discarding the real error in favor of a fabricated "AbortError: The
	// operation was aborted". Confirmed via a bare, AbortSignal-free
	// fetch() to an unreachable address, diffed against the immediately-
	// prior commit's build (which correctly reported the real dial error)
	// before filing https://github.com/nooga/paserati/issues/213 — left
	// deliberately failing rather than skipped while that was open, per
	// this project's rule that a known gap stays visible until the
	// upstream fix lands, not masked. Fixed upstream in fc187d9d; verified
	// this test passes again on its own (not just re-enabled) before
	// removing the skip note.

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { streamSimple, getModels } from "@earendil-works/pi-ai/compat";
		const model = Object.assign({}, getModels("openai")[0], { baseUrl: "http://127.0.0.1:1" });
		const stream = streamSimple(model, { messages: [{ role: "user", content: [{ type: "text", text: "hi" }] }] }, { apiKey: "x" });
		const msg = await stream.result();
		msg && msg.stopReason === "error" && String(msg.errorMessage).indexOf("connect") >= 0 ? "ok" : String(msg && msg.errorMessage)
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("streamSimple fetch error = %q, want ok", val.ToString())
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

func TestPiAgentCoreShim(t *testing.T) {
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

	cases := []struct {
		name string
		code string
	}{
		{"uuid direct", `import { uuidv7 } from "@earendil-works/pi-agent-core/dist/harness/session/uuid.js"; typeof uuidv7()`},
		{"uuid", `import { uuidv7 } from "@earendil-works/pi-agent-core"; typeof uuidv7`},
		{"agent", `import { Agent } from "@earendil-works/pi-agent-core"; typeof Agent`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := p.RunCode(tc.code, driver.RunOptions{})
			if len(errs) > 0 {
				t.Fatalf("RunCode: %v", errs[0])
			}
		})
	}

	val, errs := p.RunCode(`
		import { Agent, uuidv7 } from "@earendil-works/pi-agent-core";
		const a = new Agent({ initialState: { systemPrompt: "hi" } });
		const unsub = a.subscribe(function () {});
		unsub();
		a.state.systemPrompt === "hi" && typeof uuidv7() === "string" && typeof a.prompt === "function" ? "ok" : "no"
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
