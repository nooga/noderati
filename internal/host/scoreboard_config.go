package host

import (
	"os"
	"strings"
)

// Phase 2 scoreboard toggles (docs/real-node-plan.md). Two independent
// knobs, because installModules() mixes ledger group A (real builtins —
// must always stay on) with group B (third-party package fakes), and
// patchModuleSource applies all esmpatch.go rewrites unconditionally:
//
//   - NODERATI_DISABLE_FAKES — comma-separated group-B fake names, or "all",
//     to stop registering those shims so real node_modules resolution picks
//     up the actual package instead.
//   - NODERATI_DISABLE_PATCHES — comma-separated esmpatch.go patch names, or
//     "all", to skip individual source rewrites.
//
// Both are read fresh on every call (not cached at package init) so the
// scoreboard tool (cmd/scoreboard) can flip them per run, in-process,
// without a rebuild.

func disabledSet(envVar string) map[string]bool {
	raw := os.Getenv(envVar)
	if raw == "" {
		return nil
	}
	set := make(map[string]bool)
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			set[name] = true
		}
	}
	return set
}

func isDisabled(disabled map[string]bool, name string) bool {
	if disabled == nil {
		return false
	}
	return disabled["all"] || disabled[name]
}
