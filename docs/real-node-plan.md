# Plan: real Node surface, no per-app fakes

Paserati `main` is current as of this plan (`6450d93b`, 2026-08-30). This doc is
the noderati-side plan; the paserati-side seam work is tracked in
[`docs/noderati-host-plan.md`](https://github.com/nooga/paserati/blob/main/docs/noderati-host-plan.md)
in that repo and [issue #77](https://github.com/nooga/paserati/issues/77). This
file is the single status source of truth for noderati — update it instead of
the README's stale "Not yet" list.

## Where we actually are

We got `pi` (the coding agent, `@earendil-works/pi-coding-agent@0.80.2`) to
*start* by writing fakes for its dependencies and rewriting its source files
in place. That's not "running a real Node program" — it's running a
different, much smaller program we wrote that happens to share some type
signatures with pi. The goal now: delete every one of those, and get the real
unmodified npm tree to run because the engine and the host actually implement
the surface it needs.

### The ledger

`internal/host/host.go:installModules()` mixes three different things.
They need different treatment — conflating them is how we got here.

**A. Real Node builtins — keep, harden, fill gaps.** These implement an
actual Node module against Go stdlib or real logic, not another package's API:
`path`, `os`, `util`, `fs`, `fs/promises`, `url`, `querystring`, `assert`,
`child_process` (spawnSync), `readline`, `tty`, `events`, `buffer`, `crypto`
(real `crypto/sha256` etc.), `worker_threads`, `perf_hooks`, `module`
(`createRequire`), package.json `"imports"` (`#specifier`) resolution,
`node_modules` resolution. Spot-checked and genuinely solid: `crypto.go`,
`buffer.go`, `packageimports.go`. This is the part of noderati worth being
proud of and building on.

**A-minus. Real builtin name, fake body — needs a real implementation, not
deletion:** `string_decoder` (`stringdecoder.go` — `write()` just does
`String(c)`, no actual UTF-8 multibyte/incremental decoding), `glob`
(real Node/npm surface, but `globSync` always returns `[]` — silently
*wrong*, worse than missing). `minimatch`'s equivalent fake was resolved the
group-B way instead, 2026-08-31 — deleted outright, real `node_modules`
resolution now loads the genuine package (see Phase 3 below); it never
needed a real from-scratch implementation here, just deleting the shim.
`stream.go` also hand-rolls its own `EventEmitter` instead of reusing
`events.go`'s — pick
one.

**B. Third-party npm package fakes — delete the shim, load the real
package.** These aren't Node surface at all; they're interceptions of specific
libraries pi-coding-agent depends on, hardcoded as JS strings in Go and
registered ahead of the real files on disk: `@earendil-works/pi-tui` (every
export is a no-op — the entire TUI is fake), `@earendil-works/pi-ai` (a
from-scratch reimplementation of the real LLM client, including its own model
catalog and provider fetch calls), `@earendil-works/pi-agent-core` (a
from-scratch reimplementation of the actual agent loop), `typebox` /
`typebox/value` / `typebox/compile`, `diff`, `jiti/static`, `glob`,
`proper-lockfile`, `hosted-git-info`. (`minimatch` was here too — deleted
2026-08-31, see Phase 3 below, first of this group actually gone.)
None of these belong in a "Node host." Removing them is a deletion task, not
a build task, and it's most of `internal/host/`'s file count.

**C. `esmpatch.go` per-file source rewrites — done, file deleted
2026-09-01.** Was twelve rewrites keyed by filename, each patching around
one specific parser/compiler gap or the package fakes in (B). Ten were
deleted in the original Phase 1 close-out (2026-08-30) once the Phase 2
scoreboard confirmed each dead; `syntax-highlight-stub` the same day once
[paserati#121](https://github.com/nooga/paserati/issues/121) (a
register-allocator compiler bug) and
[paserati#122](https://github.com/nooga/paserati/issues/122) (a stale
frozen-property flag) were both fixed upstream and the real `highlight.js`
was confirmed to register 190/191 bundled languages — the one exception
(`latex`, needing regex lookahead Go's RE2 doesn't support) is a documented,
linked, architectural gap, not a reason to keep faking the whole module. The
last one, `sdk-reexports` — a real, still-needed compile-error workaround,
misidentified in an earlier pass of this doc as an `export *` issue (it
isn't) — was deleted 2026-09-01 once
[paserati#163](https://github.com/nooga/paserati/issues/163) (re-exporting
an imported name) was fixed and verified both directly and via the full
scoreboard/all-three-`pi`-invocations, matching baseline exactly. `esmpatch.go`
itself is gone (it held zero patches at that point); see Phase 2 below for
the full verification history, including why individual verification isn't
sufficient on its own (it happened twice in a row here, before this final
patch).

**D. Resolver-side dirty tricks, independent of the above:**
- `findPiCodingAgentNodeModulesRoots()` (`piai.go`) hardcodes
  `/opt/homebrew/lib/node_modules/...` and `/usr/local/lib/...` and splices
  them into every program's resolver roots, unconditionally. Real Node walks
  up from the entry file's directory through `node_modules` at each level.
  There is no walk-up implementation to fall back on right now — this hack
  isn't a shortcut alongside real resolution, it's standing in for it.
- `NodeMissingResolver` turns an unresolvable `node:*` specifier into
  a module whose *body* throws at runtime, instead of failing resolution
  immediately with a clear "no resolver for X" error and a spot to enumerate
  what got asked for. Fine as a last-resort fallback; wrong as silent policy.

## What's already fixed upstream (verified, not assumed)

Tested directly against fresh paserati `main`, as `.js` (the skip-typecheck
path pi's `dist/` actually runs on, not `.ts` — the two paths bind imports
differently and recent fixes are skip-typecheck-specific):

- optional catch binding (`catch { }`, no parameter) — works
- `await import(...)` dynamic import — works
- `export * from` re-export, consumed via named import — works
- named `const` export import, both direct and through an `export *` barrel — works

Re-checked against the actual `esmpatch.go` sites — an earlier pass of this
doc misattributed these to the wrong functions; corrected 2026-08-30:

- **optional catch** is rewritten by `patchESMPiAiSyntaxCompat` (not
  `patchESMSyntaxHighlightStub`, which is now an unrelated wholesale stub for
  highlight.js exceeding compiler limits) — genuine deletion candidate.
- **dynamic import + optional catch together** are both cited in
  `patchESMPiAiAuthContext`'s comment, but that patch is a *whole-file*
  replacement of `context.js`, not a targeted rewrite — deleting it loads the
  real file, which may still fail for a third, unrecorded reason. Needs a
  scoreboard run, not an assumption.
- **`export *`** is *not* what `patchESMSdkReexports` patches — it removes a
  named export list with an embedded comment and newlines, no `export *`
  involved. That one is very likely still needed; nothing verified above
  covers its shape. `patchESMPiAiOauthIndexReexports` is the one that
  actually strips two `export * from` lines, and it wasn't on this list
  before.
- `patchESMPiAgentCoreReexports` claims skip-typecheck can't harvest
  **class** names from `export *` specifically — the case verified above was
  a `const` through a barrel, not a class, and bug 4 below (var/class TDZ)
  is exactly the class-vs-const distinction. Don't assume this one's covered
  by the const-barrel result; it needs its own before/after check.

Don't assume any of the remaining patches are still needed either — each gets
its own before/after check against the real file, via the Phase 2 scoreboard
(below), not a blanket "probably fine."

## Phase 1's engine bugs — all four found, fixed, and pushed

Running the real, unmodified `pi` CLI (`dist/cli.js --help`) through current
noderati — with every existing shim/patch still active — used to crash
before producing any output:

```
ReferenceError: isBunBinary is not defined
  1: /**
     ^
    at line 1, column 1
```

Four paserati bugs were behind this and behind `--help`'s subsequent silence.
All four are filed, fixed, verified, and confirmed on `origin/main` as of
2026-08-30 (bugs 3–4 took four commits — `1d7aaed4`, `26e2d5c`, `3aaccdd`,
`98560e3`, all present on `origin/main` — the initial
fix plus three more leaks of the same VM exception state found across
review: a `finally`-without-`catch` leak, the symmetric fulfilled-resume
path, and an `err.Error()`-stringification instance on the ordinary-
promise-chain path). Two more gaps were found and recorded (not fixed) while
verifying bugs 3/4: [paserati#120](https://github.com/nooga/paserati/issues/120)
(no unhandled-rejection reporting for a plain async-function-call rejection
with zero handlers — distinct from bug 3, which was about an *awaited*
rejection) and a follow-up note on #119 (class TDZ is now more permissive
than spec for a pre-declaration reference — reads `undefined` instead of
throwing `ReferenceError`; more permissive than the previous wrong-error
behavior, not a regression).

1. **Module double-evaluation on resolved-path collisions — [paserati#116](https://github.com/nooga/paserati/issues/116), fixed, pushed
   (`e6bac813` on `origin/main`, confirmed 2026-08-30; the `7c99411b`
   previously cited here doesn't match origin's history, likely rewritten on
   push).**
   A module reached via two different (but resolved-equivalent) relative
   specifiers ran its top-level code twice — `pkg/vm/vm.go`'s `executeModule`
   cached execution state keyed by raw specifier text, not resolved path.
   Confirmed with a side-effect counter (`EVAL 1`/`EVAL 2` → `EVAL 1` after the
   fix). Verified: test262 `language/**` 0 new passes/failures vs. baseline,
   `tests/scripts` smoke suite unchanged. **This was not the cause of the
   `isBunBinary` crash** — fixing it alone left the crash identical.

2. **A hoisted function's body couldn't read its own module's top-level
   `const`/`let` once that module was loaded as a dependency — [paserati#117](https://github.com/nooga/paserati/issues/117), fixed, pushed.**
   This was the actual cause. Bare-minimum repro (reproduces in vanilla
   `paserati`, both `.js` skip-typecheck and fully-typechecked `.ts`):
   ```js
   // lib.js
   export const X = true;
   export function f() { if (X) return "yes"; return "no"; }
   // main.js
   import { f, X } from "./lib.js";
   console.log("X=", X, "f()=", f());   // threw: X is not defined
   ```
   Root cause (two compounding gaps in `pkg/compiler`, confirmed via bytecode
   disassembly — `f`'s `OpGetGlobal` and the module's `OpSetGlobalInit` for
   `X` read/wrote *different* heap indices):
   - `moduleGlobalKey` (namespaces a module-scope name so same-named
     top-level bindings in unrelated modules don't alias one heap slot)
     checked a flag (`loadedViaModuleLoader`) that's set on the top-level
     compiler only and isn't inherited by a nested function compiler — so
     the identical call, made while compiling `f`'s own body, silently
     computed a *different* (unnamespaced) key than the module's own
     top-level code did for the same name.
   - Separately, the "identifier not found anywhere, must be an external
     global" fallback had no way to tell "this module's own
     not-yet-compiled top-level binding" (reached because a hoisted
     function's body compiles before the module's sequential statements
     do) from a genuinely external/undeclared global, so it never even
     tried the namespaced key.

   A first attempt at the second half (broadly registering top-level
   let/const names as globals in the symbol table, mirroring how hoisted
   *function* names already are) regressed `tests/scripts/generator_nested.ts`
   — it broke a hoisted function's own local variable from shadowing a
   same-named module-level one. The committed fix narrows to exactly the one
   fallback path instead. Verified: `tests/scripts` clean (including that
   regression test), test262 `language/**` 0 new passes/failures,
   `built-ins/**` stable-to-slightly-improved (16084/23294 passed vs. a
   stable 16080 pre-fix).

   `pi-coding-agent`'s `dist/config.js` has exactly this shape
   (`export const isBunBinary = ...` + a function reading it, called from
   `main.js`) — confirmed as the crash's real cause. With the fix, noderati
   running the real, unmodified `pi` CLI now prints `pi --version` correctly
   (`0.80.2`, real package metadata) instead of crashing.

   One related gap found and deliberately **not** fixed here at the time (noted
   on the issue, not blocking pi's `--version`): the fully-typechecked `.ts`
   path has an analogous but distinct gap in the type checker (`Cannot find
   name 'X'` at type-check time) — irrelevant to pi (its `dist/` is `.js`,
   skip-typecheck) but relevant to a future `.ts`-first target (`tsc`, Phase
   6). (`export var X = ...`'s copy of this bug *was* chased down and fixed —
   see bug 4 below; it turned out to matter for pi after all.)

**`pi --help` no longer crashed, but produced no output** — exit 0, nothing
printed, despite `--version` genuinely working. Root-caused to two further
bugs, both now fixed:

3. **An unguarded `await` on a synchronously-throwing async function hangs
   the whole chain forever, silently — [paserati#118](https://github.com/nooga/paserati/issues/118), fixed, pushed.**
   `createSessionManager`'s `new SessionManager(...)` (real pi code) threw
   during `--help` (see bug 4), and `main()` awaited it with no try/catch —
   completely ordinary, spec-legal async code. The exception vanished
   without a trace: `resumeAsyncFunctionWithException` correctly unwound to
   its own native-call boundary but only checked `vm.frameCount == 0`
   afterward, missing the "stopped at a boundary, frames still present" case;
   execution fell through to `vm.run()`, whose entry guard silently treated
   the still-pending exception as "nothing to run" (`InterpretOK`). The
   caller saw a nil error and never rejected the async function's promise —
   its `.then()` reactions never fired, and the process exited 0 once
   nothing else was pending. Minimal repro needs no classes or nesting:
   ```js
   async function thrower() { throw new Error("boom"); }
   async function main() { const x = await thrower(); }
   main().then(() => {}, (e) => console.log("REJECTED:", e.message));
   // before fix: nothing prints, exit 0. after: "REJECTED: boom".
   ```
   This is what made bug 4 below (and *any* uncaught exception on this path)
   invisible rather than merely wrong — it's the reason `--help`'s failure
   looked like silence instead of an error message.

   A second, closely related leak (found and fixed in a follow-up commit,
   `26e2d5c`, while verifying this one against a `try { await … } finally { … }`
   with no `catch`): once the exception passes through a bare `finally`,
   `vm.run()` does correctly surface `InterpretRuntimeError` this time (via
   the pending-throw-after-finally path) — but never clears
   `vm.unwinding`/`vm.currentException` itself before returning. That stale
   state then leaked into the *next*, unrelated `vm.Call` (invoking the
   rejection's own `.then()` callback), which immediately "re-threw" the
   stale exception instead of ever running the callback's body — so the
   `finally` block itself ran correctly, but the `.then()` reject handler
   silently never did. Fixed by clearing that state the same way
   `handleCatchBlock` does, for this path too.

   A third leak (found by a second advisor review pass, fixed in
   `3aaccdd`): the earlier "resumeAsyncFunction doesn't call
   vm.throwException before its own vm.run(), so it isn't affected"
   reasoning only ruled out half the bug — vm.run() itself returning
   InterpretRuntimeError without clearing this state applies there too,
   whenever code *after* a successful (fulfilled) resume throws with no
   handler. Same fix, applied to `resumeAsyncFunction`. Also found and
   fixed in the same pass: both of its callers rejected a failed resume
   with `NewString(err.Error())` — since `exceptionError.Error()` is a
   fixed literal (`"VM exception"`), a real thrown `Error` object was
   silently replaced by that unrelated string before reaching a `.catch()`
   handler (`e.message` would read `undefined`). Fixed to extract the real
   value via the already-established `ExceptionError`/`GetExceptionValue`
   pattern used elsewhere in the file.

   A fourth instance of that same `err.Error()`-stringification bug (found
   by a third review pass, fixed in `98560e3`) was on `promise.go`'s
   `triggerPromiseReactions` — the code that invokes an ordinary
   `.then()`/`.catch()` handler and propagates its result, unrelated to
   async-resume internals: a `.then(handler)` whose `handler` throws
   silently handed the *next* `.catch()` an unrelated `"VM exception"`
   string instead of the real thrown `Error`. Same fix. (A fourth probe —
   `try { await … } finally { await … }`, suspending mid-`finally` while an
   exception is still pending — was also run at this point and found
   correct: no bug there.)

4. **The var/class analog of bug 2, above — [paserati#119](https://github.com/nooga/paserati/issues/119), fixed, pushed.**
   Same root cause as #117, extended to the two declaration kinds it didn't
   cover: a top-level `var` referenced from a hoisted function (real repro:
   noderati's own `uuidv7()` shim's module-scope `var _uuidSeq`, thrown as
   `ReferenceError: _uuidSeq is not defined` from a compound assignment), and
   a top-level `class` referenced from a sibling hoisted function (real
   repro: pi's own `AgentSessionRuntime` class, referenced from
   `createAgentSessionRuntime`, thrown as `ReferenceError: AgentSessionRuntime
   is not defined`) — the latter was `--help`'s actual final blocker.
   Fixed by broadening #117's `TopLevelLetConstNames` set (renamed
   `TopLevelDeclNames`) to also collect var and class names, and updating
   every fallback site that consults it (`compileIdentifier`'s read path,
   plus two near-identical fallbacks in `compileAssignmentExpression` for
   compound/simple assignment).

With all four fixed, `pi --help` now prints the real, complete, unmodified
help text (167 lines — usage, commands, every flag, environment variables,
built-in tool names) and `pi --version` still prints `0.80.2`, both against
the real npm install, both exit 0.

Verification for bugs 3 and 4 together: `go test ./...` clean; test262
`language/**` diffed against pre-fix HEAD — 0 new passes, 0 new failures
(23141/23523 both sides); `built-ins/**` after fix — 16084/23294, matching
the established pre-fix baseline exactly (a few-test wobble against a
GitKraken-hook-generated local baseline, 16080, is consistent with
pre-existing test262 flakiness already observed on both sides of these
fixes this session, not a regression).

The `isBunBinary is not defined` error's originally-reported location (`line
1, column 1`, the entry file's own docstring) was also fabricated — a
separate, lower-priority diagnostics gap noted while chasing the two bugs
above, not yet filed. Worth fixing for future debugging sessions.

## Principles for the work ahead

- **The real npm tree on disk is the target, always.** If a shim exists for a
  named third-party package, the fix is either "load the real file and make
  the engine/host handle it" or "it's out of scope and we say so" — never
  "write a smaller fake that satisfies today's call sites."
- **Every workaround names its cause.** A patch with no linked issue is a
  liability, not a fix. `esmpatch.go` already does this in its comments; keep
  that discipline and add the tracker link.
- **Pin the target, measure regressions.** `pi@0.80.2` is on disk; don't let
  it drift mid-effort. A scoreboard script that runs the unmodified tree
  end-to-end and records the first failure is the only way to tell "fixed a
  real bug" from "moved the crash."
- **Generic before specific.** `core/extensions/loader.js`'s real behavior
  (dynamic plugin loading via `jiti`) is legitimately out of scope for a
  from-scratch runtime for a long while — but the fallback should be "this
  Node/engine feature isn't supported yet, here's what's missing," produced
  generically, not a file-path-matched stub for one file in one package.

## Phased plan

### Phase 0 — inventory (done, above)
Ledger of `installModules()` by disposition; confirmed which `esmpatch.go`
entries are already dead; found and localized the current blocking crash.

### Phase 1 — fix the confirmed live engine bugs

1. **Module double-evaluation on resolved-path collisions — done, pushed.**
   [paserati#116](https://github.com/nooga/paserati/issues/116), fixed by
   commit `e6bac813` on `origin/main` (see the narrative section above for
   the `7c99411b`-doesn't-match-history correction).
2. **A dependency module's function can't read its own module's top-level
   `let`/`const` once called through an import — done, pushed.**
   [paserati#117](https://github.com/nooga/paserati/issues/117), fixed by
   commit `f6fe29c`. Fixed the `--version` crash; `--help` still produced no
   output afterward (see 3/4 below).
3. **An unguarded `await` on a synchronously-throwing async function hangs
   the whole chain forever, silently — done, pushed.**
   [paserati#118](https://github.com/nooga/paserati/issues/118), fixed by
   commits `1d7aaed4` + `26e2d5c` + `3aaccdd` + `98560e3` (four related
   leaks, found across three rounds of review — a bare `finally`, the
   symmetric fulfilled-resume path, and an unrelated-code-path instance of
   the same `err.Error()`-stringification bug), all confirmed present on
   `origin/main` as of 2026-08-30 (this doc previously said "committed but
   not yet pushed" — stale). This is what made `--help`'s underlying failure
   (bug 4) look like total silence instead of a printed error.
4. **The var/class analog of bug 2 — done, pushed.**
   [paserati#119](https://github.com/nooga/paserati/issues/119), fixed by
   commit `1d7aaed4` (same commit as bug 3 — found and fixed together). This
   was `--help`'s actual final blocker (pi's own `AgentSessionRuntime` class,
   referenced from a sibling hoisted function).
5. **Wrong error source locations on thrown `ReferenceError`s.** Real, and
   every future debugging session on a real program pays this tax until it's
   fixed, but it didn't block finding bugs 1–4 above — file it, fix when
   convenient, not a Phase 1 gate.

**Phase 1 is done modulo item 5** (a diagnostics quality-of-life gap, not a
correctness blocker): `pi --help` *and* `pi --version` both produce correct,
complete output against the real, unmodified `pi-coding-agent@0.80.2`
install. This section used to say the close-out step was removing "the three
verified-dead `esmpatch.go` patches" as a fixed list — that turned out not to
be executable as written (see "already fixed upstream" above, corrected
2026-08-30): the doc's function-name attributions didn't match the code. The
Phase 2 scoreboard (below) was built to measure instead of assume, and its
first run found ten genuinely dead patches (not three) plus a live,
previously-unknown compiler bug hiding behind one of the "dead" ones —
deleting on the strength of the doc description alone, without measuring,
would have shipped a crash. All ten are now deleted; see Phase 2's "Phase 1
close-out" note for the verified result. Removing the rest of `esmpatch.go`
(the two genuine survivors) stays Phase 3 work, gated on deleting the package
fakes it patches around, and on filing/fixing the compiler bug for
`syntax-highlight-stub`.

### Phase 2 — regression scoreboard (in progress, started 2026-08-30)
A tool (`cmd/scoreboard`) that runs `dist/cli.js --help`, `--version`, and a
scripted `-p "hello"` invocation (no API key; the expected failure is a
`connect: connection refused` dialing the default local-model endpoint —
that's real Node behavior to reproduce faithfully, not a noderati bug) against
the real unmodified `pi-coding-agent@0.80.2` tree at
`/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent`.

`installModules()` mixes ledger group A (real builtins — must always stay on,
nothing runs without them) with group B (third-party package fakes) in one
flat call list, and at the time this was built `patchModuleSource` applied
all twelve `esmpatch.go` patches unconditionally (now two, post Phase 1
close-out below) — so a single "shims off" switch can't exist; the scoreboard
needs two independent knobs:

- `NODERATI_DISABLE_FAKES` — comma-separated list of group-B fake names
  (`pi-tui`, `pi-ai`, `pi-agent-core`, `hosted-git-info`, `typebox`, `diff`,
  `jiti`, `glob`, `minimatch`, `proper-lockfile`) or `all`, to stop
  registering those shims so real `node_modules` resolution picks up the
  actual package instead.
- `NODERATI_DISABLE_PATCHES` — comma-separated list of `esmpatch.go` patch
  names or `all`, to skip individual `patchModuleSource` rewrites.

Both are read fresh on every `host.New()`/`patchModuleSource` call, so
flipping them needs no rebuild of `internal/host` itself — but each
configuration still runs as its own **subprocess** of the real `noderati`
binary (built once, into a temp dir, at scoreboard startup), not in-process:
`pi`'s own `--help`/`--version` paths call `process.exit()`, which
`internal/host` wires to the real `os.Exit()` — in-process that killed the
scoreboard tool itself on the very first invocation. `cmd/scoreboard` records
exit code + a trimmed tail of combined stdout/stderr as the signature,
diffed against a baseline (all shims on), one line per configuration. Run it
before and after every change in Phases 3–4 so "shrunk the gap" is
measurable, not felt.

**First run, 2026-08-30** (`go run ./cmd/scoreboard`, 24 configs × 3
invocations, ~2 min): full output in scoreboard git history/CI log once this
is wired into CI; summary here.

Individually toggled off, with **no diff** from baseline on any of
`--version`/`--help`/`-p "hello"` (i.e. each looked like a real deletion
candidate *on its own*):
- `NODERATI_DISABLE_PATCHES`: `keybindings-alias`, `syntax-highlight-stub`,
  `theme-typebox-stub`, `extension-loader-stub`, `pi-agent-core-reexports`,
  `pi-ai-index-reexports`, `pi-ai-compat-reexports`, `pi-ai-oauth-reexports`,
  `pi-ai-oauth-index-reexports`, `pi-ai-syntax-compat`, `pi-ai-auth-context`
  — i.e. **11 of the 12 patches**, including specifically the three this doc
  originally (and wrongly-named) targeted for Phase 1 close-out:
  `pi-ai-syntax-compat` (optional catch), `pi-ai-auth-context` (dynamic
  import), `pi-ai-oauth-index-reexports` (`export *`). Also confirms the
  `patchESMPiAgentCoreReexports` class-vs-const concern raised in "already
  fixed upstream" above: clean, so upstream's `export *` fix covers the class
  case too.
- `NODERATI_DISABLE_FAKES`: `jiti`, `minimatch`.

**Not clean** — real, still-needed:
- `patch-off:sdk-reexports` — confirms the correction above: this patch does
  not touch `export *` at all, and removing it breaks `--version`/`--help`/
  `-p` alike (`exported name 'withFileMutationQueue' not found in current
  scope`). Keep it.
- `fake-off:pi-tui`, `fake-off:pi-ai`, `fake-off:pi-agent-core`,
  `fake-off:hosted-git-info`, `fake-off:typebox`, `fake-off:diff`,
  `fake-off:glob`, `fake-off:proper-lockfile`, `all-fakes-off` — expected;
  these are Phase 3's actual work, not Phase 1 close-out.

**A new, real finding, caught before it caused damage:** `all-patches-off`
(all 12 `esmpatch.go` rewrites disabled together) was **not** clean, even
though 11 of the 12 were individually clean — `[VM PANIC] recovered:
Compiler Error: Ran out of registers!` in
`pkg/compiler.(*RegisterAllocator).Alloc`, compiling a long infix-expression
chain. So "11 individually clean" was checked against the *actual* deletion
batch (all 11 off, `sdk-reexports` alone kept) before touching any code —
still not clean, same panic. Bisected by hand (halving the 11, then the
losing half, then pairs): the crash needs exactly two patches disabled
*together*, `syntax-highlight-stub` **and** `theme-typebox-stub` — neither
alone reaches the real `highlight.js` (with `theme-typebox-stub` still
active, `syntax-highlight-stub`'s own real file is never imported; with
`syntax-highlight-stub` still active, real `theme.js`'s import of it resolves
to the trivial stub instead), but with both real, `theme.js` really does
import the real `highlight.js`, and the compiler can't compile it.

Bisected further, isolated to a minimal, dependency-free repro, and filed as
[paserati#121](https://github.com/nooga/paserati/issues/121): the real
culprit inside `highlight.js` is `lib/languages/gml.js`'s `built_in` field, a
~610-term chain of string-literal `+` concatenation. `compileInfixExpression`
allocates a fresh register for its own left operand *before* recursing into
it (`compile_expression.go:1222`), and can only free that register after the
whole recursive call returns — so a left-associative chain needs `O(chain
depth)` simultaneously-live registers, not `O(1)`, against
`RegisterAllocator`'s fixed 256-register ceiling. Confirmed via a
plain-`paserati` repro (no noderati involved): a bare top-level `const s = "a"
+ "b" + ... ;` compiles fine at 248 terms, panics at 249 — and the same
symptom reproduces for a numeric `+` chain and for an `&&` chain of the same
length, so it's not string-`+`-specific.

**Fixed upstream, 2026-08-30** — `acd1d7fa` on `origin/main`
([comment on #121](https://github.com/nooga/paserati/issues/121)): both the
standard-operator and logical-operator branches now fold a left-associative
run through a single accumulator register instead of recursing with a fresh
`Alloc()` per level. Verified after pulling the fix: the exact repro above
now handles 200,000+ terms (was ~248); `pkg/compiler.(*RegisterAllocator)`'s
max register at n=5/50/300/2000 stays flat at R3 instead of growing with n;
test262 `language`/`built-ins` both `+0/−0`.

Re-verified end to end with the fix pulled in: enabling both
`syntax-highlight-stub` and `theme-typebox-stub` together against the real
`pi` CLI no longer panics — `--version`/`--help` succeed, `-p "hello"`
reaches the expected network-dial failure. **But two more real gaps
surfaced now that the real `highlight.js` actually loads**, both caught and
logged (not crashed) by `highlight.js`'s own per-language try/catch around
`registerLanguage`, so they show up as extra `ERROR: Language definition for
'X' could not be registered` noise on every run rather than a crash:

- **`latex`**: `error parsing regexp: invalid or unsupported Perl syntax:
  `(?!`` — Go's `regexp` package (RE2) has no lookahead support at all, by
  design (it trades that expressiveness for guaranteed-linear-time
  matching). This isn't a bug to fix, it's an architectural boundary of the
  regex engine noderati/paserati is built on; supporting it would mean a
  different regex engine or a lookahead-emulation layer, out of scope here.
- **`mercury`**: `TypeError: Cannot assign to read only property 'length' of
  object` — a genuinely new paserati bug, isolated to a 5-line
  dependency-free repro and filed as
  [paserati#122](https://github.com/nooga/paserati/issues/122): a property
  slot that once held a value read from a *frozen* object stays frozen even
  after being reassigned a brand-new, unrelated, unfrozen value.
  `highlight.js`'s own `core.js` deliberately deep-freezes its shared mode
  objects (a bundled `deep-freeze-es6`) so language plugins can't mutate
  them, and `mercury.js` works around that the normal way (`STRING.contains
  = STRING.contains.slice()`) — which real Node handles fine and paserati
  doesn't yet.

**Fixed upstream, 2026-08-30** — `a1e5e22e` on `origin/main` ("Object.freeze/
seal/defineProperty mutated a *shared* Shape"), part of a larger batch that
also landed `#123` (freeze/seal/preventExtensions on functions, RegExps,
Maps, Sets) and `#126` (`Object.defineProperty` throwing on a rejected
redefinition). Re-verified: the `#122` repro now prints the correct
`[9,9,3]`; `mercury.js` registers cleanly; the full, real, unmodified
`highlight.js/lib/index.js` now registers **190 of 191** bundled languages —
the only failure left is `latex`, and that's the documented, architectural
RE2-lookahead gap above, not something further upstream work fixes.

**`syntax-highlight-stub` deleted, 2026-08-30.** One documented gap
(`latex`, linked, architectural, not a silent fake) clears the project's own
bar ("a real implementation or a linked issue, never a silent fake") more
than a wholesale no-op stub does. `esmpatch.go` is down to one patch,
`sdk-reexports`.

**Consequence for Phase 3: "clean individually" is necessary but not
sufficient — always confirm the actual batch before deleting, and again after
deleting.** Patches interact through the shared module graph, and fixing the
bug that made a patch *look* removable can uncover the next one behind it —
this happened twice in a row here (#121 fixed → #122 found; #122 fixed →
clean modulo one architectural gap).

**A second instance of that same interaction, found by the Phase 2 scoreboard
after this deletion:** `fake-off:jiti` went from clean (first scoreboard run)
to a real parse-error `DIFF` (second run) — not a regression, a previously
Phase-1-close-out-hidden real gap. `extension-loader-stub` (deleted in Phase
1 close-out, above) used to intercept `core/extensions/loader.js` before it
ever reached `jiti`; with that patch gone, the real extension loader is
unconditionally live, and it really does need `jiti` for real. Not
investigated further yet — flagged for whoever picks up Phase 3's `jiti`
item.

**Phase 1 close-out, done 2026-08-30 (revised same day once `syntax-highlight-stub`
also cleared):** `esmpatch.go` is down to one patch, `sdk-reexports` — the
only one of the original twelve confirmed to still guard a real,
still-unfixed compile error. Re-verified against the real build after each
deletion (not just the env-var toggle): `--version`/`--help`/`-p "hello"`
match baseline exactly (`--help`/`--version` now with one extra, documented,
architectural `latex` error line — see above); `go test ./...` clean. Phase
1 is now fully done except item 5 (error-location diagnostics), which
remains a fix-when-convenient, not a gate.

### Phase 3 — delete the third-party fakes (ledger group B)
One at a time, in dependency order (leaves first): `hosted-git-info`,
`proper-lockfile`, `jiti/static`, `diff`, `typebox`*, `@earendil-works/pi-tui`,
`@earendil-works/pi-ai`(+`/compat`+`/oauth`), `@earendil-works/pi-agent-core`.
For each: delete the shim registration and its `registerJSShim` string, run
the scoreboard, and either (a) it now loads the real file with no new
failure — done, or (b) it hits a new parser/host gap — file that gap
specifically (ledger group C/D discipline above), re-patch *only* if the gap
is a multi-week engine project, and note the patch's tracker link. Expect
`pi-tui` and `pi-ai` to surface the most new gaps — they're the largest fakes
and the real packages are large, real-world ESM.

**Exploration pass, 2026-08-30 (findings recorded, no fakes deleted yet —
each hits a real, currently-unfixed gap):**

- **`hosted-git-info`** — bisected to a real, significant, general paserati
  bug, isolated to a 4-line dependency-free repro (no self-reference or
  even `hosted-git-info` involved) and filed as
  [paserati#128](https://github.com/nooga/paserati/issues/128): a `class`
  declared inside a function body isn't visible to *any* closure nested in
  that function (not just its own methods) — the reference resolves as a
  bogus, never-written global instead of an upvalue capture. Confirmed via
  `-bytecode`: the closure compiles to `OpGetGlobal` for a class that's
  actually a real, local, already-populated register in the enclosing
  frame. Root-caused to `compile_class.go`'s local-class path pre-defining
  the class's own name with a placeholder `nilRegister` that isn't updated
  to the real register until *after* the whole class body (all its methods)
  has already compiled — the same "not-yet-finalized binding falls through
  to a global fallback" shape as #117/#119, but for function-scoped classes
  captured by upvalue rather than module-level ones read via `OpGetGlobal`.
  `hosted-git-info`'s `GitHost` class hits this on its own static methods
  (`addHost`, `fromUrl`, ...) because noderati's CJS interop function-wraps
  every `require()`d file. Given how common "class declared inside a
  function, referenced by its own or a sibling closure" is in real-world
  CJs, likely blocks more than just this one package.
- **`proper-lockfile`** — depends on `graceful-fs`, which fails standalone
  (no `hosted-git-info` needed to reproduce) with an unhelpfully vague
  `runtime error during user function execution` and no file/line. Bisected
  to `graceful-fs`'s `polyfills.js`, which does `require('constants')` —
  **two** separate things behind that one vague message:
  1. `constants` is a real Node builtin noderati doesn't implement yet
     (ledger group A, a noderati-side gap — not a paserati issue, just not
     built).
  2. That `Cannot find module 'constants'` error, thrown from *inside* a
     nested `require()` call (`a.js` requires `polyfills.js`, which requires
     `'constants'`), never reaches the top — it gets replaced by the useless
     generic message. Isolated to a 2-file, no-npm-package repro (`a.js`
     requires `b.js`; `b.js` just does `throw new Error(...)`), traced
     through noderati's CJS loader into `vm.Call`/`executeUserFunctionSafe`,
     and filed as
     [paserati#130](https://github.com/nooga/paserati/issues/130): a
     **reentrant** `vm.Call()` (a native call, itself already running
     inside another `vm.Call`, that throws) loses the real exception —
     `executeUserFunctionSafe`'s `InterpretRuntimeError` branch doesn't see
     `vm.unwinding`/`vm.currentException` in the state it expects, even
     though a real exception clearly occurred, and falls through to a fixed
     literal Go error string. Same failure *shape* as the four leaks #118
     fixed (a real exception replaced by a fixed-literal string before
     reaching the embedder), a fifth, distinct trigger. Once `#130` is
     fixed, `constants` will very likely need to actually be implemented too
     — the *real* error will surface, and it'll be exactly that.
- **`pi-agent-core`** — `Agent`, imported directly by name from `agent.js`,
  loads fine. The package's real `index.js` (an `export *` barrel) doesn't:
  `import * as mod from ".../index.js"` throws `TypeError: Class extends
  value undefined is not a constructor or null`. **Turned out to be a false
  lead** — that reproduces because noderati's *own* `@earendil-works/pi-ai`
  fake (still active during this test) doesn't re-export `EventStream` the
  way the real `pi-ai` package does; disabling the `pi-ai` fake too (so
  `index.js`'s `import { EventStream } from "@earendil-works/pi-ai"`
  resolves against the real package) makes the "Class extends undefined"
  disappear entirely — replaced by an earlier, real blocker: a genuine
  parse error in `@earendil-works/pi-ai/dist/auth/context.js`. Bisected that
  down to a clean, minimal, dependency-free repro and filed as
  [paserati#129](https://github.com/nooga/paserati/issues/129): a
  parenthesized `await` — `(await foo())` — fails to parse, but *only*
  inside an object-literal shorthand async method (`{ async run() { (await
  foo()); } }`); the identical expression works in a class method, an arrow
  function, or a top-level `async function`. Root-caused precisely:
  `p.inAsyncFunction` (the parser's "currently inside an async body"
  counter, correctly saved/incremented/restored at four other call sites)
  is never touched by the object-literal shorthand-method branch, so an
  `isAwaitParam` lookahead check elsewhere in the parser wrongly treats
  `await` as a candidate arrow-function parameter name. `auth/context.js`'s
  `fileExists` method (returned from an object literal) hits this on `const
  fs = (await importNodeModule(...))`.

Net: no group-B fake deleted yet. Each of the three actually tried hits a
real, currently-unfixed engine gap — exactly the outcome this phase expects
to find (see the phase's own note above: "Expect `pi-tui` and `pi-ai` to
surface the most new gaps"), just found one leaf earlier than expected.
Filed three issues today (`#128`, `#129`, `#130`).

**Fixed upstream, same day** — `357881e2` (#128), `f49b07e2` (#129),
`fa0d5451` (#130), plus more not asked for: `a88125c2` (#132, per-iteration
`let`/`const`/`class` bindings in loop bodies), `b1958dd8` (#133, stale
`frame.promiseObj` on generator/async-generator resume), `504e7e87` (#135,
constructor stack overflow now a catchable `RangeError`). Re-pulled and
re-verified against the original repros:

- `#129` — fully fixed. `pi-ai/dist/auth/context.js` now loads cleanly on
  its own, and its real `index.js` barrel loads cleanly too (`Object.keys`
  reports all 40 exports). The `proxy.js` "Class extends value undefined"
  from the earlier exploration pass was re-confirmed as the already-known
  false lead (noderati's own incomplete `pi-ai` fake, still active in that
  specific isolated test) — not a real blocker once the real package is
  used throughout.
- `#128` — fixed for the case that motivated it (arrow functions, methods,
  immediately-nested closures referencing a function-scoped class), but
  **not** for a hoisted function *declaration* referencing a sibling
  function-scoped class — same symptom, same `OpGetGlobal`-not-upvalue
  bytecode shape, isolated to a repro that differs from #128's own
  passing regression test by exactly one thing (`function inner() {}`
  instead of `const inner = () => {}` in the identical position). Filed as
  a distinct follow-up,
  [paserati#141](https://github.com/nooga/paserati/issues/141).
- `#130` — fixed for the case it targeted (an internal-invariant VM failure
  — corrupted bytecode, stack overflow — with no JS exception value to
  report; confirmed via the issue's own added tests, which now pass). An
  **ordinary JS `throw`** propagating through a *reentrant* `vm.Call()`
  (noderati's `require()` calling into another `require()`, i.e. two-plus
  nested `vm.Call()` invocations on the Go stack) still isn't fixed —
  worse than before, actually: caught, the exception value is now literally
  `null`, not even a wrong `Error`. The #130 fix's own commit message
  explicitly says it investigated and ruled out a reentrancy-based cause —
  for the trigger it tested (single `vm.Call()`, `runtimeError()` path).
  This is a different trigger (ordinary throw, nested `vm.Call()`) with
  real evidence of a *third* nested `vm.Call()` involved (`vm.go:11288`,
  converting a native function's returned Go `error` into a thrown `Error`
  object also calls `vm.Call(errCtor, ...)`). Filed as
  [paserati#142](https://github.com/nooga/paserati/issues/142).

**Practical effect on the three original blockers:**

- **`hosted-git-info`** — still blocked, but progress: `GitHost`'s own
  class-self-reference bug (#128's core case) is fixed. What's left is a
  `require('lru-cache')` deep inside `index.js`; `lru-cache`'s own bundled
  CJS file has a genuine parse error, previously invisible — masked by
  `#142`'s exception-swallowing (a require()-of-a-require() shape) until
  bisected past it. Not yet root-caused: the reported position (`2:2510`)
  is meaningless (the file is a 19KB single-line minified bundle plus a
  `//# sourceMappingURL=` comment on line 2 — the *real* diagnostics gap
  from Phase 1 item 5, still unfiled, striking again). Needs proper
  bisection (the file's too dense for line/col-based narrowing) or a
  reformatting pass before it's fileable.
- **`proper-lockfile`** — still blocked by the same `graceful-fs` →
  `require('constants')` chain from the previous exploration pass; #130's
  fix doesn't reach it either, since it's the same reentrant-ordinary-throw
  shape as `#142`, not the `runtimeError()` shape #130 actually fixed.
- **`pi-agent-core`** — `proxy.js`'s "Class extends value undefined" is
  confirmed a false lead (see `#129` note above) *once the real `pi-ai`
  package is used*; with the `pi-ai` fake still active (as in a
  `fake-off:pi-agent-core`-only scoreboard run), it still reproduces,
  correctly, since that's still testing against the incomplete fake.

Two new issues filed today (`#141`, `#142`) on top of the three from the
previous pass, three of five now fixed. No group-B fake deleted yet — every
one of these is a real engine or noderati-side gap, still being found faster
than fixed, which is the whole point of doing this exploration before
committing to deletions.

**`#141`/`#142` fixed and verified, 2026-08-31** — [paserati#143](https://github.com/nooga/paserati/issues/143)
(`357881e2`..`3b1fa14e` range via `5734ffda` #141, `3b1fa14e` #142), CI
green on all three platforms. Re-verified against the original repros:

- `#141` — fixed. The hoisted-function/sibling-class repro now prints
  `function` correctly.
- `#142` — fixed at the actual source (not the `vm.go:11288` site the
  issue guessed at — that was investigated and explicitly ruled out; the
  real cause was `executeUserFunctionSafe`/`executeUserFunctionWithNewTarget`
  leaving `vm.unwinding=true` on an *absorbed* exception handoff, poisoning
  the next unrelated `vm.run()` anywhere). The reentrant-`require()` repro
  now surfaces the real `Error` (`instanceof Error`, correct `name`) instead
  of `null`.

**Follow-on noderati-side fix, same day:** fixing `#142` upstream exposed a
real bug in noderati's *own* `internal/host/cjs.go` — `execFile`'s
`vmInst.Call` failure got string-formatted once (`formatCallError`) into an
`errors.RuntimeError`, then `require()` string-formatted *that* again into
a plain `fmt.Errorf`, so even with the real exception now surviving
paserati's side, a nested `require()`'s thrown message came out as a
double-wrapped, unreadable `Runtime Error (...): VM exception: {...full
Inspect() dump...}` instead of the plain original message. Added
`moduleThrow` (`cjs.go`): implements both `errors.PaseratiError` (so
`execFile` can still return it through its normal channel, keeping the
top-level `RunCJS` entry point's error display working) and
`vm.ExceptionError` (so `require()`'s own Go-error return hands the *raw*
exception value straight back to paserati's native-function-error handling
instead of forcing it to construct a new wrapper `Error` from an
already-stringified message). Verified: `require("./b.js")` where `b.js`
throws `new Error("boom from b.js")` now reports exactly `boom from b.js`
at any nesting depth (tested to 3 levels), caught or uncaught. `go test
./...` clean.

**Practical effect, re-verified with both fixes in noderati:**

- `proper-lockfile` — no longer masked. The real underlying cause finally
  surfaces cleanly: `graceful-fs`'s `polyfills.js` does `require('constants')`,
  a real Node builtin noderati doesn't implement yet (ledger group A/A-minus
  gap, not a paserati issue — still not built).
- `hosted-git-info` — no longer masked either, revealing a **different** real
  gap than expected: `require('lru-cache')` fails with a parse error whose
  reported position is meaningless (the file is a single 19KB minified line
  plus a `//# sourceMappingURL=` comment — the still-unfiled Phase 1 item 5
  diagnostics gap). Not yet bisected (needs reformatting or careful content
  bisection, not line/col narrowing) or filed.
- **New finding, not previously known: `fake-off:minimatch` was a false
  "clean" signal.** The scoreboard's three invocations
  (`--version`/`--help`/`-p`) never actually call `minimatch()`, so removing
  the fake showed no diff — but the *real* `minimatch` package throws
  immediately when actually exercised
  (`ReferenceError: Minimatch is not defined`), and would have shipped
  broken if deleted on the scoreboard's word alone. Bisected to a **third**
  instance of the #128/#141 family: a closure defined *before* a
  function-scoped class in source order (not hoisted — an ordinary
  `const f = () => ...`), only *called* after the class is declared (no
  real TDZ violation, completely ordinary JS — this is exactly
  `minimatch`'s own `dist/commonjs/index.js` shape: `const minimatch = (p,
  pattern, options) => { ...; return new Minimatch(...).match(p); }`
  defined near the top of the file, `class Minimatch` declared later).
  Neither #128's nor #141's fix covers this — both pre-register a class's
  spill slot at a specific compile-time trigger point (the class's own
  body, or a block's hoisted functions) that a plain, non-hoisted earlier
  statement doesn't hit. Filed as
  [paserati#144](https://github.com/nooga/paserati/issues/144). **Lesson
  for the scoreboard's own methodology:** "no diff across the three
  invocations" only proves *those three invocations* don't exercise the
  removed fake's real replacement — not that the replacement actually
  works. Don't delete a group-B fake on that signal alone; exercise the
  real package's actual functionality directly first, the way this catch
  required.

Five issues filed across this investigation (`#128`, `#129`, `#130`,
`#141`, `#142`) plus one more (`#144`) found verifying the fixes; four of
six now fixed. Still no group-B fake deleted — `minimatch` came the closest
and turned out to be actively unsafe to delete right now.

**`#141`/`#142` fixed and verified, 2026-08-31** —
[paserati#143](https://github.com/nooga/paserati/pull/143), CI green on all
three platforms (still open, not merged at verification time). Re-verified
against the original repros — both fixed, no regressions on `#128`'s own
repro either. `go test ./...` clean.

**`#144` fixed in the same PR, verified same day.** The real `minimatch`
package now works when actually exercised
(`minimatch("foo/bar.js", "foo/**")` correctly returns `true`/`false`) —
confirmed by direct functional test, not just the scoreboard's signal
(learning last time's lesson).

**Follow-on noderati-side fix, same day: `require()`'s own error message
was still double/triple-wrapped even with `#142` fixed upstream.**
`execFile`'s `vmInst.Call` failure got string-formatted once
(`formatCallError`) into an `errors.RuntimeError`, then `require()`
string-formatted *that* again into a plain `fmt.Errorf` — so a nested
`require()`'s real, now-correctly-surviving exception still came out as an
unreadable `Runtime Error (...): VM exception: {...full Inspect() dump...}`
instead of the plain original message. Added `moduleThrow`
(`internal/host/cjs.go`): implements both `errors.PaseratiError` (so
`execFile`'s normal return channel and the top-level `RunCJS` entry point's
error display keep working) and `vm.ExceptionError` (so `require()`'s own
Go-error return hands the *raw* exception value straight back to
paserati's native-function-error handling instead of forcing it to
construct a new wrapper `Error` from an already-stringified message).
Verified: a throw inside a required file now reports its exact original
message at any nesting depth (tested to 3 levels), caught or uncaught.

**Practical effect, re-verified with `#141`/`#142`/`#144` and the
`moduleThrow` fix all in place:**

- `proper-lockfile` — **loads and its main functions run** (`lockSync`,
  `checkSync`, `unlockSync` all execute) once noderati's own `constants`
  builtin (implemented same day, described below) closed its last blocker.
  But real functional exercise (not just "does it load") found a **new,
  larger, systemic noderati gap**: `fs`
  errors don't set `.code` (`'ENOENT'`, etc.) the way real Node's `fs`
  errors always do. `proper-lockfile`'s own `checkSync` relies on catching
  `err.code === 'ENOENT'` to mean "not locked, return `false`"; with no
  `.code`, it can't tell that error apart from a real failure and rethrows
  it instead. This is bigger than `proper-lockfile` — every real package
  that checks `fs` error codes (an extremely common Node idiom) hits the
  same wall. Not filed or fixed here; flagged for its own pass (ledger
  group A — real builtin, needs hardening, not a paserati issue).
  **Still not deleting this fake** — it's demonstrably not equivalent to
  the real, working package yet.
- `hosted-git-info` — unchanged, still blocked by `lru-cache`'s
  not-yet-bisected parse error.

**Implemented the `constants` builtin, 2026-08-31.** `internal/host/constants.go`:
registers the legacy, standalone `constants` module (real Node deprecated
it in favor of `fs.constants`/`os.constants`, but real packages —
`graceful-fs` among them — still `require()` it directly). Shares one list
(`fsConstantEntries()`) with `fs.constants` so the two can't drift: the
`F_OK`/`X_OK`/`W_OK`/`R_OK` access-mode constants (already existed) plus
the common `O_*` open flags via Go's own `syscall` package (correctly
platform-dispatched by Go itself). `O_SYMLINK` is gated to
`runtime.GOOS == "darwin"`, matching real Node — it only exists on
BSD-family platforms there too, and `graceful-fs` already feature-detects
it via `constants.hasOwnProperty('O_SYMLINK')`, so omitting it elsewhere is
correct, not a gap. Registered in `nativeRequireNames` (`cjs.go`) so
`require('constants')` resolves to it. Verified: `graceful-fs` now loads
cleanly end to end (previously blocked on this one missing builtin, itself
previously hidden behind `#130`/`#142`'s exception-swallowing).

**Also added: `fs.Stats.mtime` as a real `Date`, not just `.mtimeMs`.**
`fsStats` (`fs.go`) only exposed `.mtimeMs` (a number) — real Node's
`fs.Stats` has both, and real code (`proper-lockfile`'s own
`mtime-precision.js`) calls `.mtime.getTime()` directly, which surfaced
this while chasing `proper-lockfile`'s functional exercise above. Added
`newFsStats(vmInst, info)`, shared by `fs.statSync` and
`fs/promises.stat`, constructing a real `Date` via `vm.Construct` (a Go
method/field returning `vm.Value` passes straight through paserati's
struct-marshaling reflection unwrapped — confirmed by reading
`native_module.go`'s `reflectValueToVM`, not just assumed). Verified:
`stats.mtime instanceof Date` is `true`, `.getTime()` returns the same
value as `.mtimeMs`.

**First group-B fake actually deleted, 2026-08-31: `minimatch`.**
`internal/host/glob.go`'s `minimatchShim` and its `declareMinimatch()`
registration removed; `node_modules` resolution now always loads the real
package. Verified the same way the false-positive scare above should have
been verified the first time: not just "no diff across the scoreboard's
three invocations" (though it is clean there too), but an actual functional
call — `minimatch("foo/bar.js", "foo/**")` / `minimatch("foo/bar.js",
"baz/**")` correctly return `true`/`false` — and a full re-run of
`--version`/`--help`/`-p "hello"` against the real, unmodified `pi` CLI
matching baseline exactly. `glob`'s fake (a separate, sibling shim in the
same file, still genuinely broken — `fake-off:glob` hits a real, unfixed
parse error) is untouched.

**Implemented real Node-shaped `fs` errors, 2026-08-31.** New
`internal/host/fs_errors.go`: `wrapFsErr(vmInst, syscallName, path, err)`
classifies a Go stdlib `fs`/`os` error (via `errors.As` into
`syscall.Errno`, mapped through a small, portable `errnoToCode` table —
same "Go's own platform-dispatched constants" pattern as `constants.go`'s
`O_*` flags) and constructs a real JS `Error` shaped like Node's
`SystemError`: `.code` (`'ENOENT'` etc.), `.errno`, `.syscall`, `.path`,
plus a message formatted to match
(`"ENOENT: no such file or directory, stat '/path'"`). Wired through
every `fs`/`fs/promises` function that can fail (`fs.go`,
`fspromises.go`) — previously every one of them just returned the bare Go
error, which the VM stringifies into a generic `Error` with no `.code` at
all. Real Node code overwhelmingly branches on `.code`
(`if (e.code === 'ENOENT')`), not `.message` — this was invisible to that
extremely common idiom before. Verified: `fs.statSync` on a missing path
throws with `.code === 'ENOENT'` and the exact real-Node message format;
`proper-lockfile`'s full **sync** cycle (`lockSync`/`checkSync`/
`unlockSync` on a real file) now runs correctly end to end, including the
`checkSync`-after-`unlock` case that used to throw instead of returning
`false`.

**Found while verifying: a real, separate paserati bug on the `fs/promises`
(async) side.** The identical fix, wired through `fs/promises`, does *not*
work — a rejected async `fs/promises` call's `.catch()` handler receives a
bare JS **string**, not the real `Error` object with `.code`/`.message`
(`typeof e === "string"`, `e.code === undefined`). Traced to
`pkg/driver/native_module.go`'s `wrapNativeAsAsync` (`ModuleBuilder.
AsyncFunction`'s wrapper): it always does
`vmInst.NewRejectedPromise(vm.NewString(err.Error()))`, discarding
whatever real exception value a Go error carries — unlike the synchronous
native-function path (`vm.go`'s main interpreter loop), which correctly
checks for `vm.ExceptionError` first. Filed as
[paserati#147](https://github.com/nooga/paserati/issues/147), with a
minimal repro reduced to the driver API directly (no `fs` involved).

**`proper-lockfile`'s own async API (`lock`/`unlock`/`check`, not the
`*Sync` variants) is separately, still broken** — `await
lockfile.lock(path)` throws `TypeError: undefined is not a function`, a
different failure from the `#147` promise-rejection issue above (this one
happens on the *success* path, before any error/rejection is even in
play). Not yet bisected. **Still not deleting this fake** — the sync API
genuinely works now, but the package as a whole doesn't yet, and the
scoreboard's own "clean" signal would have been a second false-positive if
trusted on its own, exactly like `minimatch` was — this time caught by
checking the *async* surface specifically before acting on it, per the
lesson written down after the first one.

**Filed the Phase 1 item 5 diagnostics gap, finally, 2026-08-31**
([paserati#148](https://github.com/nooga/paserati/issues/148)) — noted
since the very first session (`isBunBinary`'s fabricated `line 1, column
1`) but never actually filed until a clean, minimal, 2-file repro was in
hand (a `.mjs` importing a sibling `.mjs` with a real syntax error on line
4): the error *message* correctly reports the real inner position
(`Syntax Error at 4:11`), but the *displayed* context snippet and final
`at line X, column Y` are unrelated — the entry file's own line 1, always.
Root-caused precisely: `vm.runtimeError()` hardcodes `Column: 1` and never
attaches `Position.Source`, so `errors.DisplayErrors` falls back to
whichever source the embedder happened to pass it (typically the
top-level entry script), not the module that actually failed — even
though the real position was sitting right there in the original error
being wrapped. This is very likely the same mechanism behind
`hosted-git-info`'s nonsensical `lru-cache` position (`2:2510` in a file
whose real line 2 is a 37-character sourcemap comment) and several other
"`Syntax Error at N:M`" positions seen throughout Phase 3 that never quite
lined up with the file's real content.

**`#147`/`#148` fixed upstream and verified, 2026-09-01** —
[paserati#155](https://github.com/nooga/paserati/pull/155) (`037aea16`
#147, `3d23031d` #148), CI green on all three platforms, plus more not
asked for (`#115`, `#154`, and general `Number()`/`Date()`/`Object()`
object-conversion-protocol fixes). Re-verified against the original
repros — both fully fixed:

- `#147` — `fs/promises`' rejections now carry the real `Error` object
  (`.code`, `.message`, everything), not a bare string.
- `#148` — error locations are now genuinely accurate: the `bad.mjs`/
  `entry.mjs` repro now shows the real failing file, the real line 4
  content, the real caret position, and a correct `at
  /path/bad.mjs:4:11` footer. This immediately paid off — see below.

**`#148`'s fix directly enabled finding a new, real, and unusually clean
bug.** With accurate positions, `hosted-git-info` → `lru-cache`'s
previously-nonsensical `2:2510` resolved to the *actual* real position
(confirmed independently via plain `paserati` against the raw file,
bypassing noderati's CJS wrapper entirely: `1:2846`), landing squarely on
`if(this.#S=D??N.defaultPerf,e!==0&&!T(e))throw new TypeError(...)`.
Bisected to a minimal, 2-line, dependency-free repro —
`let x = 1; if (x, x > 0) console.log("yes");` — and filed as
[paserati#157](https://github.com/nooga/paserati/issues/157): **a comma
expression inside an `if(...)` condition fails to parse, full stop** — no
assignment, private fields, or `??` required. `while`, `do...while`, and
`switch` all handle the identical shape correctly; only `if` doesn't.
Root-caused to a single line: `parseIfStatement`
(`pkg/parser/parser.go:2114`) parses its condition at `COMMA` precedence
(stop before consuming a comma — correct for a `var`/`let` declarator or a
default parameter value, wrong for a parenthesized condition), while
`parseWhileStatement`/`parseDoWhileStatement`/`parseSwitchStatement` all
correctly use `LOWEST`. Very likely a one-token fix. This is a general,
not `lru-cache`-specific, parser gap — `if (a = b, c)` (assign as a side
effect, test as the real condition) is an established, if uncommon, real
JS idiom.

**`proper-lockfile`'s async API, checked again, is still separately
broken** — unrelated to `#147`/`#148` or anything above: `await
lockfile.lock(path)` still throws `TypeError: undefined is not a
function`, on the success path (not a rejection at all). Not yet
bisected. Its fake stays.

**`#157` fixed upstream and verified, 2026-09-01** —
[paserati#158](https://github.com/nooga/paserati/pull/158) (`8b423b14`
#157, plus `e0fd6bd8` #156 not asked for: "an uncaught exception from a
native call no longer panics the VM"), CI green. `if (x, x > 0)` now runs
correctly.

**Also fixed on noderati's own side, 2026-09-01: the CJS wrapper's own
line-number corruption.** `execFile`'s function-wrapper
(`"(function (exports, require, module, __filename, __dirname) {\n" +
source + "\n})"`) had a leading `\n` before `source` — every real file's
own line 1 became wrapped line 2, every line 2 became line 3, and so on,
for every `require()`d CJS file. Paserati was reporting positions
faithfully the whole time — for the text we handed it, which wasn't the
real file. Dropped the leading newline (`cjs.go`): source's own line 1
now stays wrapped line 1, with zero line-number impact on every
line after the first (the vast majority of real, non-minified files).
Verified with a 4-line CJS fixture with an error on line 4 — reports
`4:11` exactly, matching the real file. (A file whose real content is
entirely on one line — e.g. a minified bundle, exactly the shape that
made `lru-cache` hard to read before — still carries a small, fixed
column offset from the ~61-character wrapper-prefix text sharing that one
line; full column correction for that specific case is a smaller
follow-up, not done here.) `patchCJSSource`'s own regex-based text
rewrites (`satisfies`, class-self-`instanceof` fixups) are a separate,
much narrower source of position drift — only the small set of files they
target, not fixed here.

**Immediate, dramatic payoff from `#148` + the wrapper fix together: the
scoreboard's own output became genuinely readable.** Nearly every
`fake-off:X` row across a fresh run now shows a real file path, real line
content, and a real caret — `diff`'s failure now reads
`import * as Diff from "diff";` at its own real position in
`dist/modes/interactive/components/diff...`; `sdk-reexports`' failure
shows the actual `export { AgentSessionRuntime, ...` line it's choking
on; `typebox`'s shows a real caret in
`typebox/build/type/engine/mapped/instantiate.mjs`. This changes the
shape of the rest of Phase 3 — positions can mostly just be read directly
off the scoreboard's tail now, instead of needing a `paserati -bytecode`
side investigation to recover them the way `lru-cache` needed twice this
session.

**That immediate payoff directly found two more real, general bugs in
`lru-cache`**, filed as [paserati#159](https://github.com/nooga/paserati/issues/159)
and [paserati#160](https://github.com/nooga/paserati/issues/160) — both
about a multi-declarator `let`/`const` statement mixing a destructuring
declarator with plain-identifier ones:

- **`#159`** (parser): `let r = 1, {a} = {a: 1};` — a destructuring
  pattern as a **non-first** declarator — fails to parse outright
  (`expected identifier or destructuring pattern after ','`).
- **`#160`** (compiler/codegen): the reverse order, `let {a} = {a: 1}, b
  = 20;`, parses but silently produces the wrong value (`a` comes out
  `undefined`) or throws `ReferenceError` if the trailing declarator has
  no initializer. Root-caused via `-bytecode`: the destructuring source's
  register gets clobbered by the second declarator's own initializer
  value before the destructuring extraction runs.

**`#159` is confirmed general, not `lru-cache`-specific** — the fresh
scoreboard run's now-accurate `fake-off:jiti` row hits the exact same
error text (`expected identifier or destructuring pattern after ','`) in
a completely different package (`jiti/dist/jiti.cjs`). One fix likely
unblocks (or moves past a blocker in) more than one Phase 3 target at
once.

**2026-09-01, second round: local paserati checkout pulled a large batch
of fixes** (`fix-159-and-160` branch — `#159`/`#160` themselves plus
seven more: TDZ markers on every `let`/`const` declarator, `var`/pattern
hoisting through loop heads and `for` heads, a rest element nested in an
object pattern, function-vs-module var scoping). Full re-verification
(`go build`/`vet`/`test`, all three `pi` invocations, full scoreboard)
confirmed clean, no regressions. `#159`/`#160` themselves verified fixed
directly (the `lru-cache` repros from the previous round now parse and
run correctly). Investigating further with the scoreboard's now-readable
output found four more bugs, three in paserati and one — a real,
previously-invisible noderati bug — in our own module resolution:

- **[paserati#162](https://github.com/nooga/paserati/issues/162)**
  (driver): a native function declared through `ModuleBuilder.Function`
  with a parameter typed `vm.Value` never receives the real argument —
  it's silently replaced with a zeroed struct, no error anywhere. Mirror
  image of an existing, working special-case on the *return* side
  (`reflectValueToVM` already passes `vm.Value` through untouched;
  `vmValueToReflectValue` has no matching case on the *argument* side).
  Found while trying to add classic Node callback-style `fs.stat(path,
  cb)` functions (as opposed to the `*Sync`/Promise variants noderati
  already has) — any attempt to accept the callback as a `vm.Value`
  parameter hit this. Not blocking (worked around by building the raw
  `vm.NewNativeFunction` closure directly, the same way
  `child_process.go` already does for `__noderatiSpawn`), but a real gap
  in the declarative path specifically.

- **[paserati#163](https://github.com/nooga/paserati/issues/163)**
  (compiler): `import { X } from 'mod'; export { X };` — re-exporting a
  name that was itself introduced by an `import` declaration, rather
  than declared locally — fails to compile: `exported name 'X' not found
  in current scope`, even though `X` genuinely is in scope. Reproduces
  for both named and default imports, regardless of how the *consumer*
  imports the re-exporting module; the one equivalent form that works is
  the direct re-export clause (`export { default as X } from 'mod'`,
  which introduces no local binding at all). This is the real, general
  version of what looked at first like a `diff`-specific problem
  (`diff@8.0.4`'s own ESM entry point, `libesm/index.js`, is a barrel
  file: `import Diff from './diff/base.js'; ...; export { Diff, ... };`)
  — any package whose ESM entry re-exports names collected from several
  internal submodules will hit this, which is an extremely common
  package-authoring pattern. High-leverage: the scoreboard's
  `patch-off:sdk-reexports` row (still needed, per Phase 1 close-out)
  now shows this exact error shape too (`exported name
  'withFileMutationQueue' not found in current scope`), so this one fix
  plausibly clears two blockers at once.

- **[paserati#164](https://github.com/nooga/paserati/issues/164)**
  (parser): `as` and `satisfies` are treated as fully reserved words
  instead of TypeScript's actual contextual keywords — reserved only in
  the specific position that introduces a type assertion, ordinary
  identifiers everywhere else. `const as = 1; console.log(as);` fails to
  parse (`of`/`from`/`type`/`async`/`get`/`set`/`let`/`namespace`/
  `declare`/`module`/`readonly` are all handled correctly by contrast).
  Found bisecting `glob@11`'s minified ESM bundle
  (`glob/dist/esm/index.min.js`): the minifier assigned the short name
  `as` to an unrelated regex variable, and referencing it later
  (`n.replace(as,fe)`) broke — which, because it's deep inside one huge
  minified line, cascaded into a confusing "Expression expected" dozens
  of characters away from the real cause. `noderati`'s own
  `patchCJSSource` previously worked around the `satisfies` half of this
  with a source-text regex rewrite for a different package — same
  underlying bug, now also hitting `as` used as an identifier.

- **noderati resolver bug, found and fixed directly (not a paserati
  issue): conditional `exports` map resolution ignored whether the
  caller was CJS `require()` or ESM `import`.** `resolveExportTarget`
  (`internal/host/nodemodules.go`) tried candidate conditions in one
  fixed order, `["node", "import", "require", "default"]`, for *every*
  resolution — meaning a plain `require('lru-cache')` (a CJS call) could
  pick the `"import"`-conditioned target purely because `"import"` came
  before `"require"` in that fixed list, handing a `require()` call an
  ESM file. Confirmed exactly this was happening:
  `require('lru-cache')` from `hosted-git-info` (its real, direct
  dependency) silently returned `{}` — no error, just nothing —  because
  the picked file was `lru-cache`'s ESM bundle, containing top-level
  `import`/`export` statements that our CJS loader doesn't error on but
  also doesn't populate `module.exports` from. Fixed by threading an
  `exportsCondition` (require vs. import) from each of the two real call
  sites — `cjs.go`'s `require()` and the ESM `NodeModulesResolver` — down
  through `resolvePackageEntry`/`entryFromExports`/`resolveExportTarget`,
  so each context only ever considers its own matching condition
  (`["node", "require", "default"]` vs. `["node", "import",
  "default"]`), never the other's. `require('lru-cache')` now correctly
  resolves to the `"require"`-conditioned build and, instead of a silent
  empty object, throws an honest `Cannot find module
  'node:diagnostics_channel'` — the actual remaining gap (see next item).
  Verified via `go test ./...` (covers this exact code path) plus the
  full scoreboard and all three `pi` invocations, both clean.

**Added a real Node builtin: `node:diagnostics_channel`**
(`internal/host/diagnostics_channel.go`), the gap the fix above exposed
— `lru-cache`'s node-specific build genuinely imports it for optional
metrics/tracing. Implemented as a pure JS shim (`channel`/
`hasSubscribers`/`subscribe`/`unsubscribe`/`tracingChannel`, with
`Channel`/`TracingChannel` supporting `publish`/`traceSync`/
`tracePromise`/`traceCallback` matching real Node's semantics) rather
than a Go native module, specifically to sidestep `#162` above —
`tracingChannel`'s `traceSync`/`tracePromise`/`traceCallback` all take a
JS callback as their first argument, and none of this needs real Go-side
capability.

**That surfaced a second, separate, pre-existing noderati bug while
verifying the new shim: `require()` of *any* JS-shim-backed built-in
returns an empty object, unrelated to anything above.** Confirmed via
`require('child_process')` — untouched by this round's changes, and
broken before it too — coming back as `{}` (`typeof cp.spawn ===
"undefined"`), while `import { spawn } from 'child_process'` works fine.
Root-caused: noderati's `requireNative` (`cjs.go`) calls
`p.LoadModule(spec, ".")` and reads `ModuleRecord.GetExportValues()`,
but `LoadModule` alone only resolves/parses/compiles a text-source
module — it doesn't execute it, and only execution populates
`ExportValues`. Tried forcing execution via `p.RunModuleWithValue`, which
does run the module correctly (confirmed via debug output: the run
completes with no errors and the correct final value), but paserati only
collects `ExportValues` when its single shared, stateful
`p.compiler.IsModuleMode()` happens to be true *at that moment* — which
a `require()` reached mid-execution of the entry script has no way to
guarantee. Filed as
[paserati#165](https://github.com/nooga/paserati/issues/165). Worked
around on noderati's side without waiting for the upstream fix: fall
back to `RunModuleWithValue`'s own final return value when
`ExportValues` comes back empty. This works reliably for noderati's own
shims specifically because every one of them is authored to end in
`export default {...}` as its last top-level statement — exactly what a
module's "final value" evaluates to — so it is *not* a general fix for
an arbitrary third-party CJS-required ESM file (documented in-line in
`cjs.go` as such). Verified: `require('child_process')` and
`require('node:diagnostics_channel')` both now return fully populated,
working objects.

**Net effect on `hosted-git-info`, first pass: significant forward
progress, not yet fully unblocked.** With its own fake off,
`hosted-git-info` no longer crashes at all (previously `undefined is not
a constructor` on `new LRUCache(...)` at its own `lib/index.js:8`) —
but `HGI.fromUrl(...)` still returns `undefined` where it should return
a parsed git-host object; not yet bisected. Its fake stays for now. Also
re-confirmed the `minimatch`-style false-positive lesson applies here
too: the scoreboard shows `fake-off:hosted-git-info` as clean on all
three invocations (none of `--version`/`--help`/`-p hello` ever call
`fromUrl`), which would be exactly the wrong signal to trust — direct
testing is what actually caught the remaining bug.

**2026-09-01, third round: local paserati checkout pulled fixes for all
four bugs filed above.** Full re-verification (build/vet/test, all three
`pi` invocations, full scoreboard) clean. Directly re-verified each
repro:

- **`#162` (vm.Value passthrough) — fixed and verified.** The exact
  probe from the filed issue now returns `true` instead of `false`.
- **`#163` (re-export of an import) — fixed and verified.** The exact
  repro now runs and prints the re-exported value correctly.
- **`#164` (`as`/`satisfies` as identifiers) — `as` fixed, `satisfies`
  narrowed and still open.** `const as = 1; console.log(as)` now prints
  `1`. `satisfies` is still broken, but the failure mode narrowed
  precisely: it's `const` specifically (`let satisfies = 1` and `var
  satisfies = 5` both work correctly now), and `satisfies` specifically
  (`const as/of/type/async = 1` all work). Commented on the issue with
  this narrowed repro rather than filing a new one.
- **`#165` (`RunModuleWithValue` losing exports on a reentrant call) —
  fixed and verified via debug instrumentation:** `rec.GetExportValues()`
  went from `0` before the fix's target commit to `3` after, for the
  exact same `require('child_process')` call that motivated the issue.
  Simplified `cjs.go`'s `requireNative` back down accordingly — deleted
  the same-session `runFallback` workaround (falling back to
  `RunModuleWithValue`'s own return value) now that the real fix makes
  it dead code; `require('child_process')` and
  `require('node:diagnostics_channel')` both re-verified still working
  through the simplified path.

**With `#163` fixed, the scoreboard's `patch-off:sdk-reexports` row went
clean — verified directly (not just trusted), and `esmpatch.go` deleted
entirely.** `sdk-reexports` was the exact same "re-export of an import"
shape #163 fixes: `pi-coding-agent`'s own `dist/index.js` re-exports
`withFileMutationQueue` and several tool factories that its `sdk.js` had
itself imported. With the patch off, all three `pi` invocations now
match baseline exactly (not just the scoreboard's diff count — verified
by running `--version`/`--help`/`-p hello` directly with
`NODERATI_DISABLE_PATCHES=sdk-reexports` and comparing output). Deleted
`esmpatch.go` outright (it held zero patches at that point) rather than
leaving an empty pass-through mechanism in place, updated its two call
sites (`nodemodules.go`, `osresolver.go`) to drop the now-nonexistent
`patchModuleSource` wrapper, and simplified `cmd/scoreboard`'s
`patchNames`-driven config generation to match (nothing left to toggle
patch-wise). **This completes Phase 1's close-out in full: zero
`esmpatch.go` patches remain, down from twelve.**

**Added a real `URL` class to the `url` module and `require('url')`
(`internal/host/url.go`)**, closing a gap discovered while bisecting
`hosted-git-info` further: `require('url').URL` didn't exist at all
(`undefined is not a constructor`), so `parse-url.js`'s `new
url.URL(...)` — the very first thing `fromUrl()` does — always failed
silently. Read-only by design (own data properties, all fields computed
once at construction, no live recomputation on mutation, no
`URLSearchParams` — nothing needs it yet): `href`, `origin`, `protocol`,
`username`, `password`, `host`, `hostname`, `port`, `pathname`,
`search`, `hash`, plus `toString()`/`toJSON()`. Built via
`driver.ModuleBuilder.Class` (backed by Go's `net/url.Parse`), which
worked cleanly for field parity but surfaced one more paserati gap along
the way:

- **[paserati#167](https://github.com/nooga/paserati/issues/167)**
  (driver): `ModuleBuilder.Class`'s constructor wrapper only ever reads
  `results[0]` from the reflected Go constructor call — a `(value,
  error)`-returning constructor's error is silently discarded, so `new
  X(...)` for invalid input evaluates to `undefined` instead of
  throwing. Mirrors `#162`'s "the declarative path has real reflection
  gaps `ModuleBuilder.Function` doesn't have" pattern. Concretely: `new
  URL("git@github.com:foo/bar.git")` (an scp-style URL, not a valid
  absolute URL) should throw but instead silently returns `undefined`.
  **Not actually blocking for `hosted-git-info`'s specific call site**:
  `parse-url.js`'s `safeUrl` does `try { return new url.URL(u) } catch
  {}`, and a non-throwing `undefined` return is externally identical to
  a caught throw for that exact pattern — verified this holds by testing
  the scp-style fallback path directly, which does correctly resolve.
  But it's a real, general gap for any other caller that actually
  distinguishes "threw" from "returned undefined".

**Net effect on `hosted-git-info`, second pass: `fromUrl()` itself is
now genuinely fixed.** `HGI.fromUrl("git+https://github.com/foo/bar.git")`
returns a real, correct object (`type: "github", user: "foo", project:
"bar"`, etc.) instead of `undefined` — verified for both a normal URL
and the scp-style (`git@github.com:foo/bar.git`) fallback path, both
resolving correctly. **Not fully done yet**: template-producing methods
on the returned object (`.shortcut()`, `.https()`, and likely the rest
of the `#fill`-based family — `.git()`, `.ssh()`, `.browse()`, etc.) all
return `null`. Traced one level further: `GitHost`'s `#fill(template,
opts)` explicitly returns `null` whenever `typeof template !== "function"`
— meaning `this.shortcuttemplate` etc., which `Object.assign(this,
GitHost.#gitHosts[type], {...})` should have copied from the
statically-registered host definition (`GitHost.addHost`, using JS
private static class fields), aren't functions by the time `#fill` sees
them. Not yet bisected further — a new, separate lead for next time, not
one of the four issues verified this round.

### Phase 4 — resolver honesty (ledger group D)
- Implement real Node `node_modules` walk-up resolution (parent-directory
  search from the importing file, not from argv[1] only) and delete
  `findPiCodingAgentNodeModulesRoots()`'s hardcoded homebrew paths entirely —
  a program should find pi's dependencies because it's *inside* pi's
  install tree, the same way Node would, not because noderati special-cased
  pi's install path.
- Change `NodeMissingResolver` to fail resolution immediately with a clear,
  structured error (module name + who imported it) instead of returning a
  module whose body throws at call time; add an opt-in mode that collects
  every miss into a report instead of throwing, for gap-survey runs.

### Phase 5 — fill real gaps found by Phase 3/4
Whatever's left after deleting fakes and fixing resolution: likely real
`stream` (Readable/Writable/Transform beyond the current hand-rolled
EventEmitter base), a real `string_decoder`, `net`/`tls` (or an honest "not
supported" boundary if that's out of scope for this push), and whatever
parser/compiler gaps Phase 1's discipline surfaced. Triage each the same way:
real builtin gap → implement; engine gap → file upstream, patch only as a
last resort with a tracker link.

### Phase 6 — second target: real `tsc`
The uncommitted `examples/tsconfig.*.json` / `hello-tsc.ts` / `lib.stub.d.ts`
artifacts are already reaching for this. `tsc` is CJS-heavy, pure computation,
no TUI/network — a deliberately disjoint surface from pi, so fixes here catch
gaps that a single-target effort would over-fit around. Not before Phase 4;
there's a live pi failure to finish first.

## Definition of done for this push

`pi --help`, `pi --version`, and a scripted single-turn `pi -p "..."` print-mode
run (mocked/no network, or with a real key if the user provides one) succeed
against the **real, unmodified** `@earendil-works/pi-coding-agent@0.80.2` npm
install, with `internal/host` containing zero package-specific shims or
per-filename source rewrites — only real Node builtin modules and real
resolution algorithms. Every remaining gap has either a real implementation or
a linked paserati issue, never a silent fake.
