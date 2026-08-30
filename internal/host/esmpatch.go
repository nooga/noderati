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
// Phase 2 scoreboard confirmed each was dead against the real, unmodified
// `pi-coding-agent@0.80.2` tree. `syntax-highlight-stub` (the eleventh) was
// deleted the same day once paserati#121's fix landed and the real
// highlight.js was confirmed to compile and register 190/191 languages
// cleanly — the one exception (`latex`, needing regex lookahead Go's RE2
// doesn't support) is a documented, linked, architectural gap, not a silent
// fake. See docs/real-node-plan.md's Phase 2 section for the full history.
func patchModuleSource(source, filename string) string {
	disabled := disabledSet("NODERATI_DISABLE_PATCHES")
	apply := func(name string, fn func(string, string) string) {
		if isDisabled(disabled, name) {
			return
		}
		source = fn(source, filename)
	}
	apply("sdk-reexports", patchESMSdkReexports)
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
