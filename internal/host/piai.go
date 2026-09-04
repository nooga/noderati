package host

import (
	"os"
	"path/filepath"
)

// findPiCodingAgentNodeModulesRoots returns install roots whose nested
// node_modules contains @earendil-works/pi-ai (global pi / pi-coding-agent).
//
// This used to also be the file holding @earendil-works/pi-ai's fake
// (registerJSShim("@earendil-works/pi-ai", ...) and its /compat and /oauth
// subpaths, plus a declarePiAi() that registered them). Deleted 2026-09-05
// (round 47/48 of docs/real-node-plan.md): a real Fireworks end-to-end test
// via `--provider fireworks -p "..."` with the fake off returned correct
// completions 3/3 runs, across both a single-word and a multi-line reply -
// the first time this pair worked against a live backend. node_modules
// resolution now always loads the real pi-ai package (this function is
// still needed for that - it's the resolver helper, not part of the fake).
// Not verified by this deletion: the Bedrock provider path specifically,
// which pulls in http-proxy-agent/https-proxy-agent (and through them
// `debug`) - blocked today on noderati having no `net` module at all (a
// separate, pre-existing gap, tracked for Phase 5) and, once `net` exists,
// on paserati#252 (a native-module reflection bug unrelated to this fake).
// Every other provider path (openai-completions, anthropic-messages,
// google-generative-ai, azure, mistral, openrouter, cloudflare) doesn't
// touch either dependency.
func findPiCodingAgentNodeModulesRoots() []string {
	candidates := []string{
		"/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent",
		"/usr/local/lib/node_modules/@earendil-works/pi-coding-agent",
	}
	var roots []string
	for _, root := range candidates {
		piAiIndex := filepath.Join(root, "node_modules", "@earendil-works", "pi-ai", "dist", "index.js")
		if _, err := os.Stat(piAiIndex); err == nil {
			roots = append(roots, root)
		}
	}
	return roots
}
