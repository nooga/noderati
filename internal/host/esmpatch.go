package host

import (
	"path/filepath"
	"strings"
)

// patchModuleSource applies small transforms for known third-party ESM quirks.
// Each rewrite is individually toggleable via NODERATI_DISABLE_PATCHES for
// the Phase 2 scoreboard (docs/real-node-plan.md) — the name in each apply()
// call is the knob's value.
//
// This used to be twelve patches. Ten were deleted 2026-08-30 after the
// Phase 2 scoreboard confirmed each was dead against the real,
// unmodified `pi-coding-agent@0.80.2` tree (see docs/real-node-plan.md's
// Phase 2 section for the run and the two genuine survivors' reasons).
func patchModuleSource(source, filename string) string {
	disabled := disabledSet("NODERATI_DISABLE_PATCHES")
	apply := func(name string, fn func(string, string) string) {
		if isDisabled(disabled, name) {
			return
		}
		source = fn(source, filename)
	}
	apply("sdk-reexports", patchESMSdkReexports)
	apply("syntax-highlight-stub", patchESMSyntaxHighlightStub)
	return source
}

// patchESMSdkReexports removes re-export blocks that Paserati cannot compile.
func patchESMSdkReexports(source, filename string) string {
	if filepath.Base(filename) != "sdk.js" {
		return source
	}
	// Drop duplicate tool re-exports; imports at top already bind for local use.
	source = strings.Replace(source, `export { withFileMutationQueue, 
// Tool factories (for custom cwd)
createCodingTools, createReadOnlyTools, createReadTool, createBashTool, createEditTool, createWriteTool, createGrepTool, createFindTool, createLsTool, };`, "", 1)
	return source
}

const syntaxHighlightStub = `export function highlight(code, _lang) { return code; }
export function supportsLanguage(_lang) { return false; }
`

// patchESMSyntaxHighlightStub replaces highlight.js-backed syntax highlighting
// with a no-op stub. Real theme.js (unpatched since 2026-08-30) always
// imports the real syntax-highlight.js, so this is the only thing standing
// between it and the real, large, real-world-ESM highlight.js.
//
// The register-allocator compiler bug that originally motivated this patch
// (paserati#121, github.com/nooga/paserati/issues/121) is fixed upstream as
// of 2026-08-30. Re-verified with the fix pulled in: the real highlight.js
// now compiles, but two more real gaps surfaced behind it —
// lib/languages/latex.js needs regex lookahead Go's RE2-based regexp
// package doesn't support (architectural, not really fixable here), and
// lib/languages/mercury.js hits paserati#122
// (github.com/nooga/paserati/issues/122, filed, not yet fixed): a property
// slot that once held a value read from a frozen object stays frozen after
// being reassigned a fresh, unfrozen value. highlight.js's own per-language
// try/catch keeps both from crashing the process, but they print
// `Language definition for 'X' could not be registered` on every run,
// which real Node doesn't. Keep this patch until at least #122 is fixed.
func patchESMSyntaxHighlightStub(source, filename string) string {
	if filepath.Base(filename) != "syntax-highlight.js" {
		return source
	}
	return syntaxHighlightStub
}
