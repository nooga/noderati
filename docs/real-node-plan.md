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
`String(c)`, no actual UTF-8 multibyte/incremental decoding), `glob`/`minimatch`
(real Node/npm surface, but `globSync` always returns `[]` and `minimatch`
always returns `false` — silently *wrong*, worse than missing). `stream.go`
also hand-rolls its own `EventEmitter` instead of reusing `events.go`'s — pick
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
`minimatch` (as a *third-party npm shim* — separate from the A-minus concern
about implementing the real Node `glob`-alike surface if we choose to keep
that name meaning something else), `proper-lockfile`, `hosted-git-info`.
None of these belong in a "Node host." Removing them is a deletion task, not
a build task, and it's most of `internal/host/`'s file count.

**C. `esmpatch.go` per-file source rewrites.** Was twelve rewrites keyed by
filename, each patching around one specific parser/compiler gap or the
package fakes in (B). Eleven are now deleted (2026-08-30): ten in the
original Phase 1 close-out once the Phase 2 scoreboard confirmed each dead,
and `syntax-highlight-stub` the same day once
[paserati#121](https://github.com/nooga/paserati/issues/121) (a
register-allocator compiler bug) and
[paserati#122](https://github.com/nooga/paserati/issues/122) (a stale
frozen-property flag) were both fixed upstream and the real `highlight.js`
was confirmed to register 190/191 bundled languages — the one exception
(`latex`, needing regex lookahead Go's RE2 doesn't support) is a documented,
linked, architectural gap, not a reason to keep faking the whole module.
**One remains:** `sdk-reexports` — a real, still-needed compile-error
workaround, misidentified in an earlier pass of this doc as an `export *`
issue (it isn't). Delete it only when its underlying bug is fixed *and*
verified via the scoreboard, including against `latex`'s existing gap — see
Phase 2 below for why individual verification isn't sufficient on its own,
even once a blocking bug is fixed (it happened twice in a row here).

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
  `runtime error during user function execution` and no file/line. Not yet
  bisected within the file (448 lines, no classes at all — a different,
  not-yet-diagnosed bug, unrelated to #128). Not filed yet.
- **`pi-agent-core`** — `Agent`, imported directly by name from `agent.js`,
  loads fine. The package's real `index.js` (an `export *` barrel,
  multiple hops deep) does not: `import * as mod from ".../index.js"`
  throws `TypeError: Class extends value undefined is not a constructor or
  null`. This looks like a deeper instance of the same family the original,
  now-deleted `patchESMPiAgentCoreReexports` patch was working around
  ("skip-typecheck cannot harvest class names from `export *`") — but the
  single-hop case was independently verified fixed while investigating
  #119 (see "already fixed upstream" above), so this needs its own
  multi-hop repro before filing. Not yet bisected or filed.

Net: no group-B fake deleted yet. Each of the three actually tried hits a
real, currently-unfixed engine gap — exactly the outcome this phase expects
to find (see the phase's own note above: "Expect `pi-tui` and `pi-ai` to
surface the most new gaps"), just found one leaf earlier than expected.
`#128` in particular looks worth fixing before continuing this phase
further — it's general enough that it may silently unblock several of the
others too.

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
