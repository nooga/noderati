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
`String(c)`, no actual UTF-8 multibyte/incremental decoding). `glob` was
listed here too (real npm surface, but `globSync` always returned `[]` —
silently *wrong*, worse than missing) until it turned out, like
`minimatch`, to need no from-scratch implementation at all: resolved the
group-B way instead, 2026-09-02 — deleted outright once
[paserati#180](https://github.com/nooga/paserati/issues/180) fixed the
real package's own blocker, real `node_modules` resolution now loads
the genuine package (see Phase 3 below). `minimatch`'s equivalent fake
went the same way, 2026-08-31.
`stream.go` also hand-rolls its own `EventEmitter` instead of reusing
`events.go`'s — pick
one.

**B. Third-party npm package fakes — delete the shim, load the real
package.** These aren't Node surface at all; they're interceptions of specific
libraries pi-coding-agent depends on, hardcoded as JS strings in Go and
registered ahead of the real files on disk: `@earendil-works/pi-tui` (every
export is a no-op — the entire TUI is fake), `@earendil-works/pi-ai` (a
from-scratch reimplementation of the real LLM client, including its own model
catalog and provider fetch calls — its bare-entry fake only exports
`modelsAreEqual`, everything else lives in the separate `/compat` fake;
real `pi-agent-core` imports `EventStream`/`parseStreamingJson` from the
*bare* specifier, coupling the two fakes — neither de-fakes cleanly
without the other, confirmed 2026-09-02), `@earendil-works/pi-agent-core`
(a from-scratch reimplementation of the actual agent loop), `jiti/static`.
(`minimatch` was here too — deleted 2026-08-31; `hosted-git-info` deleted
2026-09-01; `proper-lockfile`, `glob`, `typebox`'s own top-level entry,
`diff`, and `typebox/value` all deleted 2026-09-02 —
`typebox/value`/`typebox/compile` split off their own independent toggle
that same day, as separate real npm entry points, before `typebox/value`
itself cleared, then `typebox/compile` too once its own two-layer block
(`#190` then `#192`) merged upstream; see Phase 3 below for all of these.)
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
  Tracked upstream as [paserati#172](https://github.com/nooga/paserati/issues/172)
  (filed 2026-09-01, once `glob`'s `minimatch` dependency turned out to hit
  the identical gap unconditionally — see Phase 3 below — making this a
  wholesale blocker for a whole package, not just one `highlight.js`
  language plugin).
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
- **`#164` (`as`/`satisfies` as identifiers) — `as` fixed and verified;
  the `const satisfies` finding was our own false positive, corrected.**
  `const as = 1; console.log(as)` now prints `1`, confirmed against a
  plain paserati build. The `const satisfies` repro commented on the
  issue in the second round was tested through noderati, not plain
  paserati directly — the paserati maintainer couldn't reproduce it and
  asked for the exact command. Rebuilding plain paserati and re-testing
  found the maintainer was right: `const`/`let`/`var satisfies` all work
  correctly on plain paserati. The apparent bug was noderati's own
  `patchCJSSatisfiesKeyword` (`cjs.go`) — a blanket regex rewrite from
  before this fix existed, which unconditionally renamed any `const
  satisfies = ...` declaration to `const satisfiesFn = ...` without
  renaming later references to it, producing exactly the
  `ReferenceError` that looked like a paserati bug. Posted a correction
  on the issue, deleted the now-obsolete (and, it turns out,
  actively-harmful) patch outright — confirmed safe via the full
  scoreboard and all three `pi` invocations, unchanged. One genuine gap
  remains, found while re-verifying: `satisfies` as a function/arrow
  **parameter name** (not a declarator) still fails to parse on plain
  paserati (`function f(satisfies) {}` and `(satisfies) => satisfies`
  both fail; `as` in the identical position works fine on both) — left
  as a comment on the open issue rather than a new one, at the
  maintainer's discretion to fold in or split out. This is the lesson
  this project's own "why do you need plain paserati" thread from
  earlier in this session exists to prevent — testing through the host
  instead of the engine directly produced a real false positive here.
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
them.

**2026-09-01, fourth round: bisected the `#fill`/template gap to its
real root cause — a severe, general paserati bug, not anything specific
to private static class fields.** Turned out to have nothing to do with
`GitHost`'s class structure at all. `hosts.js` builds each host object
via `hosts[name] = Object.assign({}, defaults, host)` (template
functions copied in fine, confirmed directly), then `GitHost`'s
constructor does a *second* `Object.assign(this, GitHost.#gitHosts[type],
{...})` off that already-once-assigned object — and that second copy
silently drops every property the first one had set, template functions
included. Minimized in three steps to a one-line repro with nothing
class-related left at all:

```js
const merged = Object.assign({}, { a: 1 });
console.log(merged.a);              // 1 -- direct access works
console.log(Object.keys(merged));   // [] -- should be ["a"]
console.log(Object.getOwnPropertyDescriptor(merged, "a").enumerable); // false -- should be true
```

Root cause pinned exactly in `pkg/builtins/object_init.go`:
`objectAssignWithVM`'s copy loop calls `targetPlain.SetOwnNonEnumerable`
for a `PlainObject` target instead of `SetOwn` (the sibling `DictObject`
branch two lines down already uses the correct `SetOwn`) — every
property `Object.assign` copies onto a plain object ends up
`enumerable: false`, invisible to `Object.keys`/`for...in`/
`JSON.stringify`/spread/a second `Object.assign` reading it as a source,
while still directly readable (which is exactly what a wrong
`enumerable` flag and nothing else produces, and how this got isolated
so precisely). Filed as
[paserati#168](https://github.com/nooga/paserati/issues/168), with the
one-line fix identified (`SetOwn` instead of `SetOwnNonEnumerable`,
matching the already-correct sibling branch).

**This is almost certainly bigger than `hosted-git-info`.** `Object.assign`
called twice in a row, or read via `Object.keys`/spread/`JSON.stringify`
after one call, is an extremely common pattern — merged config objects,
`{...Object.assign({}, defaults, overrides)}`, serializing a
programmatically-built object. Nothing else in this session's Phase 3
sweep has been re-tested against it specifically; worth keeping in mind
as a candidate explanation if some other still-blocked package's failure
looks like "a property that should be there just isn't," the same shape
`#fill` had before this was traced down.

**2026-09-01, fifth round: `#167`/`#168` fixed and verified, plus a
`satisfies`-as-parameter bonus fix; `hosted-git-info`'s fake deleted for
real.** Local paserati checkout pulled fixes for both. Full
re-verification (build/vet/test, all three `pi` invocations, full
scoreboard) clean throughout. Directly re-verified each against a
freshly-built plain paserati binary (learning the lesson from this same
round's `#164` correction — see above):

- **`#167` (constructor error swallowed) — fixed and verified.** The
  exact repro from the filed issue now throws (`threw: true`) instead of
  silently returning `undefined`.
- **`#168` (`Object.assign` non-enumerable) — fixed and verified.** The
  exact repro now shows `Object.keys`/`JSON.stringify`/a second
  `Object.assign` all correctly seeing the copied property, and
  `getOwnPropertyDescriptor(...).enumerable` is `true`.
- **Bonus, not one of the filed issues:** `satisfies` as a function/arrow
  parameter name (the one gap noted in the `#164` correction) is also
  fixed — `function f(satisfies) { return satisfies; }` and `(satisfies)
  => satisfies` both work now.

**With `#168` fixed, `hosted-git-info`'s template methods work for
real**, not just `fromUrl()`'s plain-property reads. Verified directly:
`.shortcut()` → `"github:foo/bar"`, `.https()` → the real git+https
URL, `.ssh()` → the real scp-style URL, `.toString()`, `.path()`,
`.tarball()`, `.bugs()`, `.docs()` — all correct, matching real Node's
`hosted-git-info`. **Deleted the `hosted-git-info` fake** (`hostedgit.go`,
its `host.go` registration, its `cmd/scoreboard` entry) after confirming
directly — not just via the scoreboard, which would have shown a false
"clean" here too, since none of the three `pi` invocations exercise
`hosted-git-info` at all — that `pi-coding-agent`'s actual usage
(`dist/utils/git.js`: `hostedGitInfo.fromUrl(candidate)` plus reading
`.domain`/`.user`/`.project`/`.committish` off the result, multiple
hosts, scp-style and `https://` forms, the "no match" case) all produce
correct results. Replaced the old fake-testing `TestHostedGitInfoShim`
unit test with `TestHostedGitInfoReal`, matching the existing
real-package-with-skip-if-absent pattern other Phase 3 tests already
use (`findPiAgentCorePackage`, which conveniently already resolves to
the right `pi-coding-agent` root for `hosted-git-info` too, since it's a
direct dependency in the same `node_modules`).

**One caveat, not blocking pi-coding-agent's actual usage but worth
tracking: `.browse()` called with zero arguments still throws**, and
this traces to a *new*, more general, more severe bug than anything
`hosted-git-info`-specific:

- **[paserati#170](https://github.com/nooga/paserati/issues/170)**
  (vm): a variadic function (one with a rest parameter) throws a hard
  runtime error when called with fewer arguments than its non-rest
  parameter count — `function bar(path, ...args) {} bar();` throws
  `Expected at least 1 arguments but got 0` instead of running with
  `path: undefined`, matching real JS's total lack of arity enforcement
  at the language level. This is a genuine `vm/call.go` runtime check
  (not a suppressable type-checker diagnostic — there's a separate,
  related compile-time-only version of the same bug in
  `checker/call.go`, not what this issue is about), so it can't be
  worked around by skipping type-checking. `(firstArg, ...rest)` is a
  common real-world signature, so this is likely to recur elsewhere.
  **Not currently blocking**: confirmed `pi-coding-agent`'s own code
  never calls `hosted-git-info`'s `.browse()` (or any other template
  method) at all — grepped `dist/` directly — so this doesn't affect the
  actual target CLI, only a corner of `hosted-git-info`'s API surface
  nothing currently exercises.

**2026-09-01, sixth round: `#170` fixed and verified.** Local paserati
checkout pulled the fix. Full re-verification (build/vet/test, all
three `pi` invocations, full scoreboard) clean, no regressions. Verified
two ways: directly through noderati, the real `hosted-git-info`'s
`browse()` called with zero arguments now returns the correct templated
URL instead of throwing (`https://github.com/foo/bar`, matching real
Node); and confirmed the checker-level companion diagnostic
(`checker/call.go`'s `PS2001`) still fires as expected on a literal call
site with type-checking on — exactly the "separate, not what this issue
is about" scoping from the original report, not a regression. Since
noderati always runs real npm CJS/ESM with `SkipTypeCheck(true)` (real,
unannotated `.js` never gets this diagnostic in practice), that
remaining checker-side piece doesn't affect anything here. No code
changes needed on noderati's side for this round — the fix was entirely
upstream and nothing was working around it locally to begin with.

**This closes out every issue filed against `hosted-git-info`'s
investigation** — `#159`, `#160` (destructuring), `#163` (re-export of
an import), `#168` (`Object.assign` enumerability), `#170` (variadic
arity) — all fixed, verified, and `hosted-git-info`'s fake is gone for
good. Six rounds of back-and-forth from "silently returns `{}`" to a
fully-working real package, each round finding the next real bug once
the previous one stopped being the blocker.

**2026-09-01, seventh round: picked up `glob` next.** Its scoreboard
"clean" row was the expected false positive (none of the three `pi`
invocations call anything `glob`-dependent) — direct functional testing
(`globSync`/`glob` via a real, unmodified `glob@11`) found two more real
noderati-side bugs, both general and both fixed directly:

- **`looksLikeESMSource` misclassified a real ESM file as CommonJS,
  silently corrupting every export.** `import { globSync } from "glob"`
  resolved and "succeeded" with `globSync` coming back `undefined` — no
  error anywhere, matching the exact shape of a real CommonJS-vs-ESM
  mismatch, except `glob`'s own `package.json` unambiguously says `type:
  module` and its ESM build is genuinely ESM. Root-caused to
  `nodemodules.go`'s `looksLikeESMSource`: it only checked whether some
  *line* started with the literal string `"import "` or `"export "`
  (note the trailing space) — which fails two ways at once for a
  minified bundle: minifiers drop the space after the keyword
  (`export{...}`, not `export {...}`), and the whole 82KB file is one
  line, so "starts a line" only ever looks at line 1 (which starts with
  neither keyword — the real `import`/`export` statements are scattered
  mid-file, a common bundler artifact from concatenating what were
  originally separate chunks). Misclassified as CJS, `.js` extension
  tipped `shouldWrapCJS` into wrapping it in a CJS function body — which
  hides its `export{...}` inside a function scope where it's a silent
  no-op, not a syntax error, so nothing ever surfaced the problem.
  Fixed by replacing the line-prefix scan with a whole-source,
  word-boundary regex match for `import`/`export` as the reserved-word
  keyword (safe: real JS can't use either as an ordinary identifier, so
  a same-word-boundary match anywhere in the source has no real
  false-positive risk). This is a general fix, not `glob`-specific — any
  minified ESM bundle with a `.js` extension and no space after
  `import`/`export` would have hit the identical silent corruption.

- **`path.posix`/`path.win32` were almost entirely unimplemented** —
  only `sep`/`basename`/`dirname` existed on either namespace, missing
  `resolve`/`join`/`normalize`/`isAbsolute`/`relative`/`extname`/
  `delimiter`/`toNamespacedPath` entirely. With the export bug fixed,
  `glob`'s real dependency `path-scurry` got far enough to actually call
  `path.posix.resolve(cwd)` from its `PathScurryBase` constructor (chosen
  deliberately — `PathScurryPosix`/`PathScurryDarwin` pick the posix
  implementation on purpose, not because the host happens to be POSIX) —
  `posix.resolve` not existing at all threw `pathImpl.resolve is not a
  function`, which is what the earlier, much more confusing "Must call
  super constructor..." error (chased at length against the *minified*
  bundle before switching to the readable non-minified source, which is
  what actually surfaced this) turned out to really be — some other
  reporting artifact of the same underlying failure, not a real
  super-call-ordering bug at all. Implemented both namespaces properly
  and platform-independently (`internal/host/path.go`) — `posix.*` via
  Go's platform-independent `"path"` package (always forward-slash,
  correct regardless of host OS, unlike reusing `path/filepath` which
  would silently produce backslash output on a Windows host); `win32.*`
  hand-rolled (Go's stdlib has no backslash-path equivalent) — join,
  normalize, resolve, and relative all implemented and checked against
  known-correct outputs directly, not just "doesn't crash."

**`glob` itself still can't be unfaked.** With both bugs above fixed,
`globSync`/`glob` get all the way into `minimatch`'s actual
pattern-to-regex compilation — and hit the same documented, accepted,
architectural gap as `highlight.js`'s `latex` support: Go's RE2 regex
engine doesn't support lookahead (`(?!`), and minimatch's glob-to-regex
translation uses a negative lookahead unconditionally, for essentially
any pattern (confirmed: still fails identically with `dot: true`, so
it's not specific to the default dotfile-exclusion behavior). Nothing
to fix here on noderati's side — this is the same class of gap already
accepted and documented for `latex`, just newly hit via a different
package. Filed upstream as
[paserati#172](https://github.com/nooga/paserati/issues/172), since
hitting it a second time via a completely unrelated package (nothing in
common with `highlight.js` except "compiles a lookahead somewhere")
makes it a wholesale blocker for a whole package's pattern-matching
core, not a one-language edge case — worth having tracked at the
engine level, even though there's no quick fix. Verified no regressions
from either fix via the full scoreboard and all three `pi` invocations.

**2026-09-01, eighth round: `#172` (RE2 lookahead) fixed upstream —
verified, and it's a real fix, not a won't-fix closure.** Local paserati
checkout pulled `new RegExp(...)` falling back to `regexp2` (a
backtracking engine) specifically for patterns using lookaround
constructs RE2 can't compile. Verified directly against plain paserati:
`(?!`, `(?=`, `(?<=`, `(?<!` all now work correctly (tested both
matching and non-matching cases for each). Rebuilt noderati: the
`latex`/`highlight.js` noise (`ERROR: Language definition for 'latex'
could not be registered` on every single invocation, since the very
first round of this whole effort) is **completely gone** — `highlight.js`
now registers all 191 bundled languages cleanly, not 190. Full
build/vet/test, all three `pi` invocations, and the full scoreboard all
clean.

With the lookahead wall gone, `glob`/`minimatch` got past pattern
compilation and into actual directory walking — which surfaced two more
real noderati-side `fs` gaps, both fixed directly:

- **`fs.readdirSync`/`fs.readdir` (and their `fs/promises` equivalent)
  completely ignored the `{ withFileTypes: true }` option**, always
  returning plain name strings. Real Node returns `Dirent` objects in
  that mode (`.name` plus `.isFile()`/`.isDirectory()`/
  `.isSymbolicLink()`/etc.) specifically so callers that walk a tree
  don't need a second `stat()` per entry just to tell files from
  directories — exactly what `path-scurry` (glob's real filesystem-
  walking dependency) does. Without it, nothing threw anywhere: the
  caller's own `entToType()`-style dispatch (`e.isFile() ? ... :
  e.isDirectory() ? ...`) just found none of those methods on a plain
  string and fell through every branch, silently walking zero children.
  Added a real `Dirent`-shaped object (`internal/host/dirent.go`,
  shared by both the sync and async `readdir` implementations) and
  wired `withFileTypes` detection through both.
- **`fs.lstatSync`/`fs/promises.lstat` didn't exist at all** — `path-scurry`
  imports both unconditionally at its own module top level (`import {
  lstatSync, ... } from 'fs'`). Added both, using Go's `os.Lstat` (does
  not follow symlinks, matching lstat's whole reason for existing versus
  `stat`) and extended `fsStats` with `isSymbolicLink()`/
  `isBlockDevice()`/`isCharacterDevice()`/`isFIFO()`/`isSocket()` to
  round out the real `fs.Stats` method set (previously only
  `isFile()`/`isDirectory()` existed). Also corrected `isFile()` itself
  while touching this: it was `!info.IsDir()` (true for symlinks and
  device files too), now `info.Mode().IsRegular()` — real Node's
  `isFile()` means specifically a regular file.

**`glob` is still blocked, but by something new and unrelated: a
different `Object.assign` gap from `#168`'s — this time about the
*target*, not the source.** With both `fs` fixes in place, directory
entries are read from disk correctly and processed into `Path` objects
without error — confirmed directly (patched a scratch copy of
`path-scurry`'s source with temporary logging to see the real entries
flowing through). But the result was still always an empty directory
listing. Traced to `PathBase.children()`:

```js
const children = Object.assign([], { provisional: 0 });
```

`objectAssignWithVM` (`pkg/builtins/object_init.go`) has branches for a
`TypeObject` and a `TypeDictObject` *target* — nothing at all for
`TypeArray`. `Object.assign([], {...})` silently does nothing: no
error, the array just comes back with none of the source properties
copied on. `children.provisional` stays `undefined` forever, so every
later `children.provisional++` (path-scurry's own count of
just-discovered, not-yet-confirmed directory entries) produces `NaN`
instead of counting up — and `children.slice(0, children.provisional)`
with a `NaN` bound always returns `[]`. Filed as
[paserati#174](https://github.com/nooga/paserati/issues/174), verified
against plain paserati with a two-line repro
(`Object.assign([], {provisional: 0}).provisional` → `undefined`,
should be `0`). `glob`'s fake stays — this is squarely upstream, nothing
to work around on noderati's side.

**Ninth round (2026-09-01/02) — `#172` and `#174` verified fixed upstream;
`glob`'s real functionality is now 100% correct; one new blocker found and
filed.** paserati merged three fixes to `main`: the `#172` regexp2 fallback,
the `#174` `Object.assign` array-target fix, plus two related array-indexing
fixes (`667d201e` sparse-index bracket reads, `2f6fe2b0`
`Object.defineProperty` honoring attributes on array indices). Rebuilt
noderati against the updated checkout and re-verified everything directly
against plain paserati before touching noderati, per this project's standing
rule:

- `#172` (RE2→regexp2 lookahead): all four lookaround forms (`(?!`, `(?=`,
  `(?<=`, `(?<!`) verified correct again; the `latex`/`highlight.js` noise
  stays gone.
- `#174` (`Object.assign` array target): verified with three cases including
  numeric-index + `length` assignment (`Object.assign([], {0:"a",1:"b",
  length:2})` → `["a","b"]`) — correct.
- Full `go build`/`go vet`/`go test`, all three real `pi` invocations, and
  the full Phase 2 scoreboard: clean, zero regressions.
- **`glob`'s real, non-minified ESM entry (`dist/esm/index.js`) now produces
  100% correct functional output end-to-end** — both flat (`*.txt`) and
  recursive (`**/*.txt`) patterns, verified against the actual installed
  `glob@13.0.6` package. That file turned out to be a thin re-export shim
  over glob's separate real source files (`glob.js`, `has-magic.js`,
  `minimatch`'s own exports), not a bundle — so this confirms every fix
  landed this cycle (RE2 lookahead, `path.posix`/`path.win32`,
  `withFileTypes`/`Dirent`, `lstatSync`, `Object.assign` array target) really
  does add up to correct real-world `glob` behavior.

**But `glob`'s fake still cannot be deleted**, because that's not the file
real code actually loads. `glob`'s own `package.json` `exports["."].import.
default` points at `dist/esm/index.min.js` — a genuinely bundled-and-
minified file (glob's own submodules plus `minimatch`/`minipass`/
`path-scurry` all inlined into one ~83KB file) — and *that* file throws a
`ReferenceError: Must call super constructor in derived class before
accessing 'this' or returning from derived constructor` on every
`globSync`/`glob` call, at the real, unmodified `PathScurryBase` →
`PathScurryPosix` → `PathScurryDarwin` construction (minified to `It` → `rt`
→ `St`).

Chased this down at length before concluding it's a new, distinct paserati
bug rather than anything fixable on noderati's side:

- Confirmed via `try { globSync(...) } catch (e) { ... }` that it's a
  genuine `ReferenceError` from paserati's own "must call super" VM check —
  not a misattributed error — though every frame in the reported stack
  shows the same bogus placeholder position (`3:1`), which is itself
  suspicious.
- Hand-written repros using the *exact* real shapes (a 3-level
  `extends` chain built from class expressions, class fields declared
  before the constructor, `let`-destructuring preceding `super(...)`,
  indirect construction through a ternary-selected variable) all run
  correctly in isolation, against plain paserati — none reproduce the bug.
- Bisected the real file directly instead: truncating the real minified
  bundle at byte offset 66040 (right after the `St` class definition ends,
  discarding `Glob`/`globSync` entirely) and calling `new St("/tmp", {})`
  directly **still reproduces the identical error** — so it needs nothing
  from `Glob`/`globSync`/the ternary selection call site, just the
  `It`/`rt`/`St` chain plus everything the bundle defines before it
  (~65.6KB: an LRU cache, minipass, minimatch, path helpers, all bundled
  ahead of the PathScurry classes). Attempted further automated bisection
  (binary search over syntactically-valid split points) but didn't land on
  anything smaller in the time available — every candidate cut either broke
  on an unrelated dropped identifier or landed mid-token. This may depend on
  total scope size (many prior class/function declarations in one compile
  unit) rather than on any single specific construct, which would explain
  why every hand-scoped-down repro above passes.
- Re-verified the whole thing against **genuinely plain paserati** (not just
  through noderati's embedding) using `-no-typecheck` plus small local stubs
  for the handful of `node:*`/`fs` imports the truncated prefix still
  contains (none of which the `new St(...)` call path actually exercises) —
  reproduces identically, clean-room, confirming this is a paserati VM bug
  and not something noderati-specific.
- Along the way, noticed the checker rejects the *full* untruncated file
  outright (without `-no-typecheck`) on an apparently unrelated false
  positive — `Array.prototype.some`'s callback inferred as `(any,any)=>void`
  instead of `(any,any)=>boolean` for a function whose only `return`
  statement returns a `RegExp.prototype.test()` result. Could not reproduce
  that one in isolation either; noted in the issue as a secondary
  observation, not filed separately, and doesn't block noderati's real usage
  path — confirmed (not assumed) that noderati calls `p.SetSkipTypeCheck(true)`
  unconditionally at all three of its entry points in `cmd/noderati/main.go`,
  so it never invokes paserati's checker at all, for anything.

Filed as [paserati#180](https://github.com/nooga/paserati/issues/180), with
a from-scratch-verified, self-contained (66KB, no external deps once the
`node:*` imports are stubbed) reproduction script attached. `glob`'s fake
stays until this lands — it is now the *only* thing standing between `glob`
and being fully real.

**Tenth round (2026-09-02) — `proper-lockfile` deleted for real; `#180`
confirmed fixed on an unmerged upstream branch, not yet actionable.**

While waiting on `#180`, picked up `proper-lockfile` — the last known
blocker (see the ninth round's `#147` note above) was a genuine, systemic
noderati gap: **noderati's `fs` module had zero classic Node callback-style
functions** (`fs.mkdir(path, cb)`, `fs.stat(path, cb)`, etc.) — only `*Sync`
and `fs/promises`. Real Node's `fs` has three parallel styles; noderati had
two. `graceful-fs` (proper-lockfile's real, direct dependency, and used
directly by plenty of other real packages) patches every one of those onto
its own exported `fs` object — with nothing on noderati's side to find and
wrap, `require('graceful-fs').mkdir` etc. came back `undefined`, which is
exactly what made `proper-lockfile`'s real async `lock()` throw `TypeError:
undefined is not a function` inside its own `options.fs.mkdir(...)` call.

Implemented the four callback-style functions proper-lockfile's real async
core (`lib/lockfile.js`) actually calls — `mkdir`/`stat`/`rmdir`/`utimes` —
plus `realpath` (`internal/host/fs_async.go`), deferred via the same
`vmInst.GetAsyncRuntime().ScheduleNextTick(...)` mechanism `process.
nextTick` already uses, preserving real Node's guarantee that an fs
callback never fires synchronously within the same tick as its call.
`realpath` wasn't in the original plan — the first pass, scoped to
`{realpath: false}` (the option `settings-manager.js`/`trust-manager.js`
pass to `lockSync`), missed that `auth-storage.js`'s real async
`lockfile.lock()` call passes *no* `realpath` option at all, which defaults
to `true` in real proper-lockfile. Caught by testing against the *exact*
real options object each of the three real call sites actually passes, not
a hand-simplified stand-in — the same discipline that caught `minimatch`
and `glob`'s async-API gap as false positives earlier in Phase 3, doing its
job again.

This was possible cleanly now specifically because
[paserati#162](https://github.com/nooga/paserati/issues/162) (fixed a few
rounds back) means a `vm.Value`-typed parameter on a declarative
`ModuleBuilder.Function` now actually receives the real callback — no need
for the raw `vm.NewNativeFunction` workaround `child_process.go`'s
`__noderatiSpawn` still uses for the same reason predating that fix.

**Verified with real, exact call-site fidelity, not just "does it load":**
async `lockfile.lock()`/`check()` tested with `auth-storage.js`'s literal
options object (`retries` as a full retry-config object, `stale: 30000`,
`onCompromised`) — correct lock/release, correct `ELOCKED` on a second
concurrent lock attempt, correct `check()` true/false across the lock's
lifetime. Sync `lockSync()`/`checkSync()` tested with both
`settings-manager.js`'s plain `{realpath: false}` form and
`trust-manager.js`'s `lockfilePath` override form — both correct. Full
build/vet/test, all three real `pi` invocations, and the full scoreboard:
clean, zero regressions.

**`proper-lockfile`'s fake deleted** — `internal/host/properlockfile.go`
removed entirely, its registration in `host.go` and its toggle in
`cmd/scoreboard/main.go`'s `fakeNames` both removed. Re-ran the full
functional verification above with the fake actually gone (no
`NODERATI_DISABLE_FAKES` needed) — identical, correct results.

**While this was underway, unexpectedly caught `#180` mid-fix, live, in
the shared paserati checkout.** `pkg/compiler/compile_class.go` showed up
modified/uncommitted (branch `fix-180`, not pushed) — briefly broke
noderati's own build (a genuine transient: the paserati agent's edit
landed between two `go build` invocations a few seconds apart, a real risk
of the two projects sharing a live checkout, not anything self-inflicted).
Once it stabilized, the fix builds clean and is *exactly* the right root
cause: `injectFieldInitializers`' search for a class's `super(...)` call
only recognized one that was the *entire* statement expression — but a
minifier commonly merges `super(...);` with the very next statement into
one `super(...), next;` via the comma operator (legal JS; a bare
`ExpressionStatement`'s value is always discarded), which is exactly
`rt`'s real shape in `#180`'s own filed repro
(`super(t,mi,"/",{...e,nocase:s}),this.nocase=s`). Their fix flattens any
top-level comma-chain statement into separate statements before searching,
so the buried `super()` call is found correctly.

**Tested it directly against `#180`'s own filed repro and the full real
minified bundle — genuinely fixed, both ways** (after a `go clean -cache`;
the first attempt looked unfixed purely from a stale build-cache artifact,
not the fix itself — caught by testing the *exact* isolated repro from the
filed issue on a from-scratch rebuild before concluding anything). The
truncated 66040-byte repro now runs clean past the point it used to throw;
the full, untruncated real bundle's actual `globSync("*.txt", ...)` and
`globSync("**/*.txt", ...)` calls, run through noderati exactly as real
`pi-coding-agent` code would reach them, now both return correct results.

**Not deleting `glob`'s fake yet, and not commenting on `#180` yet either
— this fix is real but unmerged**, sitting uncommitted on a local branch
in a checkout this project doesn't control the lifecycle of. Every other
fix this whole effort has acted on was verified only once actually merged
to paserati's `main` (see every prior round above) — no reason to break
that discipline now just because the fix happens to be visible early from
sharing a filesystem with the person writing it. Once it's merged: rebuild,
re-verify (including the fresh-build-cache lesson learned here), then
delete `glob`'s fake the same way `proper-lockfile`'s went today.

**Follow-up fix, same day: `fs.mkdirSync`/`fs/promises.mkdir` were always
recursive, unconditionally — a pre-existing divergence from real Node
flagged (not fixed) while adding `fs_async.go` above.** Real Node's
`fs.mkdirSync(path)` — no options, or `{recursive: false}`, its own
default — throws `ENOENT` for a missing parent directory; only
`{recursive: true}` makes it behave like `mkdir -p`. Both `mkdirSync`
(`fs.go`) and the promise-based `mkdir` (`fspromises.go`) called
`os.MkdirAll` unconditionally, ignoring whatever options object was
actually passed — silently *more* permissive than real Node, not less,
so nothing depending on the correct (throwing) behavior could ever have
worked, and everything depending on the (incorrect) always-recursive
behavior would silently keep working even without `{recursive: true}`.

Checked for real-world fallout before fixing, per the ticket's own
instruction: grepped both `pi-coding-agent`'s own `dist/` and every real
package under its `node_modules/` for `mkdirSync(`/`.mkdir(` call sites.
Every single one — twenty-odd in `pi-coding-agent`'s own code, all of
them — already passes `{recursive: true}` explicitly; the one
`node_modules` non-`Sync` bare-looking call
(`tools/write.js`'s `ops.mkdir(dir)`) turned out to be a one-arg wrapper
around `fsMkdir(dir, {recursive:true})`, not a raw bypass. Zero real call
sites anywhere in the real, installed tree depend on the current
(incorrect) always-recursive default — safe to fix with no regression
risk, confirmed by measurement rather than assumed.

Fixed by parsing the already-available `opts map[string]interface{}`
parameter for a `recursive: true` flag (new `mkdirRecursiveRequested`,
`dirent.go`, alongside `withFileTypesRequested` — same shape) and
switching between `os.Mkdir` (non-recursive, real Node's own default) and
`os.MkdirAll` accordingly, in both `fs.go`'s `mkdirSync` and
`fspromises.go`'s `mkdir`. Verified directly: `mkdirSync('/tmp/x/y/z')`
with missing parents and no options now throws `ENOENT: no such file or
directory, mkdir '/tmp/x/y/z'`, matching real Node's exact message shape;
the same call with `{recursive: true}` still succeeds and creates every
intermediate directory; a single-level `mkdirSync` with no options under
an already-existing parent still succeeds (unaffected — this was always
the common case). Same three checks repeated for the async
`fs/promises.mkdir`. Full build/vet/test, all three real `pi`
invocations, full scoreboard: clean, zero regressions — matching the
zero-real-callers finding above.

**`glob`'s fake deleted, 2026-09-02 — [paserati#180](https://github.com/nooga/paserati/issues/180)
merged and confirmed.** `origin/main` pulled (`c7865334`, "fix(compiler):
a derived class handles a comma-joined super() call"), both `paserati`
and `noderati` rebuilt from a fully clean cache (the false-negative
lesson from spotting this fix mid-development still applies). Re-verified
both ways again: the issue's own filed repro now runs clean past the
"must call super" error entirely (fully clean on plain paserati once
`URL` — an unrelated plain-CLI-only gap, not part of this bug — is
available; noderati has it natively). Then went one step further than
before deleting: found `glob`'s *only* real call site in `pi-coding-agent`
(`package-manager.js`: `import { globSync } from "glob"`,
`globSync(entry, { cwd: root, absolute: true, dot: false, nodir: false
})`) and tested that *exact* pattern — bare specifier, real resolution,
real options — against a small fixture tree with a dotfile mixed in.
Correct on every count: recursive match, absolute paths, dotfile
correctly excluded.

`internal/host/glob.go` (the fake) deleted entirely; its registration in
`host.go` and its toggle in `cmd/scoreboard/main.go`'s `fakeNames` both
removed. Re-ran the full verification above with the fake actually gone
(no `NODERATI_DISABLE_FAKES` needed) — identical, correct results. Full
build/vet/test, all three real `pi` invocations, full scoreboard: clean.
The scoreboard's "candidates to delete" list is now empty — every
remaining fake (`pi-tui`, `pi-ai`, `pi-agent-core`, `typebox`, `diff`,
`jiti`) genuinely fails when disabled, no more scoreboard-clean-but-
functionally-broken candidates left to find.

**Eleventh round (2026-09-02) — investigated `diff`, filed a new, precisely
root-caused general VM bug ([paserati#182](https://github.com/nooga/paserati/issues/182)).**
`diff`'s previously-known blocker
([paserati#163](https://github.com/nooga/paserati/issues/163), the
re-export-of-an-import compile bug that motivated filing it in the first
place — `diff@8.0.4`'s real ESM entry, `libesm/index.js`, is exactly that
barrel shape) is fixed and verified; the scoreboard's current
`fake-off:diff` failure (`TypeError: object is not iterable`, at the
same bogus `line 1, column 1` position the diagnostics gap has produced
before) is something new since then.

Bisected by importing each of the barrel's thirteen real submodules
individually: `diff/line.js`, `diff/json.js`, and `patch/create.js` fail
(the third only because it transitively imports the broken `diff/line.js`
— confirmed, not assumed, by checking its own source); the other ten load
cleanly. Both real failures share one exact line:
`super(...arguments)` in a derived class's pass-through constructor.
Reduced to a clean, dependency-free repro against plain paserati, then
went further to characterize the bug precisely rather than just report
the crash: `for (const x of arguments)`, `Array.from(arguments)`,
destructuring (`const [a,b] = arguments`), and even
`typeof arguments[Symbol.iterator]` (`"function"`) **all work correctly**
on the exact same `arguments` object — only the spread operator
(`...arguments`, in a call, an array literal, or a `super()` call)
fails. Spreading other non-Array iterables (a `Set`, a hand-written
`{[Symbol.iterator](){...}}` object) works fine, so this is specific to
`arguments`, not spread-of-iterables in general.

Root-caused precisely by reading `pkg/vm/vm.go`'s `extractSpreadArguments`
(every spread context funnels through this one function): its outer
`switch iterableVal.Type()` has fast-path cases for `TypeArray`,
`TypeString`, `TypeGenerator`, `TypeSet`, `TypeMap` — no `TypeArguments`
case — so an `arguments` object (a distinct VM type, confirmed via
`value.go`'s `TypeArguments`/`ArgumentsObject`) falls to the generic
`default:` iterator-protocol path. That path's own prototype-chain walk
is a hand-rolled `if`/`else if` chain checking `current.Type()` against
`TypeObject`/`TypeGenerator`/`TypeAsyncGenerator`/`TypeDictObject`, with a
catch-all `else { break }` for anything else — `TypeArguments` isn't one
of the handled branches, so it hits that catch-all immediately, `found`
stays `false`, and the resulting error's `%s is not iterable` substitutes
`TypeArguments.TypeName()`, which (correctly, matching real JS's
`typeof arguments === "object"`) returns `"object"` — exactly the
observed message. The real `Symbol.iterator` property is genuinely
present and reachable through the ordinary property-get path (which is
why `for...of`/`Array.from`/destructuring/`typeof` all already work) —
`extractSpreadArguments` is the one place in the VM with its own private,
incomplete list of "types that count as object-like" instead of using
that same general lookup.

Not filed as a `diff`-specific issue — `super(...arguments)` is a common,
idiomatic pass-through-constructor pattern in real-world JS/TS, so this
is general and likely to recur. `diff`'s fake stays pending `#182`.
`typebox`, `pi-ai`, `pi-agent-core`, `pi-tui`, and `jiti` remain
uninvestigated this round.

**Twelfth round (2026-09-02) — investigated `typebox`, found and filed a
severe, general module-resolution correctness bug
([paserati#183](https://github.com/nooga/paserati/issues/183)).**
`typebox@1.1.38`'s real `Type` export loads fine on its own (all ~120
real exports present, richer than the fake's dozen), but the real,
evidenced usage pattern (`Type.Object({ path: Type.String({...}), ... })`,
from `dist/core/tools/write.js` and seventeen other real call sites)
throws `TypeError: undefined is not a function` deep inside typebox's own
`RequiredArray`/`IsOptional` machinery.

Bisected by testing each layer of the real package's own module graph in
isolation — `_optional.mjs`, `properties.mjs`, `object.mjs`, and even the
full `typebox.mjs` namespace all worked *individually*; the failure only
appeared once loaded through the real top-level entry, `build/index.mjs`.
That file combines several `export *` barrels
(`type/action/index.mjs`, `type/extends/index.mjs`, `type/types/index.mjs`,
plus a namespaced `export * as Type from './typebox.mjs'`) — bisecting
*those* found `type/action/index.mjs` and `type/extends/index.mjs` each
independently trigger it, `type/engine/index.mjs`/`type/script/index.mjs`
don't. The common thread: `type/action/_optional.mjs` and
`type/extends/object.mjs` are real, different files with real, different
content, that happen to share a basename with `type/types/_optional.mjs`/
`type/types/object.mjs` — the exact files `Type.Object`'s own working
implementation depends on.

Reduced to two clean, dependency-free, general (no `typebox` involved at
all) repros, both verified against plain paserati directly:

1. `dirA/_helper.mjs` and `dirB/_helper.mjs` — different files, different
   directories, each exporting a *different-named* function; `dirA/user.mjs`
   and `dirB/user.mjs` each do `import { X } from './_helper.mjs'` (a
   plain relative import, resolved from *their own* directory). Importing
   both users from one script: the first one resolved works; the second
   throws `undefined is not a function` — its own `./_helper.mjs` import
   silently got handed back the *other* directory's already-cached module
   instead of loading its own file. **Reversing the import order in the
   entry script flips which one fails** — confirms it's a "whoever
   resolves this literal specifier text first wins the cache slot"
   mechanism, not anything content-specific.
2. Same shape, but both `_helper.mjs` files export a *same-named* function
   with different logic (`x*2` vs `x+100`). No crash at all this time —
   the second file's caller just silently gets the *first* file's wrong
   implementation and returns the wrong number, with zero indication
   anything went wrong.

Filed as a general compiler/driver bug, not `typebox`-specific — likely
root cause is the module cache keying relative-import resolution by the
literal specifier text (`'./_helper.mjs'`) instead of the fully resolved,
canonical path (which necessarily differs between two different
directories). Flagged as high severity given repro 2: any real,
multi-directory project with a conventionally-reused filename
(`index.js`, `utils.js`, `base.js`, ...) can silently swap in the wrong
module's implementation with no error at all — considerably worse than a
crash. `typebox`'s fake stays pending `#183`. `pi-ai`, `pi-agent-core`,
`pi-tui`, and `jiti` remain uninvestigated.

**Thirteenth round (2026-09-02) — `#182` merged and confirmed; `diff`
re-blocked by a new, real, silent-correctness bug, filed as
[paserati#185](https://github.com/nooga/paserati/issues/185).** Pulled
`main` (`957383ca`, "fix(vm): spreading the arguments object no longer
throws 'not iterable'"), clean-cache rebuilt both `paserati` and
`noderati` (per the lesson from `#180`'s round). Re-verified `#182`'s own
three repro forms directly against plain paserati — all three now
correct (`super(...arguments)`, a plain call spread, and an array-literal
spread of `arguments` all work).

Then, rather than stopping at "does `diff`'s real barrel load without
throwing," exercised `diff`'s actual functional output against `edit-diff.js`'s
and the interactive `diff.js`'s exact real calls
(`Diff.diffLines`, `Diff.createTwoFilesPatch` with real
`headerOptions`/`context`, `Diff.diffWords`) — the same discipline that's
caught every previous false positive this whole effort. `diffLines` no
longer throws, but its output is **grossly wrong**: a 3-line-vs-4-line
diff that should produce five per-line parts instead collapses into two
giant multi-line blobs with every newline stripped out. Compared directly
against real Node running the identical installed package (same
`node_modules`, same `diff@8.0.4`) to confirm this is genuinely wrong, not
a hasty assumption — real Node's output has the correct five parts, each
line's own trailing `\n` intact.

Traced to `diff/line.js`'s own tokenizer:
`value.split(/(\n|\r\n)/)` — a capturing-group regex used specifically so
`split()` also returns each matched newline, interleaved with the line
content, per real JS spec. Minimal, dependency-free repro against plain
paserati: `"a\nb\nc\n".split(/(\n|\r\n)/)` returns `["a","b","c",""]`
(the captures silently vanish) where real Node returns
`["a","\n","b","\n","c","\n",""]`. A non-capturing group
(`(?:,|;)`) is unaffected; a single capturing group loses exactly its
captured text; **two or more capturing groups produces something more
broadly wrong**, not just "missing captures"
(`"a1b2c3".split(/([a-z])(\d)/)` → `["","","",""]` vs the correct
9-element real-Node array).

Filed as `#185`, general (a well-known, idiomatic real-world `split()`
pattern, not `diff`-specific) and flagged as a silent-correctness bug
(wrong output, no thrown error) rather than a crash — exactly the shape
this project's "measure, don't assume" discipline exists to catch, and
did. `diff`'s fake stays, now blocked by `#185` instead of the
now-fixed `#182`. Full build/vet/test, full scoreboard: unchanged, clean
(no noderati code touched this round).

**Fourteenth round (2026-09-02) — `#183` merged and confirmed; `typebox`'s
own top-level entry deleted; `typebox/value`/`typebox/compile` re-blocked
by a new, precisely-scoped compiler bug
([paserati#188](https://github.com/nooga/paserati/issues/188)); `#185`
verified fixed on an unmerged branch (informational, not acted on).**

Pulled `main` (`b8c17c8c`, "fix(modules): key module caches by resolved
path, not specifier text"), clean-cache rebuilt both. Re-verified `#183`'s
own two repros directly — both fixed (including the reversed-import-order
check and the silent-wrong-value variant, which now correctly resolves to
`105` instead of a wrongly-shared `10`). Then re-exercised `typebox`'s
real, evidenced usage — not just `write.js`'s single call site from the
prior round, but `bash.js`'s and `grep.js`'s real schemas too (multiple
`Type.Optional` fields, correctly excluded from `required`) — all correct.
**`internal/host/typebox.go` (the top-level `Type.Object` etc. fake)
deleted.**

`typebox/value` and `typebox/compile` are separate real npm entry points
(their own `package.json` `exports` subpaths) that were previously
all-or-nothing with the parent package's single `"typebox"` toggle — split
into their own independent `disabledFakes` names (`"typebox/value"`,
`"typebox/compile"`) in `host.go` and `cmd/scoreboard/main.go`, so a fake
can be deleted exactly where it's proven real without forcing the same
verdict onto a sibling entry point that isn't. Necessary this round
specifically: real, evidenced usage (`model-registry.js`/`theme.js`'s
`Compile(schema).Check(...)`) of `typebox/compile` still throws —
`Cannot read properties of undefined (reading 'Symbol(Symbol.iterator)')`,
inside typebox's own `Arguments.Match` overload-dispatch helper (used by
`typebox/value`'s `Check`/`Errors` and, transitively, `typebox/compile`'s
`Compile(...).Check`/`.Errors` — both share it).

Bisected `Match`'s `match[args.length]?.(...args) ?? (() => { throw ... })()`
down to a clean, dependency-free, general repro against plain paserati:
an **optional call whose callee is a member expression** (`obj.f?.(...)`,
`obj["f"]?.(...)`, `obj[computed]?.(...)` — any form) **combined with a
spread argument** doesn't work correctly. A plain-identifier callee with
spread works fine; a member-expression callee with literal (non-spread)
arguments works fine; only the combination of all three (member callee +
optional call + spread arg) breaks — manifesting two different ways
depending on context: a standalone hard *compile error*
(`error compiling argument in optional call expression`) as a bare
statement, or a silent `undefined` (neither the real function's result
nor the `??` fallback) when wrapped in `??`, exactly `Match`'s own shape.
Filed as `#188`. `typebox/value`'s and `typebox/compile`'s fakes both
stay pending it — confirmed via direct exercise, not assumed from the
scoreboard's own signal (`fake-off:typebox/value` shows scoreboard-clean,
same known trap as ever, since none of the three smoke invocations touch
real schema validation).

Separately, checked in on `#185` (still open on `main`, but fixed on a
pushed, unmerged `fix-185` branch — same situation `#180` was in) purely
informationally: re-ran `diffLines` against the same 3-vs-4-line fixture
from the prior round, using a clean-cache build off that branch — now
produces the correct five per-line parts with newlines intact, matching
real Node exactly. Not acted on — `main` doesn't have it yet, so `diff`'s
fake stays untouched, per this project's standing rule.

Full build/vet/test, all three real `pi` invocations, full scoreboard:
clean. `pi-ai`'s `fake-off` failure text changed since last checked
(`Cannot read property 'id' of undefined` → `ReferenceError: atob is not
defined`) — not investigated this round, noted for whenever `pi-ai`/
`pi-agent-core` get picked up.

**Fifteenth round (2026-09-02) — `#185` merged and confirmed; `diff`'s
fake deleted.** Pulled `main` (`b84eef1a`, "fix(builtins):
String.prototype.split(regex) interleaves captured groups"), clean-cache
rebuilt both. Re-verified `#185`'s own three repro forms directly against
plain paserati — all now match real Node exactly (a single capturing
group, a delimiter-alternation group, and the two-capturing-groups case
that previously produced something more broadly wrong than just missing
captures).

Then re-ran the exact real functional exercise from the prior round —
`Diff.diffLines`/`Diff.createTwoFilesPatch`/`Diff.diffWords` against
`edit-diff.js`'s and the interactive `diff.js`'s real call shapes — output
now matches real Node exactly on every field (five correct per-line
parts, newlines intact, a properly formatted unified patch).
`internal/host/diff.go` deleted; its `host.go` registration and
`cmd/scoreboard/main.go` toggle both removed. Re-ran the same functional
exercise with the fake actually gone (no `NODERATI_DISABLE_FAKES` needed)
— identical, correct results.

`go test ./...` hit one flaky failure (`TestSpawnEcho`, an unrelated
`child_process` test with no connection to anything touched this round)
— confirmed flaky by re-running it alone three times (all pass) and the
full suite again (clean); not a real regression. Full build/vet/test, all
three real `pi` invocations, full scoreboard: clean.

`typebox/compile`'s `fake-off` error text changed since last checked too
(`Cannot read properties of undefined (reading 'Symbol(Symbol.iterator)')`
→ `... unknown unicode category, script, or property 'ID_Start' in`
`` `^[\p{ID_Start}_$][\p{ID_Continue}_$‌‍]*$` ``) — `#188` is
in progress upstream and this looks like forward movement past it into a
new, likely RE2-Unicode-property-class gap; not investigated this round,
noted for whenever `typebox/value`/`typebox/compile` are revisited. Only
`pi-tui`, `pi-ai`, `pi-agent-core`, and `jiti` remain as fully
uninvestigated group-B fakes.

**Sixteenth round (2026-09-02) — `#188` confirmed fixed on an unmerged
branch (informational only); `typebox/compile` re-blocked by a distinct,
newly-filed regex-engine gap ([paserati#190](https://github.com/nooga/paserati/issues/190)).**
`#188` sits committed and pushed on `origin/fix-188` (`62cdefba`), `main`
unchanged, issue still open — same situation `#180`/`#185` were in before
they landed. Checked it out, clean-cache rebuilt, and verified: both of
the issue's own repros (a member-expression-callee optional call with a
spread argument, standalone and `??`-wrapped) now behave correctly, and
**`typebox/value`'s real `Check`/`Errors` now work end to end** — valid
and invalid values both correctly reported, confirmed directly. Not acted
on — `main` doesn't have it yet, so `typebox/value`'s fake stays untouched
regardless of this being fully verified working.

Continuing the same functional exercise onto `typebox/compile` (which
shares `Arguments.Match`, `#188`'s target, with `typebox/value`) found a
**new, unrelated** blocker: `Compile(schema)`'s real validator-codegen path
throws building a regex it constructs internally,
`/^[\p{ID_Start}_$][\p{ID_Continue}_$]*$/u` (used to decide whether a
generated property accessor needs bracket notation) — `SyntaxError:
unknown unicode category, script, or property 'ID_Start'`. Confirmed
stable against a fresh `main` checkout (unrelated to the `fix-188` branch
or its content). Bisected precisely: `\p{ID_Start}`/`\p{ID_Continue}` are
ECMAScript-specific Unicode *derived binary properties* (from Unicode's
own `PropList.txt`, added to JS in ES2018 specifically to define what a
valid identifier character is) — distinct from the ordinary
category/script names both of paserati's regex engines otherwise support.
Checked both engines directly, not assumed: RE2 fails outright
(`invalid character class range`); **`regexp2` — the fallback engine
`#172` added specifically for lookaround — also doesn't recognize these
two names** (a different error message, `unknown unicode category,
script, or property`, confirming `regexp2` genuinely was tried and itself
failed, not just that the narrower `new RegExp(...)` fallback gate missed
this pattern). So this is a deeper gap than `#172`'s — broadening the
fallback trigger alone wouldn't fix it, since neither engine has the data
this needs. Filed as `#190` with that distinction spelled out, plus a
concrete direction (both properties are static, well-published Unicode
data — expandable into an explicit character-class range in a
preprocessing pass, independent of either engine's own `\p{...}`
support). `typebox/compile`'s fake stays, now blocked by `#190` instead of
`#188`. No noderati code changes this round (nothing here is on `main`
yet); full build/vet/test unchanged, clean.

**Seventeenth round (2026-09-02) — `#188` merged and confirmed;
`typebox/value`'s fake deleted.** Pulled `main` (`18200ba4`, "fix(compiler):
optional call on a member-expression callee handles spread args"),
clean-cache rebuilt both. Re-verified `#188`'s own two repro forms
directly, plus `typebox/value`'s real `Check`/`Errors` functional
exercise — both correctly report `true`/`false` for valid/invalid values
now. `internal/host/typeboxvalue.go` deleted; its `host.go` registration
and `cmd/scoreboard/main.go` toggle both removed. Re-ran the same
functional exercise with the fake actually gone — identical, correct
results. Full build/vet/test, all three real `pi` invocations, full
scoreboard: clean. `typebox/compile` stays faked, still blocked by
`#190`. Remaining fully-uninvestigated group-B fakes: `pi-tui`, `pi-ai`,
`pi-agent-core`, `jiti`.

**Eighteenth round (2026-09-02) — a real ledger group A gap, not another
paserati bug: `atob`/`btoa` were entirely unimplemented.** With `#190`
in progress upstream, pivoted to looking for a group-A (real Node
builtin) gap instead of another engine bug — `pi-ai`'s `fake-off`
scoreboard row had already surfaced one directly: `ReferenceError: atob
is not defined`, and `typeof atob`/`typeof btoa` were both `undefined`
outright, confirmed directly (not assumed from the error text alone).
Real usage found in multiple real files, not just the one that
surfaced it: `pi-ai`'s own OAuth PKCE flow (`btoa`, `utils/oauth/pkce.js`)
and several provider auth modules, plus `pi-coding-agent`'s own HTML
export template (`atob`).

Implemented both as real globals (`internal/host/base64global.go`,
wired into `process.go` alongside the existing `structuredClone`
global — the same pattern, same registration point). Matched real
Node's actual behavior, not just the happy path: `btoa` throws on any
input character outside Latin1 range (0–255) rather than silently
truncating high bits (real Node throws too — silent truncation would
be exactly the kind of silent-wrong-output bug this whole project
chases down when found in *other* code); `atob` strips ASCII
whitespace per the WHATWG algorithm Node's own implementation follows,
tolerates missing base64 padding (real-world callers often omit it),
and throws on genuinely invalid input rather than decoding something
silently wrong. One correctness detail worth recording: a decoded/encoded
byte becomes one JS "character" via `rune(byte)`/`WriteRune`, matching
`String.fromCharCode(n)`/`charCodeAt(n)` semantics — confirmed
directly against plain paserati first — not a raw appended byte, which
would have silently corrupted any decoded byte ≥ 0x80 once paserati's
own rune-based string handling re-decoded it as UTF-8 later.

Verified every case (round-trip, raw non-ASCII bytes 0–255 through
`String.fromCharCode`/`charCodeAt`, missing padding, both throw paths)
against **real Node running the identical script side by side** — matches
exactly, not just "doesn't crash." Then exercised the actual real call
site, `pi-ai`'s own `base64urlEncode` (byte array → `btoa` → URL-safe
base64), against both noderati and real Node — identical output.

**Practical effect: `pi-ai`'s `--version`/`--help` now match baseline
exactly** (previously `ReferenceError: atob is not defined` on every
invocation) — real progress, not yet a full unblock. `-p` (the one
invocation that actually exercises real LLM streaming) now fails on a
different, deeper error entirely
(`Cannot create property 'responsePromise' on object '&{20 0 0x...}'`) —
not investigated this round; flagged as the next thread if `pi-ai` is
picked up again, though its Go-struct-shaped error text suggests a
paserati VM issue rather than another node-coverage gap. Full
build/vet/test, all three real `pi` invocations, full scoreboard: clean.

**Nineteenth round (2026-09-02) — `atob`/`btoa` boundary-verified;
`typebox/compile` blocked by a second, deeper bug (`#192`), found while
sanity-checking `#190`'s status.** `advisor()` flagged two loose ends
from the eighteenth round: `btoa`'s Latin1-range check iterates Go
runes rather than JS/UTF-16 code units, and the scoreboard's
`typebox/compile` error text had visibly changed since it was last
described in this doc. Checked both directly rather than assuming
either was fine or broken:

- `btoa` boundary case (`0xFF` vs `0x100`, and the full `0x80`-`0xFF`
  byte range) matches real Node exactly, run side by side — the
  rune-vs-code-unit distinction only diverges from real Node on lone
  UTF-16 surrogates, which both implementations reject anyway (real
  Node because the code unit is `> 0xFF`; noderati because the
  malformed rune decodes to `U+FFFD`, also `> 0xFF`). No fix needed.
- `typebox/compile`'s new error (`ReferenceError: index is not
  defined`, replacing the `#190` `ID_Start` text this doc previously
  described) turned out to be real, not doc drift: this session's
  paserati checkout had been sitting on the unmerged `#190` fix branch
  since the sixteenth round rather than `main` (a repeat of the
  standing "switch back to `main` after verifying on a WIP branch"
  hygiene rule not yet having run this round) — the branch's paserati
  gets *past* `#190`'s `ID_Start` regex failure and hits a new bug one
  layer deeper. Confirmed `#190` itself is still open and unmerged on
  `origin/main` (so the doc's existing "blocked by `#190`" statement
  stays accurate for `main`), and separately confirmed the new error is
  a real, reachable bug that will surface the moment `#190` lands, not
  an artifact of testing against the wrong branch.

Minimally reproduced with real `typebox@1.1.38`: `Compile()` on any
schema containing `Type.Record(...)` throws `ReferenceError: index is
not defined` when `typebox` and `typebox/compile` are both reached via
dynamic `import()` (either order) — the exact shape of
`pi-coding-agent`'s own real startup path (`core/model-registry.js`'s
`ModelsConfigSchema`, itself a `Type.Record(...)`, reached through a
chain of dynamic imports). The identical schema compiles and validates
correctly with ordinary static `import`, and a schema without
`Type.Record` compiles fine either way — narrowed enough to rule out
"dynamic import is broken in general" and "any `Record` schema fails,"
before filing. A synthetic three-file repro shaped like typebox's own
directory layout (a shared registry module reached via two different
relative-import depths, both loaded through dynamic `import()`) did
*not* reproduce a module-duplication problem, ruling out simple
relative-path resolution as the cause and pointing instead at
something specific to `typebox`'s `Record` codegen combined with
dynamic-import's compile path. Filed as
[paserati#192](https://github.com/nooga/paserati/issues/192) with the
repro, what's ruled out, and a pointer into `OpDynamicImport`/
`executeModule`/`moduleContextKey` in `pkg/vm/vm.go`. `typebox/compile`
stays faked — now blocked by `#192` (reachable only once `#190`
lands), not `#190` alone. Switched the shared paserati checkout back to
`main` before finishing, per the standing hygiene rule. No noderati
code changes this round; full build/vet/test unaffected.

**Twentieth round (2026-09-02) — `#190` merged; `#192` fixed on an
unmerged branch, verified for information only.** `#190` landed on
`origin/main` (closed on GitHub, confirmed with
`git merge-base --is-ancestor` rather than trusting the issue tracker
alone). `#192` has a fix on the local checkout's
`fix/module-hoisted-ref-later-decl` branch (not yet merged — confirmed
the same way), whose actual root cause turned out to be different from
this doc's own speculation: `fix(compiler): hoisted function refs to a
module's later top-level binding resolve to the right heap slot`, not
a dynamic-import module-duplication bug as guessed when `#192` was
filed.

Verified on this branch (`go clean -cache` rebuild first, per the
standing rule): `#192`'s own minimal repro (`Compile()` on a
`Type.Record(...)` schema, `typebox`/`typebox/compile` both reached
via dynamic `import()`) now returns the correct result instead of
throwing. Went further than the minimal repro, per this project's own
"exercise the exact real call pattern" rule: built the real
`ModelsConfigSchema` shape from `core/model-registry.js` (nested
`Type.Record`/`Type.Object`/`Type.Optional`) and ran both a valid and
an invalid config through `.Check()`/`.Errors()` — correct `true`/
`false` and a correct error message, matching real Node running the
identical script side by side exactly (down to `.Errors()`'s message
text). Full `pi` scoreboard run: `fake-off:typebox/compile` now
reproduces baseline on **all three** invocations (`--version`,
`--help`, `-p hello` — the last matching baseline's own expected
"no local model server" failure, not a crash), the first time this row
has been fully clean. Full paserati build/vet/test (`go test ./...`,
every package) and noderati's own build/vet/test: clean.

**`typebox/compile`'s fake stays in place** — per the standing rule,
verifying a fix on an unmerged branch is for information only; nothing
gets deleted until `#192` actually lands on `origin/main`. Switched the
shared checkout back to `main` before finishing.

**Twenty-first round (2026-09-02) — `#192` merged; `typebox/compile`'s
fake deleted.** `#192` landed on `origin/main` (`9c8ca157`, confirmed
directly, not from the issue tracker alone). Clean-cache rebuild, then
re-ran everything from the twentieth round's verification against the
actual merged commit rather than trusting the prior branch-based
result to carry over unchanged: `#192`'s own minimal repro, the real
`ModelsConfigSchema` functional exercise (nested `Type.Record`/
`Type.Object`/`Type.Optional`, valid and invalid input through
`.Check()`/`.Errors()`), `--version`/`--help` — all matched real
Node/baseline exactly, same as on the unmerged branch. Full scoreboard:
`fake-off:typebox/compile` clean on all three invocations.

`internal/host/typeboxcompile.go` deleted; its `host.go` registration
(the `typebox/compile` toggle branch) and `cmd/scoreboard/main.go`'s
`fakeNames` entry both removed; top-level ledger updated to move
`typebox/compile` from the active to the deleted list. `node_modules`
resolution now always loads the real `typebox/compile` entry point —
**`typebox`'s three real entry points (bare, `/value`, `/compile`) are
now all real**, closing out this package's own two-round, two-bug arc
(`#183` → `#188` → `#190` → `#192`). Full paserati build/vet/test, full
noderati build/vet/test, all three real `pi` invocations, full
scoreboard: clean. Remaining fully-uninvestigated group-B fakes:
`pi-tui`, `pi-agent-core`, `jiti`; `pi-ai` stays faked with its own
separate, unstudied `-p` failure
(`Cannot create property 'responsePromise' on object '&{20 0 0x...}'`)
noted but not yet chased.

`advisor()` flagged a real gap in that verification: `--version`/
`--help`/`-p hello` never actually reach a `.Check()` call on real
data (the eager `Compile()` calls at import time only prove the
module *loads*, not that a schema with real constructs *validates*
correctly), and the `ModelsConfigSchema` exercise used to verify
`#192` didn't cover `theme.js`'s other eager schema, which uses
`Type.Integer({minimum, maximum})` inside a `Type.Union` — a construct
`ModelsConfigSchema` doesn't have. Built that exact shape (string-or-
bounded-integer union, matching `theme.js`'s `ColorValueSchema`) and
ran a valid value, an out-of-range value, and `.Errors()` on the
failure through it — all three matched real Node exactly, including
the precise error-message strings (`must be string`, `must be <=
255`, `must match a schema in anyOf`). Both of `typebox/compile`'s
real eager call sites are now genuinely exercised, not just imported.
Also fixed the A-minus ledger's stale `glob` entry (still described as
"needs a real implementation" — it was actually deleted the group-B
way on `#180`, same as `minimatch`) while in the file for this.

**Twenty-second round (2026-09-02) — `jiti` investigated; a genuine,
severe parser bug found and filed as
[paserati#194](https://github.com/nooga/paserati/issues/194).** Real
usage: `core/extensions/loader.js` statically imports `createJiti`
from `jiti/static` at module scope (eagerly loaded regardless of
whether any extension actually runs — confirmed by reading the import,
not assumed), which itself statically imports jiti's real, vendored
`dist/jiti.cjs` (jiti bundles acorn for its own TS/JS transform).
Disabling the fake and importing that chain fails immediately —
`parsing failed: Syntax Error at 1:47226: ';' expected.` — not a
missing-feature gap but a parser bug on real, non-exotic code.

Pinpointed the exact failing construct using paserati's own lexer
directly (`scratch/jitidebug/tok`, a throwaway Go program dumping the
token stream around the reported column — far more reliable than
guessing at character offsets in 190KB of minified single-line JS, and
what actually cracked this after an initial mis-read of the position):
`for(e.body||(e.body=[]);this.type!==O.eof;){...}` — a for-loop
initializer that's a plain expression starting with a member access,
followed by `||`. Minimally reproduced standalone and confirmed
against a fresh `go clean -cache` plain-paserati build off `main`
(`9c8ca157`) — a real, current bug, not build-cache staleness.

Swept every operator below the suspect precedence boundary
(`||`, `&&`, `??`, `==`, `===`, `!=`, `!==`, `|`, `^`, `&`, plus every
compound-assignment operator) following a for-init starting with a
member expression, `this.x`/`this[x]`, or a paren/bracket/brace-led
expression — all fail the same way. Traced the exact root cause by
reading the parser itself (`pkg/parser/parser.go`,
`parseForStatementOrForOf` ~9206-9258 and
`parseRegularForStatementWithVar` ~9404-9470): those branches
deliberately parse the head with `parseExpression(LESSGREATER)` to
avoid swallowing a for-in's `in` (registered at exactly that
precedence) — correct for `in`, but it also stops before every other
operator below `LESSGREATER`, and the fallback function that's
supposed to continue parsing once for-in/for-of is ruled out only
knows how to handle a trailing `:`/`=` for `*LetStatement`/
`*ConstStatement`/`*VarStatement` heads, never the `*ExpressionStatement`
these branches actually produce.

`advisor()` caught that the initial "not affected: `for (e.x = 1; ...)`"
claim was checked by parsing only, not by running it — running it
turned up something worse than the operators that hard-error: plain
`=` parses with **no error at all** and silently discards the
assignment (`for (e.x = 42; false;) {}` leaves `e.x` unchanged) — the
worst class of bug this project chases, now found in the parser
itself rather than a fake. Filed with both variants, the operator
sweep as evidence, and (after `advisor()` also caught that the
first-drafted "resume the Pratt loop" fix suggestion isn't an
operation that exists in a Pratt parser) a corrected fix direction
citing the two real options and confirming there's no existing no-`in`
mechanism to reuse (`grep -rn "noIn\|allowIn\|NoIn" pkg/parser/*.go`
— nothing).

**Practical effect for `jiti`'s fake:** `#194` is a parse-time failure
on `jiti/static`'s own real module, reached eagerly at import time (not
gated behind actually loading an extension) — so once it lands,
`jiti`'s import chain should clear on all three scoreboard invocations
even though none of them exercise `createJiti()`'s actual lazy
`.import()` call (extension loading itself stays functionally
untested by the baseline scoreboard; a future round should still
exercise that real call pattern directly before deleting the fake, per
the standing measure-don't-assume rule). `jiti`'s fake stays in place
for now — no fix exists yet. No noderati code changes this round; full
build/vet/test unaffected.

**Twenty-third round (2026-09-02) — `pi-tui` investigated (while `#194`
is worked on); two real, independent bugs found and filed as
[paserati#195](https://github.com/nooga/paserati/issues/195) and
[paserati#196](https://github.com/nooga/paserati/issues/196).** By far
the largest group-B fake — every export a no-op, spanning `@earendil-
works/pi-tui`'s entire TUI component library — referenced from **90**
real `dist/` files. Confirmed it's pulled in eagerly regardless of
mode: `core/extensions/loader.js` statically imports the whole package
(`import * as _bundledPiTui from "@earendil-works/pi-tui"`), which is
itself eagerly loaded — the same shape as `jiti`'s blocker last round —
so `--version`/`--help`/`-p hello` all fail on it even though the TUI
only matters for interactive mode.

Disabling the fake fails immediately on real `dist/utils.js`'s very
first non-trivial line:
```js
const zeroWidthRegex = /^(?:\p{Default_Ignorable_Code_Point}|\p{Control}|\p{Mark}|\p{Surrogate})+$/v;
```
Two separate, independently-confirmed bugs on this one line:

1. **`#195`** — the `v` flag (ES2024 Unicode Sets mode) isn't
   recognized at all: `pkg/lexer/lexer.go`'s flag whitelist
   (`g, i, m, s, u, y`) is missing it, and the whole regex *literal*
   fails to parse as a result (`TS1109: Expression expected.`), not
   just the flag. Checking neighboring flags by hand turned up `d`
   (ES2022 `hasIndices`) missing too, with the identical failure mode
   — filed together since they're the same root gap (`grep`ing
   `pkg/vm/regex.go` confirms neither flag is handled anywhere
   downstream either, a complete absence rather than a lexer oversight
   with dead plumbing behind it).
2. **`#196`** — independent of `#195`, reachable today via the
   already-working `u` flag: `\p{Default_Ignorable_Code_Point}` isn't
   recognized by either regex engine — the same class of gap `#190`
   fixed, but `#190`'s fix (confirmed by reading its diff directly
   rather than assuming it generalized) is a hardcoded map of exactly
   `ID_Start`/`ID_Continue` and their UCD aliases, not a general
   derived-property mechanism. `\p{Control}`, `\p{Mark}`, and
   `\p{Surrogate}` in the same regex all already work fine.

`advisor()` caught a wrong claim in the `#196` draft: a literal
`RegExp` with this property appeared to construct without error and
only throw on `.test()`, which read like ordinary lazy compilation.
Checking construction vs. use on both the literal and `new RegExp(...)`
forms directly showed it isn't laziness — the two forms disagree, with
different underlying errors (the literal's `.test()` hits regexp2's
own "unknown unicode category" error; the constructor form throws
immediately with RE2's "invalid character class range," meaning
regexp2 was never even tried there) — the same constructor-vs-literal
fallback asymmetry `#190` already documented, not a new laziness
finding. Corrected before leaving it filed.

Both issues block on the exact same line, so fixing only one doesn't
move the scoreboard — `pi-tui`'s import chain needs both merged before
re-checking. Given the fake's sheer size (90 files, an entire component
library, unlike `typebox`/`jiti`'s handful of call sites), expect more
layers behind these two once they land, and its eventual deletion will
need a real functional exercise of TUI components specifically — the
three baseline invocations exercise none of it, only the import.

The shared paserati checkout had uncommitted, in-progress `#194`
changes (`pkg/parser/parser.go` + a new test script) during this whole
round — confirmed unrelated to this investigation and left completely
untouched (no stash/checkout/clean), per standing hygiene around a
resource this session doesn't own. `pi-tui`'s fake stays in place — no
fix exists yet. No noderati code changes this round; full build/vet/
test unaffected.

**Twenty-fourth round (2026-09-02) — `pi-agent-core` and `pi-ai`
investigated together (the user asked for both); three real findings,
two new paserati bugs filed as
[paserati#198](https://github.com/nooga/paserati/issues/198) and
[paserati#199](https://github.com/nooga/paserati/issues/199).** Bonus
observation on the way in: `#194` (last round's for-init parser bug)
landed on `origin/main` (`2123e0eb`) during this round — noted here for
the record; not re-verified/acted on this round since today's ask was
these two packages, not closing out `jiti`.

**Finding 1 — `pi-agent-core`'s "Class extends value undefined" isn't
a paserati bug at all.** Disabling only `pi-agent-core`'s fake failed
immediately with `TypeError: Class extends value undefined is not a
constructor or null`. Traced (not assumed) to the exact real line:
`pi-agent-core`'s real `dist/proxy.js` does
`import { EventStream, parseStreamingJson } from "@earendil-works/pi-ai";`
— the *bare* `pi-ai` specifier, not `/compat`. Reading `internal/host/
piai.go` directly: the bare `@earendil-works/pi-ai` fake
(`piAiShim`) only ever exported `modelsAreEqual` — `EventStream` and
everything else pi-agent-core needs live only in the separate
`piAiCompatShim` (`@earendil-works/pi-ai/compat`). So `EventStream`
genuinely was `undefined` on that import — a real gap in the fake's
completeness, not a bug in paserati or in pi-agent-core's real code.
Confirmed the fix isn't "patch the bare fake to add these exports"
(exactly the smaller-fake anti-pattern `docs/real-node-plan.md` itself
warns against) by disabling `pi-ai`'s fake alongside `pi-agent-core`'s:
`--version`/`--help` both then match baseline exactly. `-p` doesn't,
but for the next two reasons — not this one.

**Finding 2 — `pi-ai`'s real `-p` blocker: subclassing `Promise` and
setting an own property after `super()` throws.
[paserati#198](https://github.com/nooga/paserati/issues/198).** With
just `pi-ai`'s fake disabled, `--version`/`--help` match baseline;
`-p hello` (the one invocation that makes a real request) fails with
`TypeError: Cannot create property 'responsePromise' on object
'&{20 0 0x...}'` — a raw Go struct printed via `%v`, not a JS value,
immediately suspicious. Traced to `@anthropic-ai/sdk`'s real,
unmodified `core/api-promise.js`: `class APIPromise extends Promise`,
setting `this.responsePromise = ...` in the constructor after
`super()`. Minimally reproduced standalone; confirmed `Array`/`Error`/
`Map`/`Set` all handle the identical subclass-then-own-property
pattern correctly — only `Promise` is broken. Root-caused by reading
`pkg/vm/op_setprop.go`'s own-property type-switch directly:
`TypeRegExp`/`TypeMap`/`TypeSet`/`TypeArrayBuffer`/
`TypeSharedArrayBuffer` all have a dedicated case backed by a
`Properties` table; there's no `case TypePromise:` at all, so it falls
into the `default` branch's "not a plain object → throw in strict
mode" path (and ES modules are always strict). `PromiseObject` already
has a `prototype` field explicitly documented for subclassing support,
just no `Properties` table to match — filed with that exact gap and
the fix pattern already established by the other five cases.

**Finding 3 — `pi-agent-core`'s deeper blocker, once its own fake is
also off: `async` arrow functions derive `this` from the call site
instead of lexical capture.
[paserati#199](https://github.com/nooga/paserati/issues/199).** With
*both* `pi-agent-core` and `pi-ai` disabled, `--version`/`--help`
still match baseline, but `-p` fails differently than `pi-ai` alone —
`Cannot read property '_emitExtensionEvent' of undefined` — meaning it
doesn't even reach `#198` yet. Traced to `pi-coding-agent`'s real
`core/agent-session.js`: `_handleAgentEvent = async (event) => {
await this._emitExtensionEvent(event); }`, an ordinary auto-bind
class-field-arrow-function passed as a detached callback to
`agent.subscribe(this._handleAgentEvent)` — the single most common
idiom for this exact situation, expected to just work. It doesn't.

First characterization was wrong and `advisor()` caught it before
filing settled: the initial read was "detached calls lose captured
`this`, attached calls work," inferred from `s.handle("x")` (attached)
succeeding while `const f = s.handle; f("x")` (detached) failed.
`advisor()` pointed out that explanation contradicts arrow-function
semantics on its face — arrows ignore the call-time receiver entirely,
so "attached" shouldn't matter at all if capture were actually
working — and asked for a test that varies the receiver while holding
the arrow itself fixed. That test (`const h = obj.make()` returning an
async arrow that closes over `obj`; call `h()` bare, and
`{ h }.h()` through an unrelated `holder`) showed both calls give
**different** wrong answers, each exactly matching what a *regular*
(non-arrow) function's dynamic `this` would give for that call shape:
`undefined` for the bare call, the actual receiver (`holder`, which
has no matching property) for the member call. So the arrow isn't
failing to capture and falling back to some fixed wrong value — the
VM is computing its `this` as if `isArrowFunction` were false, full
stop, for the async case only. The earlier "detached" framing held
up only by coincidence (a class field's receiver equals its own
lexical capture when called through the same instance it's declared
on) — the corrected two-receiver repro rules that out. Confirmed
plain (non-`async`) arrows are unaffected in every shape tried, and
ordinary `async` methods (non-arrow) correctly use their dynamic
receiver — consistent with the "only isArrowFunction is being ignored
for async" diagnosis. Verified side by side against real Node.
Edited `#199` before anyone acted on the wrong framing. Not
root-caused to an exact line — flagged the likely area (async
function frame setup probably not checking `isArrowFunction` at all
before sourcing `this` from the call's receiver) rather than guessing
further into unfamiliar VM territory.

Confirmed on a fresh `go clean -cache` build off `main` (`e3059abf`)
for both issues. The shared paserati checkout moved to a new WIP
branch (`fix/for-init-member-expr-and-union-contextual-typing`) at
some point during this round, presumably other work landing — noticed
at the end, left completely untouched (this session did no checkouts
this round; the earlier builds used here were all taken while the
checkout was still on `main`, so the verification itself is
unaffected). Neither `pi-agent-core` nor `pi-ai`'s fake is safe to
delete yet — both are genuinely blocked by real, filed, unmerged
paserati bugs on their actual real-world call paths, not just
"doesn't crash the same way" as the fake. No noderati code changes
this round; full build/vet/test unaffected.

**Twenty-fifth round (2026-09-02) — `#198`/`#199` fixed on
[paserati PR #200](https://github.com/nooga/paserati/pull/200)
(unmerged), verified for information only.** Checked out the PR's
branch (`fix/promise-own-props-and-async-arrow-this`,
`a074b017`), `go clean -cache` rebuild first. Both issues' own
repros now give the correct result — `#198`'s `APIPromise` subclass
builds and its `responsePromise` reads back correctly; `#199`'s
two-receiver repro (`t13.mjs`, the one that replaced the original
wrong "detached calls" framing) gives `obj` for both call shapes,
matching real Node exactly. Re-ran every control case from both
issues too (not just the failing repros): `Array`/`Error`/`Map`/`Set`
subclassing still works (confirms the `Promise` fix didn't regress
the other five `op_setprop.go` cases it sits beside), plain arrow
functions and attached async methods still correct (confirms the
`this`-sourcing fix is scoped to the async-arrow case and didn't
touch the working paths). `advisor()` flagged that neither control set
covered the *actual* shape that motivated `#199` — a class-field async
arrow, capturing a constructor-time instance `this`, called through a
detached-callback subscribe wrapper (`t5`/`t6`/`classfield_arrow_test4`,
the closest stand-in for `agent-session.js`'s real
`_handleAgentEvent`) — since `t13`'s repro closes over an *object
method's* `this` instead, a different capture site
(`compileFunctionLiteralAsFieldInitializer` is a separate compiler
entry point from the plain-closure path `t13` exercises). Re-ran all
three: all pass, including the one with an internal `await` before the
`this.` reference — `#199` is now verified against the shape that
found it, not just the shape that minimized it. Full paserati
build/vet/test: clean.

Went past the isolated repros to the real call paths per the standing
rule: `pi-ai` alone and `pi-agent-core`+`pi-ai` together both now get
past `#198`/`#199` entirely on `-p hello` — the `Promise` and
`_emitExtensionEvent` errors are both gone — and converge on the
*same* next blocker: `URLSearchParams is not defined` (confirmed
`typeof URLSearchParams === "undefined"` directly, not inferred from
the error text). `advisor()` pushed on scope here too: noderati does
have a `url` module (`declareURL(p)`), and real Node exports
`URLSearchParams` both globally and from `node:url` — checked which of
those noderati is actually missing rather than assuming "the global
alias." Both: `import * as url from "node:url"; typeof
url.URLSearchParams` is *also* `undefined` (while `typeof URL` — the
class, not the search-params helper — is `"function"` and works
fine). So this isn't a one-line `DefineGlobal` next to `atob`/`btoa`
like the doc's first read suggested — `URLSearchParams` needs an
actual implementation (query-string parsing/serialization, iteration,
`.get`/`.set`/`.append`/`.toString()`), registered on both the global
and the `url` module's exports. Noted as the natural next thread for
either package, not implemented this round. `--version`/`--help` for
both configurations still match baseline. Switched the shared paserati
checkout back to `main` before finishing (it had moved to a different
WIP branch, `fix/for-init-member-expr-and-union-contextual-typing`,
since last round — unrelated, not touched; checked it back out
briefly, read-only, for this round's own verification, then back to
`main` again). Neither fake is deleted yet — `#198`/`#199` aren't
merged, and `URLSearchParams` is a new, separate, unaddressed blocker
regardless. No noderati code changes this round.

**Twenty-sixth round (2026-09-02) — `#198`/`#199` merged, confirmed;
`URLSearchParams` implemented; a new paserati bug found and filed
([paserati#201](https://github.com/nooga/paserati/issues/201)) before
either fake could actually be deleted.** `#198`/`#199` landed on
`origin/main` (`cde341ca`, confirmed directly). Clean-cache rebuild,
re-ran every repro and control from the last two rounds against the
merged commit — including the real class-field-arrow shape `advisor()`
had pushed for, not just the minimized one — all pass; full paserati
build/vet/test clean.

Implemented `URLSearchParams` (`internal/host/urlsearchparams.go`) —
the blocker both rounds ago identified precisely (missing from both
the global scope and `node:url`'s own exports, not a one-line alias).
Backed by an ordered `[][2]string` rather than Go's `net/url.Values`
(a map) specifically because insertion order — including which of
several same-named pairs comes first — is spec-observable via
`.toString()`/iteration, something a map would silently scramble;
reused `net/url.QueryUnescape` for decoding (parsing an incoming query
string). Scoped to what real code
actually needs (construction from a string/plain-object/array-of-
pairs, `append`/`delete`/`get`/`getAll`/`has`/`set`/`sort`/`toString`)
and explicitly not to what nothing here exercises yet (`.size`
getter, `Symbol.iterator`/`entries`/`keys`/`values`/`forEach`, copying
from another instance) — `ModuleBuilder.Class`'s reflection has no
getter or well-known-symbol support to hang the first group off of
today, and the doc-comment says so plainly, same discipline as
`url.go`'s own pre-existing "add it when something does" note for
`jsURL`. Verified every implemented method side by side against real
Node — exact match, including duplicate-name handling and `set()`'s
"replace first occurrence in place, drop the rest" semantics (not
"delete then re-append," which would move the pair to the end).
`advisor()` caught that `.toString()`'s original encoder (`net/url.
QueryEscape` for *encoding*, not just the decoding above) disagrees
with the WHATWG serializer on two characters: Go treats `~` as
unreserved (leaves it raw) and `*` as reserved (percent-encodes it);
the spec is the exact opposite (`~` encoded, `*` raw) — confirmed by
running an OAuth-shaped payload (colons, tildes, stars, parens, bangs,
quotes — the actual character classes in `pi-ai`'s real
`grant_type: "urn:ietf:params:oauth:grant-type:device_code"` value)
through both real Node and noderati side by side and diffing. Left
uncaught, this would have shipped `URLSearchParams` as "works" while
silently corrupting the wire bytes of exactly the OAuth request bodies
that motivated building it in the first place. Fixed with a small
hand-rolled `formURLEncode` implementing the spec's actual unreserved
set (`A-Z a-z 0-9 * - . _`, space→`+`, everything else percent-encoded
uppercase-hex UTF-8) instead of `QueryEscape`; added
`TestURLSearchParamsFormEncoding` pinning the exact OAuth-shaped
output, verified byte-for-byte against real Node. `net/url` is now
only used for decoding (`QueryUnescape`) on the parse side.

Registered via `m.Class` in `declareURL`, giving it automatic access
to noderati's existing (previously unexplained) mechanism that
promotes every native module's exports onto the bare global scope too
— traced that mechanism down while investigating (`Paserati.
registerNativeModuleExports`, which every `PreloadAllNativeModules`-
loaded module's exports go through, keyed by plain name into the VM's
global heap) rather than assuming an explicit `DefineGlobal` existed
somewhere unfound. Six new tests in `url_test.go` alongside the
existing `URL` ones.

Then hit a **third** real blocker exercising the actual call site —
`@anthropic-ai/sdk`'s `client.js` does `body instanceof URLSearchParams`
unconditionally on every request — and it **throws** instead of
evaluating `false`: `TypeError: Function has non-object prototype in
instanceof check`. Confirmed this isn't specific to the new class at
all: the pre-existing `URL` class throws identically on `instanceof`,
meaning this bug has been silently present since `URL` was first added
and nothing had exercised `instanceof` against it until now.
Root-caused precisely: `ModuleBuilder.Class`'s constructor
(`pkg/driver/native_module.go`'s `createClassConstructor`) builds
itself via `vm.NewNativeConstructor`, which allocates no `Properties`
table at all — unlike its sibling `vm.NewConstructorWithProps`, which
does — so there's nowhere for a `.prototype` property to even live;
`vm.go`'s `instanceof` handling has a working case for
`TypeNativeFunctionWithProps` but falls through to nothing for a bare
`TypeNativeFunction`, and what real Node treats as "not found on the
prototype chain, so `false`" paserati treats as "no valid prototype
object, so throw." Filed with the fix direction (swap to
`NewConstructorWithProps`, set an actual `.prototype` object) since
the `TypeNativeFunctionWithProps` branch already handles the rest.

**Net effect: neither fake is deleted yet.** `pi-ai` alone now clears
`#198`/`#199`/`URLSearchParams`-missing and hits `#201` instead — the
scoreboard's `fake-off:pi-ai` row is a different failure signature
each of the last three rounds, each one strictly further into the
real request path than the last, which is the actual measure of
progress here even though no row has gone clean yet.
`fake-off:pi-agent-core` is unchanged (still needs `pi-ai`'s fake off
too — Finding 1 from two rounds ago). Noticed in passing while running
the full scoreboard: `fake-off:jiti`'s error also changed since `#194`
landed (now `expected identifier, string literal, or computed property
name after 'async' in async method`, a different position in the same
bundled acorn) — another "fix uncovers the next layer" case, not
investigated this round; flagged for whoever picks up `jiti` next.
Full build/vet/test, all three real `pi` invocations, full scoreboard:
clean (baseline unaffected by the new module).

**Twenty-seventh round (2026-09-02) — chased down `jiti`'s new
async-method parser error, flagged at the end of the last round; found
and filed two distinct paserati bugs
([paserati#203](https://github.com/nooga/paserati/issues/203),
[paserati#204](https://github.com/nooga/paserati/issues/204)); no
noderati code changes.** User also confirmed `#201` is being actively
worked on upstream.

Traced the scoreboard's `fake-off:jiti` error (`expected identifier,
string literal, or computed property name after 'async' in async
method` at `jiti.cjs:1:189764`) to real source this time by locating
the byte offset directly in the bundle rather than guessing — found
`async import(e,t){...}`, a plain object literal method literally
named `import` (jiti's own `import()`/`require()`/`esmResolve()`
surface object), with `async` in front of it.

Minimized against a clean-cache plain-`main` build (`cde341ca`) —
built from the shared paserati checkout, which had `#201`'s in-
progress fix sitting as an *uncommitted* diff to `pkg/driver/
native_module.go` on the `fix/module-builder-class-instanceof` branch
at the time; checked out `main` for the plain-baseline build (the
uncommitted diff followed across the branch switch untouched, as git
does for any non-conflicting local modification — confirmed via `git
diff` before and after), then switched back to their WIP branch
immediately after. Mid-investigation, that uncommitted diff turned
into a real commit (`7a55fb39 fix(driver): ModuleBuilder.Class
constructors support instanceof`) on the same branch, matching
`origin` — nothing of theirs was at risk at any point, confirmed via
`git diff origin/... --stat` before finishing and switching the shared
checkout back to `main` as usual.

**`async import(e) {}` reproduces standalone**: `const o = { async
import(e, t) { return e; } }` fails identically to jiti's real error.
Swept every ECMAScript keyword as an `async <name>(e) {}` object-
literal method name to scope it: `import`, `class`, `delete`,
`typeof`, `default`, `new`, `this`, `super`, `static`, `of`, and
`async` itself all fail; only `yield`/`await`/`get`/`set` (plus
ordinary identifiers) work — because those four happen to be the only
ones the async-method branch's hand-maintained allowlist includes.
The generator-method branch (`*foo()`) has an exact copy of the same
narrow list and the same bug — `advisor()` pushed back on this being
asserted from reading the code alone (the async-generator form
consumes `*` first and could plausibly fall through into either
branch's list), so confirmed directly with two isolated cases:
`*import(e) { yield e; }` (plain generator, no `async`) fails with the
generator branch's own error text, while `async *class(e) { yield e;
}` (async generator) fails with the *async* branch's error text —
proving `async *foo()` falls into the async branch's list after
consuming `*`, so both hardcoded lists genuinely need the fix, not
just one read as "the same" from source. Confirmed **not** affected: plain
(non-async) object-literal methods (`import(e) {}` parses fine for
every keyword tested) and class methods of any kind including `async`/
generator (`class C { async import(e) {} }` works) — both already go
through more general property-name-parsing paths. Root-caused to
`pkg/parser/parser.go`'s two method-name branches (~7159, ~7294) each
hand-listing a handful of token types instead of calling the file's
own existing general-purpose helpers built for exactly this
(`parsePropertyName()` ~8516, or `isIdentifierNameToken()` ~24) the
way getters/setters and plain shorthand methods already do. Filed
[paserati#203](https://github.com/nooga/paserati/issues/203) with the
fix direction (swap both hardcoded lists for the existing helper).

**Bonus finding while scoping #203's control cases**: testing every
`FutureReservedWord` (`static`, `implements`, `interface`, `package`,
`private`, `protected`, `public`) as a plain (non-async) object-
literal method name surfaced a second, differently-rooted bug — all
seven fail to *compile* (not parse) with `SyntaxError: Unexpected
strict mode reserved word '<name>'`, though real Node accepts all
seven fine. `advisor()` caught that the issue's original "not
affected: class methods" claim had only actually tested one of the
seven words (`implements`) plus one inconclusive `static` case with no
output to check — swept the full class-body matrix properly (all
seven, each called and its return value printed, against both
paserati and real Node): all seven pass in both, confirming the
scoping claim but only after actually running it, not asserting it
from a single example. Root-caused to `pkg/compiler/compile_literal.go`:
`compileObjectLiteral` synthesizes a method's `FunctionLiteral.Name`
from its property key purely so the function's own `.name` reflects
correctly at runtime (~line 828); `compileFunctionLiteralWithOptions`'s
strict-mode name validation (~line 1181) then treats that synthetic
display name exactly like a real named-function-expression binding
identifier, without consulting the `isMethod` flag it already has
available — applying a check that's spec-mandated only for actual
`BindingIdentifier` positions to a `PropertyName`, which is
categorically exempt. Filed
[paserati#204](https://github.com/nooga/paserati/issues/204),
cross-referencing #203, with the fix direction (guard the check with
`!isMethod`).

Not yet confirmed whether either bug is on jiti's *only* remaining
blocker path — `fake-off:jiti` may hit further real gaps once `#203`
lands, same pattern as every other fake this session. No noderati
code changed this round; nothing to rebuild or re-verify against the
scoreboard.

**Twenty-eighth round (2026-09-02) — user reported `#201`'s fix is in
the local paserati checkout: verified it (PR#202, unmerged), then
pushed the verification past the network dial and found paserati's
next real blocker — `fetch()`'s streaming `Response.body` is entirely
unimplemented ([paserati#205](
https://github.com/nooga/paserati/issues/205)). No noderati code
changed; deletion of both fakes remains deferred, now on `#205` in
addition to `#201`/`#203`/`#204`.** Confirmed `#201`'s fix is PR#202
(`7a55fb39` on `fix/module-builder-class-instanceof`), not yet merged
to `origin/main` — per standing rule, verified there for information
only.

Checked out the branch (clean, matched `origin`), clean-cache
rebuilt, ran paserati's own full test suite (clean) and noderati's
(clean, one flaky `TestSpawnEcho` failure that passed on rerun and in
isolation — unrelated to anything this round touched). Directly
verified `#201`'s repro: `new URL(...) instanceof URL` now `true`,
non-`URL` objects correctly `false` (not throwing), a `class Sub
extends URL` subclass correctly `instanceof` both `Sub` and `URL` with
inherited `.href` intact; same matrix repeated for `URLSearchParams`.
All match real Node exactly.

Reran the real `pi` invocations with `pi-ai`'s fake off: `--version`/
`--help` still match baseline; `-p` no longer throws the instanceof
error — it fails at the network dial instead (no local model server),
with a *different* error text than noderati's own scoreboard baseline
row (`ERROR: Connection error.` vs. the raw Go dial error the fake
produces). Checked which one is actually correct by running the exact
same invocation under **real Node** (not noderati's own fake-based
baseline — the actual ground truth): real Node also prints `Connection
error.` on the identical `-p` call, byte-for-byte matching the real
`pi-ai` path through noderati. So this is real progress captured
incorrectly by the scoreboard's literal diff check — the "baseline" row
is stale because it was generated by the fake, which never modeled
this path faithfully to begin with; matching real Node is the metric
that actually matters and the real path now does, past this point.
Same result with both `pi-ai` and `pi-agent-core` fakes off together.
One divergence from real Node persists on this path and is scoped out
deliberately, not overlooked: real Node exits `0` on this connection-
refused `-p` invocation, noderati exits `1` — confirmed present in
noderati's own baseline (fakes on) too, so it's a pre-existing,
noderati-wide error/exit-code convention difference unrelated to
`#201`/`URLSearchParams`/streaming, not a fresh regression from this
round's work.

`advisor()` flagged that a bare "connection refused" match doesn't
exercise anything downstream of the first network attempt — the
response/event path is exactly where `#198`/`#199` lived, so proving
the request path matches proves nothing about the response path. Stood
up a local stub HTTP server first with a plain JSON body (not a valid
SSE stream — deliberately wrong shape, caught immediately: real Node
itself errored differently on it too, confirming the stub needed to
actually speak the protocol, not just return *something*), then a
proper OpenAI-format SSE stream (`data: {...}\n\n` chunks terminated
by `data: [DONE]\n\n`). Against that stub: real Node prints the
streamed content correctly and exits 0. noderati's own `pi-ai` **fake**
(baseline, fakes on) errors with `unexpected completions response` —
a different wrong answer, confirming the fake's simplified HTTP mock
never modeled streaming at all. noderati with `pi-ai`'s real package
(fake off, `#201` fix applied) errors with `Attempted to iterate over
a response with no body` — traced to the OpenAI/Anthropic/Google SDKs'
shared `_iterSSEMessages` helper checking `response.body` and finding
it falsy.

Root-caused directly in paserati source (confirmed unaffected by
PR#202 — `git diff main fix/module-builder-class-instanceof --stat`
shows only `native_module.go`/its test touched, so this reproduces
identically on plain `main`): `pkg/builtins/fetch_init.go`'s
`createResponseObject` never sets a `.body` property on the `Response`
object at all (the whole implementation reads the HTTP response
eagerly via `io.ReadAll` and buffers it), and `ReadableStream` doesn't
exist as a global (`typeof ReadableStream === "undefined"`) —
`pkg/builtins/blob_init.go` already has a stub acknowledging this
("ReadableStream would require significant infrastructure"). Traced
the SDKs' actual minimal required surface (`node_modules/openai/src/
internal/shims.ts`'s `ReadableStreamToAsyncIterable`): not the full
Web Streams API, just `[Symbol.asyncIterator]` or a `getReader()`
returning `{read(): Promise<{done, value}>, releaseLock(), cancel()}`
— included as a scoping note in the filed issue. Filed
[paserati#205](https://github.com/nooga/paserati/issues/205) with
that scope and a concrete fix direction (minimal `ReadableStream`
backed by a Go channel/callback feeding chunks off the HTTP
connection as they arrive, `fetch()` wired to it, `text()`/`json()`/
`arrayBuffer()` unaffected since they can keep draining eagerly
internally, `Blob.stream()` wired to the same primitive once it
exists).

**Net effect: `#205` is confirmed as the *next* blocker for `pi-ai`'s
(and thus `pi-agent-core`'s) real streamed `-p` flow, not confirmed
as the *last* one.** Everything from OAuth body construction through
the actual network request now matches real Node exactly (modulo the
pre-existing exit-code note above); the streamed-response read is
unimplemented, and that's as far as this round's stub reached — real
Node consuming the stub's chunks and printing the streamed content
proves the entry point is where noderati stops, not that everything
downstream of a working `ReadableStream` (response parsing, tool-call
dispatch, `pi-agent-core`'s real event emission through
`_handleAgentEvent`, the exact path `#198`/`#199` came from) also
works end to end once `#205` lands — that stays unverified until a
stream is actually consumed past this point. Deletion of both fakes
stays deferred: `#201` needs to merge to `main` (currently PR#202),
and `#205` needs a real implementation, neither of which happened
this round. No noderati
code changed — pure upstream investigation and verification, same as
last round.

**Twenty-ninth round (2026-09-02) — status survey ("let's see where we
stand") after a large batch of upstream paserati merges; two genuine
noderati bugs found and fixed live; two new paserati gaps found and
filed (`#210`), one comment-updated (`#205`); `#195`/`#196` status
check folded in.** Checked the shared paserati checkout: `origin/main`
had moved to `9c9532b8`, carrying `#201` (`2b68032c`), `#203`/`#204`
(`cc64d772`), `#205`'s `ReadableStream` primitive (`2c346301`),
`#195`/`#196` (`1a61ca80`/`07eca5ff`), and one more fix not tied to
any issue filed this session (`9c9532b8` itself, "synchronize Promise
state/reactions against goroutine-driven settlement"). Switched the
shared checkout to `main` (was on an unrelated, already-merged WIP
branch, `fix/promise-goroutine-race`, whose tip content had landed on
`main` under a different commit hash — ordinary squash/rebase, nothing
of concern). Clean-cache rebuilt; paserati's own full test suite:
clean.

Verified every merged fix directly against this build (all
`-no-typecheck`, since several of these repros hit narrow, pre-existing
TS *type*-checker gaps unrelated to the runtime fix itself — e.g.
`.match()`'s return type still doesn't include `.indices` for a
`d`-flagged regex, a real but separate, narrower gap not chased this
round):
- `#195` (regex `d`/`v` flags): `/abc/v`, `/abc/d` both construct;
  `.exec()` on a `d`-flagged pattern with a named group populates
  `.indices` and `.indices.groups` correctly.
- `#196` (`\p{Default_Ignorable_Code_Point}`): `.test()` no longer
  throws, matches correctly.
- `#198`/`#199`: re-confirmed once more on this now-further-advanced
  `main`.
- `#201` (`instanceof`): re-confirmed via noderati's own `URL`/
  `URLSearchParams`, including the subclass-`instanceof`-both matrix
  from the PR-branch verification two rounds ago — unchanged now that
  it's actually on `main`.
- `#203`/`#204` (object-literal method names): `async import(e,t){}`,
  `*class(e){}` (generator), `static(e){}`, `implements(e){}` — all
  construct and call correctly now.
- `#205` (`ReadableStream`): confirmed `typeof ReadableStream ===
  "function"` — but then confirmed, against the same real SSE stub
  server from two rounds ago, that `fetch()`'s `Response.body` is
  *still* `undefined` and the real `pi-ai` streaming client still
  throws identically (`git show 2c346301 --stat` confirms the commit
  touches only the new `readable_stream_init.go` file plus tests —
  `fetch_init.go` untouched). Left a comment on
  [#205](https://github.com/nooga/paserati/issues/205) distinguishing
  "the primitive landed" from "the bug this issue reports is still
  open" — the issue itself was correctly left open by whoever merged
  the primitive, not auto-closed, and this comment records precisely
  why so nobody has to re-derive it.

**Note for whoever next checks issue status**: `#203` and `#204` are
functionally fully fixed (verified above) but both still show `OPEN`
on GitHub — evidently a bookkeeping gap in how `cc64d772`'s commit
message referenced them (no `Closes #NNN`), not a sign anything is
unfixed. Confirmed this by testing, not by trusting either state.

**`#195`/`#196` were the exact two issues the user separately asked
about verifying this round** (last checked as "both open, unaddressed"
one round ago) — both are the merged/confirmed-fixed pair above; no
separate re-narration needed since this round's general sweep already
covered them precisely.

Rebuilt noderati against the fully-updated `main` and reran the full
scoreboard — caught and killed a leftover stub HTTP server (from the
prior round's `#205` verification, still running on `:1234`) that had
silently contaminated the first run's `baseline` row before reading
too much into it; reran clean. Net result across all five fakes:
zero clean rows still (nothing safe to delete yet), but real,
substantial forward movement on two of them:

- **`fake-off:pi-tui`**: no longer blocked by `#195`'s parse failure —
  now reaches and executes real code, immediately hitting a brand-new
  blocker: `Intl` doesn't exist as a global at all. Traced to
  `dist/utils.js`'s module-scope `new Intl.Segmenter(undefined, {
  granularity: "grapheme" | "word" })` (real, default-locale grapheme/
  word segmentation for terminal text measurement, paired with
  `get-east-asian-width`). Filed
  [paserati#210](https://github.com/nooga/paserati/issues/210), scoped
  precisely to what's actually used (not full ECMA-402 — just
  `Intl.Segmenter`'s two granularities at the default locale, which
  Unicode's own UAX#29 algorithms don't need locale data for).
- **`fake-off:jiti`**: no longer blocked by `#203`'s parse failure —
  and this is where the round's real noderati-side work happened (see
  below). After two live fixes, now blocked by `node:vm` (specifically
  `vm.runInThisContext`, jiti's actual TS-execution mechanism) — a
  substantial, real `vm`-module gap, not attempted this round (unlike
  `node:v8` below, this isn't a quick honest stub — real script
  execution semantics are the whole point of the module). Flagged as
  next round's natural continuation.
- `fake-off:pi-ai` / `fake-off:pi-agent-core`: unchanged (still `#205`
  and the `pi-ai`-fake-off prerequisite, respectively).

**Two genuine noderati bugs found and fixed live this round** (not
filed anywhere — noderati's own gaps are tracked in this doc, not
GitHub issues, since this session does noderati's own fixing directly):

1. **`OSPathResolver` never CJS-wrapped a relative `.cjs` import — the
   round's real find.** Traced jiti's `require is not defined` (after
   `#203`/`#204` cleared the parse failure) precisely: the real
   consumer is `dist/core/extensions/loader.js`'s `import { createJiti
   } from "jiti/static"`, resolving via jiti's own `package.json`
   `exports["./static"].import` to `lib/jiti-static.mjs`, which itself
   does `import _createJiti from "../dist/jiti.cjs"` — a completely
   ordinary relative static-ESM-import of a `.cjs` file, real Node's
   own dual-package CJS/ESM interop. First hypothesized (wrongly, and
   said so before filing anything) that this was a resolver-honesty
   bug — `require.resolve("jiti/dist/jiti.cjs")` genuinely throws
   `ERR_PACKAGE_PATH_NOT_EXPORTED` under real Node, since that subpath
   isn't in jiti's `exports` map — but then found `lib/jiti-static.mjs`
   reaches `dist/jiti.cjs` via a *relative* require/import from
   *inside* the package, which `exports` restrictions don't apply to
   at all (they only gate external bare-specifier resolution). Root
   cause was instead in `nodemodules.go`'s existing, correct
   `.cjs`-detection-and-wrapping machinery
   (`openMaybeCJS`/`shouldWrapCJS`/`cjsESMWrapper`, already used by
   `NodeModulesResolver` for bare-specifier imports) simply never being
   called by `osresolver.go`'s `OSPathResolver.Resolve` — the resolver
   that handles every `./`/`../`/absolute-path specifier — which read
   the file as plain source with `os.ReadFile` and no wrapping at all.
   Minimized to a 7-line, jiti-independent repro (`foo.cjs` +
   `main.mjs`, `import greet from "./foo.cjs"` where `foo.cjs` does
   `require("path")`) diffed directly against real Node before writing
   the fix, so the fix targets the actual interop rule, not jiti's
   particular shape. Fixed [osresolver.go](../internal/host/osresolver.go)
   by routing `OSPathResolver.Resolve` through the same `openMaybeCJS`
   `NodeModulesResolver` already uses. Verified against the minimal
   repro (now matches real Node exactly) and against the real
   dependency chain — the post-fix stack trace for the *next* error
   (below) cleanly shows every real frame (`jiti.cjs` ← `jiti-
   static.mjs` ← `loader.js` ← `resource-loader.js` ← `agent-session-
   services.js` ← `agent-session-runtime.js` ← `main.js`), proof the
   fix works through the genuine graph, not just the isolated repro.
   New test: `TestImportRelativeCJSFile`
   ([osresolver_test.go](../internal/host/osresolver_test.go)).

2. **`node:v8` didn't exist at all**, the very next blocker jiti's real
   dependency chain hit once (1) was fixed. jiti's real `dist/jiti.cjs`
   does `require("node:v8")` at module scope purely to call
   `v8.startupSnapshot.isBuildingSnapshot()` inside a `try {} catch
   {}` — so even an empty module would have silently unblocked this
   one call site, but implemented a small, *honest* slice instead (not
   a jiti-shaped patch): `getHeapStatistics()` returns real numbers
   from Go's own `runtime.MemStats`, `setFlagsFromString()` is a
   documented genuine no-op (there's no V8 to configure), and
   `startupSnapshot` is a real namespace with `isBuildingSnapshot()`
   correctly, always returning `false` (paserati never runs from a V8
   snapshot) plus no-op callback registrars for the same reason.
   `advisor()` caught that the first cut of `getHeapStatistics()` set
   `heap_size_limit` to `HeapSys` — which moves in lockstep with
   `total_heap_size`, so any real caller's "am I near the cap?" check
   (the actual thing that field exists for) would silently never fire.
   Fixed to read Go's own soft memory limit
   (`debug.SetMemoryLimit(-1)`, falling back to "no limit configured"
   the same way Go itself reports that) as the real ceiling, deriving
   `total_available_size` from it. New file
   [v8.go](../internal/host/v8.go), registered in `installModules`
   (host.go); new tests `TestV8Require`/`TestV8RequireViaCJS`
   ([v8_test.go](../internal/host/v8_test.go)) — the latter exists
   because `import "node:v8"` and `require("node:v8")` turned out to
   go through genuinely different lookup paths (see finding below),
   and only testing one would have missed that they'd diverge.

   Along the way, found that `require("node:v8")` specifically (as
   opposed to `import`) still failed after `v8.go` was declared and
   registered — `declareV8(p)` alone wasn't enough. Root cause:
   `cjs.go`'s `nativeRequireNames` is a **second, separately
   hand-maintained** map of "which module names `require()` should
   route to a native module" — the exact same drift shape `#203`/`#204`
   were about (a hardcoded list next to code that already knows the
   full set, guaranteed to miss the next addition). `driver.Paserati`
   doesn't currently expose a way to enumerate declared module names to
   derive this automatically (checked, per `advisor()`'s prompt, before
   accepting the drift) — added `v8` to the list and added a doc
   comment on `nativeRequireNames` itself flagging the drift risk
   explicitly for the next person adding a `declareX(p)` module, rather
   than silently leaving it to repeat.

Full build/vet/test, all three real `pi` invocations, full scoreboard
(rerun after killing the contaminating stub): clean. Shared paserati
checkout left on `main`, up to date with `origin/main`, clean working
tree.

**Net effect**: real, substantial forward movement on two of the five
fakes (`pi-tui`: now blocked on `Intl.Segmenter` instead of a parse
failure; `jiti`: now blocked on `vm.runInThisContext` instead of a
parse failure, after fixing two genuinely load-bearing noderati bugs
along the way), zero regressions, still zero scoreboard rows clean
enough to delete a fake. `pi-ai`/`pi-agent-core` unchanged, still on
`#205`. Newly filed/updated: `#210` (Intl.Segmenter, new), `#205`
comment (primitive-vs-bug-still-open, informational). Newly fixed in
noderati directly: `OSPathResolver`'s CJS-interop gap (likely the
single highest-value fix of this round — real ESM-imports-relative-
CJS is completely ordinary Node interop, not a jiti-specific pattern,
so this probably helps other real packages too, not just jiti);
`node:v8` (small, honest, real implementation, not a stub scoped to
one call site).

**Thirtieth round (2026-09-02) — user reported `#205` closed and
checked out locally; verified it, found it genuinely fixes streaming
but surfaced two more real gaps, one of them a regression severe
enough to change this project's own scoreboard baseline text.**
Confirmed `#205`'s actual fix landed as `dbf6d62d` on `origin/main`
(separate from the `2c346301` primitive two rounds ago — that only
added the `ReadableStream` type; this commit is what wires `fetch()`'s
`Response.body` to it). Clean-cache rebuilt; paserati's own full test
suite: clean.

**`#205` itself is genuinely fixed** — verified against the same real
SSE stub server as two rounds ago: `typeof response.body === "object"`
now (was `"undefined"`), `.getReader()` exists, and reading through it
returns the complete, correct bytes (confirmed by dumping the actual
byte array and decoding it by hand, not just checking a length) — the
earlier "only 19 bytes, no `[DONE]` terminator" first read looked like
a `ReadableStream` bug, but the raw bytes it returned were the full,
correct SSE payload; the appearance of truncation was entirely
`TextDecoder.decode()` silently failing to decode a real `Uint8Array`,
not the stream dropping or truncating data. (This round's stub sent
all four SSE frames back-to-back with no gap, so it doesn't itself
distinguish genuinely-incremental delivery from an already-buffered
body handed back in one piece — `dbf6d62d`'s own `fetch_stream_test.go`
already covers that timing question upstream, with a real gap between
chunks, so it wasn't re-verified here.) Re-aimed the investigation at
`TextDecoder` instead of filing against `#205`.

**Found and filed `#212`** — `TextDecoder.decode()` never handles a
real `Uint8Array`/`ArrayBuffer` at all; `pkg/builtins/text_codec_init.go`
only checks `vm.TypeArray` (a plain JS array), so any real typed-array
input falls through to a generic `.ToString()` fallback, producing
literal `"[object Uint8Array]"`/`"[object ArrayBuffer]"` text instead
of decoding anything. Confirmed broadly (bare `Uint8Array`,
`ArrayBuffer`, and a byte-offset subarray view all fail the same way)
before filing, and root-caused to the exact missing branch
(`vm.TypeTypedArray`, `AsTypedArray()`/`GetBufferData()`) rather than
just describing the symptom. This is likely the actual remaining
blocker for any real streaming text consumer (the OpenAI/Anthropic/
Google SDKs' shared `LineDecoder` all `.decode()` each raw chunk) —
more foundational than `#205` was, since `Uint8Array` is what
`TextEncoder.encode()` itself returns and what every stream chunk is.

**Found and filed `#213` — a genuine regression, not a pre-existing
gap.** Rebuilding noderati against the fixed `#205` broke a previously
green test, `TestPiAiStreamSimpleFetchError` (expects a real
"connection refused"-shaped error from `pi-ai`'s fake hitting an
intentionally-unroutable address). The rejected error changed from a
real Go dial error to `"AbortError: The operation was aborted"` — with
**no `AbortSignal` anywhere in the call**. Confirmed this was newly
introduced by `dbf6d62d` (not present before) by running the exact
same bare, `AbortSignal`-free repro against the still-available
pre-`#205` binary from two rounds ago: it correctly reported the real
dial error there. Root-caused precisely by reading `dbf6d62d`'s own
diff (not inferring from the new code alone — `advisor()` pushed for
the actual `git show` diff of the old code's `cancel()` handling
before accepting the narrative): the pre-fix outer goroutine did
`defer cancel()` in the *same function* as its `ctx.Err() ==
context.Canceled` check, so that `defer` only ran *after* the check
had already read `ctx.Err()`'s real value; `dbf6d62d` moved `cancel`
ownership into the callee (`doFetchRequestWithContext`), whose own
internal cleanup now fires *before* returning control to the caller,
on every single error path — so the check that used to distinguish
"a real abort happened" from "the request just failed" now reads
`context.Canceled` unconditionally after any network failure at all,
and the real underlying error is discarded. Filed with that precise
before/after mechanism, not just the symptom, plus a suggested fix
direction (track whether the abort-polling goroutine's own
`abortOnce.Do` actually fired, rather than trusting `ctx.Err()`, which
`dbf6d62d` made unconditionally true).

This regression is severe enough that it changed this project's own
scoreboard **baseline** row's literal text — `-p "hello"` against real
`pi-coding-agent` (fakes on, no local model server) now prints `ERROR:
AbortError: The operation was aborted` instead of the historic `ERROR:
Post "http://...": dial tcp ...: connect: connection refused`, purely
because the fake's own bespoke `completeOnce()` hits the same
paserati-level bug. Recorded here explicitly so a future round doesn't
mistake the new baseline text for a fresh noderati-side problem — it
isn't; it's `#213`, tracked upstream. Notably, `fake-off:pi-ai`'s row
(the *real* package) still matches real Node exactly despite this:
the real OpenAI SDK wraps every `fetch()` failure into its own generic
`"Connection error."` message regardless of the underlying error
type, so the regression doesn't change `pi-ai`'s real *observable*
behavior in this specific scenario — though it would still matter for
any real code that branches on `error.name === "AbortError"` vs a
plain network failure, which is exactly the class of bug `#213`
documents.

**Found and filed `#214`** while investigating `#213`: `fetch()`
rejects with a bare *string*, never a real `Error`/`TypeError`/
`DOMException` instance (`typeof e === "string"`, `e instanceof Error`
is `false`, `.name`/`.message` are `undefined`) — confirmed
pre-existing (present on the build immediately before `dbf6d62d` too,
not part of the regression), root-caused to `fetch()`'s manual
`RejectPromise(promiseObj, vm.NewString(...))` calls bypassing
whatever real-`Error`-construction path native-function errors
normally go through.

**`TestPiAiStreamSimpleFetchError` is deliberately left failing, not
skipped.** First instinct was `t.Skip` with a comment, reasoning
"the suite must report clean" — `advisor()` correctly called this out
as backwards: this project's own rule is that a known gap stays
*visible* until fixed upstream (the scoreboard's DIFF rows are never
hidden either), not masked from the next person's test run. Reverted
to a plain failing test with a comment explaining the exact mechanism
and linking `#213` — it goes green on its own once the fix lands, and
nobody has to remember to re-enable it.

**Twice this round, a leftover stub HTTP server from earlier
verification work silently contaminated a scoreboard/CLI run** before
being caught (once via an unexpected `unexpected completions response`
baseline text, once by simply remembering to check `lsof -i:1234`
before trusting a "-p" result) — both times killed and the run
redone before drawing any conclusion from it.

No noderati code changed this round except the one test file
(`piai_test.go`, un-skip + comment). Full build/vet/test (one failure,
tracked, expected: `TestPiAiStreamSimpleFetchError`/`#213`; one
confirmed-flaky child-process test unrelated to anything touched this
round), full scoreboard, three real `pi` invocations: all consistent
with the picture above, zero unexplained regressions. Shared paserati
checkout left on `main`, clean, up to date with `origin/main`.

**Net effect**: `#205` genuinely fixes streaming (verified, not just
trusted from the closed-issue label), but exercising it precisely
surfaced two more real gaps — one a straightforward missing feature
(`#212`, `TextDecoder`), one a real regression (`#213`) serious enough
to reach into this project's own baseline — plus one more pre-existing
gap found in passing (`#214`). None of `pi-ai`'s fake's blockers are
fully cleared yet: even once `#212`/`#213` land, real SSE text
decoding through the real SDK stack is still unverified end-to-end.
`#210` (`Intl.Segmenter`) is "in the works" per the user, not yet
checked out anywhere accessible this round — nothing to verify there
yet.

**Thirty-first round (2026-09-02) — user reported a large batch of
paserati merges; pulled `main`, verified all four outstanding issues
fixed, ran the full sweep, found and filed one more.** `origin/main`
had gained six commits since last round: `fc187d9d` (fixes `#212`,
`#213`, `#214` together), `19b41dad` (fetch's JSON-body `TypeError`
built safely off the VM goroutine), `078b5ce7` (a throwing
`toString`/`valueOf` accessor now propagates out of string coercion
instead of being swallowed), `27073103` (`#210`, `Intl`/
`Intl.Segmenter`), `e89e75b3` (strings kept as canonical WTF-8,
sliced/searched by UTF-16 code unit), `ee6f1aa6` (spreading/
destructuring a string as an iterable). Switched the shared checkout
off an unrelated already-merged WIP branch (`feat/intl-segmenter`,
whose tip had landed on `main` the same way `fix/promise-goroutine-
race` did two rounds ago) onto `main`, fast-forwarded, clean-cache
rebuilt. Paserati's own full test suite: clean.

**Verified all four fixes directly against their exact filed repros**,
not just trusted the closing commit references:
- `#210`: `new Intl.Segmenter(undefined, {granularity: "grapheme"})`/
  `"word"` both construct and segment correctly (`"héllo"` → `h|é|l|l|o`
  as graphemes, `"hi there"` → `hi| |there` as words).
- `#212`: `TextDecoder.decode()` now correctly decodes a bare
  `Uint8Array`, an `ArrayBuffer`, and a byte-offset subarray view — the
  filed repro's single-byte view (`ij` vs `i`-with-no-offset would
  look the same by coincidence) was re-checked with a genuinely
  distinguishing two-byte offset view (`new Uint8Array([104,105,106,
  107]).buffer` sliced `(1, 2)` → `"ij"`, not `"hi"` or `"jk"`) before
  trusting that offset handling, not just presence, is correct.
- `#213`: the exact bare, `AbortSignal`-free repro that previously
  produced a fabricated `AbortError` now rejects with the real
  `TypeError` carrying the actual dial-refused message.
- `#214`: that same rejection is now a genuine `Error` instance
  (`instanceof Error` true, `.name`/`.message` populated), not a bare
  string.

Rebuilt noderati against the fully-updated `main`: full test suite is
now **fully green**, including `TestPiAiStreamSimpleFetchError` — the
test left deliberately failing (not skipped) two rounds ago to track
`#213` — passing again on its own, unedited, is itself confirmation
the regression is genuinely fixed, not just that the issue was closed.
Updated its doc comment to record the fix landed in `fc187d9d` rather
than leaving the "currently failing" framing stale. Rebuilt the
noderati binary and reran the three real `pi` invocations: the
baseline `-p "hello"` error text is back to the historic, correct
`dial tcp ...: connect: connection refused` — `#213`'s effect on this
project's own baseline is fully gone.

**Full scoreboard rerun — real, further movement on `pi-tui`, mixed
signal on `pi-ai`.** `pi-tui` is now past `Intl` entirely (thanks to
`#210`) and hits a new blocker: `numbered backreferences like \1 are
not supported (Go regexp limitation)`, thrown with no informative
stack trace (a `SyntaxError` from regex compilation, before any JS
exception unwinding, so the exact line inside pi-tui's minified bundle
wasn't pinned down). Minimized standalone: `new RegExp("(a)\\1")`
throws that error, while the exact same pattern as a **literal**,
`/(a)\1/`, works fine — the identical "constructor doesn't get the
regexp2 fallback a literal does" asymmetry class `#190`/`#196` already
documented for Unicode property escapes, this time for backreferences.
Root-caused precisely in `pkg/vm/regex.go`: `compileRegexEngines`
(regex literals) unconditionally falls back to `regexp2` on any
`translateJSFlagsToGo`/`regexp.Compile` failure, but `NewRegExp` (the
`new RegExp(...)` constructor) returns `translateJSFlagsToGo`'s error
immediately — its own regexp2 fallback is gated by
`needsRegexp2Fallback`, which only recognizes the four lookaround
openers, not backreferences, so a backreference pattern trips the
constructor's earliest possible error return before that gate is ever
reached. Filed
[paserati#218](https://github.com/nooga/paserati/issues/218) with the
exact asymmetry and two concrete fix directions.

`pi-ai` (real package, fake off) still shows `Connection error.`
against the plain no-server baseline (still correctly matching real
Node, since the real SDK normalizes every `fetch()` failure to that
message regardless of type) — but exercising it against a real local
SSE stub server surfaced something not yet root-caused: a bare
`streamSimple(...)` call against the stub via a standalone script
succeeds partially (returns a `stopReason: "error"` message with
`errorMessage: "unexpected completions response"`, since the stub
always sends SSE-shaped chunks even when the real client didn't
request streaming) — but running the exact same stub through the real
`pi-coding-agent` CLI's own `-p "hello"` path instead produces
`ERROR: undefined is not a function`, a different failure entirely.
The two paths clearly exercise different code (the CLI's own model/
provider configuration presumably differs from the bare `getModels
("openai")[0]` used in the standalone script). The standalone script's
`unexpected completions response` is very likely just an artifact of
this round's stub (it always sends SSE-shaped chunks regardless of
whether the request asked for streaming, so a non-streaming call gets
a body its own client can't parse) — **not** a real gap; the CLI's
`undefined is not a function` is the one actually worth chasing, since
that's the real invocation path. This round didn't narrow it down
further — flagged as an open thread for whoever picks up `pi-ai` next,
starting from the CLI path specifically, not filed anywhere yet since
the exact
call site isn't pinned down.

Full build/vet/test (now **fully clean**, zero known failures), full
scoreboard, three real `pi` invocations: consistent with the above,
zero unexplained regressions. Shared paserati checkout left on `main`,
clean, up to date with `origin/main`. Stub HTTP server used for this
round's `pi-ai`/`#212` verification killed before finishing (checked
`lsof -i:1234` explicitly this time before drawing conclusions from
any run, after getting burned by leftover-stub contamination twice
last round).

**Net effect**: `#210`/`#212`/`#213`/`#214` all confirmed genuinely
fixed by direct re-verification, not assumed from issue state. Real
forward movement on `pi-tui` (now blocked on `#218`, a much narrower
gap than a missing global) and a partially-characterized-but-not-yet-
root-caused new gap on `pi-ai`'s real streaming path via the actual
CLI. No fakes deleted yet — zero scoreboard rows clean.

**Thirty-second round (2026-09-02/03) — implemented `node:vm`
(`internal/host/vm.go`), the actual next `jiti` blocker; verified end
to end against the real dependency chain; found and filed one more
parser bug (`#220`).** User asked where `vm.runInThisContext` should
live given noderati already instantiates a paserati VM — the answer,
worked out in conversation before writing any code: Node-specific
surface (not ECMAScript/WHATWG standard, unlike `fetch`/
`ReadableStream`/`Intl`) belongs in noderati, same split every prior
Node module has followed. First pass proposed only the narrow
`runInThisContext` slice, reasoning `vm.createContext`/`runInContext`
would need "a genuinely separate global object/realm" as if that were
a big new paserati-side lift — user pushed back precisely: paserati
already instantiates a VM and already has realms. Checking before
answering (rather than defending the original claim) found this was
completely right and the original claim wrong: `pkg/vm/realm.go`'s
`Realm` type is a real, complete ECMAScript-Realm-shaped mechanism
("the foundation for ECMAScript Realm support and ShadowRealm API",
per its own doc comment) — `vm.CreateRealm()`, `vm.WithRealmValue()`,
and `driver.InitializeRealmBuiltins()` (which even already clones the
main realm's heap *layout* onto a new realm so compiled global-slot
indices resolve identically across realms) together give everything
`vm.createContext`/`runInContext`/`runInNewContext` need, already
public, already wired end to end. `vm.runInThisContext` itself maps
even more directly onto an existing primitive:
`driver.Paserati.IndirectEvalCode`'s own doc comment ("creates a new
declarative environment for let/const/class... var goes to the global
environment... does NOT inherit strict mode") is a verbatim match for
real Node's own description of `runInThisContext` as behaving like
indirect `eval()`. So: the *whole* module, not just the narrow slice,
needed zero new paserati-core work — user said "build it" once that
was on the table.

Implemented `internal/host/vm.go`: `createContext`/`isContext`/
`runInThisContext`/`runInContext`/`runInNewContext` plus a `Script`
class, mapped exactly onto the primitives above. A contextified
sandbox object's identity (`*vm.PlainObject` pointer, stable across
`vm.Value` wrappers) is the only piece with no existing paserati
equivalent — kept in a package-level Go map to a `*vm.Realm`, since a
JS object can't itself directly carry an opaque Go pointer; documented
its one real limitation honestly (entries are never freed, so a
context created and dropped leaks its Realm for the process's life —
acceptable for real usage found so far, revisit if that changes).
Also documented two other deliberate scope cuts rather than silently
omitting them: `Script` doesn't eagerly parse at construction time the
way real Node does (a syntax error only surfaces on first run, since
`IndirectEvalCode` compiles and runs in one step with no reusable
"compiled, not yet run" split) — a real, disclosed gap from spec
fidelity, not a silent one; and sandbox-to-context linkage is
one-directional (existing sandbox properties become context globals;
a script defining a *new* global isn't synced back onto the sandbox
afterward) rather than real V8's live two-way binding, since no real
call site needs the two-way case and a fragile partial simulation of
it would be worse than an honest one-way copy.

Wrote 7 tests including one specifically checking Realm isolation is
real, not just a naming trick (a value set as a global in the main
realm is invisible inside a context; a global a context script defines
is invisible in the main realm afterward) — all pass, including on
the very first build (every API assumption from the design discussion
held). Registered in `host.go`; added `"vm"` to `cjs.go`'s
`nativeRequireNames` — the same second, separately-hand-maintained
`require()`-routing list `node:v8` needed an entry in two rounds ago,
confirmed needed again by testing `require("node:vm")` explicitly
(not just `import`) before considering the module done.

**Verified against the real dependency chain, not just synthetic
tests**: `fake-off:jiti` no longer fails on `Cannot find module
'node:vm'` — it progresses further into `jiti.cjs`'s own real load
chain and hits a *different* error entirely
(`Syntax Error at 1:66759: '(' expected`), with no informative stack
trace (a parse-time error, before any JS exception machinery runs).
Traced precisely rather than guessed at: added a temporary,
env-var-gated trace to `cjs.go`'s `execFile` (removed before
committing — investigation-only, not shipped) to log every file
loaded; the last one before the crash was `jiti/dist/babel.cjs`
(jiti's own vendored Babel build, loaded via `jiti-static.mjs`'s
`import _babelTransform from "../dist/babel.cjs"` found two rounds
ago) — confirming `node:vm` itself is no longer the blocker; something
inside Babel's own real bundle is. Used the same paserati-lexer-dump
technique from the original jiti investigation (rather than
unreliable raw byte-offset text slicing) to tokenize the *exact*
wrapped source paserati actually parses and find the real position:
`function satisfies(e,t,r){...}` — a plain function declaration named
`satisfies`, tokenized as the reserved `SATISFIES` keyword (paserati's
TS `satisfies`-operator token) rather than a plain identifier.

Minimized standalone, then swept every TypeScript contextual keyword
as a function-declaration name and checked each against real Node
before filing: **wrongly rejected** (real Node accepts all seven):
`satisfies`, `is`, `infer`, `readonly`, `override`, `abstract`,
`keyof`; correctly already accepted: `as`, `asserts`, `type`,
`module`, `namespace`, `of`, `static`, `declare`, `accessor`,
`unique`, `global`, `out`; correctly rejected by both engines (genuine
reserved words, not contextual, so paserati's rejection here is
right even if its error text is less specific than real Node's):
`interface`, `enum`, `implements`, `in`, `const`. Root-caused to
`pkg/parser/parser.go`'s `parseFunctionLiteral` (~line 2601) — the
same hand-maintained-keyword-allowlist class of bug as `#203`/`#204`,
this time for function declaration/expression names (used by both
`function` and `async function`), with a precedent fix already in the
same file for the *adjacent* case (function **parameter** names,
`d97d4bf6`) that evidently didn't also cover this position. Filed
[paserati#220](https://github.com/nooga/paserati/issues/220) with the
full swept scope and both fix directions (extend the list, or switch
to the general `isIdentifierNameToken()` helper the way `#203`/`#204`
already suggested elsewhere).

Full build/vet/test, all three real `pi` invocations, full scoreboard:
clean, zero regressions — `fake-off:jiti`'s row now shows exactly
`#220`'s error, confirming it's the sole remaining blocker at this
depth. Shared paserati checkout was found switched to another agent's
WIP branch for `#218` (fast turnaround — already a real commit,
`7bf84209`, pushed since last round) partway through this round;
confirmed clean, switched back to `main` without touching it.

**Advisor pass caught two real gaps before this round was called done**,
both fixed rather than waved off:

1. *Untested error path.* No test exercised `runInContext`/`Script`
   against a non-context object actually throwing — only that
   `isContext({})` reports `false`. Added
   `TestVMRunInContextRejectsNonContext` covering both the
   module-level function and the `Script` method. It caught a real
   bug: `new vm.Script(code).runInContext({})` (and, once probed
   further, *any* error from *any* `Script` method, including a plain
   syntax error from `runInThisContext()`) silently evaluated to
   `undefined` instead of throwing — full stop, not narrower to the
   context-rejection case.
2. *Isolation test proved less than its own doc comment claimed.*
   `TestVMContextIsolation` only checked a *property* assigned via
   `globalThis.x = ...`, which could pass even if realms shared heap
   storage (property vs. heap-slot bindings are different storage).
   Added `TestVMContextIsolationRealBinding`, declaring a genuine
   `var` binding in each direction — it also passed, so isolation is
   confirmed real, not an artifact of testing the wrong mechanism.

Root-causing gap 1 (not just patching around it) led one level
deeper than the module itself. `pkg/driver/native_module.go`'s
`createBoundMethod` — the reflection wiring behind every `m.Class`
instance method, `Script`'s three `run*` methods included — only ever
reads `results[0]` from a Go method's return and hardcodes a nil error
back to the VM, never checking for the `(T, error)` shape at all. Both
`goFunctionToVM` (module-level `Function`s) and
`createClassConstructor`'s own constructor-call path already
special-case exactly this shape and turn a non-nil error into a real
throw (the constructor path fixed once already, for `#167`) — only the
*instance method* wiring never got the same treatment. Filed
[paserati#221](https://github.com/nooga/paserati/issues/221) with the
comparison and a suggested shared-helper fix.

Until `#221` lands, `vm.go`'s `Script` methods work around it directly:
`vmThrow` calls the VM's own `ThrowExceptionValue`/`ThrowTypeError`
inline rather than trusting the return value, reusing an
`ExceptionError`'s real thrown value when there is one and building a
generic `Error` otherwise (mirroring what the *working* module-Function
error path already does). The first version of this workaround
compiled and looked right but still didn't propagate — instrumenting
confirmed why: `handleCatchBlock` (`pkg/vm/exceptions.go`) finds the
in-frame catch handler and correctly repoints `frame.ip` at it, but
only sets `vm.handlerFound` when `vm.helperCallDepth > 0`, and
`OpCallMethod`'s own same-frame-catch fallback check is gated behind
`!calleeVal.IsCallable()` — never true for a real bound method. Wrapping
the throw in `vm.EnterHelperCall()`/`vm.ExitHelperCall()` (the exact
bracket `call.go`'s own doc comment prescribes for "native functions
[that] call helpers... that might throw exceptions which need to be
caught by try/catch blocks") fixed it; confirmed via a new
`TestVMScriptRunInThisContextSyntaxError`, mirroring the existing
module-level syntax-error test but through the `Script` class. All 10
`vm_test.go` tests pass; full build/vet/test, all three real `pi`
invocations, and the full scoreboard re-run clean afterward —
`fake-off:jiti`'s row is unchanged (still exactly `#220`'s error),
confirming this fix is additive, not a behavior change to anything
already working.

**Net effect**: `jiti` gained a real, substantial noderati-side
capability (`node:vm`, likely useful to more than just jiti, matching
`OSPathResolver`'s CJS-interop fix two rounds ago) and is now blocked
on `#220` — one specific, narrow parser gap, not a missing module. Along
the way, found and fixed a real correctness gap in `vm.Script` (every
method's error path was silently swallowed) rather than shipping it
unnoticed, and filed the paserati-side root cause (`#221`) rather than
leaving the workaround unexplained. `pi-tui`'s blocker, `#218`, shows
as **closed** upstream as of this round (seen via `gh issue list` while
filing `#221`) — not yet re-verified against the actual merged fix;
next round should pull paserati and re-check before deleting anything.
No fakes deleted yet.

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
