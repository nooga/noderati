package host

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nooga/paserati/pkg/builtins"
	"github.com/nooga/paserati/pkg/driver"
)

// New builds a Paserati session with Node-like process, timers, and builtins.
func New(argv []string) *driver.Paserati {
	inits := append(builtins.GetStandardInitializers(),
		NewProcessInitializer(argv),
		driver.NewHostTimerInitializer(),
	)
	p := driver.NewPaseratiWithInitializers(inits)
	installModules(p)
	if err := p.PreloadAllNativeModules(); err != nil {
		fmt.Fprintf(os.Stderr, "noderati: preload native modules: %v\n", err)
	}
	installBufferGlobal(p)
	installWorkerThreadsExports(p)
	dirs := append(entryScriptDirs(argv), findPiCodingAgentNodeModulesRoots()...)
	p.AddResolver(NewNodeModulesResolver(dirs...))
	p.AddResolver(NewPackageImportsResolver())
	p.AddResolver(NewJSShimResolver())
	p.AddResolver(NewOSPathResolver())
	p.AddResolver(NewNodeMissingResolver())
	return p
}

func entryScriptDirs(argv []string) []string {
	if len(argv) < 2 {
		return nil
	}
	script := argv[1]
	if script == "-e" || script == "-p" {
		return nil
	}
	abs, err := filepath.Abs(script)
	if err != nil {
		return nil
	}
	return []string{filepath.Dir(abs)}
}

func installModules(p *driver.Paserati) {
	declarePath(p)
	declareConstants(p)
	declareOS(p)
	declareUtil(p)
	declareFS(p)
	declareURL(p)
	declareQuerystring(p)
	declareAssert(p)
	declareChildProcess()
	installChildProcessNatives(p)
	declareReadline()
	declareTTY(p)
	declareEvents()
	declareUndici()
	declareStream()
	declareBuffer(p)
	declareCrypto(p)
	declareFSPromises(p)
	declareModule()
	declareWorkerThreads(p)
	declareDiagnosticsChannel()
	declareV8(p)
	declareVM(p)

	// Ledger group B (docs/real-node-plan.md): third-party npm package
	// fakes, individually toggleable for the Phase 2 scoreboard.
	disabledFakes := disabledSet("NODERATI_DISABLE_FAKES")
	// pi-tui's fake was deleted 2026-09-03 — paserati#195/#196 (v flag and
	// \p{Default_Ignorable_Code_Point}), #218 (new RegExp() backreference
	// fallback), and #222–#225 (arrow function `this` lost as an
	// object-literal property value; a getter on a Function-typed
	// prototype not found via inheritance — chalk's own real root cause
	// for always emitting color; \p{RGI_Emoji} and \p{Script=Value}/
	// \p{Script_Extensions=Value} regex property-escape gaps) all merged
	// upstream. Deletion required both: the CLI-invocation scoreboard
	// matching baseline (necessary but not sufficient, per the
	// twenty-third round's own note that those three invocations only
	// exercise pi-tui's import) AND a real functional exercise of the
	// actual component surface (utils.js/fuzzy.js/keys.js/keybindings.js/
	// terminal-colors.js, and the Box/Text/TruncatedText/Markdown/
	// SelectList/SettingsList/Container components' render() methods,
	// exercised via real call patterns and theme shapes copied from
	// pi-coding-agent's own real theme.js) diffed byte-for-byte against
	// real Node — see docs/real-node-plan.md's Phase 3 section. Not
	// exercised (deliberately, not by oversight): TUI's live
	// differential-render loop and raw-stdin listening, which need an
	// actual attached terminal/pty to test meaningfully on any engine —
	// that surface stays unverified by this deletion. node_modules
	// resolution now always loads the real pi-tui package.
	if !isDisabled(disabledFakes, "pi-ai") {
		declarePiAi()
	}
	if !isDisabled(disabledFakes, "pi-agent-core") {
		declarePiAgentCore()
	}
	declarePerfHooks()
	declareStringDecoder()
	// typebox's own top-level entry (Type.Object etc.) was deleted
	// 2026-09-02 (paserati#183 fixed, real package verified working via
	// actual functional exercise — see docs/real-node-plan.md's Phase 3
	// section) — node_modules resolution now always loads the real
	// typebox package for the bare "typebox" specifier. typebox/value's
	// fake was deleted 2026-09-02 (paserati#188 fixed, real Check/Errors
	// verified working via actual functional exercise — see
	// docs/real-node-plan.md's Phase 3 section). typebox/compile's fake
	// was deleted 2026-09-02 too, once its own two-layer block cleared:
	// paserati#190 (Unicode ID_Start/ID_Continue property escapes,
	// needed by Type.Record's key-pattern codegen) then paserati#192 (a
	// hoisted-function-reference heap-slot bug it uncovered next, hit
	// via typebox + typebox/compile both reached through dynamic
	// import() in pi-coding-agent's real startup path) — both merged
	// upstream; real Compile(...).Check/.Errors verified against the
	// actual ModelsConfigSchema shape (nested Type.Record/Type.Object/
	// Type.Optional) matching real Node exactly — see
	// docs/real-node-plan.md's Phase 3 section. node_modules resolution
	// now always loads the real typebox/compile entry point.
	// diff's fake was deleted 2026-09-02 — paserati#182 (spread of
	// arguments) and paserati#185 (String.split capture groups) both
	// merged upstream; real package verified via its exact real call
	// sites (Diff.diffLines, Diff.createTwoFilesPatch, Diff.diffWords —
	// edit-diff.js and the interactive diff.js) matching real Node's
	// output exactly — see docs/real-node-plan.md's Phase 3 section.
	// node_modules resolution now always loads the real diff package.
	if !isDisabled(disabledFakes, "jiti") {
		declareJiti()
	}
	// minimatch's fake was deleted 2026-08-31 (paserati#144 fixed, real
	// package verified working via actual functional exercise — see
	// docs/real-node-plan.md's Phase 3 section) — node_modules resolution
	// now always loads the real minimatch package.
	// glob's fake was deleted 2026-09-02 — paserati#180 (the "must call
	// super constructor" bug on glob's real minified ESM bundle) merged
	// upstream; real package verified via its exact real call pattern
	// (bare `import { globSync } from "glob"`, pi-coding-agent's own
	// options shape) — see docs/real-node-plan.md's Phase 3 section.
	// node_modules resolution now always loads the real glob package.
	// proper-lockfile's fake was deleted 2026-09-02 — real package's
	// full async (lock/unlock/check) and sync (lockSync/checkSync/
	// unlockSync) APIs both verified directly against pi-coding-agent's
	// exact real call patterns from all three real call sites
	// (auth-storage.js, settings-manager.js, trust-manager.js) — see
	// docs/real-node-plan.md's Phase 3 section. node_modules resolution
	// now always loads the real proper-lockfile package.
	declareCJSInterop(p)
}
