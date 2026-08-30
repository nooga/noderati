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
// with a no-op stub — the real module hits a real paserati compiler bug
// (register allocator exhaustion compiling a long expression chain), not yet
// filed. Confirmed still required 2026-08-30: individually this patch looked
// dead (the real file is never reached with patchESMThemeTypeboxImport still
// stubbing theme.js's own highlightCode), but disabling both together
// reaches the real highlight.js via the real theme.js and panics. Keep until
// the compiler bug is filed and fixed.
func patchESMSyntaxHighlightStub(source, filename string) string {
	if filepath.Base(filename) != "syntax-highlight.js" {
		return source
	}
	return syntaxHighlightStub
}
