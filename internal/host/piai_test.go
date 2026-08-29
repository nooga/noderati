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

func TestHostedGitInfoShim(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import hostedGitInfo from "hosted-git-info";
		hostedGitInfo.fromUrl("https://github.com/a/b") ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("hosted-git-info = %q, want ok", val.ToString())
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
