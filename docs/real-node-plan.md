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
registered ahead of the real files on disk: `@earendil-works/pi-ai` (a
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
(`#190` then `#192`) merged upstream; `@earendil-works/pi-tui` — the
entire TUI component library, every export a no-op — deleted 2026-09-03
once paserati#195/#196/#218/#222–#225 all merged and a real functional
exercise of its actual component surface matched real Node byte-for-byte;
see Phase 3 below for all of these.)
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

**D. Resolver-side dirty tricks, independent of the above — closed 2026-09-06
(round 64, Phase 4).** Both fixed; see the Phase 4 section and the round-64
entry for the verification. Kept here, struck through in spirit rather than
deleted outright, so this section's own history stays legible:
- ~~`findPiCodingAgentNodeModulesRoots()` (`piai.go`) hardcodes
  `/opt/homebrew/lib/node_modules/...` and `/usr/local/lib/...`...~~ — turned
  out `NodeModulesResolver`'s own `findPackageDir` already implements the
  real walk-up this note assumed didn't exist; deleted the hardcoded
  fallback (and `piai.go` itself) once testing confirmed the walk-up alone
  reaches everything a real invocation needs.
- ~~`NodeMissingResolver` turns an unresolvable `node:*` specifier into
  a module whose body throws at runtime...~~ — that part turned out to
  already match how ES module linking actually fails in both engines
  (eagerly, before the importing module's own code runs); what was
  genuinely wrong was the *message*, a paserati-internal string instead of
  Node's own `ERR_MODULE_NOT_FOUND`/`ERR_UNKNOWN_BUILTIN_MODULE` shapes.
  Fixed, and extended to cover every specifier shape nothing else could
  resolve (bare packages, relative/absolute paths), not just `node:*`.

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

**Thirty-third round (2026-09-03) — pulled paserati, confirmed `#218`
genuinely fixed; `fake-off:pi-tui` matches baseline for the first time,
but the fake stays (not yet a real deletion candidate).** Checkout was
clean on `main`; pulled in `39ba5a67` ("fix(vm): fall back new RegExp()
backreferences to regexp2 like literals do") — read the diff before
trusting the issue-closed state, same as every prior round: it routes
`translateJSFlagsToGo`'s hard-coded backreference error into the same
narrow `needsRegexp2Fallback` gate `NewRegExp` already had for
lookaround, rather than special-casing it separately, and extends that
gate to trigger on `\1`-`\9` (skipped under `u`/`v`, where Annex B's
relaxed backreference rule doesn't apply and a bare `\1`-`\9` must stay
a `SyntaxError` — confirmed the author checked this against
`unicode_restricted_octal_escape.js` in Test262, per the commit
message). Verified against the exact filed repro in plain paserati
first: `new RegExp("(a)\\1")` now compiles and `.test("aa")`/
`.test("ab")` return `true`/`false` correctly, not just "no longer
throws."

Rebuilt noderati against the pulled checkout (`go.mod`'s `replace`
points at the local path, no version bump needed) — full build/vet/
test clean. Scoreboard run twice for stability: `fake-off:pi-tui` now
matches `baseline` on all three invocations (`version`/`help`/`print`),
the **first non-empty line ever on the "candidates to actually delete"
list** in this phase. Checked for stub-server contamination on the
matching `print` failure (`lsof -i:1234`, empty) — both sides
genuinely fail to connect to a nonexistent local LLM server for the
same real reason, not a coincidence.

**Did not delete the fake.** The twenty-third round's own note on this
exact fake says exactly why not: "its eventual deletion will need a
real functional exercise of TUI components specifically — the three
baseline invocations exercise none of it, only the import." `#218`
fixed precisely the import-time blocker that note anticipated — the
scoreboard turning green here confirms the *load path* now works, not
that any of the 90 real call sites' actual rendering/component behavior
does. That still needs the same kind of real-call-site functional
exercise `typebox`/`diff`/`glob`/`proper-lockfile` each got before their
fakes came out, not yet done for `pi-tui`.

One more thing checked rather than assumed, prompted by advisor:
`pi-tui`'s real `native-modifiers.js` best-effort-loads a platform/arch-
gated `.node` native addon (`darwin-modifiers.node`, arm64/x64 only)
through a `try/catch` that falls through to `undefined` on failure — a
real risk if noderati's `require()` on a `.node` path aborted instead of
throwing a catchable error. It doesn't abort: `require()` on that exact
path throws a catchable `Syntax Error at 1:63: Invalid character` —
noderati has no native-addon loading at all, so `require()` falls back
to treating the binary's bytes as JS source and hits garbage on the
first non-ASCII byte. The degradation is real and catchable, just via
an accidental mechanism (a parse failure) rather than a deliberate
"native modules unsupported" rejection — functionally fine for now
(both catch and continue identically), but worth knowing precisely
rather than assuming "graceful" meant "deliberate."

No noderati code changes this round — verification only. No fakes
deleted.

**Thirty-fourth round (2026-09-03) — the real functional TUI exercise
the thirty-third round called for; found four distinct engine bugs and
filed all of them (`#222`-`#225`), plus a fifth minor one (`#226`);
`pi-tui`'s fake stays.** Built a standalone script
(`/private/tmp/.../pitui-check/check.mjs`, scratchpad-only, not
committed) importing the **real** `@earendil-works/pi-tui` package
directly by absolute path and exercising its actual exported surface
against real call patterns: `utils.js`'s width/truncation/wrapping
functions, `fuzzy.js`, `keys.js`'s `parseKey`/`matchesKey` against a
battery of raw terminal escape sequences, `keybindings.js`,
`terminal-colors.js`'s OSC report parsers, and the `Box`/`Text`/
`TruncatedText`/`Spacer`/`Markdown`/`SelectList`/`SettingsList`/
`Container` components' `render(width)` methods — `Markdown`/
`SelectList`/`SettingsList` constructed with theme objects whose field
shapes were copied verbatim from pi-coding-agent's own real
`dist/modes/interactive/theme/theme.js` (`getMarkdownTheme`/
`getSelectListTheme`/`getSettingsListTheme`), rebuilt with real `chalk`
colors instead of that file's own theme-loading machinery (typebox
schemas, fs watchers, config-dir resolution — unrelated to pi-tui
itself). Deliberately out of scope: `TUI`'s live differential-render
loop and raw-stdin listening, which need an actual attached
terminal/pty to test meaningfully on *any* engine, not just noderati.

Ran the identical script under real Node (v26.3.0) first to establish
a byte-exact baseline, then under noderati with `pi-tui`'s fake
disabled, with every call wrapped so one failure couldn't abort the
rest of the exercise (record success or `THREW <message>` per case,
diff the two full reports afterward) — the lesson from the very first
attempt, which crashed on the first emoji-width call and would have
hidden everything behind it.

**Four real, distinct engine bugs found, each root-caused to an exact
file/line before filing** (ranked by severity):

1. **[paserati#222](https://github.com/nooga/paserati/issues/222)** —
   an arrow function written directly as an object-literal property's
   value silently loses its lexical `this` binding when `this` is the
   *only* thing it captures. `this` isn't tracked via the compiler's
   `freeSymbols` mechanism (it compiles to a dedicated `OpLoadThis`
   opcode, not a symbol lookup), so such an arrow has
   `len(freeSymbols) == 0`; `compileObjectLiteral`'s named-arrow path
   (`compileArrowFunctionWithName` + `emitClosureGeneric`) has a
   zero-upvalue fast path that emits a bare `OpLoadConstant` instead of
   `OpClosure` in that case, so `Closure.CapturedThis` — how arrow
   `this` binding actually works at the VM level — never gets
   populated. Isolated the *exact* boundary with nine variants (object
   literal vs. array vs. separate-then-referenced; class method vs.
   plain-object method; `this`-only vs. named-outer-var capture) before
   filing, then grepped every other call site of both
   `compileArrowFunctionWithName` and `emitClosureGeneric` to confirm
   the blast radius really is exactly this one pattern — destructuring
   defaults and plain `const x = () => ...` declarations all route
   through a different, always-correct `emitClosure` helper. This is
   real Node's own `pi-tui` `Markdown` component's actual pattern
   (`getDefaultInlineStyleContext()`'s `applyText` field), so
   `Markdown.render()` throws on any real markdown input. Flagged as
   the most severe of the four: it fails **silently** whenever the
   missing `this` happens not to be dereferenced in a way that throws,
   not just here.
2. **[paserati#223](https://github.com/nooga/paserati/issues/223)** —
   a getter/setter defined via `Object.defineProperty` on a
   **Function**-typed object isn't found via inherited property access
   from a different object whose prototype is that function — the
   lookup silently returns `undefined` rather than invoking the
   getter, confirmed by instrumenting the getter body itself (it never
   runs). A plain **data** property on the same function-as-prototype
   *is* found correctly, and the same getter pattern on a **plain
   object** prototype also works — narrowing this precisely to
   accessor lookup specifically through a Function-typed link in the
   chain. This is **chalk's actual root cause** for emitting color
   unconditionally: confirmed directly against the real installed
   `chalk/source/index.js` (not just the synthetic repro) that
   `chalk.cyan.level` — the exact property `applyStyle` gates ANSI-code
   emission on — reads `undefined` under noderati where real Node
   reads `0`, because chalk's style-builder functions inherit `level`
   from a Function-typed `proto` via exactly this getter shape, while
   `chalk.level` itself (an own data property on the root object) reads
   fine. Means **any** noderati output piping through chalk's
   auto-detection gets stray ANSI codes in non-TTY contexts — not
   `pi-tui`-specific at all, found through it.
3. **[paserati#224](https://github.com/nooga/paserati/issues/224)** —
   `\p{RGI_Emoji}` (a Unicode "property of strings" — matches whole
   emoji *sequences*, not single codepoints, `v`-flag-only) isn't
   recognized by either engine; confirmed regexp2 can't parse it either
   (`unknown unicode category, script, or property`), so unlike
   `#218`'s fix this can't be rescued by routing into the existing
   fallback gate — it needs paserati to synthesize an alternation from
   real Unicode emoji-sequence data, a harder class of gap than
   `#190`/`#196`'s single-codepoint property gaps, flagged as such
   explicitly. Blocks `pi-tui`'s `visibleWidth()` (and everything built
   on it) on any string containing an emoji.
4. **[paserati#225](https://github.com/nooga/paserati/issues/225)** —
   the `\p{Name=Value}` property-escape *grammar* itself (`\p{Script=
   Han}`, `\p{Script_Extensions=Han}`, `\p{General_Category=Letter}`)
   isn't recognized by either engine at all — not a missing-value gap,
   the whole syntax form fails identically regardless of which name is
   used. `regex_properties.go`'s `expandDerivedUnicodeProperties` its
   own doc comment says this form "passes through untouched for the
   engines to judge" — checked whether either engine actually can
   judge it, and neither can, so quoted that comment directly in the
   filed issue since it's a documented deferral to a capability that
   doesn't exist. Blocks `pi-tui`'s `wrapTextWithAnsi()` (used by
   `Text`/`Markdown`) unconditionally, since the failing regex
   (`cjkBreakRegex`) is built at module load time regardless of input.

**Fifth, minor finding, filed rather than left in scrollback per
advisor's prompt**:
[paserati#226](https://github.com/nooga/paserati/issues/226) — a
`const`-declared arrow function nested inside another function cannot
reference itself by name from within its own body (`Cannot find name`
from the type checker), even though this is legal and real Node/tsc
both accept it; only affects a nested `const` arrow's self-reference
specifically (top-level self-reference, and a nested named function
declaration's self-reference, both work). Tripped over this while
minimizing `#222`'s repro (mimicking chalk's real self-referencing
`createBuilder` pattern) and routed around it with `-no-typecheck`
rather than let it block that investigation.

Advisor caught two things before this was called done: (1) initially
inferred `#223` was chalk's cause from mechanism-reading alone without
confirming against the real file — added the direct `chalk.cyan.level`
check against the actual installed `chalk/source/index.js` before
claiming the link; (2) hadn't checked whether `emitClosureGeneric`'s
other call site (`ShorthandMethod`, `compiler.go:1519`) shared `#222`'s
bug — grepped every caller of both `compileArrowFunctionWithName` and
`emitClosureGeneric` and confirmed it's a method-compilation path
unaffected by `CapturedThis`, narrowing the filed blast radius instead
of leaving it an open question.

**Net effect**: the functional exercise `pi-tui`'s own twenty-third-
round note called for is done, and it answered the question definitively
— three of the four bugs are hard failures on core rendering paths
(`visibleWidth`, `Text`/`wrapTextWithAnsi`, `Markdown`), the fourth
produces silently wrong output rather than crashing. `pi-tui`'s fake
**stays** despite `#218` fixing its import-time blocker; the scoreboard
being green here was necessary but nowhere near sufficient. No noderati
code changes this round — verification and issue-filing only. No fakes
deleted.

**Thirty-fifth round (2026-09-03) — all five of `#222`-`#226` merged
in one PR (`#227`); re-verified each against its own filed repro; the
full functional exercise now byte-identical to real Node; deleted
`pi-tui`'s fake.** User reported the merge; per standing rule, verified
directly rather than trusting the closed-issue state — re-ran all five
exact repros from the thirty-fourth round's filed issues against fresh
`paserati` `main` (`981401bb`/`8fdb9097`/`41a6ceaa`/`59c1d818`/
`d123a4f8`, one commit per issue): the object-literal `this`-capture
case now prints `42:x`, the nested self-referencing `const` arrow now
resolves, the Function-typed-prototype getter chain now returns `5`
instead of `undefined`, both `\p{Script=Han}` and
`\p{Script_Extensions=Han}` now match, `\p{RGI_Emoji}` now matches —
all five genuinely fixed, not just closed.

Re-ran the *exact* functional-exercise script from the thirty-fourth
round (same file, unmodified) with `pi-tui`'s fake still toggled off
via env var first: **zero-line diff** against the real-Node baseline
captured that round — every one of the four found bugs' symptoms
(the `visibleWidth`/`wrapTextWithAnsi` throws, the `Markdown` crash,
chalk's stray ANSI codes on `SelectList`/`SettingsList`) is gone, and
nothing regressed elsewhere in the same 30-odd-case battery. Checked
cross-fake coupling before deleting (grepped every other group-B fake
file for `PiTui`/`pi-tui`/`pitui` — none found, unlike the documented
`pi-ai`/`pi-agent-core` coupling) so the deletion couldn't silently
change either of those.

Deleted `internal/host/pitui.go` and its `declarePiTui()` registration
in `host.go` (replaced with a dated comment matching every prior
group-B deletion's style, listing every issue that had to land and
noting the functional-exercise verification, plus the deliberately
unexercised `TUI` differential-render-loop/raw-stdin surface); moved
the two unrelated tests bundled into `pitui_test.go`
(`TestPerfHooksShim`, `TestStringDecoderShim`) into a renamed
`shims_test.go` rather than deleting them along with the pi-tui-
specific ones; dropped `"pi-tui"` from `cmd/scoreboard/main.go`'s
`fakeNames`. Full build/vet/test clean. Rebuilt and re-ran the same
functional-exercise script with the fake *actually* gone (no env var)
— still a zero-line diff against real Node. Full scoreboard re-run and
diffed line-by-line against the pre-deletion capture: `baseline`
unchanged, `fake-off:jiti` still exactly `#220`'s error,
`fake-off:pi-agent-core` still the Class-extends error, `fake-off:pi-ai`
still `Connection error`, `all-fakes-off` still jiti's error — nothing
moved, exactly as the already-matching `fake-off:pi-tui` row from the
prior round predicted, confirmed rather than assumed. All three real
`pi` invocations (`--version`/`--help`/`-p "hello"`) unchanged.

**Net effect**: `pi-tui` — the largest group-B fake, an entire TUI
component library, 90 real call sites — is gone. Ledger group B is now
down to `pi-ai`(+`/compat`), `pi-agent-core` (coupled to `pi-ai`'s fake
per the standing note above), and `jiti/static` (blocked on `#220`).

**Thirty-sixth round (2026-09-03) — `#228` merged, fixing both `#220`
(jiti's blocker) and `#221` (the bound-method error-swallowing bug
`vm.go`'s `vmThrow` worked around); verified both, simplified `vmThrow`
away, found and filed jiti's next blocker (`#229`).** User reported the
merge; verified rather than trusted, as standing practice requires:

- `#220` — all seven previously-rejected TypeScript contextual keywords
  (`satisfies`/`is`/`infer`/`readonly`/`override`/`abstract`/`keyof`)
  now parse and run as function declaration names, confirmed against
  the exact swept repro from the round that filed it.
- `#221` — wrote a standalone Go program using paserati's driver
  directly (a `Class` whose bound instance method returns
  `(vm.Undefined, error)`) to confirm the underlying fix, independent
  of noderati's own workaround: the error now propagates and is
  genuinely caught as a JS exception, without `vmThrow`'s
  `EnterHelperCall`/`ExitHelperCall` dance. Removed `vmThrow` from
  `internal/host/vm.go` and reverted `vmScript`'s three `run*` methods
  to the natural `return vm.Undefined, err` shape — the workaround this
  round's own doc comment predicted removing once `#221` landed for
  real, rather than leaving dead defensive code in place once its
  reason stopped existing. Full `vm_test.go` suite (including the
  syntax-error-through-`Script` regression test written specifically
  to catch `#221`) still passes with the simpler code.

**Real jiti chain progressed past `#220` into a new, different
blocker** — re-ran the exact same env-var-gated `cjs.go` trace-and-
lexer-dump methodology from two rounds ago (added, used, removed
before committing) to locate it precisely: still `jiti/dist/babel.cjs`,
now much further in (`1:158447` vs. the old `1:66759`). The failing
text is an object literal with a method literally named `async`
(`async(...t){return Promise.resolve(e.apply(this,t))}`, part of
Babel's own async-wrapping helper) — a plain, non-async method whose
*name* happens to be the word "async," not the async-method modifier.
Minimized to a 4-line repro, then checked the boundary before filing:
the analogous class-method case (`class Foo { async(...) {} }`) and
the analogous `get`/`set`-as-object-literal-method-name cases already
parse correctly — only the object-literal-shorthand-method-named-
`async` case fails, root-caused to one missing disambiguator
(`lexer.LPAREN`) in `parser.go`'s object-literal property parser
(~line 7150), which otherwise correctly treats `async` as a plain name
rather than a modifier when followed by `:`/`,`/`}` but was missing the
fourth grammatically-unambiguous case, `(`. Filed
[paserati#229](https://github.com/nooga/paserati/issues/229).

Full build/vet/test, both real `pi` invocations, full scoreboard: clean
— `fake-off:jiti`'s row now shows exactly `#229`'s error at the new
position, confirming genuine forward progress rather than a
regression; every other row unchanged. No fakes deleted this round;
`jiti` moves one blocker closer.

**Thirty-seventh round (2026-09-04) — went after `pi-ai`'s real
CLI-path `undefined is not a function` (both group-B fakes off); ruled
out the stub as the cause, found and filed two engine bugs instead
(`#230`, `#231`), plus a smaller compat gap (`#232`); the call site
itself is still open.** `#229` and `#204` are reported fixed upstream
but not yet landed, so this round picked up the other standing
question: an earlier round's `-p` invocation against real `pi-ai`/
`pi-agent-core` produced a bare `undefined is not a function` with no
stack, and that round left open whether it was a real engine bug or an
artifact of the hand-rolled SSE stub used in place of a real LLM
endpoint.

Rebuilt the stub (`sse_stub.py`, OpenAI-compatible `/v1/chat/
completions`) correctly this time, having found two real bugs in the
*stub itself* first — always worth stating plainly since both would
have looked like noderati bugs otherwise: it didn't send the trailing
empty-choices `usage` chunk `stream_options.include_usage` requires,
and it left the connection open with no `Content-Length` on an SSE
body, so the client (real Node's OpenAI-SDK-based HTTP client) waited
forever for a close signal that never came. Fixed both — final empty-
usage chunk after `finish_reason`, `Connection: close` +
`self.close_connection = True` — and verified under **real Node
first**, established as the known-good oracle before testing
noderati: real `pi` completes an entire turn against the stub
end-to-end, including a full tool-call round trip (the stub
conditionally emits a `bash` tool call on the first request, a plain
reply on the follow-up). With the stub now demonstrably correct,
re-ran the same `-p` invocation under noderati with `pi-ai` and
`pi-agent-core` both disabled: **same `undefined is not a function`,
occurring before any HTTP request reaches the stub at all.** That
settles the open question from the earlier round — this is a real
compat gap, not a stub artifact — even though the exact throw site is
still unknown.

Two debugging attempts to get a stack trace, both by temporarily
patching the *real installed npm package* (never paserati's own
source), each backed up first and restored via `cp` + `diff`-confirmed
byte-identical after: patching `print-mode.js`'s `catch (error)` to
also try `error.stack` found nothing new (the actual line printing
`ERROR: ...` turned out to be a different one,
`assistantMsg.errorMessage || ...`, which never carries an `Error`
object to begin with); patching `pi-agent-core`'s `agent.js`
`handleRunFailure` with an unconditional debug print never fired at
all — confirmed this wasn't a broken `process.env` gate by probing
`process.env` support directly (`FOO=1 noderati -e
'console.log(process.env.FOO)'` → `1`, works fine) — so the real
failure happens **before** `Agent.run`'s own try/catch is ever entered,
ruling out that whole code path as the origin.

Tried a third technique: a wrapper `.mjs` entry script
(`tracewrap.mjs`) monkeypatching `globalThis.TypeError` to a
subclass that logs a stack trace whenever a `"... is not a function"`
message is constructed, then `import()`s the real `cli.js`. Building
this surfaced two more findings before it could even run:

- Top-level `await import(...)` in the `.mjs` wrapper threw
  `ReferenceError: await is not defined` — paserati doesn't support
  top-level await in ES modules at all, a real ECMA-262 feature gap,
  independent of anything pi-specific. Routed around it with a bare
  `import(...)` (no `await`) rather than blocking on it, then filed
  [paserati#232](https://github.com/nooga/paserati/issues/232)
  separately with a clean 3-line repro so it doesn't get lost — not
  chased further, since this round wasn't about the module system.
- With that workaround in place, loading the wrapper **crashed the
  entire process** — a raw Go panic
  (`index out of range [4096] with length 4096`), not a catchable JS
  exception. Traced it: `ThrowTypeError` re-resolves the global
  `TypeError` binding fresh on every call via
  `vm.getRealmAwareGlobal("TypeError")`, so once JS code reassigns
  `globalThis.TypeError`, any internal engine-triggered
  `ThrowTypeError` call (e.g. for `undefined()`) recurses through the
  user's replacement constructor indefinitely — and this reentrant
  `vm.Call` path doesn't share the stack-depth guard that already
  exists at the bytecode level (`OpCall`/`OpSpreadNew` in `vm.go` /
  `op_spreadnew.go`) for exactly this class of runaway recursion, so
  it blows a fixed-size internal buffer instead of raising `RangeError:
  Maximum call stack size exceeded` like every other unbounded-
  recursion case in the engine already does. Minimized to a 5-line
  repro (a plain empty-constructor class, no `extends`, assigned to
  `globalThis.TypeError`, crashed by any subsequent internal
  `TypeError` trigger), confirmed real Node handles the identical
  script with zero special behavior, and filed
  [paserati#231](https://github.com/nooga/paserati/issues/231) with
  the traced root cause and a suggested two-part fix (cache the
  original native constructor at VM-init time, and/or route the
  reentrant call through the existing depth check). This is the
  session's most severe finding by a wide margin: an uncatchable
  process crash from five lines of legal, unexceptional JS, not a
  compatibility gap — and it means the monkeypatch-a-global technique
  itself is now off-limits for any future debugging on this engine
  until `#231` lands.

Separately, while reading `console_init.go` during this investigation,
noticed `console.error`/`console.warn` write to **stdout**, not
stderr, and add a non-standard `"LEVEL: "` prefix real Node doesn't —
this is why `-p`'s crash message actually reads `ERROR: undefined is
not a function` instead of the bare `undefined is not a function` a
real Node stderr write would produce. Filed
[paserati#230](https://github.com/nooga/paserati/issues/230) with a
clean repro. To be precise about what this does and doesn't explain:
it accounts for the *formatting* of the printed message (channel and
prefix), not the underlying `undefined is not a function` failure
itself — that root cause is still open.

**Net effect**: the original question this round set out to answer —
is `pi-ai`'s CLI-path failure real or a stub artifact — is now
answered (real), but the actual throw site inside `pi-ai`/
`pi-agent-core` is still unknown; the two debugging techniques tried
so far both dead-ended (one ruled out a code path, the other crashed
the engine and got redirected into filing `#231`). No noderati code
changes this round beyond the leftover-process/state cleanup below;
`internal/host/vm.go`'s `TypeError`-adjacent code untouched (the crash
lives entirely in paserati). No fakes deleted, no fakes' scoreboard
status changed. Housekeeping: killed a stray `sse_stub.py` left
running on `:1234` from an earlier test in this round (confirmed via
`lsof` before and after), and confirmed both patched-then-restored npm
files (`print-mode.js`, `agent.js`) and the `paserati` checkout
(`git status` clean on `main`) left no residue.

**Thirty-eighth round (2026-09-04) — `#234` merged, fixing `#230`,
`#231`, and `#232` in one PR; verified all three against original
repros; found and fixed a fourth, noderati-side bug hiding behind
`#232`'s misdiagnosis (`.mjs` files not always treated as ESM).** User
reported the merge; pulled `paserati` `main` (`e79eb07d`) and verified
each fix directly rather than trusting the merge:

- `#230` (console routing) — re-ran the exact filed repro
  (`console.error`/`warn`/`log` in one script) with stdout and stderr
  captured separately: `error`/`warn` now land on stderr with no
  prefix, `log` stays on stdout, byte-identical to real Node on both
  streams.
- `#231` (TypeError-recursion crash) — re-ran the exact filed 5-line
  repro (plain class assigned to `globalThis.TypeError`, then an
  internally-triggered `TypeError`): now prints `"caught"` and exits 0,
  matching real Node exactly, no panic. Per the PR's own write-up the
  fix covers a second, worse variant (a revoked `Proxy` assigned the
  same way, previously an unrecoverable `fatal error: stack overflow`
  that `panic`/`recover` can't even catch) — built that repro too
  rather than trusting the description, confirmed it also now matches
  real Node byte-for-byte (`"caught"`, exit 0).
- `#232` (top-level await) — this is where verifying paid off. The
  PR's own body flags that my original diagnosis was wrong: top-level
  await itself was never broken; my filed repro's *actual* failure
  (traced by the paserati maintainer to `cmd/paserati`) was an absolute
  entry path breaking relative-import resolution, since `os.DirFS`
  rejects any path starting with `/` and `cmd/paserati` was passing the
  CLI's absolute filename argument straight through as the resolver's
  base path. Built that exact scenario (absolute-path `.mjs` entry
  importing a relative sibling module) and confirmed it now resolves,
  matching real Node.

  But re-running my *original* filed repro (a bare `await
  Promise.resolve()`, no import at all) through `noderati` still threw
  the identical `ReferenceError: await is not defined` — unchanged.
  Confirmed this wasn't stale by running the same file through
  `cmd/paserati` directly: it worked (module-mode top-level await is
  fine in paserati itself, as the PR claimed — modulo a separate, minor
  `Promise.resolve()` zero-arg type-check quirk that's out of scope
  here). So the discrepancy is entirely on noderati's side, not
  paserati's. Root cause: `cmd/noderati/main.go`'s `runFile` decides
  module vs. CommonJS by extension for `.ts`/`.mts` only — everything
  else, `.mjs` included, falls through to `looksLikeESM`, a heuristic
  that just greps for `import `/`export ` prefixes. My repro has
  neither (only a bare top-level `await`), so a `.mjs` file with no
  static import/export statements was silently routed through
  `host.RunCJS`'s function-wrapping, where `await` genuinely isn't
  valid syntax — matching CJS's real behavior, but real Node treats
  `.mjs` as always-ESM by extension alone, never by sniffing content.
  Added `.mjs` to the always-module extension check in
  `cmd/noderati/main.go`; re-ran the original repro — now prints
  `before`/`after` and exits 0, matching real Node. `go build`/`go
  vet`/`go test ./...` clean. This means `#232` as I filed it was a
  compound report: the maintainer's fix addresses a real paserati bug
  my repro also happened to trip over in a different way at the time,
  but the specific symptom I described (bare top-level await failing)
  was never paserati's bug — it was this noderati-side extension gap,
  now fixed here instead.

Also found and cleared unrelated environment rot while re-running the
scoreboard to confirm no regressions: a stale `~/.pi/agent/
settings.json.lock` directory (no process holding it, empty, dated
from an earlier crashed run in this session) was making even
`baseline` fail every invocation with a lockfile error, which would
have made every scoreboard row's `[DIFF]`/`[=]` marker meaningless
until removed. After clearing it, full scoreboard: `baseline` and
`fake-off:pi-ai` both green on `version`/`help` again; `print` differs
only because no LLM stub was running this round (expected, not
compared). `fake-off:pi-agent-core` unchanged (`Class extends value
undefined`, the documented `pi-ai` coupling). `fake-off:jiti` and
`all-fakes-off` both moved to a **new** syntax-error position
(`242:314286`, further in than `#229`'s old failure) — confirms
`#229`'s separate fix (landed in the same `paserati` pull, commit
`3bf33d90`) is genuinely in and jiti progressed again, but this round
didn't chase the new blocker; that's for next time.

**Net effect**: three upstream fixes verified genuine (not just
closed), one additional real noderati bug found and fixed as a direct
result of doing that verification carefully instead of taking the PR's
"top-level await works fine" claim at face value for *my* specific
repro. No fakes deleted, no fakes' scoreboard status changed this
round (jiti's new blocker is a forward step, not a deletion
candidate).

**Thirty-ninth round (2026-09-04) — picked up jiti's new blocker from
the thirty-eighth round; root-caused and filed `#235`.** Reproduced
directly (`NODERATI_DISABLE_FAKES=jiti`, `-p "hello"`): same file as
two rounds ago, `jiti/dist/babel.cjs`, now failing further in
(`242:314286` vs. the old `1:158447`). Re-added the env-var-gated
`cjs.go` trace (added, used, removed before committing, same
methodology as before) to confirm the file, then located the exact
character at that position directly in the real file (`awk`/`python3`
indexing — line 242 is 546,029 characters long, a single bundled/
minified line): a `/` starting `/[^ \t]/.exec(r[e])` inside
`for(let e=0;e<r.length;e++)/[^ \t]/.exec(r[e])&&(i=e);` — an
un-braced `for` loop whose entire body is a regex-literal expression
statement.

Minimized to a 3-line repro, then swept the boundary before filing
(same discipline as every prior round's filed issues): `if`/`while`
with an un-braced regex-starting body fail identically; the braced
equivalents (`for (...) { hit = /x/.test(...); }`) work fine; plain
division after a value-producing parenthesized expression
(`(4 + 2) / 3`) works fine. So the bug is exactly "control-flow header
`)` followed by an un-braced body starting with `/`" — not regex
literals or `for`-loops in general.

Root-caused in paserati's parser (read-only, as always): the lexer's
`canBeRegexStart` is a pure previous-token lookup table, and there's
already a purpose-built escape hatch for cases like this —
`Parser.rescanPeekAsRegex()`, called after every other statement-
boundary token the parser recognizes (closing braces, semicolons,
etc. — ~14 call sites). It's just never called after the `RPAREN` that
closes an `if`/`while`/`for` header before falling into the un-braced-
body parse path; `parseIfStatement` and `parseWhileStatement` have the
identical missing-call shape at the exact same spot. Filed
[paserati#235](https://github.com/nooga/paserati/issues/235) with the
repro, the boundary sweep, and the precise fix location (didn't verify
`parseForStatement`'s equivalent spot carries the same shape, but the
repro fails identically for `for` so it's presumably the same gap).

Full build/vet/test, scoreboard: clean, `fake-off:jiti`/`all-fakes-off`
now show `#235`'s error at the new position — forward progress
confirmed, nothing else moved. No fakes deleted, no noderati code
changes (trace instrumentation added and removed within this round,
confirmed via `git diff` before committing).

**Fortieth round (2026-09-04) — tested `pi-ai`'s real CLI path against
a live Fireworks endpoint (user's own configured account) instead of
the hand-rolled SSE stub; found and fixed a user config bug, then found
and filed two real, precisely-traced `fetch()` engine bugs (`#237`,
`#238`).** User pointed out their `~/.pi/agent/models.json` already has
a `fireworks` provider configured — a real LLM backend removes the
stub as a variable entirely, closing off the "is this a stub artifact"
question for good rather than just arguing it's unlikely.

First request 404'd (`Path not found: /chat/completions`) — the
configured `baseUrl` (`https://api.fireworks.ai/inference`) was
missing `/v1`; confirmed with a direct `curl` before touching anything
(`.../inference/chat/completions` → 404, `.../inference/v1/chat/
completions` → 200 with the same key/model). Asked before editing the
user's own global config file (outside the repo, a standing personal
setting); user confirmed. Fixed the `baseUrl`, verified against **real
Node first** as always: `pi --provider fireworks --model
accounts/fireworks/models/glm-5p2 --no-session -p "..."` returns a
real completion end-to-end.

Ran the identical invocation under noderati with `pi-ai`+
`pi-agent-core` both off: a **new, different** failure —
`415 Incorrect content type, was  but should be application/json` from
Fireworks' own server. This is real progress in itself: unlike the
old client-side `undefined is not a function` (which happened *before*
any request left the process, per the thirty-seventh round), this is
an actual HTTP response from a real server, meaning the request now
gets far enough to leave the process and reach the network — the
`#234` fixes and/or the `#232`-adjacent `.mjs` fix moved something
forward, even though this specific run wasn't set up to isolate which.

Traced the 415 directly rather than guessing: wrote a minimal `fetch()`
repro sending `Content-Type` via a real `new Headers()` object (not a
plain object literal) to `httpbin.org`'s echo endpoint - dropped,
deterministically, every time. Read `pkg/builtins/fetch_init.go`
(read-only): `createHeadersObject` builds a `Headers` instance whose
*only* own properties are its methods (`get`/`set`/`append`/etc, all
`SetOwnNonEnumerable`) - the actual header data lives entirely in
Go-side closure state, never exposed as an own enumerable JS property.
But `doFetchRequestWithContext`'s header-extraction only knows how to
read a plain object's `OwnKeys()`. So `new Headers()` - the constructor
initializer, `.set()`, `.append()`, all of it - silently loses every
header, 100% of the time; a bare object literal passed as `headers`
works fine, which is exactly why this is easy to miss with casual
testing and only bites once real code goes through the spec-correct
`Headers` API - which the `openai` npm SDK dependency does internally
(`buildHeaders()` in its own `internal/headers.js`), explaining the
415 exactly. Filed
[paserati#237](https://github.com/nooga/paserati/issues/237) with the
precise mechanism and a suggested fix location.

While isolating that repro, hit a second, unrelated symptom: a plain
single `fetch()` with no custom headers at all intermittently
(~1-in-15 across repeated runs) failed with `"Top-level await: promise
remains pending with no microtasks to process"` - something real Node
never did across the same repeated testing. Rather than file "flaky",
traced the mechanism: `fetch()`'s success path resolves the returned
promise from one goroutine (`fetchFn`'s own, once
`doFetchRequestWithContext` returns) while a *separate* goroutine (the
body-drain goroutine `doFetchRequestWithContext` spawns internally)
independently decrements the `BeginExternalOp`/`EndExternalOp` counter
the top-level-await drain loop in `vm.go` watches - and nothing
orders these two relative to each other. For a small/fast body (like
httpbin's tiny JSON), the body-drain goroutine can finish and zero the
counter before the other goroutine gets scheduled to actually resolve
the promise, so the drain loop can observe "no pending external ops"
and "still pending" in the same instant and falsely declare deadlock.
Corroborated rather than just asserted: built with `-race` (which
perturbs scheduling and tends to widen real ordering races), the exact
same single-fetch repro went from ~1-in-15 to failing on essentially
every run - and no `WARNING: DATA RACE` was printed, meaning this is a
genuine unsynchronized-ordering bug between two goroutines rather than
a raw memory-safety issue (`-race` would have caught that separately).
Filed [paserati#238](https://github.com/nooga/paserati/issues/238)
with the traced mechanism, the `-race` corroboration, and an honest
caveat that no actual interleaving was captured to prove it outright.

**Net effect**: the Fireworks connection did what it was for - it
replaced "is my stub correct" with "what does a real backend actually
see," and that surfaced two real, previously-invisible `fetch()` bugs
that a stub (or manual testing with plain object header literals, the
easy path) would never have caught, plus fixed a real misconfiguration
in the user's own `models.json`. `pi-ai`'s CLI path is still blocked,
now on `#237` specifically for anything that builds requests via the
standard `Headers` API - which is most real HTTP client code,
including the SDK pi-ai itself depends on. No fakes deleted, no
noderati code changes.

**Follow-up same round, post-advisor-review**: `#237`'s own body had
initially speculated it "may also explain" the thirty-seventh round's
`undefined is not a function` failure — wrong, and corrected on the
issue directly: that older failure was confirmed (with a request-
logging stub) to happen *before any HTTP request left the process*,
so a header-dropping bug can't be its cause; the two are unrelated.
Also ran the discriminating check that should have been in the
original filing: pointed pi's real CLI (`--provider local`, fakes off)
at a raw-socket header logger (no HTTP framework normalizing
anything) instead of Fireworks, to see the literal bytes on the wire.
Confirmed directly rather than inferred: **every** SDK-set header is
gone — `Content-Type`, `Authorization`, `Accept`, `OpenAI-
Organization`, all of it, not just `Content-Type` — only Go's http
client's own defaults (`Host`/`User-Agent`/`Content-Length`/`Accept-
Encoding`) survive. Confirms `#237`'s scope as filed (the whole
`Headers` object is invisible to the request builder) and explains why
Fireworks answered `415` rather than `401`: `Authorization` was
equally missing, so whichever check the server runs first is the one
that surfaces. Posted both corrections as an issue edit + comment on
`#237` rather than leaving a misleading trail for whoever picks it up.

Housekeeping worth recording since it's outside this repo and won't
show in any `git status` here: `~/.pi/agent/models.json`'s `fireworks.
baseUrl` is now permanently fixed (missing `/v1`), and the Fireworks
API key from that file appeared in this session's transcript (read to
diagnose the 404, then passed to a `curl` command to confirm the fix)
— worth a rotation if the user is ever concerned about a transcript
being shared.

**Forty-first round (2026-09-04) — `#235` merged; verified genuinely
fixed; jiti moved past its syntax-error blocker entirely into a new,
more severe compiler crash, root-caused and filed as `#239`.** User
reported the merge; pulled `paserati` `main` (`61959df9`) and
re-verified directly: all three original boundary-sweep repros
(`for`/`if`/`while` with an un-braced regex-starting body) now match
real Node exactly, and the fix ships with its own regression test
(`tests/scripts/regex_after_unbraced_control_flow.ts`). Full build/
vet/test clean.

Re-ran the scoreboard: `fake-off:jiti`/`all-fakes-off` moved again —
`babel.cjs` now **parses completely** (confirms `#235`'s fix is
real, not just passing its own narrow repro), but compiling it now
crashes with `panic: Compiler Error: Ran out of registers!` deep in a
long `compileInfixExpression`→`compileNode`→`compileInfixExpression`→
... recursive stack. Root-caused with a synthetic repro rather than
trying to bisect the minified bundle by hand: a plain left-associative
chain of 300 `x + x + x + ...` compiles fine (each partial sum frees
its temporary immediately), but the same length **right**-nested
(`x + (x + (x + (x + ...)))`) panics past ~120-130 levels — matches
`RegisterAllocator`'s hard 255-register-per-function cap
(`regalloc.go:97/105`) exactly, since a right-nested shape forces the
compiler to hold every outer pending left operand's register live
across the whole remaining right subtree, so live-register count grows
linearly with depth instead of staying flat. Confirmed real Node
handles the identical shape trivially at 1000+ levels.

Also confirmed, via the standalone-repro path (not through noderati's
CJS require() nesting), that this is **worse** than the babel.cjs
symptom suggested: hit from a top-level compile, it's a raw uncaught
Go panic that kills the whole process outright, not a catchable JS
error - the babel.cjs case only looked like a normal `PS4001 [ERROR]`
because it happened to occur while compiling was triggered lazily
inside an already-running VM frame (`require()`), whose own `run()`
loop has a `defer`/`recover()` that incidentally caught it. Filed
[paserati#239](https://github.com/nooga/paserati/issues/239) with
both the minimal repro and this severity distinction, plus a
suggested minimum-safety-net fix (turn the panic into a catchable
compile error even before the deeper register-allocator work lands).

Noticed in passing, not touched: the paserati checkout (shared with
the paserati team's own concurrent work per this session's standing
context) has an uncommitted, in-progress fix for `#237` sitting in
`pkg/builtins/fetch_init.go` - a `SetInternalSlots`-based
`mergeHeadersFrom` that looks like exactly the right shape for the fix
suggested in that issue. Left it alone; not this session's work to
commit or evaluate.

**Net effect**: `#235` is confirmed fixed. jiti's fake is not close to
deletable - the blocker just moved from a parse-time to a compile-time
failure, and the new one (`#239`) is a more severe class of bug (an
uncatchable process crash) than the one it replaced. No fakes deleted.

**Forty-second round (2026-09-04) — `#237`, `#238`, `#239` all merged;
verified all three directly against their exact original repros
(never the closed-issue label alone).** User reported the merges;
pulled `paserati` `main` (`5ba1d1ad`) - the same shared checkout's
in-progress `#237` work noticed last round had landed for real. Full
build/vet/test clean before touching anything.

- `#237` (headers dropped) - verified the best way available: the
  exact real-world case, not just the earlier synthetic `httpbin.org`
  repro. Pointed pi's real CLI (fakes off) at the raw-socket header
  logger from the fortieth round again. Every header the SDK sets now
  arrives - `Authorization: Bearer lm-studio`, `Content-Type:
  application/json`, `Accept`, every `X-Stainless-*` header - nothing
  missing. Genuinely fixed.
- `#238` (intermittent false-deadlock race) - reran the exact
  single-`fetch()` repro 20 times on a normal build (0/20 failures,
  was ~1-in-15) and 5 times on a fresh `-race` build (0/5, was
  near-100% under `-race` before the fix). Genuinely fixed.
- `#239` (register-exhaustion crash) - the original 150-level
  threshold repro now compiles and runs correctly (the "ease
  right-nested chains" half of the fix genuinely raised the practical
  ceiling); pushed further to 1000 and 5000 levels to confirm the
  other half - both now fail as a clean, catchable `PS3001 [ERROR]:
  register exhaustion: expression too deeply nested` compile error,
  exit code 1, no raw panic. Genuinely fixed as scoped (the crash).

Re-ran jiti end to end with the fix in hand: `babel.cjs` still doesn't
run, but the failure mode improved again in exactly the way `#239`'s
fix should - it's now a clean, catchable, well-named compile error
naming the exact file (`@babel/types`' generated assertions index)
instead of a process-killing panic. This is real, live confirmation
that the underlying 255-register architectural cap `#239`'s own body
flagged as needing a deeper fix (wider register operands / more
aggressive spilling) is still reachable by real bundled code, even
past the improved threshold - left as a comment on the now-closed
`#239` (with the exact file and error text) as a ready-made real-world
test case for whenever that deeper fix is picked up, rather than
filing a redundant new issue for the same root cause.

Full scoreboard re-run: `fake-off:jiti`/`all-fakes-off` now show the
new register-exhaustion message at the same `@babel/types` file;
`fake-off:pi-agent-core` and `fake-off:pi-ai` unchanged from last
round. No fakes deleted - jiti's fake stays, its blocker just keeps
changing shape and severity rather than clearing.

**Forty-third round (2026-09-04) — root-caused the actual construct
behind `@babel/types`' register exhaustion: not a right-nested `+`
chain (`#239`'s trigger) at all, but a long comma-operator chain that
issue `#121`'s existing chain-folding fix simply doesn't cover. Filed
`#242`.** User asked to dig into the babel/types failure specifically.
Extracted the real virtual module's source directly from `babel.cjs`
(it's a bundler-packed `"path"(exports,module,require){...}` map, not
a separate file on disk) rather than guessing: `Object.
defineProperty(t,"__esModule",...)` followed by exactly 308
comma-joined `t.assertName=function(...){...}` assignments, all one
expression statement - a completely ordinary minifier statement-
merging pattern, not anything exotic.

Built a minimal synthetic repro of that exact shape (N comma-joined
function assignments) and found the threshold: ~230 terms compile
fine, ~250+ hit the identical `PS3001 register exhaustion` diagnostic
`#239` introduced. Real Node handles the real 308-term case instantly,
as expected.

Read `compile_expression.go` (read-only, as always) to find why comma
specifically, when `+` chains of the same length are already fine
since `#121`: `compileInfixExpression` has a comma-operator special
case (~line 1441, needed for its "discard left, keep right in tail
position" semantics) that recurses into `node.Left` after allocating
a register for it - the exact register-per-recursion-level pattern
the comment on the very next block down (~line 1463) says `#121`
already fixed for arithmetic/comparison/bitwise chains via an
iterative fold (`compileInfixChain`). The comma special case returns
*before* ever reaching that fold path, so `foldableChainLink`'s
explicit operator list (`+ - * / % ** <= >= == != < > in instanceof
=== !== & | ^ << >> >>>`) never gets a chance to include or exclude
comma on purpose - it's excluded by control flow, not by a semantic
decision the way `&&`/`||`/`??` (which need real short-circuit
control flow and genuinely can't fold the same way) are. Comma's
"discard left, evaluate next" semantics are if anything an *easier*
fit for iterative folding than arithmetic accumulation - no running
accumulator needed - and `compile_class.go` already has a
`flattenCommaExpression` helper (built for class-field-initializer
comma chains) that does exactly the tree-flattening a comma-chain
fold would need. Filed
[paserati#242](https://github.com/nooga/paserati/issues/242) with the
exact code locations, the real-world repro, and the fix direction
already sitting in the same file.

**Net effect**: the `@babel/types` blocker now has a precise,
narrowly-scoped root cause distinct from `#239`'s - not a deeper
architectural register-width problem, just one operator that missed
an already-built fix. No fakes deleted, no noderati code changes.

**Forty-fourth round (2026-09-04) — `#242` merged; verified genuinely
fixed against every prior repro (including 5000-term chains); jiti
moved past `babel.cjs` entirely into a fresh, `#242`-caused regression
in its own `jiti.cjs` loader, bisected and filed as `#244`.** User
reported the merge; pulled `paserati` `main` (`c2fea91b`).

Verified `#242` itself first, thoroughly: the synthetic 250/308/1000/
5000-term comma-chain repros from the forty-third round all now
compile and run correctly (previously failing past ~230-250 terms).

Re-ran jiti end to end: it moved *past* `babel.cjs` completely - no
more register-exhaustion error there at all - into a new failure one
file over, in `jiti.cjs` itself (jiti's own loader/bootstrap, a
separate webpack-bundled file, not `babel.cjs`): a raw Go panic,
`index out of range [17] with length 0`, on `OpLoadUninitialized`
inside `vm.run`. Found a one-line standalone repro
(`require("<path-to>/jiti.cjs")` alone, no pi CLI involved) and used
it to bisect cleanly: checked out `pkg/compiler/compile_expression.go`
alone at `5ba1d1ad` (pre-`#242`) inside an otherwise-`c2fea91b` tree -
the repro loads fine; back at `c2fea91b`'s version of that one file -
crashes every time. `#242` is confirmed the cause, isolated to the
single file it touched.

Traced the mechanism precisely via `call.go`'s frame setup
(`requiredRegs := calleeFunc.RegisterSize; newFrame.registers =
vm.registerStack[...:...+requiredRegs]`): some function's
`RegisterSize` - computed by `regAlloc.MaxRegs()` at the end of
compiling that function's body - is coming out as `0` despite its own
bytecode needing at least register 17, so its call frame gets a
zero-length register window and the first register write past that
panics. Read `RegisterAllocator.MaxRegs()`/`Free()`/`TryAlloc()`
directly (read-only) looking for the exact defect and came up empty -
the free-list-based design looks internally consistent for every
scenario checked by hand.

Tried nine synthetic repro shapes to isolate it standalone
(object/class methods - sync, async, generator, async-generator,
static, instance; comma chains as a whole function body, mid-function,
inside a zero-arity IIFE matching jiti.cjs's own outer shape; a comma
chain with a nested closure term inside a function with several
already-active locals) - all compiled and ran correctly. None
reproduced it. Filed
[paserati#244](https://github.com/nooga/paserati/issues/244) with the
bisection, the traced mechanism, and an explicit list of what was
tried and ruled out, recommending the real file (a reliable,
one-line repro) as the better bisection starting point over further
blind synthetic guessing - the same honesty-about-the-boundary
approach used for `#238`'s race when a full interleaving proof wasn't
in reach either.

Cleaned up the env-var-gated `cjs.go` trace added mid-investigation
(added, used to locate `jiti.cjs` as the crashing file, removed before
committing - confirmed via `git diff` showing no residual change).

**Net effect**: `#242` is genuinely fixed. jiti's fake stays - the
blocker is now a fresh regression `#242` itself introduced, one file
further into jiti's real dependency chain than before. No fakes
deleted, no noderati code changes.

**Forty-fifth round (2026-09-04) — `#244` merged; verified genuinely
fixed; jiti moved past both `babel.cjs` and its own register-exhaustion
class of bugs entirely into a new, unrelated bug - unqualified
`Object.prototype` methods (`hasOwnProperty`, `toString`, ...) throw
`ReferenceError` instead of resolving through the global object's
inherited properties. Root-caused precisely and filed as `#246`.**
User reported the merge; pulled `paserati` `main` (`157a2161`).

`#244`'s own title (`fix register-count wraparound at the
255-register boundary`) immediately explained why nine synthetic
repro shapes from the forty-fourth round all failed to reproduce it:
`Register` is a `uint8`, and `RegisterAllocator.MaxRegs()`'s `ra.maxReg
+ 1` silently wraps to `0` in `uint8` arithmetic exactly when a
function's peak usage lands on register 255 - a boundary condition no
hand-written test happens to land on by chance, but a 190KB real
bundle can. Verified directly: the exact `require(jiti.cjs)`
one-liner from `#244`'s own filed repro now loads cleanly. Tried to
reconstruct the boundary condition itself with a synthetic 255-local
function to confirm the exact mechanism further, but it didn't
reproduce even pre-fix (different register-allocation path than
whatever `#244`'s own new `regalloc_test.go` exercises) - didn't chase
that further since the real repro and the commit's own explanation
already settle it. Full build/vet/test clean.

Ran the full `pi` pipeline (`--version`/`--help`/`-p "hello"`, `jiti`
fake off) end to end: jiti now gets *past* both `babel.cjs` and
`jiti.cjs` completely - deep into `@babel/core`'s own transformation
pipeline - before hitting a brand new, unrelated failure:
`ReferenceError: hasOwnProperty is not defined`, thrown from inside
`@babel/core`'s bundled code the moment it references
`hasOwnProperty` bare (`hasOwnProperty.call(t,"sourceMap")` and
`h[hasOwnProperty.call(h,a)?a:".js"]`, found by extracting the exact
virtual module's source from the bundle, same technique as prior
rounds).

Recognized this immediately as a real spec-compliance gap rather than
broken vendor code (real Node ships this exact `@babel/core` build
fine): in sloppy-mode, non-module code, an unresolved bare identifier
falls back to a property lookup on the global object, and `globalThis`
is an ordinary object whose prototype chain reaches `Object.prototype`
- so `hasOwnProperty`/`toString`/`valueOf`/`isPrototypeOf`/
`propertyIsEnumerable`/`toLocaleString`/`constructor`, referenced bare
with no receiver, all resolve via inheritance in real Node. Confirmed
with a clean repro (`typeof hasOwnProperty` → `"function"` in real
Node, `"undefined"` under paserati; calling it bare throws under
paserati) and, critically, isolated the bug precisely by confirming
what *does* work: `globalThis.hasOwnProperty` and `obj.hasOwnProperty`
(ordinary property access) both already resolve correctly under
paserati, matching real Node exactly - so the gap is specifically in
unqualified-identifier resolution, not in prototype-chain lookup
generally.

Root-caused in `pkg/vm/vm.go` (read-only): `OpGetGlobal` and
`OpTypeofIdentifier`'s "not in the heap, check `GlobalObject` for a
manually-defined property" fallback both call
`vm.GlobalObject.HasOwn(name)` - an **own**-properties-only check -
instead of `vm.GlobalObject.Has(name)`, which already exists and is
implemented as a real `[[Get]]`-based lookup
(`pkg/vm/object.go:1373`, the same one ordinary property access goes
through). Checked for other occurrences of the same pattern before
filing: a third `GlobalObject.HasOwn` call in `OpSetGlobal`'s
strict-mode delete-detection is checking something genuinely
different (was this specific previously-read own property removed)
and is correct as-is - not part of the bug. Filed
[paserati#246](https://github.com/nooga/paserati/issues/246) with
both exact call sites and the swap-`HasOwn`-for-`Has` fix direction.

**Net effect**: `#244` is genuinely fixed - two consecutive
register-allocator regressions from the comma-chain work are now both
resolved, and jiti has cleared the entire register-exhaustion class of
blockers. The new `#246` finding is a different kind of bug than the
last several rounds' compiler/VM internals - a general spec-
compliance gap likely to recur in any bundled code that uses this
common `hasOwnProperty`-as-bare-identifier minification pattern, not
specific to jiti or babel. jiti's fake stays; the blocker keeps moving
forward without clearing. No fakes deleted, no noderati code changes.

**Forty-sixth round (2026-09-04) — while `#246` was in progress,
root-caused the long-standing `pi-ai`/`pi-agent-core` CLI-path
`undefined is not a function` mystery (open since a round many
sessions back), reproduced it standalone, and filed
[paserati#247](https://github.com/nooga/paserati/issues/247). Also
added a `fake-off:pi-ai+pi-agent-core` scoreboard row, since the two
individual rows have never measured the coupled pair.** User asked
for something else to work on; offered a choice, picked
`fake-off:pi-agent-core`'s blocker.

First correction before any real digging: `fake-off:pi-agent-core`
alone tests an incoherent combination — pi-ai's fake stays *on* while
only pi-agent-core is disabled, a mismatch the standing coupling note
already flags. That row's `TypeError: Class extends value undefined`
isn't a real finding; it's the expected shape of a broken combination
and always has been. Tested the coupled pair together instead
(`NODERATI_DISABLE_FAKES=pi-ai,pi-agent-core`): `--version`/`--help`
already matched baseline cleanly - real, previously-unmeasured
progress the scoreboard's one-at-a-time-plus-all-off structure has
never been able to show.

`-p` against a live Fireworks completion (real endpoint, `#237`'s
fixed `models.json`) still failed with the exact old `undefined is not
a function`. Traced it precisely this round, now that `#237`/`#238`
had eliminated two major confounding variables (dropped headers, the
false-deadlock race):

- A minimal script using the real `openai` npm SDK directly against
  Fireworks (bypassing `pi-ai` entirely) streamed a complete response
  correctly under noderati - ruling out the SDK's own `Promise`
  subclassing (`APIPromise extends Promise`, overridden `then`/`catch`/
  `finally`) and the fetch/streaming layer as the cause; both work.
  Confirmed the `APIPromise` shape specifically with its own isolated
  repro too - also fine.
- Patched `pi-ai`'s real `openai-completions.js` (backed up, restored,
  `diff`-confirmed after - same methodology used throughout this
  project) with a `try/catch` right around the exact failing call,
  found via a first patch that printed `error.stack` on the
  `stopReason: "error"` path: `getContentIndex`, a closure over `const
  blocks = output.content` declared near the top of a fire-and-forget
  `(async () => {...})()` IIFE that streams chunks in via `for await`.
  Instrumented `getContentIndex` directly per plan: `blocks` was
  `typeof "object"`, **not an array**, `blocks !== output.content`
  even though nothing in the source ever reassigns `output.content` -
  a `const`, still in the same unbroken lexical scope, had lost its
  value.

Reproduced standalone (not guessed cold): built a class with a real
`async *[Symbol.asyncIterator]()` method that `await`s a manually-
created `Promise` before yielding (matching `pi-ai`'s own hand-rolled
`EventStream`), driven via `for await` by one function while a
*separate*, concurrently-running fire-and-forget async IIFE declares a
`const` over a nested property and reads it back through a closure
after its own `await`. Minimal repro corrupts the `const` every time
(3/3+ runs) - in one narrowing variant, the corrupted value was even a
`Symbol`, not just `undefined`, which rules out "silently defaults to
undefined" and points at an unrelated value bleeding in from
elsewhere. Removing any one ingredient (a generator that only `yield`s
without its own internal `await`; never actually consuming the
generator; collapsing producer and consumer into one function) made
the corruption disappear in every variant tried - it's specifically
*two independently-suspended async frames running concurrently* that
triggers it. Filed with the repro, the narrowing notes, and an
explicit "didn't verify against `pkg/vm` source, filed from black-box
narrowing" disclosure (the corrupted-into-a-Symbol detail, plus this
session's three already-fixed register-allocator bugs, points at
frame/register-window isolation for concurrently-suspended async
frames as the likely area, but that's a hypothesis for whoever picks
it up, not a confirmed read of the source).

Added `fake-off:pi-ai+pi-agent-core` to `cmd/scoreboard/main.go`
(`configs`), with a comment explaining why the two individual rows
were never a real signal on their own. Confirmed the new row shows
exactly what was found by hand:
`version[=]`/`help[=]` matching baseline, `print[DIFF]` on the still-
open `#247`. Full build/vet/test clean.

**Net effect**: the multi-round-old pi-ai/pi-agent-core mystery finally
has a name and a standalone repro - not a header/fetch/race issue (all
three already fixed this session), but a genuine async/generator
concurrency bug in the VM. This is very plausibly the single most
consequential finding of the whole push: it blocks the *entire*
group-B pair, not one file in one dependency chain, and it's a
correctness bug (silent data corruption), not merely a missing
feature. No fakes deleted; the scoreboard change is the only
noderati code committed this round.

**Forty-sixth round, follow-up (same day) — corrected #247's own
imprecision.** advisor caught that #247's body blended two different
programs together: it described the `"symbol"`-valued corruption as
"a variant" of the 53-line filed repro, but that repro only ever
produces `undefined` (3/3+ runs) - the `"symbol"` value came from a
separate, larger program closer to `openai-completions.js`'s real
shape (~80 simulated SSE chunks, a `Map`-based lookup, a
`thinkingBlock` mutated across iterations). Reconstructed that larger
program from session notes, re-ran it (Fireworks credentials not
involved this time - purely local, no network), confirmed it still
deterministically prints `typeof blocks: symbol isArray: false
blocks!==output.content` on paserati vs. real Node's clean
`processed 81 events` with nothing printed. Posted a correction
comment on #247 with that program attached as a second, independent
repro, explicit that the filed minimal repro (`undefined` case) is
still the right one for regression-testing a fix, and that this larger
one is the one that actually produced the `Symbol` value.

Also used the comment to sharpen the suspected mechanism: in both
repros, the producer IIFE's `stream.push(event)` doesn't just run
concurrently with the consumer's suspended generator - it's the exact
call that *resolves the promise the generator's own internal `await`
is parked on* (`waiter({value, done:false})` settles what
`[Symbol.asyncIterator]`'s body is suspended on). So the trigger looks
narrower than "two suspended frames coexisting": it's specifically
one frame's own step *resuming the other frame*, at the moment the
corruption becomes observable in the resuming frame. Noted this as a
hypothesis for whoever fixes it, not a confirmed source-level finding
(still haven't read `pkg/vm`'s frame/register-stack code for this).

One more thing noticed in passing and deliberately not chased: an
earlier narrowing variant (`min4.mjs`, not filed) printed the
consumer's `"done"` line *before* the producer's post-`await`
diagnostic line - an ordering real Node cannot produce for that
program (the consumer's `for await` can only see `end()` after the
producer has already run past the point where it prints). That's a
second, distinct scheduling anomaly in the same code path, orthogonal
to the value-corruption bug #247 is about. Recording it here rather
than filing it now: if #247 lands a fix and this ordering is still
observably wrong afterward, it's worth its own issue then, not before.

**Housekeeping note**: while reconstructing the `"symbol"` repro this
round, a real Fireworks API key from `~/.pi/agent/models.json`
appeared in a temp script and thus in this session's transcript for a
*second* time (first time was the Forty-third round's investigation).
Deleted the temp file both times, but the value itself was already in
scrollback each time - flagging plainly rather than treating "the file
is gone" as the fix. Worth rotating that key if this transcript is
ever shared or retained beyond this machine.

**Forty-seventh round (2026-09-05) — verified `#246`/`#247` fixed, hit
the pi-ai+pi-agent-core pair working end-to-end for the first time,
found a noderati gap and a new engine bug along the way.** User
reported `#246`/`#247` plus "some other fixes" merged to paserati
main. Pulled: three new commits landed, not one -
`64b7e5e5` (`#246`), and for `#247` a trio - `88712482` (the actual
fix: stop a suspended generator/async closure's captured local from
aliasing whatever reuses its freed register-stack slot), plus two
follow-ups the paserati team found while fixing it -`64703c62`
(spill slots weren't restored across a suspend/resume, same bug class
as `#247` but for spilled locals instead of register-backed ones) and
`393f229b` (a suspended-frame-reuse path left stale open-upvalue
chains, discovered chasing what first looked like a timer-ordering bug
and turned out to be the same root issue). Re-verified against the
exact original repros rather than trusting "merged" - `#246`'s bare
`hasOwnProperty`/`toString`/etc. repro, `#247`'s filed 53-line minimal
repro (the `undefined` case), and the larger `"symbol"`-producing
program from this session's correction comment - all three clean, the
`#247` ones 3/3 runs. `#247` still shows OPEN on GitHub despite the
fix commit landing - noting the label mismatch rather than assuming
it means anything.

**The headline result**: ran the real pi CLI's actual print-mode path
against a real Fireworks endpoint
(`--provider fireworks --model accounts/fireworks/models/glm-5p2
--no-session -p "..."`) with `NODERATI_DISABLE_FAKES=pi-ai,pi-agent-core`
- the exact command that's been failing with `getContentIndex`'s
`undefined is not a function` since the Forty-third round. It now
returns the correct completion, 3/3 runs, both for a single-word
reply and a multi-line one. This is the first time the coupled
pi-ai/pi-agent-core pair has worked end-to-end against a real backend.
The scoreboard's own `fake-off:pi-ai+pi-agent-core` row still shows
`print[DIFF]` - that's *not* a remaining bug, it's the scoreboard's
`-p "hello"` invocation dialing a local stub at `127.0.0.1:1234` with
nothing listening, same as `baseline` does; the failure text differs
(`Connection error.` vs. baseline's `dial tcp ... connection refused`)
only because the real `pi-ai` HTTP client reports a closed connection
differently than whatever baseline's fake path reports - it's an
artifact of the scoreboard's fixed unreachable test target, not
evidence of anything wrong. `version[=]`/`help[=]` already matched
baseline cleanly. Recording this explicitly so a future reader doesn't
mistake `print[DIFF]` here for a regression.

**jiti**: re-tested with `#246`'s fix in place. Moved past `babel.cjs`
and `jiti.cjs`'s own register-exhaustion class of bugs entirely (both
already confirmed fixed in earlier rounds) into a brand new failure:
`--version` with `fake-off:jiti` now panics inside `debug`'s (an
npm package jiti bundles) Node backend with `undefined is not a
function`. Traced it by hand through the bundled, minified
`debug@4.4.3/src/node.js` module body to `t.destroy=s.deprecate(...)`
- called unconditionally at module-load time - where `s` is
`require("util")`. Two real noderati gaps: `util.deprecate` and
`util.formatWithOptions` didn't exist in `internal/host/util.go` at
all. Implemented both properly (not stubs): `deprecate(fn, msg)`
returns a wrapper that still calls `fn` and stays callable (no
warning emitted - this host has no `process.emitWarning`/`warning`
event machinery for a caller to suppress or listen to, and printing
an ad-hoc line on first call would just be surprise stderr output
mid-run for whoever happens to trigger it first, e.g. `debug`'s own
`destroy()` on teardown); `formatWithOptions`/`format` now share a
real `%s/%d/%i/%f/%j/%o/%O/%%`-directive implementation instead of
just space-joining args (verified: directives substitute correctly,
leftover args append, zero-vararg call doesn't crash). Caught and
fixed one self-inflicted bug before committing: an initial draft of
`deprecate` dropped the unused `msg string` parameter from its Go
signature entirely, which panicked with "reflect: Call with too many
input arguments" the moment `debug` called it with both arguments -
paserati's native-module reflection binding has no arity tolerance,
so a Go function's parameter count must exactly match every call site
in practice, not just the ones that use every parameter.

With that fixed, hit the *next* failure past `debug` cleanly: a raw Go
panic, "value is not a float", inside `tty.isatty(process.stderr.fd)`
- a completely ordinary call `debug`'s node.js backend makes to decide
whether to color its output. Root-caused precisely and standalone
(`tty.isatty(process.stderr.fd)` alone reproduces it, no jiti/babel
involved): `pkg/driver/native_module.go`'s `vmValueToReflectValue`
converts a `vm.Value` argument into a Go native-module function's
`float64`/`float32`/`int`/`int64` parameter by calling `.AsFloat()`
whenever `vmVal.IsNumber()` is true - but `IsNumber()` covers *both*
of paserati's internal number representations
(`TypeFloatNumber` and `TypeIntegerNumber`), while `.AsFloat()` is a
raw accessor that panics on anything but `TypeFloatNumber`.
`process.stderr.fd` is set via `vm.IntegerValue(...)` in
`internal/host/process.go` - an entirely ordinary host-side value,
indistinguishable from any other JS number at the JS level. The
engine already has the right general conversion for this
(`Value.ToFloat()`, which every one of paserati's own builtins that
needs a number - e.g. `Math.abs` - goes through via `VM.ToNumber`);
`vmValueToReflectValue` just reaches for the wrong one in three
identical spots. Filed as
[paserati#252](https://github.com/nooga/paserati/issues/252) with the
standalone repro, the exact three call sites, and the one-line fix
(`.AsFloat()` → `.ToFloat()`, no signature changes needed). Posted a
follow-up correction after advisor caught that the issue's Impact
section overclaimed string `.length` as a second reachable path -
checked directly, it isn't (that `IntegerValue` use in `vm.go:17825`
is in a narrower path than plain string `.length` reads); narrowed the
issue to what's actually confirmed (`process.stdout/stderr.fd` and,
by construction, any other host-set `IntegerValue` property).

jiti's fake stays blocked - now on `#252`, a fresh, precisely-scoped
engine bug rather than any of the six prior blockers. Committed
`internal/host/util.go`'s two new real implementations (build, vet,
`go test ./internal/host/...` all clean) - no fakes deleted this
round; the pi-ai/pi-agent-core pair's real-network verification is
strong enough that deleting `fake-off:pi-ai`/`fake-off:pi-agent-core`
looks like the obvious next step, but that's a call to make explicitly
next round, not fold into a check-in-progress-fixes round.

**Forty-eighth round (2026-09-05, same day) — deleted pi-ai/pi-agent-core's
fakes, verified `#252` fixed, found and filed jiti's next blocker
(`#254`).** User asked directly whether pi-ai/pi-agent-core could be
marked done given `#252` was "underway," or whether they needed more
fixes first. Investigated rather than assuming: read pi-ai's own
`package.json` and every one of its `dist/api/*.js` provider files -
only `bedrock-converse-stream.js` imports `http-proxy-agent`/
`https-proxy-agent` (and through them `debug`), and it does so via a
genuinely lazy dynamic `import()` (`bedrock-converse-stream.lazy.js`,
confirmed by reading it), matching the empirical fact that the
Forty-seventh round's real Fireworks test never touched it. Confirmed
directly that requiring `https-proxy-agent` standalone fails today on
`Cannot find module 'net'` - noderati has no `net` module at all, a
separate, pre-existing Phase 5 gap unrelated to `#252` or `#247`.
Checked the Google/Vertex provider path too (`google-logging-utils`,
which is what would pull in `debug` there) - its `debug` wiring is
opt-in via an explicit `setBackend(getDebugBackend(require('debug')))`
call that nothing in this dependency tree actually makes, so it's dead
code here. Net: `#252`/`#247` landing doesn't gate pi-ai/pi-agent-core's
main path at all - every non-Bedrock provider was already clear before
`#252` even existed; `#252` was really jiti's blocker specifically,
discovered by continuing past `#246`'s fix into `debug`'s own
`tty.isatty(process.stderr.fd)` call. Presented this via AskUserQuestion
- user chose "mark done now, note Bedrock gap."

Deleted `internal/host/piagentcore.go` entirely and stripped
`internal/host/piai.go` down to just `findPiCodingAgentNodeModulesRoots`
(the real resolver helper `New()` still needs - not part of the fake),
replacing the removed shim declarations with a dated note carrying the
verification and the Bedrock caveat. Removed the two
`declarePiAi()`/`declarePiAgentCore()` calls from `host.go`. Three
existing unit tests (`TestPiAiCompatShim`, `TestPiAiStreamSimpleFetchError`,
`TestPiAgentCoreShim`) tested the *fake's own* invented API surface
(a `/compat` and `/oauth` subpath that never existed in the real
package) - replaced with `TestPiAiReal`/`TestPiAgentCoreReal`, network-
free smoke tests against the real packages' actual exports
(`modelsAreEqual` from `models.js`, `Agent`/`uuidv7` from the real
`agent.js`/`harness/session/uuid.js`) matching the pattern already used
for `TestHostedGitInfoReal`. Updated `cmd/scoreboard/main.go`:
`fakeNames` drops to just `jiti`; the coupled-pair combined row (added
Round 46, obsolete the moment there's nothing left to combine) is gone
too. `go build`/`go vet`/`go test ./...` all clean.

Meanwhile the user reported `#253` (the PR fixing `#252`) merged. Pulled
paserati main (`c987c5b1`): confirmed the fix covers more than my own
filed repro - `vmValueToReflectValue`'s three `.AsFloat()` call sites
plus a fourth I hadn't noticed (`ValueConverter.convertVMValueToReflectValue`,
a duplicate of the same logic) all now use `.ToFloat()`, and the
Int/Int64 case additionally now does `.Convert(targetType)` /
`reflect.Zero(targetType)` instead of assuming `int64` always matches
the target - a real additional fix beyond what I'd suggested. Re-ran
the exact standalone `tty.isatty(process.stderr.fd)` repro: clean, 3/3.

Ran the full `fake-off:jiti` pipeline again with `#252` fixed: past
`debug`'s `tty.isatty` crash entirely, past `babel.cjs`'s whole
config-chain loading, into a *new* failure one file over -
`@babel/plugin-proposal-decorators/lib/transformer-legacy.js`,
`undefined is not a function`. Root-caused precisely via the same
backup/patch/test/restore methodology used on `openai-completions.js`
earlier this session (temporary diagnostic `console.log`, confirmed
byte-identical restoration via `diff` after): the real `@babel/template`
package builds its public export as
`t.default = Object.assign(i.bind(void 0), { smart: i, statement: o,
statements: a, expression: l, program: p, ast: i.ast })` - a bound,
callable function with named sub-builders `Object.assign`ed onto it -
and `@babel/core` re-exports that via a lazy `template` getter
(`get template() { return _template().default }`). The decorators
plugin then does `n.template.statement(...)` at module-load time;
`n.template.statement` comes back `undefined`.

Minimized standalone, outside jiti/babel entirely: `Object.assign`
onto *any* function value (bound or plain - narrowed both ways)
silently drops every property (`hasOwnProperty` false afterward,
`Object.assign` onto a plain object or array in the same script works
fine). Direct assignment (`fn.prop = x`) does work and reads back
correctly - only `Object.assign`'s copy path is broken for a function
target. Found a second, related defect while narrowing: a *bound*
function's directly-assigned own property is readable
(`hasOwnProperty` true, correct value) but isn't enumerable
(`Object.keys`/`for...in` both miss it), while the identical operation
on a plain function is enumerable correctly - very likely the same
underlying gap (function values not getting a normal property bag the
rest of the object machinery agrees on), filed together rather than as
two issues. Filed as
[paserati#254](https://github.com/nooga/paserati/issues/254) with both
repros and the exact `@babel/template` real-world trigger. Caught and
fixed one overclaim in the Impact section before it shipped (advisor,
the third time this session after `#237`/`#247`) - "a pattern used
elsewhere in the ecosystem" had exactly one confirmed instance;
narrowed to just that.

**Status after this round**: pi-ai/pi-agent-core done for every
provider path except Bedrock (a pre-existing, separate gap - no `net`
module at all - not something this round's work changed). jiti stays
blocked, now on `#254` - its eighth distinct blocker shape this
project, each one real forward progress even though the fake still
isn't deletable. Re-ran the scoreboard post-deletion: `baseline`'s
`print` failure text changed from the old fake's `dial tcp
127.0.0.1:1234` wording to the real pi-ai client's `Connection error.`
- expected, since pi-ai is unconditionally real now and nothing is
listening at the scoreboard's stub target; not a regression.

**Forty-ninth round (2026-09-05, same day) — `#254` merged upstream,
verified; scoreboard shows `fake-off:jiti`/`all-fakes-off` clean for
the first time ever, but that's a scoreboard-blind-spot, not a green
light - found and fixed four real noderati gaps chasing jiti's actual
runtime path, four deep with an unknown number still ahead.** User
reported `#254`'s fix merged. Pulled paserati main (`8599a674`): one
commit fixes both repros together (`Object.assign` onto a function
target, and a bound function's enumerability), confirming advisor's
earlier guess that they shared a root cause. Re-verified both original
repros standalone (3/3 each) and the exact `@babel/template`
construction shape (`Object.assign(fn.bind(...), {...})`) - all clean.

Re-ran the full `fake-off:jiti` pipeline: `--version` and `--help` both
now succeed with **no error at all** - past `debug`, past all of
`babel.cjs`'s config-chain loading, past the decorators plugin,
straight through to real output. Ran the scoreboard: `fake-off:jiti`
and `all-fakes-off` both match `baseline` on all three invocations -
**the first time in this entire project `all-fakes-off` has ever gone
clean.**

Did not treat that as license to delete jiti's fake. Per this doc's
own established rule (first stated for pi-tui's deletion, restated for
pi-ai/pi-agent-core last round): CLI-invocation matching is necessary
but not sufficient, and here it's less sufficient than usual - none of
the scoreboard's three invocations (`--version`/`--help`/`-p`) ever
actually call `createJiti()`. `loader.js` only *imports* `jiti/static`
(which is why babel.cjs's whole graph loads and why `#242`/`#244`/
`#252`/`#254` all surfaced) - `loadExtensionModule` only invokes
`createJiti` when there's an actual extension file to load, which none
of pi's own three scoreboard invocations do. So the clean scoreboard
proves jiti's *import graph* is now fully clean, and says nothing
about whether jiti's actual job (transforming and loading a real
TypeScript extension file) works at all.

Tested that directly: a real `.ts` extension file (interface, enum,
default-typed values - genuine TS syntax needing real transformation,
not just syntax jiti could pass through untouched) loaded via jiti's
own real API, exactly as `loader.js:302`'s `createJiti(...)` /
`jiti.import(...)` call shape (confirmed via both `jiti/lib/jiti.mjs`
and, to be sure it's not entry-point-specific, `jiti/static`'s real
`jiti-static.mjs` - identical crash shape either way). Found and fixed
four real noderati gaps in sequence, each only visible once the
previous one stopped blocking:

1. `require.resolve` didn't exist at all (`require.resolve`,
   `Module.createRequire(...)`'s returned require - neither had it),
   traced to `createJiti`'s `N.resolve.paths` access
   (`N = Module.createRequire(...)`) throwing "Cannot read property
   'paths' of undefined" because `N.resolve` itself was undefined.
   Implemented `require.resolve(specifier[, options])` in
   `internal/host/cjs.go`, delegating to the exact same resolution
   logic `require()` itself already uses (not a new algorithm - stays
   inside Phase 4's boundary deliberately), honoring
   `options.paths` by anchoring at its first entry (the one real
   caller found so far). Also added `.resolve.paths()`, `.cache`,
   `.extensions`, `.main` as presence-only placeholders (nothing found
   so far reads them beyond copying them onto jiti's own wrapper
   object) and `Module._nodeModulePaths` - a genuinely well-defined,
   self-contained utility (every ancestor's `node_modules`
   subdirectory, closest first, pure path arithmetic, no
   package.json/exports handling), not the real resolution algorithm
   Phase 4 is still about.
2. `Module.builtinModules` didn't exist, hit as
   `Be.builtinModules.includes(name)` ("Cannot read property
   'includes' of undefined") in jiti's own native-vs-transform
   bundling check. Implemented as a sorted array derived directly from
   `nativeRequireNames` (the same hand-maintained list `require()`
   itself already checks) rather than a separately-maintained list -
   noted in that list's own comment that it now has a second
   consequence (a name missing there is now a *wrong* answer to "is
   this a builtin", not just an unreachable `require()`).
3. `crypto.createHash("md5")` wasn't supported (only `sha256` was) -
   hit via jiti's own filesystem cache-key hashing
   (`getCache`/`utils_hash`), an entirely ordinary non-cryptographic
   use. Added `md5`/`sha1`/`sha384`/`sha512` alongside the existing
   `sha256` in `internal/host/crypto.go`.
4. Past all three, hit a new failure inside `eval_evalModule` itself -
   `TypeError: undefined is not a constructor` - genuinely inside
   jiti's module-evaluation core, not a shallow host-builtin gap like
   the three above. Did not chase this one down this round.

Caught one correctness issue on `require.resolve` before committing
(advisor): real Node's `require.resolve("node:fs")` returns
`"node:fs"` verbatim (prefix round-trips), but the implementation was
returning the stripped `"fs"` - fixed to return the original
specifier for builtins, verified against both prefixed and bare forms.
Full `go build`/`go vet`/`go test ./...` clean; re-ran the scoreboard
after these fixes too - unchanged (still clean on all three
invocations, as expected, since none of them exercise this code path
either).

**Honest status, not an estimate**: jiti's real transform path is
untested past `eval_evalModule`'s own `undefined is not a constructor`
failure. Four host gaps fixed getting this far into it in one sitting,
each one revealed only by fixing the last - deliberately stopping here
rather than guessing how many more remain. jiti's fake stays exactly
where it was: not deletable, and the scoreboard's all-clean row is a
known, documented blind spot, not evidence to the contrary.

**Fiftieth round (2026-09-05, same day) — picked up the
`eval_evalModule` crash: root-caused and fixed two more real gaps,
jiti's self-contained transform path now genuinely works, but an
extension that imports anything still fails, six blockers deep.**
User asked to pick up where the last round stopped.

Root-caused `undefined is not a constructor`: jiti's own module-loading
core does `new Be.Module(c)` (`Be = require("module")`) to build a bare
module record for a freshly transformed file. Real Node's
`require("module")` IS the `Module` constructor itself
(`Module.Module === Module`, self-referencing), with
`builtinModules`/`_nodeModulePaths`/`createRequire` as static
properties directly on it - confirmed field-by-field against real
Node (`new Module(id)` gives `id`, `path` (`dirname(id)`), `exports:
{}`, `filename: null`, `loaded: false`, `children: []`). noderati's
`module` shim was a plain namespace object holding those three as
named exports, not a constructable class. Rewrote
`internal/host/module.go` as a real `class Module` matching the shape
exactly, with the three statics attached directly on it. First
cross-builtin import among noderati's own shims (`module` importing
`path` for `dirname`) - noted as safe today (no cycle back) but worth
knowing about.

That cleared into "reflect: Call with too many input arguments" from
`vm.runInThisContext(code, {filename, lineOffset, displayErrors})` -
jiti passes real Node's actual optional options argument, which
paserati's native-module reflection binding (zero arity tolerance,
same class of bug as `util.deprecate` two rounds ago) doesn't accept a
slot for. Added tolerant (accepted, ignored - matching this file's own
already-documented "no Script constructor options" scope) trailing
options parameters to `createContext`/`runInThisContext`/
`runInContext`/`runInNewContext` and the `Script` class's constructor
and three `run*()` methods.

With both fixed: **jiti's actual transform-and-load path now works**.
A real `.ts` extension file (interface, enum, default export) and a
second, more complex one (private class field, async/await, a real
`setTimeout`-based await) both loaded via jiti's real API - both entry
points that matter (`jiti-static.mjs`, the one pi's own `loader.js`
actually imports, and plain `jiti.mjs`) - produced output identical to
real Node, 3/3 runs, both shapes. First time jiti's *actual job* (not
just its import graph) has been verified working.

Per advisor: neither test extension imported anything from inside the
transformed file, and every real pi extension does exactly that -
tested the discriminating case directly (one bare specifier, one
relative file). Hit a real `undefined is not a constructor` again, one
layer up: `new URL(relativeSpec, parentFileURL)` - real Node's optional
second `base` argument for resolving a relative URL, which
`internal/host/url.go`'s `newJSURL` didn't accept at all (one
parameter only). Implemented it via Go's `url.URL.ResolveReference`
(RFC 3986 relative resolution - not WHATWG-exact, but every real
base+relative-path combination found so far doesn't need the
difference, same disclosure style as the file's existing "no live
setters" scope note - `jsURL` is still a construction-time snapshot,
`base` resolution included, not a live-recomputing URL). Verified
against real Node: resolving a sibling path, a parent-directory path,
the absolute-no-base case, and the correct-throw-on-truly-relative
case all match exactly.

That cleared into a sixth blocker, this one **located but not
root-caused**: transforming the *imported* file (not the entry file)
throws inside babel's own `gensync`-based `transformSync` -
`Error: Got unexpected yielded value in gensync generator: undefined.
Did you perhaps mean to use 'yield*' instead of 'yield'?`. Traced via
the same backup/patch/test/restore methodology used all session:
splitting a fused conditional expression
(`r.babel&&Array.isArray(r.babel.plugins)&&...`) into separate
statements with prints between them changed the *visible* symptom from
`TypeError: Cannot read property 'error' of undefined` to this gensync
error - meaning the `.error of undefined` was a downstream mask, not a
separate bug, and gensync's `${JSON.stringify(e)}` in its own error
message renders as `"undefined"` for a wrong *Symbol* too (symbols
aren't JSON-serializable in real Node either), so the literal word
"undefined" in the message doesn't mean the yielded value actually was
JS `undefined`.

Formed and disproved two hypotheses rather than guessing which one is
right: (1) a blanket generator/yield-value bug - built a minimal
gensync-shaped repro (a real `Symbol.for` start-sentinel, a real
`function*` yielding it, driven by the same `evaluateSync` loop
gensync itself uses) outside jiti/babel entirely; matches real Node
exactly, no bug found. (2) a `Symbol.for` registry split across
nested/recursive call contexts (jiti's recursive `createJiti` call for
an imported file looked like it could do this) - tested `Symbol.for`
identity across a nested function call, across a `yield`, and across a
real `await`; all three match real Node exactly. Both ruled out
empirically, not by inspection. What's left is somewhere inside
`@babel/core`'s own, more complex nested gensync chains (config
loading/plugin resolution for a *second*, freshly-encountered file) -
not reproducible standalone, so not filed; recorded here as
located-not-root-caused with the four things ruled out, for whoever
picks this up next (including a future round of this same
investigation).

**Status**: jiti's transform path genuinely splits in two now - a
self-contained `.ts` extension (no imports) works correctly, verified
two shapes/two entry points/3-for-3; an extension that imports
anything (the shape every real pi extension actually has) still fails,
now inside babel's own gensync machinery rather than any host gap.
jiti's fake stays not-deletable. `go build`/`go vet`/`go test -count=1
./...` and the scoreboard all clean; only `internal/host/url.go`'s fix
was uncommitted mid-investigation, committed with this entry.

**Correction (same day, filed with the next round's entry below): the
"self-contained `.ts` extension works" claim above is false. It was a
false positive from jiti's own filesystem cache** (`fsCache`, written
under `$TMPDIR/jiti` and `$TMPDIR/node-jiti`, keyed by content hash -
independent of which engine produced the cached output). The "self-
contained, no imports" test in this round ran *after* an earlier real-
Node run had already populated that cache for the same file content;
noderati's run then silently read the real-Node-produced cached
transform instead of exercising its own transform path at all. Caught
next round by explicitly clearing both cache directories and passing
`fsCache: false` to every `createJiti(...)` call - at which point the
previously-"working" self-contained case immediately failed with the
same error as the import case. There is no working/failing split: every
transform was broken by the same bug (paserati#256, see below) once
cache-masking was removed. The "six blockers deep" / "sixth blocker,
gensync" framing above was chasing a downstream symptom of that same
bug, not a distinct one. **Methodology note for next time**: any jiti
test needs both `fsCache: false` *and* `rm -rf` on `$TMPDIR/jiti` /
`$TMPDIR/node-jiti` beforehand, or a prior real-Node run (or even a
prior noderati run) can silently make a broken path look like it works.

**A second correction, found next round**: this round's "restored
babel.cjs cleanly (diff-confirmed)" claim was also false - the
`[DBG]`-prefixed print statements from this round's bisection were
still present in the real, installed
`.../jiti/dist/babel.cjs` at the start of the *next* round, meaning
every real-Node baseline and every noderati run in this round actually
ran against a modified file, not the pristine package. The backup this
round relied on (`/tmp/gensync_dig/babel.cjs.orig`) was itself deleted
as part of this round's own end-of-round temp-file cleanup, so there
was no way to re-verify the restore after the fact - the "diff-
confirmed" claim was checked once, immediately after the patch/restore
cycle, and then silently didn't survive to the next session. This is a
second, independent contamination source alongside `fsCache` above -
not a duplicate of it. Fixed going forward (see next round): verify a
restored real npm file against `npm pack <name>@<version>` output
extracted fresh, not only a `/tmp` backup that a later cleanup step can
delete out from under the claim.

**Fifty-first round (2026-09-05, same day) — root-caused the "gensync
bug" precisely: it isn't gensync's, it's paserati's compiler, and it's
not the sixth blocker, it's the *only* one. Filed as paserati#256.**
User asked to keep digging on the gensync bug and file an issue.

Rebuilt the repro environment fresh (the prior round's `/tmp` scratch
dir had been cleaned up) and immediately hit the fsCache trap described
in the correction above - clearing it changed the symptom. With the
cache genuinely out of the way, re-bisected inside `jiti/dist/babel.cjs`
using the same backup/patch/print/restore/diff-confirm method as every
prior round: split the fused
`r.babel&&Array.isArray(r.babel.plugins)&&c.plugins?.push(...r.babel.plugins);`
line into separate statements with a print between each, narrowing the
silent-stop point down to the `c.plugins?.push(...r.babel.plugins)` call
itself. Restored `babel.cjs`/`jiti.cjs` clean afterward (diff-confirmed
byte-identical).

Reduced to a minimal, jiti/babel-independent repro:
`obj?.push(...arr)`. Confirmed the shape and both its symptom variants
directly:

```js
const arr = [1, 2, 3];
const obj = { push: (...a) => console.log("pushed", a) };
console.log("before");
obj?.push(...arr);
console.log("done");
```

Real Node: `before` / `pushed [ 1, 2, 3 ]` / `done`. Paserati: prints
`before` only - no `pushed`, no `done`, no thrown error, no non-zero
exit, `try/catch` around it catches nothing; execution just silently
stops making progress past that statement. With `obj = null` instead
(the chain's own short-circuit should apply, evaluating to `undefined`
without calling anything - confirmed directly against real Node with
`console.log(JSON.stringify(obj?.push(...arr)))`, which prints
`undefined`, not just inferred from spec): paserati instead crashes with
`[VM Debug] Unknown opcode 255 at ip=55` - a raw invalid-bytecode panic,
not a JS-level exception.

Checked `gh issue list --search "optional chaining spread"` before
filing anything new: found #188 (CLOSED), read its full body, and
re-verified both its original repros still pass right now. #188 covers
`obj.method?.(...args)` - optional **call** on a plain member-expression
callee. This is the mirror image, `obj?.method(...args)` - optional
**member access**, with an ordinary trailing call folded into the same
chain per spec. Isolated the exact discriminating boundary with control
variants, all run against both real Node and paserati in the same
script: `obj.push?.(...arr)` (optional call, #188's shape) works;
`obj?.push(...arr)` (optional access, spread call) is broken;
`obj?.push(1,2,3)` (optional access, literal args) works;
`obj?.["push"](...arr)` (bracket variant) is broken the same way;
`f?.(...arr)` (no member access at all) works. Confirms a genuinely
distinct, unfixed bug, not a regression or partial fix of #188.

Root-caused by reading paserati source directly (no edits, no commits -
not my repo): `pkg/parser/parser.go`'s `parseOptionalChainContinuation`
(~line 8809) parses `obj?.method(...)` as a single
`OptionalChainingExpression` node whose `.Continuation` is a
`*parser.CallExpression{Function: nil}` placeholder, filled in at
compile time - a different AST shape from #188's, built by a different
parser path entirely.
`pkg/compiler/compile_expression.go`'s
`compileOptionalContinuationWithReceiver` (~line 393) compiles that
continuation; its `case *parser.CallExpression:` branch (~lines 465-509)
is a second, separate call-compiling path from the main
`compileCallExpression` dispatcher - and unlike that dispatcher (which
checks `c.hasSpreadArgument(node.Arguments)` at ~line 2267 and routes to
`compileSpreadCallExpression`/`compileMultiSpreadCall` for real spread
handling), this continuation branch has **no spread check at all**: it
computes `argCount := len(node.Arguments)` (wrong when one argument is a
spread - 1 instead of the spread's actual runtime length) and compiles
each argument, spread included, via the generic `compileNode` dispatch
into a fixed contiguous register block, then emits `emitCall`/
`emitCallMethod` against that block. Whatever `compileNode` emits for a
bare `*parser.SpreadElement` in this position is bytecode the fixed-
block calling convention was never designed to receive - consistent
with both observed symptoms (sometimes corrupts the following bytecode
stream badly enough to leave an invalid jump target behind, landing on
`255 255`; sometimes the corruption is silent).

Filed as
[paserati#256](https://github.com/nooga/paserati/issues/256), with the
full repro (both variants), an explicit "this is NOT #188" section with
the control variants, the exact root-cause file/line locations and
buggy snippet, and a suggested fix (add the same
`hasSpreadArgument`/spread-aware-array-building path the main dispatcher
already has, for both the `emitCallMethod` and `emitCall` cases).
Per-advisor correction made before filing: softened an initial "this is
very likely the root cause of jiti's blocker" claim to state plainly
that it's *a* confirmed blocker on that path (a real call site in
babel.cjs hits this exact shape) without claiming it's the *only* one -
the full jiti pipeline hasn't been re-run against a fixed paserati
build yet, so anything waiting behind #256 is still unknown.

**Status**: no noderati source changed this round (paserati-side
root-causing and issue-filing only). jiti's fake stays not-deletable,
now blocked on a single, precisely-identified, filed upstream compiler
bug (#256) instead of a vague "gensync" symptom. Re-testing jiti's full
pipeline is pending #256 landing upstream - not attempted yet.

**Fifty-second round (2026-09-05, same day) — pulled paserati main with
#256's fix merged, verified it, verified #258 (a sibling bug filed by
the paserati maintainer while fixing #256), then found and filed a
second, distinct this-binding bug one call site further into jiti's
real pipeline: paserati#260.** User asked to pull latest paserati main
and resume.

`git pull` hit a `cannot lock ref 'refs/remotes/origin/main'` race (the
paserati repo is concurrently edited by its own maintainer/agent in the
same shared checkout - expected per this doc's standing note); a retry
a few seconds later succeeded cleanly. Fast-forwarded to `5c9d39a1`,
three commits ahead of the `8599a674` this doc's ledger last recorded:
`250d7de6` (fix: thread `this` through `obj?.[expr](...)` continuations
- turned out to be the fix for paserati#258, filed by the maintainer
while working on #256), `e89ae9db` (fix: handle spread args in
optional-chain call continuations - #256's actual fix), `5c9d39a1`
(a doc/comment fixup). `go build`/`go vet` clean on both repos.

Re-verified **#256 fixed**, precisely, against both original repro
variants plus every control from the filing: `obj?.push(...arr)` (was:
silent full-script abort) now prints `pushed [1, 2, 3]` / `done`,
matching real Node exactly; the `obj = null` variant (was: `Unknown
opcode 255` crash) now evaluates to `undefined` with no crash, also
matching real Node exactly; all four controls (`obj.push?.(...arr)`,
`obj?.push(1,2,3)`, `obj?.["push"](...arr)`, `f?.(...arr)`) still work.

Re-verified **#258 fixed** (found by the maintainer, not filed by this
investigation, but load-bearing for jiti so re-checked directly rather
than trusted from the "CLOSED" label alone): `o?.["m"](5)` now returns
`12` with the correct `this`, matching `o?.m(5)`'s already-correct
result.

With both confirmed, re-ran jiti's real transform pipeline (import
shape, `fsCache: false`, cache dirs cleared first) against a fresh
two-file `.ts` extension (interface, enum, private class field,
async/await). **Correction, caught next round**: the line originally
here claimed "the transform itself now succeeds and produces correct
output" - that was wrong, the same "got further" ≠ "worked" mistake
made twice already this investigation (the fsCache false-positive, the
babel.cjs restore false-positive). Execution reaching babel's own
error-formatting code (below) *means* a transform error already
occurred (`__JITI_ERROR__` was already set) - no output was ever
produced or checked at this point, only the absence of the two
previously-fixed crashes was observed and mistaken for success. It
then hit a **new, distinct crash**:
`TypeError: String.prototype.replace called on null or undefined`
inside babel's bundled error-formatting code
(`e.code?.replace("BABEL_","").replace(...)`, used to sanitize a Babel
error's code/message for jiti's own error-reporting path). Re-patched
`babel.cjs` with one line of debug printing (backup taken *and this
time cross-checked against a freshly-`npm pack`'ed `jiti@2.7.0` tarball,
not only the `/tmp` backup - see the correction on round 51's entry
above) to confirm the real error being formatted was itself a genuine
`GENSYNC_EXPECTED_START` gensync error with a well-formed `.message`
and `.code` - ruling out a "the thrown error object is malformed" theory
before restoring the file (diff-confirmed against the `npm pack` output
this time, not just the deleted `/tmp` copy).

Minimized outside jiti/babel entirely: `e.code?.replace("a","").replace
("b","c")` (no spread, no bracket access - both already-fixed shapes)
throws the same TypeError under paserati, prints the correct string
under real Node. Built a `this`-tracing repro
(`makeChainable`/`.next().next()`) to see *why*: the **first** chained
call after an optional-chain continuation gets the correct `this`
(confirmed: `this===obj? true`), the **second** call chained onto the
first call's result does not (`this===obj? false`, silently becomes a
plain function call) - and separately confirmed the same loss happens
whether the continuation starts via dot-access or via `?.[...]`
(#258's shape), so this is a different bug from both #256 (spread) and
#258 (bracket-form *first* call), at the same fix site
(`compileOptionalContinuationWithReceiver`) but a different call depth.
Root-caused by reading the current `pkg/compiler/compile_expression.go`
directly: the function's single `receiverReg` parameter, threaded
unchanged through every recursive call, is only ever used to set `this`
when `node.Function == nil` (`isFirstCall`) - any *later* call in the
same continuation, whose `Function` is itself a `MemberExpression`
wrapping an earlier call's result, computes the right object register
internally (`objReg`, in the `MemberExpression` case) but never surfaces
it back up as a receiver, so the enclosing `CallExpression` falls
through to a plain, receiver-less call.

Filed as
[paserati#260](https://github.com/nooga/paserati/issues/260), with
both repros (a `this`-tracing method-chain and the minimal
`.replace().replace()` case), the bracket-form cross-product test, the
explicit "not #256/#258" distinction (both original repros re-verified
against current `main` before filing), the exact root-cause walkthrough
of the compiler's recursive receiver handling, and a suggested fix
(compile a non-first call's `MemberExpression`/`IndexExpression`
`Function` sub-expression as its own object+property pair and use that
call's own object register as its receiver, independent of
`isFirstCall`/the outer `receiverReg`).

**Status**: no noderati source changed this round. jiti's fake stays
not-deletable - one confirmed blocker down (#256), one sibling bug
independently found and fixed by the maintainer (#258), one new
blocker filed (#260) one call site further into the same real pipeline.
Re-testing the full jiti pipeline remains pending #260 (and anything
past it) landing upstream - not attempted past this point this round.

**Fifty-third round (2026-09-05, same day) — pulled #260's fix, verified
it, found jiti's real transform *never* actually succeeded (a third
"got further ≠ worked" correction, see above), root-caused a precise
generator/closure variable-shadowing bug and a masking
`Error.captureStackTrace` gap, filed both.** User asked to pull latest
paserati main and continue.

Pulled `349821bb` (#260's fix: "give every property-access call in an
optional-chain continuation its own receiver"). Re-verified #260
directly against all three of its filed repros (the `this`-tracing
method chain, the minimal `.replace().replace()` case, the bracket-form
cross-product) plus regression-checked #256 and #258 - all match real
Node exactly, no regressions.

Re-ran jiti's real transform pipeline (import shape, `fsCache: false`,
cache cleared) - it progressed past the `.replace().replace()` crash
site into a **new** crash: `TypeError: undefined is not a function` at
jiti's own module-loader `next()` function. Traced this immediately
(not by design, by necessity): jiti's real, unmodified `dist/jiti.cjs`
unconditionally calls `Error.captureStackTrace(l, jitiRequire)` in its
error-reporting path with no feature check - and paserati's `Error`
doesn't implement `captureStackTrace` at all (`typeof
Error.captureStackTrace === "undefined"`, confirmed directly). This
means *any* real transform error - regardless of what it actually is -
crashes jiti's own attempt to report it, hiding the real error
entirely. Filed as
[paserati#263](https://github.com/nooga/paserati/issues/263).

To see past this masking crash for diagnosis (not as a permanent
workaround - #263 stays filed and open), guarded jiti.cjs's one call
site (`Error.captureStackTrace&&Error.captureStackTrace(...)`,
backed up and restored/diff-confirmed against a freshly-`npm pack`'ed
tarball afterward) rather than polyfilling `Error.captureStackTrace`
globally in the test script - a global polyfill would have also flipped
a *different*, feature-detected `s` flag inside babel.cjs itself
(`s=!!Error.captureStackTrace&&...`, gating a `beginHiddenCallStack`
stack-trace-customization wrapper), contaminating the very thing being
diagnosed. Confirmed by testing both ways: the same real error surfaces
identically with or without the global polyfill, so this specific
concern didn't end up mattering for this bug, but the guarded,
single-call-site patch is the methodologically clean choice regardless
and is what's recorded as the approach.

With the masking crash bypassed, the real error underneath was, again,
`GENSYNC_EXPECTED_START: Got unexpected yielded value in gensync
generator: undefined...` - and testing showed it now happens on
**every** transform, even the most trivial possible input
(`console.log("hello");` alone, no imports, no TS syntax at all) -
meaning the round-52 claim that "the transform itself now succeeds" was
never actually true; no successful transform has yet been observed this
entire investigation once #256/#258/#260's masking was removed (see the
correction on round 52's entry above).

Instrumented gensync's core driving loop (`evaluateSync`/`assertStart`,
in babel.cjs, backup/restore/diff-confirmed against `npm pack` each
time) to trace exactly what's yielded and when - real Node's version of
this same operation completes in one `.next()`-pair (yields the "start"
sentinel once, then finishes); paserati's yields "start" correctly on
the first call, then yields the "suspend" sentinel on the second call
instead of completing - meaning the generator's `if (!resume) return
sync.call(...)` early-return check evaluated `resume` as **truthy**,
even though gensync's synchronous driving loop (`evaluateSync`) never
sends any value into `.next()` - `resume` must be `undefined` there,
always, by construction. Built five separate standalone repros
attempting to reproduce this via generic gensync usage (plain
`.sync()`, nested `.sync()` calls, `yield*` delegation one and two
levels deep, `gensync.all` composition) - every one passed, matching
real Node, because none of them could actually reach the "resume is
truthy" state (evaluateSync structurally never provides one) - a
guessing loop that self-corrected once the actual discriminating test
was run: instrumented the *exact* real minified generator body
(`const n=yield t;if(!n){...}`) to print `typeof n`/`n` right after the
yield. It printed `number 1` under paserati (real Node: `undefined`).

`1` was recognizable: gensync's `buildOperation({name, arity, sync,
async})` (destructuring `arity` into a variable also named, after
minification, `n`) returns `function* (...e){ const n = yield t; ... }`
- the *inner* generator body's own local `const n` has the **same name**
as the *outer* `arity` parameter it's nested inside and closes over.
Minimized to two repros entirely outside jiti/babel/gensync:

```ts
function outer(n) {
  function* g() {
    const n = 99;
    console.log("inner n:", n);
    yield 1;
  }
  return g;
}
outer(1)().next();
```

Real Node: `inner n: 99`. Paserati: `inner n: 1` - the generator body's
own `const n` reads back the *outer*, closed-over parameter's value
instead of its own assignment. Narrowed precisely with five control
variants (all cross-checked against real Node): generator-specific (the
identical structure with a plain non-generator inner function reads
correctly); not `yield`-specific (no yield needed at all, a bare
`const n = 99` triggers it); not `const`-specific (`let` shows the same
bug); needs the outer variable to specifically be an **enclosing
function's parameter**, not just any outer scope (a module-level outer
`n` shadowed the same way inside a *non-nested* top-level generator
reads correctly); and it's specific to a **body-local declaration**,
not the generator's own parameter list (if the generator's own
parameter shares the outer name, it correctly resolves to its own
argument, not the outer closure).

Filed as
[paserati#262](https://github.com/nooga/paserati/issues/262), with both
minimal repros, all five narrowing variants, and the trace from the
real gensync generator (`typeof n` printing `number 1`) that led to it.

**Status**: no noderati source changed this round. jiti's fake stays
not-deletable - no successful jiti transform has been observed yet at
any point in this investigation; #256/#258/#260 (now all fixed
upstream) were necessary but not sufficient. Two new bugs filed
(#262, #263), both confirmed independent of #256/#258/#260 and of each
other. Both `babel.cjs` and `jiti.cjs` restored and diff-confirmed clean
against a freshly-`npm pack`'ed `jiti@2.7.0` tarball before finishing
(not a `/tmp` backup alone, per round 51/52's corrections). Re-testing
the full jiti pipeline remains pending #262 and #263 landing upstream.

**Fifty-fourth round (2026-09-05, same day) — pulled #262/#263's fix
(`64073a29`), verified both, found and fixed a real noderati-side CJS
`this`-binding bug along the way (the first noderati source change in
four rounds), and root-caused + filed a third, distinct paserati bug
(#265) after a long, partly-unproductive detour that a JS-level
interception hook eventually cut through.** User asked to pull latest
paserati main and continue.

Pulled `64073a29` ("feat(builtins): add Error.captureStackTrace (#263)"
+ "fix(compiler): predefine a generator body's own let/const before
OpInitYield (#262)"). Re-verified both against their exact filed
repros (`inner n: 99`, matching real Node; `typeof
Error.captureStackTrace === "function"` and a working call) plus
regression-checked #256/#258/#260 - all clean.

Re-ran jiti's real transform pipeline (import shape, `fsCache: false`,
cache cleared) - **cleared `GENSYNC_EXPECTED_START` for the first time
this entire investigation**, progressing into a new crash: `TypeError:
[BABEL] /: Method Generator.prototype.next called on incompatible
receiver` deep inside `@babel/core`'s real plugin-loading
(`loadPluginDescriptor`).

Intercepted `Generator.prototype.next` from plain JS (wrapping
`Object.getPrototypeOf(Object.getPrototypeOf((function*(){})()))
.next`, no engine changes needed) to log the failing receiver directly
instead of guessing from minified source - the first two rounds of
this technique cost real time on a wrong turn, corrected below. The
receiver was `globalThis` (confirmed by its enumerable own keys:
`__noderatiSpawnSync,__noderatiSpawn,Buffer` - noderati's own
process-spawning natives, set directly on `globalThis` in
`child_process.go`). Built a `probe.cjs` test (`this` at a CJS module's
own top level) that confirmed a genuine, distinct **noderati** bug
(not paserati's): `internal/host/cjs.go`'s CJS module wrapper called
the module function with `thisArg: vm.Undefined` instead of real
Node's actual convention (`this === module.exports` at CJS top level,
until reassigned) - for a non-strict wrapper function, `vm.Undefined`
correctly-per-sloppy-mode-rules resolves to `globalThis`, matching
paserati's (correct) semantics but not real Node's actual CJS
contract. Fixed in
[`internal/host/cjs.go`](../internal/host/cjs.go) by passing
`exportsVal` as the `thisArg` instead - re-verified with `probe.cjs`
directly against real Node (`this === module.exports: true` now
matches on both). First noderati source change in four rounds of
paserati-side-only investigation.

The `globalThis`-as-receiver crash persisted unchanged after that fix
though - a different call site, not the CJS top-level one. Spent
significant, partly wasted effort chasing *where the bad receiver came
from* (tested `.call()`/`.apply()` explicit-this forwarding, arrow
function lexical `this` across a `.call()` boundary, destructured
generator parameters shadowing enclosing parameters, webpack's own
`e[r].call(s.exports,...)` module-invocation convention - all
correct, all ruled out one at a time, none of it the actual bug)
before recognizing the real question was backwards: real Node's
*identical* unbound-call/sloppy-`this` mechanics also produce
`globalThis` as a receiver (verified directly) - so real Node must
simply never *reach* this particular `.next()` call, not receive a
different receiver at it. Re-aimed the same interception hook at
*counting* calls and diffing argument shapes call-by-call against an
identical real-Node run of the same script: real Node makes 1246 total
`.next()` calls and completes; noderati's argument types first diverge
from real Node's at calls #13, #25, #62 (an `object`/`function` where
real Node passes `undefined`) and noderati dies at call #75.

That diff pointed squarely at gensync's own `isAsync` check
(`@babel/core`'s bundled `gensync-utils/async.js`:
`t.isAsync=_gensync()({sync:()=>!1,errback:e=>e(null,!0)})`, called
pervasively - `yield*(0,n.isAsync)()` - throughout real plugin/preset
loading) - an `object`/`function` argument where `undefined` is
expected is exactly what a `resume` value that should be falsy but
isn't would produce. Testing `gensync`'s real, unmodified npm package
directly (no babel/jiti) with this exact `{sync, errback}` operation
shape reproduced a **third, distinct paserati bug**: calling
`gensync({sync: () => 42}).sync()` recurses into itself indefinitely
instead of returning `42`. Bisected (removing pieces until it stopped
reproducing, not guessing new ones) to the essential ingredient: an
object-literal property whose *key* is `sync`, whose function *value*
closes over an outer variable *also named* `sync`, calling that
variable - reading it back inside the property's own body resolves to
itself instead of the outer closure, causing infinite self-recursion
(a tail-position variant spins forever with no stack growth; a
non-tail variant crashes cleanly with `Maximum call stack size
exceeded`, a stack of frames literally named `sync` calling `sync`).
Confirmed the *same* closure-nesting depth with a **named function
declaration** instead of an object-literal property does not
reproduce - isolating property-key name inference specifically, not
closure depth in general, as the trigger.

Filed as
[paserati#265](https://github.com/nooga/paserati/issues/265), with
both repro variants (the hanging tail-position one and the
fast-crashing one, explicitly flagged which is which), the ruled-out
named-function-declaration control, and the measured (not assumed)
link to the real plugin-loading crash via the call-count/argument-type
diff - stated as "implicated, not confirmed as the sole cause," since
the crash's own symptom (an invalid-receiver `TypeError`) differs from
this bug's two directly-observed symptoms (a hang or a clean overflow),
so something downstream may still be unaccounted for.

**Status**: one real noderati bug found and fixed this round (the CJS
`this` binding - `internal/host/cjs.go`), verified against real Node.
#262/#263 verified fixed upstream. A third paserati bug filed (#265),
strongly implicated in (but not proven to fully explain) the plugin-
loading crash that's now jiti's blocker. No successful jiti transform
has been observed yet at any point across this entire multi-round
investigation. Both `babel.cjs` and `jiti.cjs` confirmed clean against
a freshly-`npm pack`'ed tarball before finishing. `go build`/`go
vet`/`go test -count=1 ./...` and the scoreboard all clean.

**Fifty-fifth round (2026-09-05, same day) — pulled #265's fix
(`2aa36815`), verified it against both filed repros, confirmed
(measured, not assumed) that it does *not* clear the pipeline - the
"implicated, not sole cause" hedge in #265's own filing turned out to
be right - and narrowed the remaining divergence further without yet
finding its root cause.** User asked to continue after the merge.

Pulled `2aa36815` ("fix(compiler): don't self-bind an inferred function
name over a same-named outer variable"). Re-verified against both of
#265's repro variants (the fast-crashing generator-driven one and the
tail-position one that hangs rather than crashing - confirmed the
tail-position case now returns `42` cleanly too, no hang) plus
regression-checked #256/#258/#260/#262/#263 - all clean.

Re-ran jiti's real pipeline: **same crash, same call site**
(`TypeError: Method Generator.prototype.next called on incompatible
receiver`, inside `loadPluginDescriptor`). Re-ran the call-count/
argument-type diff from round 54 to confirm this precisely rather than
assume it: identical divergence profile as before #265's fix - argument
type differs from real Node's `undefined` at calls #13 and #25 (now
confirmed via direct inspection to be an actual `Generator` object,
`String(v) === "[object Generator]"`, not just "some non-undefined
object"), and at calls #62 and #69-73 (a `function`, consistent with
`evaluateAsync`'s resume-callback pattern - unexplained why that driver
would be active at all under a pure `transformSync` call; noted, not
chased further this round). #265's fix genuinely fixed a real,
distinct bug (verified independently against its own repros) but,
exactly as its filing hedged, doesn't touch this call site.

Checked one loose thread from round 54's `[SEQ]` output repeating
several times with counters resetting: instrumented babel.cjs's own
`transform()` entry point (backup/restore/diff-confirmed against a
fresh `npm pack`'ed tarball, as established) to count real invocations
- it's called exactly once per file under noderati (matching real
Node's count of one call per file, two files total) - the repeated
`[SEQ]` bursts were an artifact of the diagnostic harness itself, not a
retry loop in babel/jiti. Ruled out cleanly rather than left as an open
question.

Formed and tested the most direct hypothesis for the `Generator`-as-
`.next()`-argument divergence: since neither of gensync's own two
drivers could produce it (`evaluateSync` passes no argument at all;
`evaluateAsync` passes a resume *function*, matching calls #62/#69-73
separately), the remaining candidate is paserati's own `yield*`
implementation forwarding the wrong value when delegating a resume
value into a nested delegate. Tested directly and cleanly, no
gensync/babel involved: a three-level `yield*` chain
(`outer` → `mid` → `inner`), driven with explicit resume values
(`it.next("SENT-1")`, `it.next("SENT-2")`) rather than the no-argument
driving every previous round's `yield*` tests used (which is exactly
why none of them exercised this path) - resume values forward correctly
through all three levels, matching real Node exactly. This specific,
well-targeted hypothesis is ruled out; the actual mechanism producing a
`Generator` object as a `.next()` argument at calls #13/#25 remains
unidentified. Three attempts this round to hand-construct the exact
compound gensync shape at that call site (nested `yield*` through a
cache-wrapping layer, mirroring `makeCachedFunction`'s
`isIterableIterator`/`onFirstPause` structure read in round 53) failed
to reproduce - one of the three attempts turned out to be an invalid
construction (failed identically in real Node, not a real repro at
all) rather than a genuine negative result, a mistake caught before it
was treated as evidence.

**Status**: #265 verified fixed and confirmed (not assumed) not to be
the pipeline's remaining blocker. The divergence is now precisely
characterized - two distinct symptom clusters (a `Generator` object
appearing as a `.next()` argument at calls #13/#25; a resume-style
`function` appearing at #62/#69-73, real Node passes `undefined` at
every one of these) - but neither has a confirmed root cause or a
minimal repro yet; the most direct standalone hypothesis for the first
cluster was tested and ruled out. Not filed - a symptom without a
located mechanism isn't yet actionable the way #256/#258/#260/#262/
#263/#265 were, each of which had a precise, minimal, hand-verified
trigger before filing. This pipeline is now five real paserati bugs
deep (all fixed upstream) with no successful transform observed at any
point. Both `babel.cjs` and `jiti.cjs` confirmed clean against a
freshly-`npm pack`'ed tarball. `go build`/`go vet`/`go test -count=1
./...` and the scoreboard all clean; no noderati source changed this
round.

**Fifty-sixth round (2026-09-05, same day) — kept digging on the
`Generator`-argument divergence per user request, found the actual
signature (paserati's own native `.next` implementation leaking into a
`yield` result as a value), and filed it without a minimal repro -
the maintainer has the VM source to grep for what's actually leaking;
this session doesn't.** User asked to keep digging and file with
paserati if anything was needed from it.

Confirmed, before anything else, that the earlier round's "the
`#13`/`#25` generator-as-argument calls might be harmless" reasoning
was wrong: counted every `.next()` call across a full real-Node run
(1246 total) and found **zero** function-typed and **zero**
generator-typed arguments anywhere in it. Real Node computes nothing
divergent at those points at all - so whatever noderati computes there
is evidence of a wrong computation even where its immediate effect
happens to be masked by spec-mandated first-call argument discarding.
Corrects this round's own earlier framing, caught within the round
rather than left standing.

Tagged the two most-likely candidate generators directly in real
babel.cjs (backup/restore/diff-confirmed against a fresh `npm pack`
tarball each time, as established) - `mergeChainOpts`'s own instance,
and the plugins/presets loader-thunk results it calls via `yield*r()`/
`yield*n()` - neither carried the tag when the divergence fired,
ruling both out as the self-passing generator's identity. Traced the
call stack for the actual failing generator's first-ever invocation in
full instead: six-plus frames deep, nested inside two separate
`evaluateSync` calls with `mergeChainOpts` between them but no other
named frame - consistent with `@babel/core`'s real caching machinery
(`makeCachedFunction`/`onFirstPause`, read in round 53) but not
confirmed by tagging alone.

Formed and tested four more specific hypotheses, all measured directly,
all ruled out: `yield*` resume-value forwarding through 7 levels of
nesting (not just 3, tested last round) - correct; multiple distinct
`buildOperation`-protocol leaf operations called sequentially (not
nested) from one enclosing generator, mirroring `makeCachedFunction`'s
own multi-step body - correct; `isAsync` invoked via `yield*` (the
exact real call shape) - correct, confirming #265's fix holds under
real usage, not just its own `.sync()`-based repro; and the `async`/
`errback` variants of #265's exact bug shape (a function stored as an
object-literal property whose key matches a closed-over outer variable
name, using those two property names instead of `sync`) - both correct,
confirming #265's fix is general rather than narrowly special-cased.

Reapplied the technique that found #265: patched gensync's one shared
`buildOperation` generator body (the `const n=yield t; if(!n){...}`
line every gensync operation compiles to) to print the value whenever
`n` ("resume") comes back truthy - which should be structurally
impossible under a pure synchronous drive, since gensync's own
`evaluateSync` loop never sends a value into `.next()` at all. It
printed `function function next() { [native code] }` - **paserati's
own native `Generator.prototype.next` implementation**, an internal
engine value no JS code anywhere in this chain could have supplied.
This is what makes the operation wrongly take gensync's async branch
under a sync-only transform, which is what produces the function-typed
`.next()` arguments observed at calls #62/#69-73 (gensync's own
`evaluateAsync` legitimately passes resume *callbacks*, just never
under a working `.sync()` drive) and ultimately the `incompatible
receiver` crash itself.

Filed as
[paserati#267](https://github.com/nooga/paserati/issues/267) without a
standalone repro - explicit per-advisor guidance and the user's own
"file if anything is needed" framing this round, after eight total
reconstruction attempts (this round and last) failed to reproduce it
outside the real pipeline, several at patterns already measured to
work. The issue leads with the strongest evidence (the 1246-call
zero/zero real-Node count vs. noderati's divergence profile), gives the
exact instrumentation applied to reproduce the native-`next` signature
against real jiti/babel, lists every ruled-out mechanism from this
round and the last, and states plainly that the exact structural
trigger is unidentified - a measured symptom report, not a guess,
handed to the person with VM-source-level grep access instead of
attempting a ninth hand-reconstruction.

**Status**: no noderati source changed this round - all investigation,
instrumentation, and filing on the paserati side. Sixth real bug filed
against this one pipeline (#256, #258, #260, #262, #263, #265 already
fixed; #267 newly filed). No successful jiti transform has been
observed at any point across this entire multi-round investigation.
Both `babel.cjs` and `jiti.cjs` confirmed clean against a freshly-`npm
pack`'ed tarball before finishing.

**Fifty-seventh round (2026-09-05, same day) — pulled #267's fix
(`9a66741f`, a register-contiguity bug in `yield*`/error-throw argument
allocation - exactly the mechanism this doc's round 56 entry
described, root-caused by the maintainer directly), verified it,
confirmed the pipeline advances rather than loops, and root-caused +
filed a seventh bug at the new failure point.** User asked to pull
latest paserati main and continue.

Pulled `8f5e5e04` (four commits past round 56's last-recorded
`2aa36815`: `9a66741f` fixes #267 - "OpCallMethod/OpCall read their
arguments from funcReg+1 onwards... the yield* lowering allocated them
with two separate Alloc() calls and assumed adjacency... once earlier
statements in the generator had freed registers in a non-contiguous
pattern the two were no longer neighbours and iterator.next() received
whatever stale value happened to live at nextMethodReg+1 - in babel's
gensync pipeline that was the freshly loaded native next method
itself"; plus `2f590bb6` adding type annotations on catch clause
bindings, incidentally fixing the `catch (e: any)` parser gap hit
several times this investigation; plus a docs commit). `go build`/`go
vet` clean on both repos.

Re-ran jiti's real transform pipeline: the `GENSYNC`/`incompatible
receiver` crash is gone, replaced by a **new, different** crash -
`TypeError: [BABEL] /: undefined is not a constructor` - confirming
#267's fix genuinely moved the pipeline forward rather than looping on
a masked version of the same bug. Traced with the same babel.cjs
instrumentation technique used throughout this investigation (backup/
patch/restore, diff-confirmed against a fresh `npm pack` tarball) to
`loadPluginDescriptor`'s own `new o.default(c,t,s,i)` call site, one
line further into the exact function the previous six bugs on this
pipeline all led into.

Read `@babel/core`'s bundled source around that call site: a sibling
generator (`makeWeakCache`-wrapped, feeding `loadPluginDescriptor`) has
a conditional block (`if(c.inherits){ const o = yield*forwardAsync(...);
...}`) declaring a block-scoped `const o`, with `new o.default(...)`
later in the *same* generator intending to reference an outer,
module-level `o` (an imported `Plugin` class) - not the block-scoped
one. Minimized to two small, `yield`-free repros entirely outside
jiti/babel/gensync: a generator with an `if` block (condition false,
block never runs) declaring a `const` that shadows an enclosing
function's variable of the same name, and later returning
`new o(...)` - `undefined is not a constructor` under paserati, correct
under real Node; and a bare-block (no `if`, no condition) variant -
`object is not a constructor` (the block *did* run this time, so `o`
resolves to the shadow's value instead of being left uninitialized -
same underlying shared-binding bug, different symptom depending on
whether the block executed). Confirmed generator-specific (the
identical structure with a plain function reads correctly) and
independent of any `yield`/suspend behavior (neither repro yields at
all).

Read paserati#262's actual fix commit (`be20f91d`) directly rather than
inferring the root cause from behavior alone: its own commit message
explains a generator body is compiled via a hand-rolled statement loop
that bypasses the normal `BlockStatement` "pass 0" pre-registration
step for `let`/`const` names, and the fix added exactly that
pre-registration - but only for the generator body's own **direct,
top-level** statements (the `for _, stmt := range remainingStmts`
loop in `pkg/compiler/compile_literal.go`, switching on
`*parser.LetStatement`/`*parser.ConstStatement`/the two destructuring
declaration types). It doesn't recurse into nested block-bearing
statements (`*parser.IfStatement`, bare `*parser.BlockStatement`) to
predefine names declared inside them - confirming, not just inferring,
that this is a precise sibling gap in #262's own fix, one block level
deeper than what it covered.

Filed as
[paserati#271](https://github.com/nooga/paserati/issues/271), with
both repros, the generator-specific/yield-independent narrowing, the
confirmed (not inferred) root cause read directly from #262's fix
commit, and the real-pipeline context - the seventh distinct bug found
on this one pipeline (#256/#258/#260/#262/#263/#265/#267 all already
fixed), each one leading directly into the next rather than the
pipeline looping on a single masked failure.

**Status**: no noderati source changed this round. Both `babel.cjs`
and `jiti.cjs` confirmed clean against a freshly-`npm pack`'ed tarball
before finishing. No successful jiti transform has been observed at
any point across this entire multi-round investigation; jiti's fake
stays not-deletable, now blocked on #271.

**Fifty-eighth round (2026-09-05, same day) — pulled #271's fix
(`bf3f78ee`) plus three other VM commits, verified #271 against both
its own filed repros, re-ran the full regression suite, and pushed the
real jiti pipeline one bug further — past plugin-descriptor
construction and into real plugin *execution* for the first time this
investigation, where it hit an eighth distinct bug: `Object.assign`
never invokes source getters or target setters.** User asked to pull
latest paserati main and continue.

Pulled paserati main to `bf3f78ee` (four commits past round 57's
`8f5e5e04`): `bf3f78ee` fixes #271 - `isCompilingFunctionBody` (a flag
meant to be consumed by exactly one `BlockStatement` `compileNode`
call, telling it not to open its own enclosed scope) was left `true`
across the entire hand-rolled statement-by-statement generator-body
compile walk; since a generator's own top-level body never itself
passes through `compileNode`, the flag survived to be wrongly consumed
by the *first nested* block statement instead (an `if`'s consequent, a
bare block), which then predefined its `let`/`const` bindings straight
into the generator's top-level symbol table, leaking them past the
block. Fix: don't set the flag for the generator body's special
compile path at all - it was never read at that level. Plus three
further VM commits (`23934449`, `3d5f89df`, `939014ec`: async frame
`args`/`calleeValue` setup, rest-parameter population and
arguments-cache reset in async calls, fresh empty-rest identity and
constant-pool capacity checks) pulled and present for this round's
tests but not individually re-derived. `go build`/`go test -count=1
./...` clean on both repos.

Verified #271 directly against its own two filed repros
(`check1.ts`/`check2.ts`, the if-block and bare-block variants): both
now return the correct `{"value":{"tag":"real"},"done":true}`, no
leaked binding. Re-ran the standing regression suite covering
#256/#258/#260/#262/#263/#265: all six still pass.

Re-ran jiti's real transform pipeline (the two-file `ext3/`
interface/enum/private-field/async extension test): the previous
`undefined is not a constructor` crash from round 57 is gone, replaced
by a new crash - `TypeError: [BABEL] /: Cannot read property
'importExpression' of undefined`. Confirmed it reproduces even for the
most trivial possible input (`console.log("hello")` alone), consistent
with it firing during Babel's own bundled-plugin setup, before any
input-file content is examined - a genuinely different failure class
from every prior round's crash, all of which fired during descriptor
*construction*; this one fires during a plugin factory's own body,
i.e. plugin *execution* has now begun for the first time in this
investigation.

Instrumented `babel.cjs`'s `transform()` catch block (backed up via a
fresh `npm pack jiti@2.7.0`, diff-confirmed pristine before patching,
restored and diff-confirmed clean again after) to print the real
error's name/message/stack, tracing the crash into
`loadPluginDescriptor`'s plugin-invocation chain. Grepped for
`importExpression` and found the exact source:
`@babel/plugin-transform-modules-commonjs`'s visitor object literal
uses a computed key `["CallExpression" + (e.types.importExpression ?
"|ImportExpression" : "")]`, where `e` is the plugin's `api` parameter
and `e.types` comes back `undefined`. Read `@babel/core`'s own
descriptor-loading code building that API object:
`u=Object.assign({},i,e(a,l))`, merging a base API object `i` - which
provides `.types` via a lazy getter, a real babel convention avoiding
the cost of resolving `@babel/types` for plugins that never touch it -
with per-plugin-kind extras. Hypothesized `Object.assign` silently
drops the getter's value rather than invoking it, and confirmed this
directly and minimally: `Object.assign({}, source)` where `source` has
an accessor property copies the *key* (`"types" in target` is `true`,
appears in `Object.keys`) but not the *value* (reads back `undefined`
instead of calling the getter); object spread (`{...source}`) on the
identical source correctly invokes the getter, isolating the defect to
`Object.assign`'s own implementation, not a shared enumerable-property-
copy path. Checked the write side too, since `Object.assign` also
`[[Set]]`s onto the target: a target-side setter is never invoked
either (`Object.assign(target, {x: 42})` against a `target` with an
`x` setter leaves the setter's captured value `undefined` instead of
`42`, where real Node gives `42`) - the defect is two-sided, not one.

Checked for prior related issues before filing: #168 ("copies
properties as non-enumerable") and #254 ("`Object.assign` onto a
function target drops every property") are both closed, both distinct
from this getter/setter defect, both in the same builtin - making this
the third separately-found `Object.assign` bug via real package usage
alone, noted in the filing as a pattern worth the maintainer's
attention without prescribing a fix. Called `advisor` before filing,
who confirmed the finding was clean and complete, named the missing
setter-side test as one cheap addition (added, and it also failed,
confirming the two-sided defect above), and suggested naming the
tested commit explicitly (added). Filed
[paserati#274](https://github.com/nooga/paserati/issues/274) - the
eighth distinct bug found on this one pipeline
(#256/#258/#260/#262/#263/#265/#267/#271 all already fixed), with the
minimal repro, the key-lands-value-doesn't narrowing, the spread
control, the setter-side confirmation, and the exact real-babel
connection traced from source to crash.

**Status**: no noderati source changed this round. Both `babel.cjs`
and `jiti.cjs` confirmed clean against a freshly-`npm pack`'ed tarball
before finishing. No successful jiti transform has been observed at
any point across this entire multi-round investigation; jiti's fake
stays not-deletable, now blocked on #274 - though the pipeline crossed
a real boundary this round, advancing from plugin-descriptor
construction into actual plugin execution for the first time.

**Fifty-ninth round (2026-09-05, same day) — pulled #274's fix
(`5b3abc8e`), verified it against both getter and setter sides,
confirmed the regression suite, and pushed the real jiti pipeline past
plugin execution into babel's own bootstrap - where it hit a ninth
bug, this time a raw Go VM panic rather than a JS-level divergence,
filed after five negative isolation attempts and one hypothesis ruled
out by reading the engine's own source.** User asked to pull latest
paserati main and continue.

Pulled paserati main to `5b3abc8e` (fixes #274 - `Object.assign`'s own
Go-native implementation used `plainObj.GetOwn(key)` and a plain
`SetOwn(key, value)` with no accessor awareness at all, unlike
`OpObjectSpread` which already checked `GetOwnAccessor`; fixed by
mirroring that check on both the read side - call the source's getter
via `vmInstance.Call` when present - and the write side - call the
target's setter when present, matching `vm.SetProperty`'s existing
own-accessor check). `go build`/`go test -count=1 ./...` clean on both
repos.

Verified #274 directly: the original getter repro
(`getter_assign.ts`) now prints `getter invoked` / `typeof
target.types: object` / `{"real":true}`, matching real Node exactly;
the setter repro (`setter_assign.ts`, added on advisor's suggestion
last round to check the write side too) now prints `42`, also
matching. Re-ran the standing six-bug regression suite
(#256/#258/#260/#262/#263/#265): all still pass.

Re-ran jiti's real transform pipeline: the `Cannot read property
'importExpression' of undefined` crash from round 58 is gone. New
failure, and a different *kind* entirely - not a JS `TypeError` but a
raw Go panic surfacing through noderati's own VM-panic recovery
wrapper: `index out of range [19] with length 18` at `vm.go:1999`
(`OpMove: registers[regDest] = registers[regSrc]`), reached through
`resumeGenerator`, three levels of nested generator resumption deep,
under `executeAsyncFunctionBody` - gensync's `evaluateSync`-over-
`yield*` machinery loading babel's config/plugins. `vm.runtimeError()`
still attaches a source position from whatever chunk was current at
panic time, pointing into `@babel/core`'s bundled
`build-external-helpers.js` module (`buildVar`'s enclosing scope, a
19-way object destructuring from `@babel/types`). Confirmed
deterministic and content-independent: reproduces on the two-file
`ext3/` extension test and on the most trivial possible input
(`console.log("hello")` alone) identically - this fires during babel's
own bootstrap, before any input file is examined.

Five isolation attempts, all negative (all four synthetic probes ran
correctly, matching real Node; only the real pipeline diverges):
extracting the exact panicking module's source standalone (stubbing
its four dependencies) and calling it the same way the real pipeline
does - passes, including the 19-way destructuring in isolation on its
own; the same standalone body driven through 3 levels of nested,
gensync-shaped `yield*` delegation - passes; three nested generators
with deliberately different live-local counts (3/25/8), chained with
an intervening plain call before and after the delegation point, to
force a real (non-tail-call) register window to persist across
suspend/resume - passes; a 400-property object literal (mimicking the
enclosing webpack module map) with one 19-local method - passes; and
[#244](https://github.com/nooga/paserati/issues/244)'s own original
repro (bare `require(jiti/dist/jiti.cjs)`, no transform) - still
passes cleanly, confirming this is not a resurgence of that bug's
exact trigger (a comma-chain register-count wraparound, fixed by
`157a2161`), though it's the identical *symptom* (an undersized
register window causing a raw index-out-of-range panic).

Read the source directly to check one concrete mechanical hypothesis:
that a generator/async frame's register window gets expanded past its
declared `RegisterSize` via TCO at some point, gets saved in that
expanded state, and `resumeGenerator` silently re-slices it back down
on the next resume - which would produce exactly this panic with no
repro needed. Ruled out by the code itself, not just by testing: both
TCO call sites (`OpTailCall`/`OpTailCallMethod`) unconditionally
disable TCO the moment `frame.generatorObj != nil` or the callee
`IsGenerator`/`IsAsync`, and `relocateOpenUpvalues`'s own comment
already documents this as a deliberate invariant elsewhere in the same
file. Noted for the maintainer, independent of whether that invariant
holds here: `resumeGenerator`'s restore is a plain Go `copy()`, which
silently truncates on any future length mismatch with no signal at the
copy site - a length assertion there would turn any recurrence of this
exact panic shape into an immediate diagnostic. Also flagged one real,
still-open asymmetry found by reading `resumeGenerator`'s four exit
paths: its error-path register-reclaim is guarded by `vm.frameCount >
0` where the matching increment has no such guard - a genuine
inconsistency, though not confirmed to be what fires in this specific
crash.

Called `advisor` twice this round before filing (once mid-investigation,
which redirected the TCO-window-mismatch read that ended up ruling out
that hypothesis cleanly; once on the finished draft, which caught an
unverified "fixed by" commit attribution for #244 - confirmed via `git
log --grep` before use - and an overselling closing paragraph, both
corrected before filing). Filed
[paserati#276](https://github.com/nooga/paserati/issues/276) - the
ninth distinct bug found on this one pipeline
(#256/#258/#260/#262/#263/#265/#267/#271/#274 all already fixed),
explicitly framed as a Go-level engine panic rather than a JS semantic
divergence, with the full negative-probe ledger, the ruled-out TCO
hypothesis, the open asymmetry, and a pointer to `debugGeneratorStates`
as the fastest next step for whoever picks it up (not flipped here -
this is a shared, actively-developed repo, never left in an edited
state).

**Status**: no noderati source changed this round. Both `babel.cjs`
and `jiti.cjs` confirmed clean against a freshly-`npm pack`'ed tarball
before finishing. No successful jiti transform has been observed at
any point across this entire multi-round investigation; jiti's fake
stays not-deletable, now blocked on #276 - a VM-internals bug rather
than a JS-semantics one, the first of that kind hit directly by this
investigation (as opposed to #244, hit and fixed earlier in paserati's
own history before this investigation reached it).

**Sixtieth round (2026-09-06) — pulled #276's fix, verified it and the
full nine-bug regression suite, then found and fixed the *actual*
real blocker: not a tenth paserati bug, but noderati's own `assert`
module being non-callable. A trivial real `.ts` file transformed and
executed end to end through jiti's real, unmodified pipeline for the
first time in this entire investigation.** User asked to pull latest
paserati main and continue.

Pulled paserati main to `0af3487d` (fixes #276 via `c39ae9d7`:
`compileDestructuringTargetRef`/`assignToDestructuringTargetRef` and
three sibling destructuring-assignment paths resolved a captured
(upvalue) identifier target with a plain `currentSymbolTable.Resolve()`
and emitted a raw `OpMove` straight into its register number, with no
check for whether that register belonged to the *current* function's
own space or an *enclosing* one - writing a foreign register number
either silently corrupted an unrelated local or crashed the VM outright
once the number exceeded the current function's own `RegisterSize`;
`0af3487d` itself added richer VM-panic diagnostics - dumping the
panicking frame's function name/`RegisterSize`/`allocatedRegSize`/
flags plus a chunk disassembly - which is what let the maintainer see
past #276's misleadingly-generator-shaped Go stack trace to the real,
ordinary-closure culprit). `go build`/`go test -count=1 ./...` clean on
both repos.

Verified #276 directly against the maintainer's own extracted repro
(`tests/scripts/issue276_destructure_upvalue_target.ts`, five distinct
destructuring-upvalue shapes) and the standing nine-bug regression
suite (#256/#258/#260/#262/#263/#265/#267/#271/#274): all pass.

Re-ran jiti's real transform pipeline: the previous `index out of range`
Go panic is gone, replaced by a *different* raw panic -
`reflect: Call with too many input arguments` - deep in `@babel/core`'s
plugin-descriptor construction. Read the maintainer's own `c39ae9d7` fix
commit message directly, which stated plainly: "the full babel.cjs
pipeline now transforms input successfully end to end" - a claim worth
reconciling against, not just trusting or dismissing.

Found the maintainer's own scratch harness for #276
(`paserati/scratch/i276/` - `shim.js`, `run.mjs`, `empty.ts`) - a
hand-rolled Node-builtin shim driving `babel.cjs` directly (no jiti.cjs
wrapper at all), confirming their "end to end" claim's actual shape: a
different entry path than this investigation's `createJiti(url,
{fsCache:false, moduleCache:false}).import(file)`. Running their own
`run.mjs`/`empty.ts` against the current build reproduced the *same*
`reflect` panic - not a contradiction once traced further: building an
equivalent direct-`babel.cjs`-plus-shim driver by hand and running it
confirmed **the shim succeeds and the jiti pipeline fails on identical
input**, isolating the difference to noderati's own host module
implementations (used for real by the jiti path; bypassed entirely by
the shim). Bisected by selectively substituting the shim's stub for one
real noderati builtin at a time (`fs`, `path`, `os`, `process`,
`module`, `assert`, `url`, `util`, `tty`) while driving `babel.cjs`
directly via noderati's own real `require()` - stubbing every module
except `assert` still crashed; stubbing `assert` alone fixed it.

Root cause, confirmed directly: real Node's `assert` module exports a
*callable function* (`assert(value, message)`, shorthand for
`assert.ok`) that also carries `.ok`/`.equal`/`.strictEqual`/etc. as
properties - noderati's `declareAssert` (`internal/host/assert.go`)
instead used a bare `m.Default(nil)`, building a plain, non-callable
namespace object. `typeof assert` was `"object"` where real Node gives
`"function"`, and calling it directly - `assert(cond)`, the form real
code (including `@babel/helper-validator-option`'s `OptionValidator`,
hit for real inside babel's plugin/option-loading chain) uses far more
often than the equivalent `assert.ok(cond)` - threw a raw
`TypeError: object is not a function`. This was the actual blocker
behind every "jiti transform crashes deep in babel's own pipeline"
investigation this whole session, all the way back through the dozen
paserati engine bugs found and fixed along the way - each one real, but
none of them the last blocker. The maintainer's own harness never hit
this because `scratch/i276/shim.js` stubs `assert` as callable from the
start.

Fixed on noderati's own side (`internal/host/assert.go`, `host.go`):
added `installAssertGlobal`, mirroring the existing
`installBufferGlobal` pattern - builds the default export as a
`vm.NewNativeFunctionWithProps` callable and copies the module's named
exports onto it as properties. Verified: `typeof assert` is now
`"function"`, `assert(true)` no longer throws, all eight `TestAssert*`
still pass, and - the actual milestone - `console.log("hello")`
through the real `createJiti(...).import()` pipeline against pi's
real, unmodified jiti 2.7.0/babel.cjs **transformed and executed**,
printing `hello`, with `babel.cjs` re-confirmed byte-for-byte clean
against a fresh `npm pack` tarball first (ruling out the earlier
options-logging instrumentation as the cause of the success). Committed
separately from everything else in this round, since a first successful
transform in a dozen-round investigation shouldn't sit uncommitted
while chasing the next crash.

Re-ran the full multi-file `ext3/` extension test (interface, enum,
private class field, async/await, real cross-file import) and hit an
eleventh bug, immediately: the same `reflect: Call with too many input
arguments` panic, now inside `@babel/traverse`'s
`Scope.prototype.generateUidIdentifier` chain. Traced via the new
`0af3487d` panic diagnostic and the Go panic's own stack trace
(`ArrayInitializer.InitRuntime.func19` → `CallArgs3` → ... →
`goFunctionToVM.func1` → `reflect.Value.Call`) directly to
`pkg/driver/native_module.go`'s non-variadic Go-function bridge:
`Array.prototype.forEach` always invokes its callback with exactly 3
arguments (element, index, array) per spec
(`pkg/builtins/array_init.go:1248`'s `CallArgs3`), and `goFunctionToVM`
sizes its `reflect.Value` argument slice to the *caller's* argument
count rather than the Go function's own declared arity, then calls
`reflect.Value.Call` with that oversized slice - which panics instead
of clamping, unlike the adjacent variadic branch (which slices correctly)
and the adjacent too-few-arguments branch (which pads correctly) in the
very same function. Confirmed standalone and host-agnostic:
`["a.ts","b.ts"].forEach(path.extname)` alone reproduces the identical
panic through any embedder-declared `ModuleBuilder.Function` with fewer
than 3 parameters - not specific to assert, path, or noderati at all.
Called `advisor`, who correctly redirected away from patching every
affected host function one at a time ("unbounded... you cannot
enumerate the call sites in a 1.5MB bundle") toward filing the actual
defect. Filed
[paserati#278](https://github.com/nooga/paserati/issues/278) - a
source-verified, one-line-shaped fix location (the non-variadic branch
needs the same bound the variadic branch already has), with the
standalone repro, the exact contrast with the correctly-behaving
variadic branch, and the real-world path that reaches it. Applied the
same variadic-trailing-parameter workaround used by the correct branch
directly to `assert.equal`/`strictEqual`/`notEqual`/`notStrictEqual`/
`ok`/`fail` (`extra ...string`) as a noderati-side mitigation, committed
separately - not a fix to paserati's own bug, just enough to stop
*this* module from tripping over it.

Two real, measured (not guessed) host gaps surfaced along the way,
neither on the current failure path: `os.cpus()` and
`process.hrtime()` are both entirely missing from noderati's `os`/
`process` modules (`typeof os.cpus === "undefined"` where real Node
gives a function) - flagged as a background task rather than fixed
inline this round. Also noted, not fixed: `Object.keys(assert)` returns
`[]` against real Node's ~21 keys (the fix attaches properties to
`NativeFunctionObjectWithProps.Properties`, which `.SetOwn` makes
readable but not `Object.keys`-enumerable - matching `buffer.go`'s
identical pre-existing shape, not a regression this round introduced)
- a known fidelity gap for the ledger, not a blocker.

**Status**: `assert` is now real and callable; the arity workaround is
in place; both `babel.cjs` and `jiti.cjs` confirmed clean against a
freshly-`npm pack`'ed tarball. Trivial single-file `.ts` input
transforms and executes successfully end to end through jiti's real,
unmodified pipeline - genuinely new territory for this investigation.
Multi-file input with real imports/classes/enums still blocked, now on
paserati#278 (a systemic, already-filed reflection-arity bug) rather
than on anything specific to this pipeline. For the first time across
this entire multi-round investigation, the active blocker sits on
noderati's own side of the fence rather than paserati's.

**Sixty-first round (2026-09-06, same day) — verified #278's fix and
the background-spawned `os.cpus`/`process.hrtime` implementation, then
pushed the multi-file pipeline past every remaining VM-level crash into
ordinary parse behavior - and found the actual reason TypeScript syntax
has never once worked through this pipeline: a three-line
comma/ternary/destructuring miscompilation, unrelated to anything this
investigation had touched before, that silently empties
`@babel/parser`'s own enabled-plugin set.** User reported paserati
PR #279 merged and the spawned `os.cpus`/`process.hrtime` task done;
asked to continue.

Pulled paserati main to `33c00d2d` (merges #279, fixing #278: `goArgs`
in `goFunctionToVM`'s non-variadic branch now bounds itself to
`fnType.NumIn()` instead of `len(args)`, matching the variadic branch's
existing correct behavior; the same fix was additionally applied to
three sibling reflection sites - `createClassConstructor`,
`createBoundMethod`, `ValueConverter.wrapGoFunction` - found by
grepping for the identical `make([]reflect.Value, len(args))` pattern).
Found the background-spawned task's own commit already on this branch
(`5cfcad2`: `os.cpus()` synthesizing a `runtime.NumCPU()`-sized array
shaped like real Node's, `process.hrtime()` as a real monotonic
`[seconds, nanoseconds]` tuple with the relative-delta form). `go
build`/`go test -count=1 ./...` clean on both repos.

Verified #278 directly: `["a.ts","b.ts"].forEach(path.extname)` (and
`.basename`/`fs.existsSync`) - all three previously-panicking bare
callback shapes - now run without error. Verified `os.cpus()`/
`process.hrtime()` against real Node directly: shapes match
(`cpus()[0]` keys `model`/`speed`/`times`; `hrtime()` returns a
`[seconds,nanoseconds]` tuple; the relative-delta form works).

Re-ran the full multi-file `ext3/` pipeline test: no crash of any
kind, for the first time - a legitimate `ParseError` instead
(`Support for the experimental syntax 'flow' isn't currently enabled`,
on the *imported* `helper.ts`'s `export interface Greeting {...}`).
Confirmed real Node succeeds completely on the exact same two files
(prints `Hello, World! fast` / `1 2` / `done`), ruling out a test-setup
mistake. Narrowed via a minimal same-shape second file
(`export function greet(name: string): string {...}`, no interface, no
imports of its own) - still fails, at the `:` of the parameter's type
annotation. Ran `helper.ts` **alone**, as the sole entry file (no
import chain at all) - still fails, ruling out "second sequential
transform in one process" as the mechanism. Tested six independent
TypeScript syntax families as standalone entry files - typed function
parameters, `let x: T`, arrow-function parameter types, `interface`,
`type` aliases, `enum`, a typed class field - **all six fail**,
confirming TypeScript-specific syntax has never actually been exercised
successfully through this real pipeline; last round's "hello" success
contained no TS grammar at all, so it never tested this.

Reproduced independently of jiti and of every noderati host module:
built a direct driver (`shim.js` + real `babel.cjs`, no `createJiti`
involved at all - the same technique from round 59/60) calling
`transform()` with `ts:true` on a bare typed function - identical
`BABEL_PARSE_ERROR`/`UnexpectedToken` at the exact same position.
Instrumented a **private copy** of `babel.cjs` (`/tmp/verify279/
babel_copy.cjs`, never the real npm-installed file) at three
successively deeper points, each ruling out one subsystem before
moving to the next, per `advisor`'s explicit redirection each time away
from naming a region and toward finding the mechanism (the same
discipline that had been skipped, then caught, on the TCO and pipe-key
hypotheses two rounds ago):

- `removePlugin`'s own array-splice logic (`@babel/plugin-syntax-
  typescript`'s `manipulateOptions`, which removes conflicting `flow`/
  `jsx` parser-plugin entries before pushing `["typescript",{}]`) -
  isolated and run standalone: byte-identical output on both engines.
- `parserOpts.plugins` itself, logged right after `@babel/core`'s own
  `manipulateOptions` aggregation loop: byte-identical 12-entry array
  on both engines, `["typescript",{}]` present in both, same order,
  same pass/plugin counts.
- The plugin-name-to-options `Map` construction pattern
  (`Array.isArray(p)?p[0]:p` / `new Set(...)`/`new Map(...)`) tested in
  isolation with the same shape: correct on both engines.

Found the actual divergence one level deeper, in `@babel/parser`'s own
bundled `getParser` (the function building a `Map<pluginName,options>`
from that same, confirmed-identical `parserOpts.plugins` array, then
folding plugin-mixin subclasses over the base `Parser` class for every
*enabled* name) - logged its own intermediate `enabledOrdered` list and
the built class's `tsParseTypeAnnotation` method: `["typescript"]` /
`"function"` on real Node, **`[]` / `"undefined"`** on paserati - the
TypeScript parser mixin is silently never applied, despite the input
plugin list being provably identical. Traced to the exact three-line
shape responsible, read directly from the bundle's own source:

```js
for(const t of e.plugins){
  let e,r;
  "string"==typeof t?e=t:[e,r]=t,
  n.has(e)||n.set(e,r||{})
}
```

- a ternary whose **alternate branch is a destructuring assignment**
  (`[e,r]=t`), itself the **first operand of a comma expression**
  (continuing into `n.has(e)||n.set(...)`). Minimized to a fresh,
  shadowing-free 3-line standalone repro reproducing the identical
  divergence with no jiti, no babel, no host modules at all; narrowed
  with three separate controls (each removing exactly one ingredient,
  each passing correctly on both engines): the ternary alone with no
  comma-continuation; the equivalent `if/else` *statement* form instead
  of a ternary *expression*; the destructuring-assignment alone in a
  comma expression with no ternary. All three necessary; remove any one
  and it's correct. Two distinct corruption modes depending on what
  follows the comma (a bare `1` panics with `undefined is not a
  function`; a boolean-returning expression instead silently produces
  wrong values - the destructured variable receiving `[null,{}]`
  instead of the correct scalar, its source array left unmutated).

Called `advisor` three times across this narrowing (once per
instrumentation layer, matching the "one mechanism-settling probe per
call, not a guess" discipline established two rounds ago) plus once
more on the finished draft, which caught an unverified mechanism guess
in the write-up (softened to the observed values only, per the same
correction pattern as #271's root-cause overclaim) and suggested the
title name the shape rather than the symptom. Filed
[paserati#283](https://github.com/nooga/paserati/issues/283) - noted
as a structural sibling of #276 (both destructuring-assignment
compilation defects, in different expression positions), with the full
negative-probe ledger (six falsified hypotheses in order: `removePlugin`
isolated correct, `parserOpts.plugins` byte-identical, Set/Map
extraction correct, ternary-without-comma correct, `if/else`-statement-
form correct, destructuring-without-ternary correct), the exact
real-world consequence (`enabledOrdered` empty, TS parser mixin never
applied, all six TS syntax families fail identically), and the minimal
repro.

**Status**: `babel.cjs` and `jiti.cjs` confirmed clean against a
freshly-`npm pack`'ed tarball (only a private scratch copy was ever
patched this round). #278 and the `os.cpus`/`process.hrtime` gap are
both closed. The pipeline now reaches ordinary, catchable parse errors
on any TypeScript-specific syntax rather than any VM-level crash - a
different, narrower kind of blocker than every prior round hit, and,
for the first time, one with a three-line standalone repro rather than
a real-pipeline-only reproduction.

**Sixty-second round (2026-09-06, same day) — verified #283's fix,
confirmed the ten-bug regression suite still holds, watched all six
TS-syntax-family variants advance past #283 into a new, single
`OpGetSuper` runtime failure, and - via a from-scratch marker-
bisection technique rather than guessed repro shapes - pinned the
exact failing call site inside `@babel/parser`'s bundled TypeScript
mixin, then confirmed the base class it should dispatch to is never
entered. Root cause still not found: ten increasingly faithful
synthetic repros - up to and including the real 56KB mixin class body
extracted **verbatim** from `babel.cjs` - all pass correctly, so the
discriminator is somewhere in the surrounding context (closure/upvalue
wiring, the real constructor, or the real multi-plugin fold), not in
any syntactic shape tried so far.** User reported "FIXES ON MAIN
local," instructing continuation.

Pulled paserati main (no-op fetch/checkout - already current). `git
log -1 --format=%B b3c76792` confirms #283's fix and, importantly,
corrects last round's own framing: b3c76792 is explicit that #283 is a
**pure parser/precedence bug** (`parseArrayDestructuringAssignment`/
`parseObjectDestructuringAssignment` parsed their RHS at the lowest
precedence instead of `ARG_SEPARATOR`, the precedence the plain
`x = value` path already used correctly) - **not** a structural sibling
of #276 (a compiler/codegen defect, an `OpMove` into a foreign
register) as round 61's issue text claimed. Recorded here plainly
rather than glossed over, per the maintainer's own correction. The
same pull also brought in `f9569ee4` (#50, block-scope register-reclaim
determinism) - unrelated to this investigation, but it means one
paserati binary now compiles a given input identically on every run,
which retroactively firms up every "passed on this run" result logged
in this whole session.

`go build ./...` clean on both repos; `go test -count=1 ./...` clean on
noderati. Verified #283 fixed three ways: the maintainer's own filed
test (`issue283_ternary_comma_destructure.ts`, all five cases -
`ALL_CHECKS: true`) and both of round 61's own minimal repros
(`shadow_repro7.ts`, `shadow_repro10.ts`), both now matching real Node
exactly (previously one crashed with `undefined is not a function`,
the other silently produced `[null,{}]`). Ran a combined ten-bug
regression script (#256, #258, #260, #262/#271, #263, #265, #274,
#276, #278) - all still pass.

Re-ran all six TS-syntax-family jiti-pipeline variants from round 61
(typed function parameters, `let x: T`, arrow-function parameter
types, `interface`, `type` alias, `enum`, typed class field) - all six
now fail **identically** with a *different* error than #283's:

```
PS4001 [ERROR]: super keyword is only valid inside methods
  191:       `}},x=Object.assign({},b,{prop(e){const{property:t}=e.node...
       ^
    at .../jiti/dist/babel.cjs:191:1
```

Confirmed genuinely divergent (real Node succeeds completely, prints
the expected value for each variant) and confirmed a single root cause
producing six identical symptoms, matching the same pattern as #283
itself. Re-confirmed `babel.cjs`/`jiti.cjs` clean against the
`npm pack` reference before investigating further, ruling out leftover
instrumentation from a prior round.

Grepped for the error text and found it is **not** a parser-time
error at all - it's a VM runtime check. `pkg/vm/vm.go` has five
opcodes that each carry their own identical "super keyword is only
valid inside methods" check (`OpLoadSuper`, `OpGetSuper`, and three
others); the one actually hit here is `OpGetSuper`, at
`pkg/vm/vm.go:12342` (an earlier read of this investigation misnamed
it `OpLoadSuper` at line 12263, a different opcode's identical check -
corrected here). It throws whenever the executing frame's (or, for an
arrow, its `closure.CapturedHomeObject`'s) home object is
undefined/null. This reframed the investigation: the failure is a
runtime home-object-capture defect, not a parse-time gap, and the
natural first hypothesis (an arrow function nested in a class/object
method losing its captured `[[HomeObject]]`) needed a *tested*
mechanism, not an asserted one.

Four synthetic repros of that arrow-capture hypothesis were built and
**all four passed correctly** on noderati (falsified, in order):
`super.foo()` inside an arrow inside a real `class X extends Base`
method; the same inside an arrow inside an object-literal method with
an explicit `__proto__:` base; the same inside a `mixin = e => class
extends e {...}` factory (matching `@babel/parser`'s own
`getParser`-style plugin-mixin architecture, previously described in
round 61's writeup); the same again with a **four-level-deep** mixin
chain (`mixinD(mixinC(mixinB(mixinA(Base))))`), to rule out a dynamic-
prototype-chain-depth miscomputation of home object. None reproduced.

Rather than keep guessing shapes, built a **marker-bisection** tool
instead: used `acorn` (via `npm install --no-save acorn`, a read-only
analysis dependency, never added to either repo) to parse the real,
unmodified `babel.cjs` and enumerate every literal `Super` AST node
(244 total across the bundle) with each one's enclosing call
expression's exact byte range. Wrote a script that patches a byte-
precise copy of `babel.cjs`, wrapping 235 of the 244 sites (skipping 9
unsafe to wrap - assignment targets, update-expression targets, `for-
in`/`for-of` left-hand sides) as `((console.error("SITE_HIT",N),0)||
super.foo(...))` - a construct that preserves the exact call semantics
of `super.foo(...)` (unlike wrapping the callee alone, which would
extract the method and lose its special `this`-binding) while logging
its own site id immediately beforehand. Verified the patch is
behaviorally transparent first: real Node, run against the *real npm-
installed* `babel.cjs` (backed up first, restored and diff-confirmed
clean immediately after each patch cycle, per the established real-
file-patching discipline), still printed the correct transformed
output and fired 41 markers naturally during legitimate `super` calls.

Ran the patched pipeline on noderati: **exactly one marker fired -
`SITE_HIT 205` - before the crash.** Site 205 is:

```js
parse(){return this.shouldParseAsAmbientContext()&&(this.state.isAmbientContext=!0),super.parse()}
```

- a method named `parse`, inside the same `typescript:e=>class extends
e{...}` mixin already identified in round 61's #283 investigation,
whose body is a `return` of a comma expression ending in
`super.parse()`. This is the TypeScript mixin's override of the
Parser's own top-level `parse()` entry point.

Three more synthetic repros followed, each built to match this exact
site more faithfully than the last, and **all three also passed
correctly** (falsified): a minimal `class Sub extends Base { parse()
{ return cond && (assign), super.parse(); } }`; the same embedded
alongside five structurally-similar sibling methods (a `try/finally`-
wrapped `super.parseClass(...)` call, a rest-args-plus-`.bind()`-heavy
constructor calling `super(...args)`, several other comma-expression-
shaped `super.foo()` callers) to test whether a *neighboring* method's
compilation corrupts shared compiler state (the same register-reuse
family of bug as #276) before `parse()` is compiled - still correct.

Called `advisor` between each instrumentation layer, per this
session's established discipline. Its review of the marker-bisection
result made two corrections worth recording plainly: first, that
wrapping site 205 as `((console.error(...),0)||super.parse())`
*already* moves `super.parse()` out of comma-expression-tail position
and into the RHS of a parenthesized `||` - so the fact that it *still*
failed already exonerates the comma-expression framing entirely,
meaning the three post-bisection repros built around that framing were
never going to find it (a mistake this round made rather than
avoided, despite catching two similar mistakes in earlier rounds).
Second, that `SITE_HIT 205` printing only proves the marker executed
before `super.parse()` was evaluated - it does **not** prove that
`OpGetSuper` itself is what fails, since real Node's own tail
(`163, 195, 144, 146`) shows site 205 is reached mid-parse, well
before the file finishes, so there is real, unexamined execution
between the marker and the crash (specifically: whatever `super.parse()`
itself dispatches into, most directly the base-class `parse()` method
its `[[HomeObject]]`'s prototype should resolve to).

Ran that check: patched a single `console.error("BASE_PARSE_ENTERED")`
at the very first statement of the base (non-mixin) `Parser.parse()`
method - the one at `babel.cjs` byte offset 861356, `parse(){this.
enterInitialScopes();...}`, confirmed via the same acorn-based
`MethodDefinition` search to be the only candidate `parse()` with no
`super` call of its own (the other two - the TypeScript mixin's, at
773775, and an `estree` mixin's, at 660486, both call `super.parse()`
themselves and were already covered by the marker-bisection's 235
sites, neither of which fired). Verified transparent on real Node
first (prints `BASE_PARSE_ENTERED` then the correct output). On
noderati: **the marker never printed** - confirming the TypeScript
mixin's own `super.parse()` dispatch is the failure itself, not
something downstream of it. Restored `babel.cjs` clean immediately
after.

With the failing site now doubly confirmed, tried a decisive new
angle instead of another guessed shape: extracted the TypeScript
mixin's class body **verbatim** from `babel.cjs` (acorn located the
enclosing `ClassExpression`, bytes 722512-778866, 56KB, `superClass`
a bare identifier `e` matching `class extends e{...}`), wrapped it as
`const mixin = (e) => <verbatim class text>;`, applied it to a small
stub `Base` with matching method names, and constructed an instance
via `Object.create(mixin(Base).prototype)` (skipping the real
constructor, which reads several undeclared free variables - `Z`,
`TypeScriptScopeHandler`, etc. - that only exist as upvalues inside
babel.cjs's own module closure). **This passed correctly on both
engines** - the exact, byte-for-byte real class, calling `.parse()`,
works fine standalone. Confirmed via a disassembly comparison first
that the compiled bytecode for the *method itself* is not the
differentiator: `paserati`'s own `--bytecode --disasm-filter=parse`
flag (an existing, uncommitted-to feature, used read-only via a
locally-built `paserati` CLI binary) shows the real TypeScript mixin's
`parse()` compiles to the identical `OpGetSuper`+`OpTailCallMethod`
instruction pair as a minimal working repro's `parse()` - so the two
endpoints (verbatim class in isolation: works; same class inside the
full bundle: fails) mean the discriminator is in the *surrounding
context* the class is compiled/instantiated within, not in the class's
own text or its compiled method bytecode.

`advisor` named three concrete candidates for that context, in
priority order: (a) the class expression is a **nested closure with
upvalues** inside babel.cjs's own module-wrapper function (`Z`,
`TypeScriptScopeHandler`, etc. are free variables captured from that
enclosing scope, not undefined globals as in the isolated extraction);
(b) the real `new Parser(...)` constructor path (this round's isolated
test used `Object.create` to skip the constructor entirely); (c) the
real `getParser` fold combining multiple enabled plugin mixins. All
three were built and run and **all three also passed correctly** on
both engines - eleventh through thirteenth falsified probes. (c) was
tested precisely rather than guessed: patched a private copy of
`babel.cjs` to log the fold's actual enabled-plugin list right after
it's computed (`for(const r of se)e.has(r)&&t.push(r)`), and it came
back identical on both engines - `["typescript"] 1 cacheHit false` -
a single mixin applied once, exactly matching every synthetic repro
already tried; multi-plugin folding was never the discriminator.

With all context-level hypotheses exhausted, `advisor` pointed at the
one thing no repro had varied: **how `parse()` itself gets called**.
Every repro so far called it as `inst.parse()` - an ordinary method
call. `grep`ping the real `babel.cjs` for the actual call site
(`getParser(t,e).parse()`, found verbatim, three occurrences) showed
it's a **tail call** - and the disassembly gathered earlier this round
was already dense with `OpTailCallMethod` for exactly this reason
(`return ..., super.parse()` is itself in tail position). Read
`pkg/vm/vm.go`'s `OpTailCallMethod` handler directly (starting at line
4013): its frame-reuse step reassigns `frame.closure`, `frame.ip`,
`frame.thisValue`, `frame.isConstructorCall`, `frame.isDirectCall`,
`frame.isSentinelFrame`, `frame.generatorObj`, `frame.promiseObj`,
`frame.argCount`, `frame.args`, and `frame.spillSlots` for the new
callee - but never `frame.homeObject`, the exact field `OpGetSuper`
reads for non-arrow closures. Built the first repro all round to
finally reproduce standalone:

```js
class Base { parse() { return "base-parse"; } }
const mixin = (e) => class extends e { parse() { return super.parse(); } };
const Sub = mixin(Base);
function run(p) { return p.parse(); }   // tail call position, NOT itself a method
console.log(run(new Sub()));
```

Fails on paserati (both the bare `paserati` CLI and via noderati),
succeeds on real Node. A control removing only the tail position
(`return p.parse() + ""`) passes on both engines, isolating tail-call
frame reuse as the exact differentiating ingredient after eleven other
shapes failed to isolate it. A second case (`super.toString` read,
not called, same tail-position shape) fails identically, showing the
defect isn't specific to `super.x()` calls - a bare `super.x` property
read reached the same way fails too.

Filed as [paserati#285](https://github.com/nooga/paserati/issues/285),
scoped to the observed facts per `advisor`'s review: which frame
fields the reuse step does and doesn't reassign, and that behavior
flips exactly on tail-vs-non-tail position - without asserting the
unverified specifics of what stale value ends up read (never printed
it) or whether the arrow-function branch is equally affected (untested,
noted as an open question rather than implied coverage).

Confirmed `babel.cjs` restored clean (`diff -q` against the
`npm pack` reference) after every patch cycle this round.

**Status**: #283 verified fixed (with the corrected parser-vs-
compiler framing above); ten-bug regression suite still holds. The new
blocker - a method invoked in tail position losing `[[HomeObject]]`,
so any `super` usage inside it throws - is root-caused to
`OpTailCallMethod`'s frame-reuse step in `pkg/vm/vm.go` and filed as
paserati#285, with a four-line standalone repro and a flipping control.
Thirteen synthetic-repro shapes were built and falsified before the
fourteenth (varying the *call site*, not the *callee*) finally
reproduced it - the marker-bisection technique that narrowed the
search space, and the fold-list instrumentation that closed off the
multi-plugin hypothesis, were both necessary to get there. This is the
same TCO-adjacent subsystem flagged, then dropped without testing, in
an earlier round of this session - this time verified against source
and a working repro before being named.

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

**Sixty-third round (2026-09-06, same day) — pulled paserati#285's fix,
verified it, then watched all six TS-syntax-family jiti-pipeline
variants and a real multi-file import chain succeed end-to-end for the
first time this whole investigation. Ran the real, unmodified
`pi-coding-agent@0.80.2` CLI against the built noderati binary:
`--version` and `--help` both succeed (exit 0, correct real output);
bare `-p "hello"` fails on `"Connection error."` against this
environment's default (unreachable local) provider, but with an
explicit `--provider fireworks` flag and the real key already
configured on this machine, `-p` returns correct completions on 4/4
runs - a real, live, credentialed LLM round trip through the whole
stack, and the actual case this document's "Definition of done"
asks for. Deleted the jiti/static fake, ledger group B's last
third-party-package fake - one third-party shim (`undici`'s,
real-Node-vendored but still a package, not a builtin) remains as the
outstanding item.** User reported "fixes on main," asked to pull and
check for progress; after the first pass reported `-p` failing on a
generic connection error, user pointed out a real Fireworks provider
is already configured locally and `pi -p` should work against it -
correct, and recorded as a same-day correction below rather than a
new round.

Pulled paserati main (`f9569ee4..67d90d68`, two commits): `26a3bd68`
("fix(vm): preserve [[HomeObject]] across tail calls (fixes #285)")
and a follow-on `67d90d68` (an unrelated compiler register-allocation
fix for emitted error throws). The #285 fix matches exactly what this
investigation found by reading source: `OpTailCall`/`OpTailCallMethod`
reused the current frame for the callee without updating
`frame.homeObject`, mirrored from the assignment `prepareCall` already
did for regular calls - the maintainer's commit message independently
describes the identical mechanism this round's write-up landed on
without asserting.

`go build ./...` clean on both repos; `go test -count=1 ./...` clean
on noderati. Verified #285 fixed three ways: the filed issue's own
4-line repro (tail-called method losing `super`), the plain
`super.toString` property-read variant (no call), and the maintainer's
own filed regression test (`tco_tail_call_super_homeobject.ts`) - all
three now match real Node. Ran an eleven-bug combined regression sweep
(#256/#258/#260/#262/#271/#263/#265/#274/#276/#278/#283/#285) - all
pass.

Re-ran all six TS-syntax-family jiti-pipeline variants that have
anchored every round since the fifty-ninth: typed function parameters,
`let x: T`, arrow-function parameter types, `interface`, `type` alias,
`enum`, typed class field. **All six now transform and execute
correctly**, matching real Node's output exactly, for the first time
in this entire investigation. Re-ran the multi-file `ext3/` test from
round 61 (an importing `main.ts` plus an imported `helper.ts` with its
own `interface`/function/const exports) - **also passes end-to-end**,
matching real Node's three lines of output exactly.

With the pipeline itself unblocked, ran the real target: built
noderati, pointed `cmd/scoreboard` at the real, unmodified
`/opt/homebrew/.../pi-coding-agent/dist/cli.js`. `pi --version` and
`pi --help` both succeed (exit 0; version prints `0.80.2`; help prints
the real, full command listing). `pi -p "hello"` exits 1 with
`"Connection error."` - per `advisor`'s pushback, checked this rather
than inferring it from the message string alone (a message match isn't
proof the code path that produces it was actually reached). Traced
`"Connection error."` to `@anthropic-ai/sdk`'s own `APIConnectionError`
class (`node_modules/@anthropic-ai/sdk/core/error.js`), thrown only
after `client.js`'s request path awaits a real `fetchWithTimeout(...)`
call that rejects - not a generic top-level catch. Confirmed paserati's
own `fetch` builtin (`pkg/builtins/fetch_init.go`) is backed by Go's
real `net/http`, not a stub, and that noderati's `undici` shim (see
below) only reuses whatever `globalThis.fetch` already is rather than
replacing it - so no fake sits between the SDK and a real socket.
Timed the run: **~19 seconds wall-clock** before failing, consistent
with a real DNS/TCP connection attempt plus the SDK's retry-with-
backoff logic, not an immediate synchronous rejection (which would
return in milliseconds). `ANTHROPIC_BASE_URL` is set in this sandbox
to the real `https://api.anthropic.com`, and no API key is present -
so the standing "no network access in this sandbox" explanation is
consistent with a real connection attempt failing at the network
layer specifically (as opposed to getting a real HTTP 401 back, which
the SDK would surface as a different error class entirely). Not
independently confirmed via a packet capture or proxy - the timing and
code-path evidence together are strong but circumstantial. This is the
exact three-invocation set named in this document's own "Definition of
done" section; two of three succeed outright, the third fails at a
boundary outside this project's control rather than inside it.

**Correction, same day**: the above `-p "hello"` analysis tested
whatever provider `~/.pi/agent/settings.json` names as
`defaultProvider` - in this environment, `"local"` (LM Studio at
`127.0.0.1:1234`), which simply isn't running here. The user pointed
out a real, working Fireworks provider is already configured in
`~/.pi/agent/models.json` (a live API key, stored in plaintext in that
file - flagged to the user directly, not reproduced here) and that
`pi -p` should work against it. It does: `pi --provider fireworks
--model accounts/fireworks/models/glm-5p2 --no-session -p "..."`
returned correct completions on **4/4 runs** (a single word, "4" to a
"what is 2+2" prompt, and a 3-line numbered list), all exit 0, ~7-8s
each - a real, live, credentialed LLM round trip through the entire
stack (noderati, paserati, the real jiti/babel pipeline, the real
`@anthropic-ai`-shaped OpenAI-completions client, real `net/http`).
This is the actual real-key case the "Definition of done" section
names ("...or with a real key if the user provides one"), and it's
satisfied. What is *not* established is bare `pi -p "hello"` with no
provider flags - that still fails, but for a local-config reason (no
LM Studio server running) rather than an engine or host gap; the
~19s-timing/`APIConnectionError`-tracing paragraph above still stands
as an accurate account of *that* specific (default-provider) case, not
of Fireworks. Both facts matter and neither should be read as
superseding the other.

Checked whether the jiti/static fake (`internal/host/jiti.go`,
ledger group B's last remaining entry) was now safe to delete, per
this ledger's established measure-then-delete pattern. The
CLI-invocation scoreboard alone couldn't settle it: `fake-off:jiti`
already matched baseline on every invocation, but none of
`--version`/`--help`/`-p` actually exercise pi-coding-agent's extension
loader (the only real consumer of `jiti/static`) without a configured
extension - the same "match is necessary but not sufficient" gap this
ledger hit before deleting pi-tui's fake. Did the real functional
exercise instead: lifted the exact call pattern from pi-coding-agent's
own `dist/core/extensions/loader.js` (`createJiti(import.meta.url,
{moduleCache:false, alias})` then `jiti.import(path,{default:true})`)
and ran it against an actual TypeScript extension-shaped module -
matched real Node exactly, both with the fake on (returns the fake's
stub `{}`, confirming the fake was still live and would have masked a
regression) and with it disabled (returns the real, working module).
Deleted `internal/host/jiti.go`, its `installModules()` call site, and
emptied `cmd/scoreboard/main.go`'s now-zero-length `fakeNames` list
(kept as an empty slice rather than removed outright, since
`cmd/scoreboard` still owns the toggle mechanism directly - unlike
`disabledSet`/`isDisabled` in `scoreboard_config.go`, now genuinely
dead code but left in place per this ledger's own established
"harmless plumbing for a future fake" precedent, the same call already
made for `NODERATI_DISABLE_PATCHES`). Rebuilt clean, re-ran every test
above against the new binary - all still pass, and the scoreboard's
own `all-fakes-off` row still matches baseline (there being nothing
left to disable).

Audited every remaining `registerJSShim` call in the tree
(`child_process`, `module`, `diagnostics_channel`, `events`,
`string_decoder`, `perf_hooks`, `readline`, `undici`, `stream`,
`stream/promises`) against this document's "Definition of done"
criterion. Caught an overclaim before writing it down as fact: `undici`
is **not** a Node builtin - it's a third-party npm package Node
vendors internally to implement `fetch`, but `import "undici"` from
user/dependency code resolves through node_modules like any other
package, not through Node's builtin-module registry. Every other name
in that list is a genuine Node builtin. `undici`'s own shim
(`internal/host/undici.go`) is small (`setGlobalDispatcher`/
`EnvHttpProxyAgent`/`install`, the last delegating to whatever
`globalThis.fetch` already is rather than replacing it) and untested
against the real npm package this round - named here as the one
outstanding ledger-group-B-shaped item, not deleted.

**Status**: the jiti-pipeline blocker this investigation has chased
since round 57 is closed - #285 was the last link in a six-bug chain
(#274→#276→#278→#283→#285) surfaced one at a time by getting real
`@babel/core`/`@babel/parser` further through its own real transform
pipeline each round. All six TS-syntax-family variants and a real
multi-file import chain now succeed end-to-end. All three of this
document's "Definition of done" invocations now succeed against the
real, unmodified npm install: `pi --version`/`pi --help` unconditionally;
`pi -p` with an explicit `--provider fireworks --model ...` flag and
the real key already configured in `~/.pi/agent/models.json`, 4/4 runs
correct. Bare `pi -p "hello"` (no provider flags) still exits 1 against
this environment's *default* provider (`"local"`, LM Studio on
`127.0.0.1:1234`, not running here) - a local-config gap, not an
engine or host one, and distinct from the real-key case the
definition-of-done wording actually asks for. `internal/host` is down
to one remaining third-party-package shim (`undici`'s), not zero -
this document's own "definition of done" wording needs `undici` named
explicitly rather than assumed covered by "zero package-specific
shims," and that shim is untested against the real package as of this
round (this round's Fireworks run did exercise `fetch` through it
without incident, which is evidence the shim isn't breaking anything,
but not the same as a real-package functional exercise). Not yet
attempted: verifying `undici` against the real npm package, and any
exercise of pi-coding-agent's TUI/interactive mode (per the standing
pi-tui deletion note, that surface needs an attached terminal/pty to
test meaningfully on any engine and stays deliberately unverified
here). Neither was attempted or claimed this round.

**Sixty-fourth round (2026-09-06, same day) — committed and fast-
forward-merged all 93 commits of the phase1-close-phase2-scoreboard
branch into main, then did Phase 4 (resolver honesty) end to end: both
of ledger group D's items turned out smaller than the plan assumed,
verified by testing rather than trusted from the plan text, and both
are now closed.** User asked to commit, merge to main, then prep for
the next phase.

Merged cleanly (main hadn't diverged, straight fast-forward,
91ef991→860b9ec). Recon before committing to a plan: `NodeModulesResolver`'s
`findPackageDir` already implements a real Node-style walk-up (climbs
parent directories checking `node_modules/<pkg>` at each level) - the
plan's own "there is no walk-up implementation to fall back on" note
(this document's ledger group D) was stale. Tested with both
`findPiCodingAgentNodeModulesRoots()`'s hardcoded homebrew paths *and*
`entryScriptDirs()`'s entry-script fallback completely removed: `pi
--version`/`--help`/`-p` (real Fireworks backend), the full scoreboard,
and pi-coding-agent's own real extension-loader call pattern (run from
`loader.js`'s own real path, matching how it's actually invoked) all
still passed. Asked the user before committing to implementing (recon
this cheap and this validated didn't need to stay just a plan) -
confirmed, proceeded.

Deleted the `extraDirs` mechanism from `NodeModulesResolver` entirely
(not just called with empty args - the parameter, the field, and the
fallback loop are gone), `entryScriptDirs()`, and
`findPiCodingAgentNodeModulesRoots()` along with the now-fully-dead
`piai.go` that held it. Moved that file's Bedrock-provider-unverified
caveat into `host.go`'s own comment rather than letting it vanish with
the file (`advisor` caught the dangling "see piai.go" reference before
commit).

Fixed `NodeMissingResolver` next, after directly testing (not
assuming) what it currently does wrong. Real Node's actual behavior
for an unresolvable import: a static `import` of a genuinely-missing
specifier throws *before* any of the importing module's own top-level
code runs (confirmed: a script that prints something before such an
import never gets to print it, in both engines) - so the existing
"module whose body throws" approach was already behaviorally correct
for the common case, contrary to the plan's framing. What was
genuinely wrong: the *message* - a paserati-internal "no resolver
could handle specifier: X" instead of any of Node's own three shapes,
confirmed directly against real Node for each:

- `node:xxx` naming an unrecognized builtin → `ERR_UNKNOWN_BUILTIN_MODULE`,
  `"No such built-in module: node:xxx"` (not `ERR_MODULE_NOT_FOUND`'s
  "Cannot find module", which the previous implementation used and
  which is real Node's message for a *different* case).
- a relative/absolute path specifier → `ERR_MODULE_NOT_FOUND`,
  `"Cannot find module '<absolute path>' imported from <fromPath>"` -
  the path in the message is the *resolved* absolute path, not the raw
  specifier as written (confirmed: `./does-not-exist.mjs` becomes
  `/private/tmp/does-not-exist.mjs` in real Node's own message).
- an ordinary bare package specifier → `ERR_MODULE_NOT_FOUND`,
  `"Cannot find package 'xxx' imported from <fromPath>"`.

`NodeMissingResolver`'s `CanResolve` was `node:`-prefix-only before;
widened to unconditionally `true` so it also catches the second and
third cases (previously falling through to the same generic loader
message with no specifier-shape awareness at all). Verified this
doesn't shadow any real resolver: grepped every resolver's own
`Priority()` across both repos - `NodeMissingResolver` sits at 200,
every resolver host.go registers sits at -50/-10/0/50, and paserati's
own unregistered-by-default `FileSystemResolver`/`MemoryResolver` sit
at 100/50 - all below 200, so nothing this resolver could shadow was
ever going to be tried after it regardless. Confirmed with a positive
case rather than trusting the priority numbers alone: a real relative
import (`import { v } from "./dep.mjs"`) still resolves and prints `42`
correctly with the widened `CanResolve` in place (`advisor` asked for
this specifically - the earlier "relative import fails with the new
message" test couldn't distinguish "message improved" from "resolution
silently broke," since both look identical from that one test alone).

One honest caveat, worth stating plainly rather than glossing over:
`node:net` (the specifier this ledger's own pre-existing test asserts
against) is a *real* Node builtin - just one noderati doesn't
implement (a tracked Phase 5 gap) - so real Node doesn't error on it
at all. `"No such built-in module: node:net"` is therefore not a
byte-exact match to what real Node would actually do here (nothing);
it's the more honest of the two message shapes available given
noderati has no registry of real-builtin-names-not-yet-implemented to
consult, and it does correctly describe *this* runtime's own gap.
Updated the two existing tests asserting the old, wrong message to
assert the new one, with this caveat recorded in both the test and
`NodeMissingResolver`'s own doc comment so a future round doesn't
have to re-derive it or mistake it for an exact-match claim.

Re-verified everything end to end after both changes: `go build`/`go
test` clean; `pi --version`/`--help`/`-p` (Fireworks) all still
succeed; the scoreboard still matches baseline with nothing left to
disable; all six TS-syntax-family variants and the multi-file `ext3/`
test from round 61 all still pass, run from a real anchor point inside
pi-coding-agent's own tree (a `dist/core/extensions/` scratch
directory, matching how `loader.js` itself is actually invoked) rather
than from `/tmp`, since the `/tmp`-anchored version of that same
harness no longer resolves `jiti` at all without the deleted fallback
- correctly so, matching what real Node also does for a script that
was never really part of any package's node_modules tree (confirmed
this distinction directly, not assumed). The `ext3/` re-run's own
relative `./helper.ts` import was rewritten to an absolute path for
this scratch relocation, so that specific re-run no longer exercises
relative-import resolution through this resolver chain (jiti resolves
that internally); the separate `/tmp/relcheck` positive test above is
what actually covers the `CanResolve` widening's safety, not this one -
worth being precise about rather than implying broader coverage than
was actually run.

**Status**: ledger group D (resolver-side dirty tricks) is closed -
both items were real, but smaller and differently-shaped than the plan
described, confirmed by testing rather than trusted from the plan
text. `internal/host` no longer contains any hardcoded install-path
fallback or entry-script-directory special-casing; resolution is
real-Node-shaped walk-up, unconditionally. Missing-module errors now
match real Node's own three message shapes (unknown builtin, missing
relative/absolute path, missing bare package), each confirmed directly
against real Node, with the one honest caveat above recorded rather
than glossed over. Not yet attempted: Phase 5 (the `net`/`tls` gap that
blocks Bedrock, a real `stream` beyond the current EventEmitter base,
verifying `undici` against the real npm package) and Phase 6 (real
`tsc`) - both still open, neither started this round.

**Sixty-fifth round (2026-09-06, same day) — user asked for a runnable
command to launch pi's TUI with Fireworks, then reported it back
broken ("nothing happens, no tui renders, no input accepted"). Found
and fixed three real, compounding engine/host gaps that had made
noderati's entire interactive-mode surface inert: `process.prependListener`/
`addListener` missing, `process.kill` missing (with zero OS-signal-to-JS
bridging at all), and `process.stdin.setRawMode` being a no-op combined
with `resume()` never starting a reader for TTY stdin in the first
place.** Unplanned work (this document had no Phase 5 TUI item yet),
driven directly by the user's own bug report rather than by the ledger.

The command given was `pi --provider fireworks --model
accounts/fireworks/models/glm-5p2` (no `-p`, so pi's default interactive
TUI mode). It hung: no render, no error, no input echo - the same
"silently swallowed exception inside an async chain" failure shape this
document has hit before (`assert` in round 60, the tail-call
`[[HomeObject]]` bug before that), so the same methodology applied:
patch a **scratch copy** of the real, unmodified pi-coding-agent
install (`dist/` + `node_modules/`, copied to `/tmp/pi_scratch/`, never
the real npm install) with `appendFileSync`-based `TRACE()` markers in
`main.js` and `interactive-mode.js`, then re-run under a real pty to
see exactly where execution stops. A bare Bash tool call has no real
tty at all (so `stdin.isTTY` reads false and masks the actual bug path -
confirmed by first trying `timeout 8 ... < /dev/null`, which returned
instantly with zero output and no repro), so reproducing this at all
required macOS `script -q <file> <cmd> < /dev/null` to allocate a
genuine pty, and later `expect` to inject real keystrokes into a live
one.

**Gap 1 - `process.prependListener`/`addListener` missing entirely.**
pi-coding-agent's `registerSignalHandlers()` calls
`process.prependListener`, which didn't exist on noderati's `process`
object (`typeof` → `undefined`) - a synchronous `TypeError` thrown
inside an async `init()` that nothing ever surfaced, hanging the whole
process with zero output instead of a visible error. Fixed in
[emitter.go](../internal/host/emitter.go): `newEventEmitterObject`
(the shared helper backing `process`, streams, etc.) gained
`addListener` (a plain alias for `on` - real Node's EventEmitter
exposes both), `prependListener`, `prependOnceListener`,
`removeAllListeners`, `listenerCount`, and `listeners`.
`addListener`/`once`/`prependListener`/`prependOnceListener` all now
route through one `addListener(vmInst, obj, event, listener, once,
prepend)` helper, with `prepend` inserting at index 0 (shifting
everything else up) instead of appending.

**Gap 2 - `process.kill` missing, and zero OS-signal-to-JS bridging at
all.** With gap 1 fixed, the same silent-hang shape recurred one layer
in: `@earendil-works/pi-tui`'s `ProcessTerminal.start()` self-signals
via `process.kill(process.pid, "SIGWINCH")` to force a terminal-size
refresh, and `process.kill` didn't exist either. Beyond just adding
that one function, actually supporting it exposed that noderati had
**no OS-signal-to-JS-event bridge whatsoever** - even a correctly-sent
signal (self-sent or a real external `kill -TERM <pid>`) would arrive
at the OS level and go nowhere, since nothing translated it into a JS
`process.emit("SIGxxx")` call. Both fixed in the new
[signals.go](../internal/host/signals.go): `installProcessKill` adds
`process.kill(pid, signal)` (default `SIGTERM`, signal `0` as Node's
existence-probe, real `ESRCH`/`ERR_UNKNOWN_SIGNAL` errors via a new
generic `simpleException`/`simpleNodeError` pair mirroring
`fs_errors.go`'s existing `vm.ExceptionError` pattern); `startSignalBridge`
calls `signal.Notify` on every signal name Node recognizes that's
actually catchable, and re-emits each as a same-named event on
`process` via `rt.ScheduleNextTick` (never touching VM state directly
from the OS-signal-delivery goroutine). `SIGKILL`/`SIGSTOP` stay in
`nodeSignals` (so `process.kill` can still *send* them - that part is
real everywhere) but are explicitly skipped by the bridge, since no
process on any OS can catch either one - a deliberate asymmetry between
the two tables, called out in the code so a future pass doesn't "fix"
it by symmetrizing them.

**Gap 3 - `process.stdin.setRawMode` was a complete no-op, and
`resume()` never read TTY stdin at all.** The last and biggest gap:
`setRawMode(true)` only ever flipped a JS-visible `isRaw` flag - it
never touched the real terminal, so the OS driver stayed line-buffered
and echoing regardless of what the app asked for. Independently,
`resume()` explicitly skipped starting its stdin-reading goroutine
whenever stdin was a TTY. Together these meant **real interactive
keyboard input was never read by noderati at all**, TTY or not - the
most direct possible confirmation of the user's own "no input accepted"
report. Fixed in [process.go](../internal/host/process.go):
`setRawMode` now calls `golang.org/x/term`'s `MakeRaw`/`Restore`
(already imported in this file for TTY size detection), tracking the
returned `*term.State` in a closure so it can be restored later; the
TTY special-case was deleted from `resume()` entirely, since
`os.Stdin.Read` blocks correctly either way (line-buffered without raw
mode, byte-at-a-time with it) - there was never a real reason to
special-case TTY stdin out of reading at all.

Noted directly in `setRawMode`'s own comment rather than left implicit:
`term.MakeRaw` mutates the *real* controlling terminal, and nothing
restores it automatically if the process dies without calling
`setRawMode(false)` first - a panic, a skip-cleanup `os.Exit`, or a hard
kill all leave the user's shell stuck in raw mode after this process
exits. Real Node has the identical footgun. Checked pi-tui's own
source rather than assuming: its `Terminal.stop()` (called from pi's
`shutdown()`, which its `SIGTERM`/`SIGINT` handlers call, which now
actually fire thanks to gap 2's fix) does call
`process.stdin.setRawMode(this.wasRaw)` as part of normal cleanup - so
the common paths are covered by the app itself, not by noderati. A
`SIGKILL` or an unhandled crash still has no recovery; that's inherent
to raw mode on any platform, not a gap this host can close.

**Verification.** Each gap confirmed independently before moving to the
next (isolated one-liner tests reproducing just that `TypeError`/no-op,
then re-tracing the full TUI startup after the fix). After all three:
a full pty capture (`script`) of the TUI now renders identically to
real Node's own render of the same command; a fresh `expect` script
that types `"hello world"` shows the text landing correctly inside the
TUI's own styled editor box (three occurrences, properly ANSI-wrapped,
surviving an intervening "Update Available" re-render) rather than as
raw OS-level echo, which is what happened before this round. Honest
caveat, not glossed over: that same test's Enter keypress did not
visibly submit the message before the test's own timeout killed the
process - most likely because an `expect`-driven synthetic pty doesn't
answer the terminal capability queries (e.g. pi-tui's
`queryAndEnableKittyProtocol()`) a real terminal emulator would, not a
remaining noderati bug, but **not independently confirmed either way** -
this needs a real terminal, which none of this session's tools can
fully provide. The user is the authoritative test for this specific
question: re-running the original TUI command in their own terminal
now that rendering and keystroke-capture are confirmed fixed.

Also checked, since these three changes touch shared, high-traffic
surface (`process`, its EventEmitter base, and stdin's resume path) and
not just TUI-specific code: a plain non-TUI script that calls
`process.stdin.resume()` then exits on its own timer still exits
cleanly (no hang from the now-always-started stdin-reader goroutine);
`pi --version`, `pi --help`, and `pi -p "..." --provider fireworks
--model accounts/fireworks/models/glm-5p2` (a real, live, credentialed
round trip) all still succeed against the real, unmodified npm install;
`go build ./...` and `go test -count=1 ./...` both clean; the scoreboard
still matches baseline with nothing left to disable.

**Status**: all three TUI-blocking gaps are fixed and independently
verified up through "keystrokes render correctly inside the app's own
UI." Not yet confirmed: Enter-key message submission, arrow-key
navigation, and any ctrl-key handling in the TUI - none of this
session's tools can drive a genuine interactive terminal session, so
these remain open questions for the user to confirm directly rather
than claims this document asserts as verified.

**Sixty-sixth round (2026-09-06, same day) — user reported "works until the
agent makes some tool calls"; traced the real call chain instead of
guessing, found the actual gap was three layers deeper than the first
plausible-looking candidate, fixed all three real bugs uncovered along
the way.** Direct continuation of round 65's TUI work - same session,
same reported command now confirmed rendering and accepting plain
messages in the user's own real terminal.

**First candidate, real but not the cause: `child_process.spawn`
silently dropped its whole options argument.** Read pi-agent-core's own
real shell-exec harness
([`nodejs.js`](file:///opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core/dist/harness/env/nodejs.js))
side by side with noderati's `child_process.go`: the JS-level
`spawn(command, args, options)` shim already forwarded `options`
correctly across the JS-to-Go boundary, but `__noderatiSpawn`'s native
handler only ever read `args[0]`/`args[1]` - `cwd`, `env`, and
`detached` all crossed intact and were then dropped. Fixed by threading
a real `spawnOptions` (parsed via a new `parseSpawnOptions`) through to
`exec.Cmd.Dir`/`.Env`, and by adding `setDetached` (new
`child_process_unix.go`/`child_process_windows.go`, POSIX
`SysProcAttr.Setpgid`) so a detached child gets its own process group -
which is what makes `process.kill(-pid, sig)` (already correct since
round 65's signal work, since it forwards whatever pid JS passes,
negative included, straight to `syscall.Kill`) actually reach the whole
subprocess tree the way pi-agent-core's own `killProcessTree` (used for
both its per-call timeout and Escape-key cancellation) expects. Added
`TestSpawnCwdOption`/`TestSpawnEnvOption`/`TestSpawnDetachedGetsOwnProcessGroup`
to `child_process_test.go`, all passing, all against the real,
documented Node contract (an `env` option *replaces* the child's
environment, it never merges with the parent's - confirmed against
Node's own docs before writing the test, not assumed).

**Found in passing while adding those tests: `TestSpawnEcho` was
already flaky before this round, unrelated to the options-forwarding
fix** - reproduced independently on the pre-fix code via `git stash`,
so this was a **pre-existing** bug, not introduced this round. Root
cause: `spawnProcess` ran `waitSpawnProcess` (which calls `cmd.Wait()`)
concurrently with the two `pumpSpawnStream` goroutines (which read
`cmd.StdoutPipe()`/`StderrPipe()`), racing to schedule "close" against
"data"/"end" onto the VM's event-loop queue with no ordering guarantee
between them - a direct violation of Go's own documented contract for
`StdoutPipe`/`StderrPipe` ("it is incorrect to call Wait before all
reads from the pipe have completed"), and exactly the shape that made a
fast-exiting command like `echo hello` intermittently report empty
stdout. Fixed with a `sync.WaitGroup` (`pumpDone`): both pump goroutines
signal it on completion, and `waitSpawnProcess` now waits on it before
calling `cmd.Wait()` - so "close"/"exit" can only be scheduled after
every "data"/"end" event that logically precedes them, not just usually
after. 50 back-to-back runs of the spawn test suite (previously flaky
within single digits) came back clean.

**Real root cause, found by tracing the actual "bash" tool's real call
chain end to end rather than trusting the first plausible bug found:**
built the fix above, verified it standalone against pi-agent-core's own
`NodeExecutionEnv.exec()` (worked), then re-ran the real, unmodified
`pi -p "... using the ls tool ..." --provider fireworks` CLI end to end
- and it still failed, with the real session log
(`~/.pi/agent/sessions/`, real fireworks tool-call IDs, real
usage/cost metadata - a genuinely fresh LLM turn each time, not stale
history) showing the bash tool returning `"undefined is not a
function"` on every attempt. `NodeExecutionEnv.exec()` turned out to be
a red herring: the real "bash" tool wired into pi's own tool list is
`createBashToolDefinition`/`createLocalBashOperations`
(`pi-coding-agent`'s own `dist/core/tools/bash.js`, a separate,
independent implementation from pi-agent-core's harness class) - a
different call shape entirely (`ops.exec(command, cwd, {onData,
signal, timeout, env})`, no `setEncoding` call anywhere in it). Traced
by reproducing each layer of the real call chain directly against real
dist files, narrowest to widest, until the exact throw site showed up
in a stack trace: `createBashToolDefinition(...).execute()` ->
`OutputAccumulator.snapshot()` (`core/tools/output-accumulator.js`) ->
`truncateTail()` (`core/tools/truncate.js:126`) -> `Buffer.byteLength(content,
"utf-8")`.

**`Buffer.byteLength` was missing from noderati's `Buffer` shim
entirely** ([`buffer.go`](../internal/host/buffer.go)) - only
`from`/`alloc`/`isBuffer` existed as statics. A repo-wide count across
the real, unmodified pi install (`grep -c 'Buffer\.byteLength('`) found
**36 separate call sites** - by far the most common `Buffer` static
after `Buffer.from` itself, because every real bash-tool call's output
goes through `truncateTail()`'s truncation-decision logic, which needs
a byte length before it can decide whether to truncate. This is why
plain conversation worked (no `Buffer` involved at all) but *every*
tool call failed on its very first output line, regardless of what the
command even was - matching the user's report exactly, and explaining
why round 65's raw-stdin fix alone wasn't enough to make the TUI fully
usable. Fixed: `Buffer.byteLength(input, encoding)` returns a Buffer
argument's own already-byte-counted `.length` unchanged, or a plain
JS string argument's real UTF-8 byte length (`len()` of the Go string
`ToString()` produces - Go strings are UTF-8 natively, so this matches
Node's own default `'utf8'` encoding without needing to actually parse
the encoding argument).

**Verification.** Re-ran the exact failing repro
(`createBashToolDefinition(...).execute()` against a real cwd) after
the `Buffer.byteLength` fix: succeeds, returns the real file listing.
Re-ran the full, real `pi -p "run ls ... using the ls tool ..."
--provider fireworks` CLI end to end: the agent now genuinely calls the
bash tool, gets real output, and reports the real files present in the
real directory - the first time in this entire investigation a real
LLM-driven tool call has completed successfully against noderati.
`go build ./...`/`go vet ./...` clean; full test suite (including the
three new spawn-options tests) clean across three repeated runs, no
flakes; `pi --version`/`--help`/`-p` (Fireworks) all still succeed; the
scoreboard still matches baseline.

**Also produced this round, at the user's request before the fix: a
full transitive dependency count for the real, unmodified pi install**
- 144 distinct npm packages (one, `retry`, present at two hoisted
versions), walked directly from the real `node_modules` tree rather
than via `npm ls` (which errors on this global-install layout with no
lockfile to resolve against). Not recorded in detail here since it's a
point-in-time count, not a tracked gap - see the chat transcript if it
needs regenerating.

**Status**: the three real host-layer gaps found this round
(`child_process.spawn` options-dropping, the `pumpSpawnStream`/
`waitSpawnProcess` ordering race, and `Buffer.byteLength` missing) are
all fixed and verified against the real, unmodified pi install,
including a genuine end-to-end LLM tool call. Not yet confirmed:
whether the TUI's interactive rendering of a running/completed tool
call (the `renderCall`/`renderResult` methods on the same bash tool
definition, which use `@earendil-works/pi-tui`'s `Container`/`Text`
components) has any further gaps of its own - this round's verification
was all through `-p` print mode and direct dist-file reproduction, not
a live interactive TUI session with a real terminal. The user's own
terminal remains the authoritative test for that, same as round 65.

**Sixty-seventh round (2026-09-06, same day) — user confirmed the TUI now
works end to end in their own real terminal, asked to survey and start
closing missing-Node-API gaps.** Surveyed before touching anything, per
this whole investigation's own discipline: extracted every real
`node:`/bare-specifier `import`/`require` across the *entire* real,
unmodified pi dependency tree (144 packages, all of `dist/` +
`node_modules/`) and diffed it against noderati's own declared module
list, rather than guessing from Node's own module list what "should" be
missing.

**Headline finding: every top-level Node builtin module the real pi
tree directly imports is already declared in noderati** - `fs`, `path`,
`os`, `child_process`, `crypto`, `readline`, `url`, `module`,
`worker_threads`, `events`, `string_decoder`, `perf_hooks` account for
100% of the real import sites found (`fs`/`path`/`os` alone: 30/26/12
files). This reframes "implement missing Node APIs" from breadth (new
modules) to depth (make already-declared ones real) - the first time
this document's own Phase 5 speculation has been checked directly
against evidence rather than assumed, in contrast to Phase 4's stale
"no walk-up" claim two rounds ago.

**`net`/`tls`/`http`/`https`/`dgram`/`dns`/`zlib`/`cluster`/`repl`/
`domain`/`punycode`/`inspector`/`trace_events`/`async_hooks`/`timers`
are genuinely absent, and genuinely unreachable by anything currently
in use.** A second pass specifically inside `@aws-sdk`'s own bundled
tree found the one real exception: `@smithy/node-http-handler` (AWS
Bedrock's actual HTTP transport) imports `https` directly, not via
`fetch` - so this gap is real, but scoped precisely to `--provider
bedrock`, which nothing tested so far has exercised. Real undici (the
one remaining ledger-group-B fake, `undici@8.5.0` in the tree) also
imports `net`/`tls`/`dns` in its own internals - so de-shimming
`undici` and implementing `net`/`tls` turn out to be **the same
decision**, not two independent ones. Deferred, deliberately, not
forgotten: recorded here so the next round that touches either one
finds this note first.

**Two real fixes made this round, `worker_threads.Worker` and
`stream.pipeline()`, both chosen for the same reason: a lying no-op is
worse than an honest gap.** Both were unreachable by any real,
currently-exercised pi code path (confirmed, not assumed) - but a
silent no-op invites exactly the failure class this whole
investigation keeps finding: something that looks like it worked while
quietly doing nothing.

- **`worker_threads.Worker`**: investigated via pi's own real
  `dist/utils/image-resize.js` (the one real call site for `Worker` in
  the whole dependency tree - offloads Photon/WASM image resizing off
  the TUI's event loop, with a deliberate, documented in-process
  fallback if the worker can't be used). First checked whether real
  concurrent `worker_threads` is even buildable on paserati today,
  before writing any host code for it: two `driver.Paserati` instances
  running concurrently in separate goroutines race under `go test
  -race`, inside `Paserati.PreloadAllNativeModules`'s own module-loader
  path, before any user code even runs. That's a paserati engine
  capability gap (module loading isn't thread-safe across VM
  instances), not something this host layer can build around - noted
  here rather than filed as a bug, since it isn't a behavioral defect
  in single-VM semantics, just a missing capability.

  With real concurrency ruled out for now, traced what the *existing*
  fake actually did, expecting (per the fallback's own doc comment) a
  clean reject into `resizeImageInProcess`. Reproduced directly against
  the real `resizeImage()` first (a 4x4 PNG, no hang, fast `null`
  result) before touching any code - and found the true reason was
  simpler than the first theory: `buildWorkerConstructor` used
  `vm.NewNativeFunction`, which defaults `IsConstructor` to `false`, so
  `new Worker(...)` never reached this file's own code at all - it hit
  paserati's own generic `"Worker is not a constructor"` guard first.
  The postMessage/terminate no-ops were unreachable dead code; a first
  hypothesis (that `worker.once` being undefined, compounded by
  `worker.terminate().catch()` failing on a non-Promise return, was
  producing an accidental-but-correct fast reject) was written down,
  then disproven by testing `new Worker(...)` in isolation before
  trusting it - recorded here as a caught overclaim, not silently
  corrected. Fixed properly: switched to `vm.NewNativeConstructor` and
  throw one honest, specific `ERR_WORKER_NOT_SUPPORTED` error instead
  of leaving paserati's generic message live. Re-verified: `resizeImage()`
  still reaches the same fallback, same speed, for the actual real
  reason this time. `resizeImageInProcess` itself returns `null` here
  regardless (Photon needs real WASM - see below) - unrelated to this
  fix, not silently masked by it.

- **`stream.pipeline()`** (both `node:stream` and `node:stream/promises`)
  was a literal `async function pipeline(...) {}` - resolves
  immediately, pipes zero bytes, looks like success. Zero direct call
  sites anywhere in the real pi tree (checked, not assumed) - but the
  existing `Readable`/`Writable` shim classes already implement real
  `pipe()`, so a correct minimal implementation was cheap: chain
  `.pipe()` calls between consecutive streams, reject on the first
  `"error"` from any stream in the chain, resolve when the last one
  fires `"finish"`/`"end"`. `node:stream`'s own `pipeline` additionally
  supports Node's real callback form (`pipeline(s1, s2, cb)`), not just
  the promise-only form `stream/promises` exposes - both share one
  implementation via a cross-shim import (the same pattern `module.go`
  already established importing from `path`). Three new tests added
  (`stream_test.go`): real data transfer through both call shapes, and
  error propagation.

**Separately found, real, explicitly deferred (not attempted this
round):**

- **`WebAssembly` is completely absent** (`typeof WebAssembly` ===
  `"undefined"`) - confirmed directly, not assumed, while tracing why
  `resizeImageInProcess` (the fallback `Worker`'s fix now correctly
  reaches) still returns `null`: Photon's real image processing is
  WASM-backed (`@silvia-odwyer/photon-node`), and `loadPhoton()`
  gracefully returns `null` when it can't load - by design, per its own
  code, not a crash. This is the actual remaining blocker for real
  image resizing to work end to end, and it's a large, separate engine
  project (a WASM runtime, or shelling out to one), not a
  `worker_threads` question at all - recorded here precisely so it
  isn't conflated with the fix just made.
- **Native `.node` addon loading appears entirely unaddressed** - found
  in passing while scanning for dynamic `import()`/`require()` calls
  with non-literal specifiers (per a review note, since the earlier
  static-specifier scan alone could miss real usage): `@mariozechner/
  clipboard`, a real N-API native addon pi bundles for OS clipboard
  access, resolves its platform-specific binary dynamically. Whether
  noderati's `require()` can load and execute a compiled native
  addon at all is a real, open, unexplored question - not investigated
  further this round, flagged for whoever picks up clipboard support
  next.
- **The recurring stray `1` file, attributed to pi's own behavior in
  earlier rounds, checked properly this time rather than re-asserted**:
  reproduced with an explicit, correct `> out.txt 2>&1` redirect (no
  shell-quoting mistake on this session's own side) and the file still
  appeared, containing a real pi session-log JSONL record. Genuinely
  pi's own write, confirmed; the exact mechanism (why literally named
  `"1"`, rather than a real session-log path) is still unconfirmed and
  worth a closer look if it keeps surfacing.

**Verification.** `go build ./...`/`go vet ./...` clean; full test
suite (three new `stream` tests, one new `worker_threads` test) run 3x
with no flakes; `pi --version`/`--help`/`-p` (Fireworks) all still
succeed; scoreboard matches baseline.

**Status**: no new top-level Node module gaps found reachable by real
pi usage - the survey's own conclusion is now the tracked fact, not a
guess. Two real lying-no-op gaps closed (`Worker`, `stream.pipeline`).
Three real, larger gaps found and deliberately deferred with reasons
recorded: `net`/`tls`/`http`/`https` (Bedrock-only, tied to the
`undici` de-shimming decision), `WebAssembly` (blocks real image
resizing; a genuine engine-scale project), and native `.node` addon
loading (blocks clipboard; unexplored). Concurrent-VM thread-safety in
paserati confirmed absent via `-race`, noted as an engine capability
gap rather than filed as a behavioral bug.

**Sixty-eighth round (2026-09-06, same day) — user picked "finish Phase 5
depth" then "net/tls + de-shim undici" as priority; recon reframed both
halves of that choice before any code was written, then implemented real
`node:http`/`node:https`.** Asked advisor before committing to an
approach, twice, as the actual shape of the work kept changing under
direct investigation rather than assumption.

**Undici, reconsidered.** Grepped the real pi tree for `undici`'s only
consumer: `core/http-dispatcher.js`, which calls exactly
`undici.setGlobalDispatcher(new undici.EnvHttpProxyAgent({...}))` and
`undici.install()` - configuration calls, zero raw socket work. Real
undici's own internals do need `net`/`tls`/`dns`, but only if the real
package is actually loaded to run its own HTTP/1.1 client - which pi
itself never asks for. Replacing the shim with the real package would
mean running undici's JS HTTP client on top of new `net`/`tls`, strictly
worse than Go's own `net/http` for zero benefit pi can observe. **Not
de-shimmed, deliberately** - the right call per this round's own
evidence, not the ledger's usual delete-the-fake default; recorded here
so it doesn't read as an unclosed item.

**The actual gap undici's shim was covering for turned out to live in
paserati, not noderati.** `pkg/builtins/fetch_init.go:1229` builds a
fresh `http.Transport{ResponseHeaderTimeout: 30 * time.Second}` per
fetch call - no `Proxy` field (so `HTTP_PROXY`/`HTTPS_PROXY` are
silently ignored) and no way to reach the timeout from outside the
function (so pi's own `configureHttpDispatcher(timeoutMs)` has nowhere
to plug in). No exported setter or package-level variable exists to
configure this from a host. Filed as
[paserati#290](https://github.com/nooga/paserati/issues/290) rather than
patched, per the standing rule about the shared, actively-developed
paserati checkout - noderati's own shim staying an honest no-op is a
correct stand-in until that has a real home.

**Bedrock's actual blocker, verified precisely rather than assumed from
the plan text: `net`/`tls`/`http`/`https` genuinely absent, needed by
`@smithy/node-http-handler` (`import { Agent, request } from
"node:https"`), not by undici at all.** Read that file and its four
timeout/keep-alive helper siblings before writing anything (per advisor's
explicit steer) to scope exactly what `request.socket` needs to look
like: `.connecting`, `.on("connect")`, `.setTimeout(ms, cb)`,
`.setKeepAlive(on, ms)` - not a real socket.

**Deliberate architecture decision, labeled as such rather than left to
read as an incomplete socket implementation:** `node:http`/`node:https`
(new [`http.go`](../internal/host/http.go)/[`http_shim.go`](../internal/host/http_shim.go))
are built directly on Go's `net/http.Client`, not on raw `net`/`tls`
sockets. Real Node builds `http` on sockets it manages itself; nothing
reachable in the whole pi tree needs a raw `net.Socket` directly (survey
confirmed, not assumed), so building sockets first would be foundational
work with no consumer, and hand-rolling HTTP/1.1 framing on top of them
would just reinvent protocol-parsing bugs Go's stdlib already gets
right. `node:net`/`node:tls` stay unimplemented.

Implemented: `http.request`/`https.request`/`.get()`, a real `Agent`
(owns a real, shared `*http.Transport` per instance - genuine Go-level
keep-alive/connection pooling, not a per-request throwaway, keyed via
the same handle-registry pattern `child_process.go` already uses for
`*exec.Cmd`), `ClientRequest` (writable, real `write()`/`end()` via an
`io.Pipe` fed from a buffered channel so a synchronous native call never
blocks the VM's own single execution thread on an unbuffered pipe
write), and `IncomingMessage` (built on `newReadableStream` -
`setEncoding`/`destroy`/`pipe` come for free, the same fixes round 67
already made). Both this Transport and the Agent's each default to
`http.ProxyFromEnvironment` - since these are ours to build, there's no
reason to leave the same real-world proxy gap paserati#290 describes
for fetch unfixed here too.

**A real, encountered-not-hypothetical bug, caught by testing before
trusting the design:** the first version gave each `ClientRequest` a
bespoke `socket` object with a hand-rolled `.on("connect", cb)` that
only fired `cb` immediately if already connected, silently dropping the
listener otherwise. Against a real endpoint, this lost the event nearly
every time - listeners are normally registered *before* the connection
completes, which is the common case, not an edge one. Fixed by building
the socket on `newEventEmitterObject` like everything else in this
codebase, so `.on()`/`.emit()` genuinely queue and deliver regardless of
registration order.

**A second real bug, found on this same read-through rather than left
for someone else to hit:** the body-pipe goroutine every request starts
runs unconditionally, but a request-construction failure (e.g. an
invalid HTTP method) replaced `write()`/`end()` with no-ops that never
touch the channel feeding it - leaking that goroutine forever on every
malformed request. Fixed by closing the channel on that path too; a new
test (`TestHTTPRequestConstructionErrorEmitsError`) guards it.

**Verification**, in order: a real `https.request` against `example.com`
by hand first; then a fully deterministic local Go test server (exact
byte counts, multi-write streaming, POST bodies, a deliberately-refused
port for error handling) once a public test service's own flakiness
(a truncated response from `httpbin.org`, confirmed as *their*
variance and not a bug here by reproducing byte-exact against the local
server instead) made it clear a third-party endpoint wasn't a reliable
oracle; then, per advisor's explicit instruction, driving
`@smithy/node-http-handler`'s own real `NodeHttpHandler` class directly
- this is what actually distinguishes "the http shim works" from
"Bedrock's transport works," not a synthetic smoke test. Six new Go
tests (`http_test.go`) cover GET, POST-with-body, genuinely incremental
streaming (guarding the exact class of bug rounds 65/66 already found
elsewhere: a response that looks done before its data arrived), both
request-time and construction-time error paths, and the `"connect"`
event fix. `go build`/`go vet` clean; full suite (four new
`worker_threads`/`stream` tests from round 67 plus six new `http` tests)
run 3x with no flakes, and once more under `-race` given this file's new
concurrency - clean. `pi --version`/`--help`/`-p` (Fireworks) all still
succeed; scoreboard matches baseline.

**A separate, real blocker found and explicitly not chased further this
round**: driving `NodeHttpHandler.handle()` against the real
`@smithy/core/protocols` subpath import threw `ReferenceError: require
is not defined`. Traced far enough to rule out the first, wrong
hypothesis (a conditions-priority bug in noderati's own
`exportsCondition.candidates()`, `nodemodules.go` - checked directly:
its hardcoded `["node", "import", "default"]` ordering actually
happens to match what real Node's own key-order-dependent algorithm
would also pick for this specific package, since `"node"` appears
before `"import"` in `@smithy/core`'s own `package.json` - so this
isn't the bug, and saying so plainly here rather than leaving the wrong
theory standing). The real cause is more likely a CJS-file-reached-via-
ESM-import interop gap once the correct target file is selected, not
mis-selection of the file itself - not isolated further, flagged for
whoever picks up Bedrock next rather than guessed at.

**Status**: `net`/`tls`/`http`/`https` genuinely absent is closed for
the part that's reachable (`http`/`https`, real and verified); `net`/
`tls` stay unimplemented, deliberately, with no current consumer.
Undici's shim stays exactly as it was, now with a clear reason recorded
rather than an open question. The proxy/timeout gap in paserati's own
fetch is filed upstream, not patched. Bedrock has one more real,
distinct blocker beyond transport (the `@smithy/core/protocols`
resolution/interop failure above) before an actual end-to-end call
could be attempted - not yet reached, no AWS credentials available in
this environment to test past it regardless.

## Definition of done for this push

`pi --help`, `pi --version`, and a scripted single-turn `pi -p "..."` print-mode
run (mocked/no network, or with a real key if the user provides one) succeed
against the **real, unmodified** `@earendil-works/pi-coding-agent@0.80.2` npm
install, with `internal/host` containing zero package-specific shims or
per-filename source rewrites — only real Node builtin modules and real
resolution algorithms. Every remaining gap has either a real implementation or
a linked paserati issue, never a silent fake.

## Round 69: paserati#291 merged (fetch proxy/timeout), undici fake called out as debt

paserati#290 (filed round 68) got fixed and merged upstream as
[paserati#291](https://github.com/nooga/paserati/pull/291): `fetch()`'s
`http.Transport` now reads two package-level vars instead of hardcoding
them - `FetchProxy` (defaults to `http.ProxyFromEnvironment`) and
`FetchResponseHeaderTimeout` (defaults to the same 30s, now mutable).
Since `go.mod` has `replace github.com/nooga/paserati => ../paserati`,
this landed for noderati automatically, zero code changes - confirmed,
not assumed, with a real test (`t.Setenv("HTTP_PROXY", ...)` against an
`httptest` proxy server, fetching a nonexistent host that's only
reachable at all if actually routed through the proxy - passed). Full
suite re-run clean against the new commit. paserati explicitly did
*not* add a body/idle-timeout knob (undici's `bodyTimeout`/
`headersTimeout`) - the `#205` fix already made the response body
intentionally unbounded for real SSE streams, so there's nothing to
plug pi's idle-timeout setting into on paserati's fetch side; flagged
by paserati as a deliberate scope note, not an oversight.

Separately, a fair challenge surfaced this round: `internal/host/
undici.go` is a hand-rolled JS fake for a *third-party npm package*,
which is exactly the pattern this project has otherwise always closed
by making the *engine* capable of the real thing instead (jiti,
typebox, diff, minimatch, glob, proper-lockfile all went that way).
Checked why it's still a fake: real `undici` (a real dependency
already present in pi's own `node_modules`, 2.1MB/109 files) doesn't
use node's `http` module at all - it drives raw `net.connect`/
`tls.connect` sockets itself (`lib/core/connect.js`: `setNoDelay`,
`setKeepAlive`, TLS session-cache reuse, ALPN, `secureConnect`/
`session` events, real backpressure). Noderati has no `net`/`tls` at
all - `http.go` (round 68) deliberately stayed at the `net/http.Client`
level rather than exposing raw sockets, since that's all Bedrock's
actual call path needed. Real undici, once `net`/`tls` exist, replaces
`globalThis.fetch` entirely for anything that calls
`undici.install()` (which is exactly what pi's own
`configureHttpDispatcher()` does at startup) - meaning once that
lands, pi's idle-timeout setting would actually take effect for real,
via real undici's own client, independent of paserati's fetch() at
all.

Spun off as its own task rather than done inline here, given the size
(real socket-level Duplex streams + TLS handshake plumbing is a
different, harder thing than an HTTP round-trip): build real
`net.connect`/`tls.connect` on Go's `net.Dial`/`crypto/tls`, verify
against real `httptest` TCP/TLS servers, then delete `undici.go` and
its `installModules` registration and let real npm `undici` load and
run - mirroring exactly how every other fake here got retired.

## Round 70: real `node:net`/`node:tls` built and verified; real undici probed end-to-end and found genuinely blocked, precisely - not deleted

Picked up round 69's spun-off task. Asked advisor before writing any Go,
per its own steer to probe real undici's hard blocking constraints
*first* rather than assume net/tls alone would unblock it - that probe
turned out to be the actual deciding factor for this round's scope.

**Built** [`net.go`](../internal/host/net.go)/[`tls.go`](../internal/host/tls.go):
real `net.connect`/`net.createConnection`/`net.Socket` on Go's own
`net.Dial`, and real `tls.connect`/`tls.Socket` on `crypto/tls.Client`,
sharing one `socketState` core (writer/reader goroutines, backpressure,
pause/resume) between both - a `*tls.Conn` satisfies `net.Conn` exactly
like a `*net.TCPConn`, so the byte-pump plumbing underneath doesn't care
which one it's holding. Backpressure is real: `write()` enqueues onto an
unbounded Go-side queue (matching Node's own semantics - Node's `write()`
doesn't block the event loop either), a dedicated goroutine drains it
onto the actual conn, and `write()` returns `false` once queued bytes
exceed `highWaterMark`, with `'drain'` firing for real once the queue
empties - not on a timer. `pause()`/`resume()` gate the reader goroutine
itself (not just event delivery), so a paused socket genuinely stops
pulling bytes off the OS socket. TLS adds SNI (`ServerName`), ALPN
negotiation (`ALPNProtocols`/`alpnProtocol`), and a shared
`tls.ClientSessionCache` for real session resumption - deliberately
*not* wired to a JS-visible `'session'` event (undici's own connector
listens for one to populate its own cache); synthesizing a fake session
object to satisfy that listener would be exactly the lying-no-op this
project rejects elsewhere, so that event just stays unfired and Go's own
resumption happens invisibly to JS instead. `net.createServer` stays
deliberately unbuilt - round 69's own survey found nothing reachable
needing one, and every test here uses Go as the server-side oracle
anyway.

**A real, encountered-not-hypothetical bug, found while writing this
against undici's own `lib/core/connect.js` (read directly, not
assumed):** [`emitter.go`](../internal/host/emitter.go)'s `emitOnObject`
called every listener with `vm.Undefined` as `this`. Undici's connector
does `.once('connect', function () { cb(null, this) })` and relies on
`this` being the socket itself - with `undefined`, undici would hand
back `undefined` as its own socket and every downstream dispatch would
fail. Fixed for every `EventEmitter` in the codebase (the receiver should
be the emitter universally, not just for sockets), including the
`once()` wrapper which had the same bug independently; a new test
(`TestNetConnectThisIsSocket`) guards it directly, and the full suite
was re-run clean after - nothing in this codebase's existing tests
depended on the old, wrong behavior.

**Verification**: `net_test.go`/`tls_test.go` against real local Go
TCP/TLS servers (a real `tls.Listen` with a freshly-generated self-signed
cert, `rejectUnauthorized: false` on the client side, exactly like a real
Node test against a self-signed endpoint) - byte-exact echo, a
multi-write streaming test, a real cert-rejection test (default
`rejectUnauthorized`, a genuine `crypto/tls` x509 verification failure,
not skipped), a real connection-refused error test, and two tests that
turn the stated backpressure/pause requirements into actual assertions:
`TestNetSocketBackpressure` (4MB over a deliberately slow reader, `write()`
must return `false` at least once *and* `'drain'` must actually fire
afterward) and `TestNetSocketPauseResumeStopsReads` (an 8MB push from the
server, `pause()` must keep the client from having pulled more than a
small fraction of it before `resume()`). Full suite (including the
pre-existing `TestNodeMissingResolver`/`TestMissingNodeBuiltinNamedError`,
retargeted from `node:net` to `node:dgram` now that `node:net` is real)
run 3x clean, plus once under `-race` given this file's concurrency -
clean; `go vet` clean.

**Then the actual, load-bearing part of this round: probing real,
unmodified npm `undici` end-to-end before touching `undici.go` at all**,
per advisor's explicit instruction not to assume net/tls alone would be
enough. Built a throwaway CLI (`go build ./cmd/noderati`) and drove real
undici (the exact copy already vendored under
`@earendil-works/pi-coding-agent/node_modules/undici`) via the same call
pattern pi's own `core/http-dispatcher.js` uses
(`new undici.EnvHttpProxyAgent({allowH2:false,...})`,
`setGlobalDispatcher`, `install()`, then a real `fetch()` against a local
Go test server), with `declareUndici()` temporarily commented out so
resolution could reach the real package - never committed in that state;
restored immediately after each probe, full suite re-confirmed clean
against the restored (shimmed) state afterward. This surfaced, in order,
every real thing actually blocking real undici from loading at all -
each fixed if it was noderati's own gap, filed if it was paserati's:

1. **`require("node:net")`/`require("node:tls")` (and, found the same
   way, `require("node:http")`/`require("node:https")` - missed when
   round 68 added them) threw "Cannot find module"**, even though
   `import`ing the same names worked fine - `cjs.go`'s own
   `nativeRequireNames` list (a second, hand-maintained registry the
   file's own doc comment already warns about) hadn't been updated. Real
   undici is loaded via `require()` internally (`index.js` is plain
   CJS), so this was a real, load-bearing gap, not a hypothetical one.
   Fixed: all four names added to `nativeRequireNames`.
2. **`node:events`'s CJS `require()` result was a wrapper object
   (`{ EventEmitter }`), not the class itself**, because the shim's
   `export default` was `{ EventEmitter }` and `cjs.go`'s
   `requireNative` hands the default export straight back as the
   `require()` result. Real Node's `require("events")` returns the
   `EventEmitter` class directly. Undici's `dispatcher.js` does
   `const EventEmitter = require('node:events'); class Dispatcher extends
   EventEmitter` - extending a non-constructor object throws "Class
   extends value object is not a constructor or null". Fixed
   ([`events.go`](../internal/host/events.go)): default export is now
   `EventEmitter` itself, with `EventEmitter.EventEmitter = EventEmitter`
   (a self-reference real Node also has), matching real Node exactly;
   nothing else in this codebase depended on the old shape (checked).
3. **`util.debuglog` was missing entirely** - undici's
   `lib/core/diagnostics.js` calls it unconditionally at module load.
   Added ([`util.go`](../internal/host/util.go)) as a genuinely-gated
   no-op: it actually checks `NODE_DEBUG` (case-insensitively) and only
   prints when the section name matches, matching real Node's behavior
   rather than accepting-and-ignoring the argument.
4. **A real paserati parser bug, isolated to a minimal, clean repro and
   filed as [paserati#292](https://github.com/nooga/paserati/issues/292):**
   ASI fails after a class field with a *computed* key
   (`[k1] = (x) => {...}`, no trailing semicolon) when the *next* class
   member also has a computed key - confirmed the same shape with a
   non-computed key parses fine, and an explicit semicolon fixes it
   immediately. This is what actually blocks `require("undici")` from
   completing *at all* right now (`lib/dispatcher/pool-base.js`, a
   non-optional dependency of `pool.js` reached from `index.js`'s own
   top-level requires, hits exactly this shape). Bisection: truncating
   `pool-base.js` member-by-member (with its requires stubbed) narrowed
   the failure to the `[kOnConnectionError] = (...) => {...}` field
   immediately followed by `get [kBusy] () {...}`; reducing further to a
   14-line standalone snippet confirmed it has nothing to do with
   getters specifically (a plain computed method reproduces it too) and
   nothing to do with undici's own code. Not fixed here (paserati is a
   separate, actively-developed checkout per this project's standing
   rule) - only worked around in a scratch copy, never in the real
   installed package, to keep probing further.
5. **`AggregateError` is missing entirely** (`typeof AggregateError ===
   "undefined"`) - confirmed `FinalizationRegistry`/`WeakRef`/
   `queueMicrotask` are all present, only this one global is gone. Real
   undici's connector (`lib/core/connect.js`) checks
   `err instanceof AggregateError` to normalize `net.connect`'s
   `autoSelectFamily` errors. Filed as
   [paserati#293](https://github.com/nooga/paserati/issues/293) rather
   than patched, same reason as above.
6. **`node:async_hooks` is missing entirely** (a whole different
   subsystem - `AsyncLocalStorage` and friends - not something in scope
   to build for this task) - reached once the ASI bug above was
   worked around in a scratch copy, confirming there's at least one more
   real, load-bearing gap beyond it. Not investigated further; noted
   here rather than left undiscovered for whoever picks this back up.
7. **Still not reached**: real undici's own llhttp HTTP/1.1 parser is
   compiled to WebAssembly (`lib/llhttp/llhttp-wasm.js`, instantiated
   lazily in `client-h1.js`'s `lazyllhttp()` - confirmed by reading the
   file directly, `/* global WebAssembly */` and a literal
   `new WebAssembly.Module(...)` call). `typeof WebAssembly ===
   "undefined"` in paserati today, already recorded in round 67's survey
   as "a genuine engine-scale project" - this round adds a second,
   concrete real-world consumer (real undici's own parser, not just
   image-resizing) to that same already-known gap, rather than
   discovering a new one.

**Status, stated precisely rather than left to read as
"almost done": real undici (unmodified, as vendored in pi's own
`node_modules`) does not load at all today** - blocked first by
paserati#292 (item 4), with `AggregateError` (item 5), `node:async_hooks`
(item 6), and `WebAssembly` (item 7) each waiting behind it, confirmed in
that order by direct probing, not assumed. **`undici.go`'s shim was
therefore *not* deleted and `declareUndici()` was *not* removed from
`installModules`** - doing either now would be a straight regression
(pi's `http-dispatcher.js` calls `undici.install()` unconditionally at
startup), not a de-shim; the task's own definition of done for this push
is explicitly conditioned on real undici actually completing a fetch
end-to-end, which it demonstrably cannot yet. Stated plainly rather than
left to read as a smaller claim: **`net.go`/`tls.go` have no consumer in
this codebase today** - round 68's own note that building sockets first
would be "foundational work with no consumer" is now literally the
state, since the one real consumer this work targeted (undici) can't
load at all yet. They're real, complete (for the http/1.1-only,
no-Unix-socket, no-server surface this task scoped in) and verified
directly against real local Go TCP/TLS servers on their own merits - but
until paserati#292 (and the gaps behind it) close, they're real code
sitting unused, not yet load-bearing for anything pi actually runs. The
four fixes above (nativeRequireNames, events.go, util.go's `debuglog`)
are the exception - real, standalone improvements independent of whether
undici ever gets de-shimmed, and stay regardless. Re-ran the full,
real, credentialed smoke test after every change in this round: `pi
--version`/`--help` and both `pi --provider fireworks --model
accounts/fireworks/models/glm-5p2 --no-session -p "..."` (a plain reply)
and the same with a real tool call (`ls`, real tool-call IDs, correct
final answer) all still succeed, unregressed by the `net`/`tls`
additions, the `emitOnObject` `this` fix, or any of the four probe-driven
fixes above.

## Round 71: paserati#292 fixed upstream; #293 was a stale pin, not a real bug - corrected and closed

[paserati#294](https://github.com/nooga/paserati/pull/294) landed
upstream same-day, fixing #292 for real (`parseInfixContinuation` now
correctly refuses to treat `[`/`.`/`(`/tagged-templates as continuing an
unparenthesized `ArrowFunctionLiteral`, plus a second, previously-masked
bug in `parseComputedProperty` it uncovered along the way) and
investigating #293. The PR's own finding on #293, read directly rather
than assumed: **not reproducible on current `main`** -
`AggregateError` had already been fully implemented before this task
even started probing, and round 70's `typeof AggregateError ===
"undefined"` result was against a stale pin of the sibling `paserati`
checkout, not a real engine gap. (The PR author's own investigation
found a real, adjacent bug instead - `vm.IterableToArray` panicking on
any non-plain-object iterable, e.g. `new AggregateError(someGenerator)`
or `Promise.all(someGenerator)` - and fixed that plus two masked
`Promise.all`/`allSettled`/`any`/`race` spec gaps in the same PR.)

Pulled `../paserati` to `origin/main` (`0ec3c9a0`, fast-forward, no
divergence to reconcile) and rebuilt noderati against it. Verified both
fixes directly rather than trusting the PR's own description alone:
`typeof AggregateError` is now `"function"` with real
`.errors`/`instanceof Error` behavior, and round 70's own minimal
14-line ASI repro (`[k1] = (x) => {...}` immediately followed by another
computed-key member, no semicolon) now parses cleanly. Full noderati
suite re-run 3x plus once under `-race` against the updated engine -
still clean; `go vet` clean; the real, credentialed `pi --version`/
`--help`/`-p` (plain reply and a real tool call) smoke tests still
succeed, unregressed by the engine update.

**Re-ran round 70's own undici probe against the truly unmodified real
npm package** (re-copied fresh from `pi-coding-agent`'s `node_modules`,
not the scratch copy with the semicolon workaround from before) with
`declareUndici()` again temporarily disabled, restored immediately
after: `require("undici")`'s chain now gets past both `pool-base.js`'s
ASI shape and `AggregateError` cleanly, confirming both fixes for real
rather than by reading the PR description. **The next real blocker is
exactly where round 70 said it would be**: `require("node:async_hooks")`
- genuinely missing from noderati, a whole different subsystem, still
out of scope for this task. `node:async_hooks`, then `WebAssembly`
(item 7 from round 70, still unreached) remain; `undici.go`'s shim stays
exactly as-is for the same reason as before - real undici still can't
complete a load, let alone a fetch.

Commented on and closed
[paserati#293](https://github.com/nooga/paserati/issues/293) with this
confirmation, matching the maintainer's own diagnosis rather than
leaving a stale-pin false-positive open against the real project.

## Round 72: node:async_hooks, util.types/promisify, node:console, node:timers, node:dns built - real undici's require chain now reaches node:zlib

User picked up round 71's own next-blocker item directly ("let's do the
async hooks then"). Grepped every real async_hooks call site in the
vendored undici before writing anything, per this round's own recurring
discipline: `class X extends AsyncResource { constructor() { super('Y') }
}` then `this.runInAsyncScope(fn, thisArg, ...args)`, in exactly five
files (api-request/pipeline/upgrade/connect/stream.js) - nothing else.

**Built** [`async_hooks.go`](../internal/host/async_hooks.go): a real
`AsyncResource` (pure JS shim, no Go natives needed - `runInAsyncScope`
genuinely is `fn.apply(thisArg, args)` for a host with no async_hooks
instrumentation behind it, not a stand-in for missing behavior).
**Deliberately did not export `AsyncLocalStorage`**: its one real
consumer in this whole dependency tree, `@aws/lambda-invoke-store`'s
`InvokeStoreMulti.create()` (used by `@aws-sdk/core` on Bedrock's
request-middleware path), is only reached when
`AWS_LAMBDA_MAX_CONCURRENCY` is set or a caller forces multi-instance
mode - checked directly in the real source, neither is ever true for pi
running as a CLI tool, which always takes the sibling
`InvokeStoreSingle` path instead. A real `AsyncLocalStorage.run(store,
fn)` needs `store` to survive across an `await` inside `fn` - Node does
this by hooking every promise continuation at the engine level; a naive
JS-level stack that pops in a `finally` block pops the instant `fn`'s
pending promise is returned, not when `fn` actually finishes, so
`getStore()` would silently return the wrong thing after the first
`await` - exactly the shape `InvokeStoreMulti.run()` is actually called
with. Shipping that behind the real name would be a lying no-op wearing
the right API shape, and since nothing reachable needs it, there's
nothing to build. (Advisor flagged this distinction explicitly before
any code was written - the naive stack version was the wrong direction
this round almost took.)

**Then re-ran round 71's undici probe and kept fixing whatever it hit
next**, the same real-package, `declareUndici()`-disabled-then-restored
methodology as every round since 69:

- **`util.types`/`util.promisify` missing** - `lib/mock/mock-utils.js`
  does `const { types: { isPromise } } = require('node:util')`, a
  *nested* destructure that throws "Cannot destructure 'undefined'" the
  instant `types` itself is absent (not just a silently-undefined
  `isPromise`). Fixed in [`util.go`](../internal/host/util.go)'s new
  `installUtilNatives`: `types.isPromise`/`isProxy`/`isArrayBuffer`/
  `isSharedArrayBuffer`/`isAnyArrayBuffer`/`isDataView`/`isTypedArray`/
  `isArrayBufferView` - only the confirmed-reachable subset (grepped
  across undici and pi-coding-agent's own dist, not Node's full ~30-name
  list), each mapping onto one of paserati's own dedicated `ValueType`
  tags (`TypePromise`/`TypeProxy`/`TypeArrayBuffer`/.../`TypeDataView`) -
  real, exact engine-level checks, not a heuristic
  `Object.prototype.toString` guess. `promisify` is a real
  implementation too (built on `vmInst.NewPromiseFromExecutor`, the same
  primitive backing real `new Promise()`), not a stub - cheap to get
  right and a genuinely useful, common API, even though its only real
  call site here (`mock-client.js`'s `close()`) is mock-only and never
  reached by an actual fetch.
- **`node:console`'s `Console` class missing** -
  `lib/mock/pending-interceptors-formatter.js` does `new Console({
  stdout: someTransform, ... })` to pretty-print `MockAgent`'s pending-
  interceptor list (a debugging convenience, never reached by a real
  fetch). paserati already has a real global `console` singleton
  (confirmed directly - `pkg/builtins/console_init.go`), just no
  constructible class alongside it. Built as a pure JS shim
  ([`console.go`](../internal/host/console.go)): formats and writes to
  whichever stream it's given, falling back to the real global console
  when none was - genuine behavior, just a small surface, since nothing
  reachable calls its methods yet.
- **`node:timers` missing** - `lib/mock/snapshot-recorder.js` does
  `const { setTimeout, clearTimeout } = require('node:timers')`.
  Re-exports the same globals every other real timer call in this
  codebase already uses ([`timers.go`](../internal/host/timers.go));
  does *not* export `setInterval`/`setImmediate` under Node's real
  names, because paserati has neither as a global at all (checked
  directly - a separate, pre-existing, unrelated gap, not something to
  paper over here).
- **`node:dns` missing** - `lib/interceptor/dns.js` (required
  unconditionally at `index.js`'s own top level, as one of undici's
  built-in interceptors) does `const { lookup } = require('node:dns')`,
  called as `lookup(hostname, { all: true, family, order: 'ipv4first'
  }, (err, addresses) => {...})`. Built for real
  ([`dns.go`](../internal/host/dns.go)) on Go's own
  `net.DefaultResolver.LookupIPAddr` - genuine DNS resolution, not a
  synthetic answer, dispatched off the VM thread via the same
  `BeginExternalOp`/`ScheduleNextTick` pattern every other async host
  call in this codebase uses. Real Node's stdlib made this exactly as
  cheap to build correctly as it would have been to fake, so it's real.

**Verification**: nine new Go tests
(`async_hooks_test.go`/`console_test.go`/`util_test.go`/`dns_test.go`)
drive the exact real call shapes found above - `AsyncResource`
subclassed with `runInAsyncScope` forwarding args/`this`/thrown errors,
a `Console` bound to a fake stream actually receiving written output,
`util.types` predicates against real typed arrays/`ArrayBuffer`/
`DataView`/`Promise` values, `util.promisify` both resolving and
rejecting a real Node-style callback function, and `dns.lookup` in both
its `all:true` and single-result shapes plus a real resolution-failure
path (an `.invalid`-TLD hostname, RFC 2606-reserved to never resolve).
Full suite re-run 3x plus once under `-race`, `go vet` clean; the real,
credentialed `pi --version`/`--help`/`-p` (plain reply and a real tool
call) smoke tests still succeed, unregressed by any of the five new
modules.

**Status**: real undici's `require()` chain now clears async_hooks,
util's nested-destructure gap, console, timers, and dns cleanly -
confirmed by re-running the same real-package probe after each fix, not
assumed from the fix alone. The next (and, as of this round, current)
blocker is **`node:zlib`**: `lib/interceptor/decompress.js` (also
required unconditionally at `index.js`'s top level) does
`const { createInflate, createGunzip, createBrotliDecompress,
createZstdDecompress } = require('node:zlib')`. This is a materially
bigger ask than anything this round built - `createInflate`/
`createGunzip` map cleanly onto Go's own `compress/flate`/`compress/gzip`,
but `createBrotliDecompress`/`createZstdDecompress` have no Go stdlib
equivalent at all and would need a third-party pure-Go dependency (e.g.
`github.com/andybalholm/brotli`) added to `go.mod` - a real scope/
dependency decision, not a same-shape "grep the call site, build the
Go native" round like this one, so it's flagged here rather than
started without checking in first. `undici.go`'s shim stays exactly as
before - real undici still can't complete a `require()`, let alone a
fetch.

## Round 73: zlib (gzip/inflate real, brotli/zstd honestly refused), util/types, File, MessagePort, Event/EventTarget/CustomEvent, stream.Transform built - two real paserati bugs found and filed; require chain now blocked on one of them

User's call on round 72's zlib question: "gzip/inflate for real, throw for
brotli/zstd for now." Built exactly that, then kept re-running the same
real-undici probe from round 72 and fixing whatever it hit next, the
same grep-first methodology as every round since 69 - six more real
gaps closed this round, two genuine paserati bugs found and filed
(rather than worked around silently), and the require chain now
reaches a blocker this project can't fix itself.

**[`zlib.go`](../internal/host/zlib.go)**: `createGunzip`/`createInflate`/
`createInflateRaw` are real, incremental decompression Transform
streams on Go's own `compress/gzip`/`compress/zlib`/`compress/flate` -
write()/end() feed an `io.Pipe` via the same buffered-channel-plus-
feeder-goroutine discipline `http.go`'s request bodies already use, a
second goroutine reads decompressed bytes back out and emits real
`'data'`/`'end'`/`'error'` events incrementally, never buffering a whole
response first. `createBrotliDecompress`/`createZstdDecompress` throw a
clear, real error naming exactly why (no Go stdlib decoder, and faking
decompression would silently corrupt a real response body) rather than
existing as a lying no-op. `createGzip`/`createDeflate`/`createDeflateRaw`
(the compression direction) aren't built at all - grepped every real
call site across undici and pi-coding-agent's own dist and found none,
so nothing was built against a guess.

**A real, race-detector-caught bug, found writing zlib.go's own tests,
not hypothetical:** `errorValueFromGo` (which calls `vmInst.Construct`
on the `Error` constructor, mutating shared VM fields like
`currentThis`/`inConstructorCall`) was being called *eagerly* on a
background goroutine in several places - `scheduleEmit(vmInst, obj,
"error", errorValueFromGo(vmInst, err))` evaluates `errorValueFromGo`
before `scheduleEmit` ever runs, on whichever goroutine made the call,
not deferred onto the VM's own tick the way it looks. `go test -race`
caught it in `zlib_test.go`'s corrupt-data test; auditing every
`errorValueFromGo` call site in the codebase found the same bug already
present in `net.go`'s `writerLoop`/`readerLoop` (present since round 70,
just never triggered by `-race` before now) - not something new this
round's own code introduced in isolation. Fixed by adding
`scheduleErrorEmit` (net.go) - constructs the Error value only inside
the `ScheduleNextTick` closure that actually runs on the VM thread - and
using it at every affected site. `go test -race` clean across 3 runs
after the fix, where it wasn't reliably clean before.

**`util.go`/[`util_types.go`](../internal/host/util_types.go)**: real
undici's `lib/mock/mock-utils.js` does
`const { types: { isPromise } } = require('node:util')` (fixed in round
72), but `lib/web/websocket/websocket.js` and `lib/web/fetch/util.js`/
`body.js` separately do `require('node:util/types')` as its *own*
distinct module, not `util`'s `.types` property - added `isUint8Array`
(checked via paserati's own `TypedArrayObject.GetElementType() ==
TypedArrayUint8`, not just "is a typed array") alongside the existing
predicates, and a real `util/types` module reusing the exact same
predicate functions (exposed via `globalThis.__noderatiUtilTypes`)
rather than a second copy of the same logic.

**[`file_global.go`](../internal/host/file_global.go)**: real undici's
`lib/web/webidl/index.js` does `webidl.is.File =
webidl.util.MakeTypeAssertion(File)` at its own module top level - a
missing `File` throws immediately at require() time, unconditionally.
paserati already has real `Blob`/`FormData`/`Headers`/`Request`/
`Response`/`fetch` globals, just no `File` alongside them - and a real
File genuinely just is a Blob with `name`/`lastModified` added (the
WHATWG spec defines it exactly that way), so this builds a real Blob
subclass: `vmInst.Construct(blobCtor, ...)` builds a genuine Blob
instance, then re-parents it onto File's own prototype (itself pointing
at Blob's real prototype) - `.slice()`/`.arrayBuffer()`/`.text()`/
`.stream()` all come from the real Blob implementation for free, not a
second copy.

**A second real, filed paserati bug, found building `file_global.go`,
not assumed:** the first version installed `File` by running a small
JS snippet (`globalThis.File = class File extends globalThis.Blob {
...}`) through `p.RunCode`/`p.EvalCode` from inside `New()`, before the
real user script ever runs. Bisected a previously-passing test
(`runInAsyncScope` propagating a thrown `Error`) that started silently
failing only once this ran - down to *any* prior `RunCode`/`EvalCode`
call on the same `*driver.Paserati` instance, reproduced even with a
prior script as trivial as `1+1`, regardless of `RunCode` vs `EvalCode`
vs `Script`-mode vs module-mode. Filed as
[paserati#298](https://github.com/nooga/paserati/issues/298) with a
minimal Go-level repro rather than silently worked around. The actual
fix: build `File` as a real native Go constructor (`vm.NewConstructorWithProps`
+ direct `vm.Value` manipulation) instead of evaluating JS source at
all - the same discipline every other global this codebase installs at
construction time (`Buffer`, `util.types`/`promisify`, ...) already
followed, for reasons that are now very concretely justified rather
than just stylistic.

**[`message_port_global.go`](../internal/host/message_port_global.go)**:
same shape as `File` - `webidl.is.MessagePort =
webidl.util.MakeTypeAssertion(MessagePort)`, confirmed every other real
`MessagePort` reference is confined to `lib/web/websocket/events.js`'s
own WebIDL converter setup (WebSocket-specific, never touched by a
plain `fetch()`), so a minimal but real `EventEmitter`-shaped
constructible class (`postMessage`/`start` honest no-ops - there's no
paired port for a message to actually flow to; `close()` is real, it
actually emits `'close'`) is the correctly-scoped build here, not a
placeholder and not a full `MessageChannel`.

**[`event_global.go`](../internal/host/event_global.go)**: real undici's
`lib/web/websocket/events.js` does `class MessageEvent extends Event` /
`class CloseEvent extends Event` at module top level - confirmed every
real `Event`/`EventTarget` construction/subclass site across the
vendored package is confined to WebSocket/EventSource support, same
never-reached-by-plain-fetch shape as `File`/`MessagePort`. Built real,
spec-shaped `Event` (bubbles/cancelable/composed/defaultPrevented/
target/currentTarget/timeStamp state, real `preventDefault`/
`stopPropagation`/`stopImmediatePropagation`/`composedPath`),
`CustomEvent` (adds `.detail`), and `EventTarget` -
`addEventListener`/`removeEventListener`/`dispatchEvent` built directly
on `emitter.go`'s own real `newEventEmitterObject`/`addListener`/
`removeListener`/`emitOnObject`, not a second implementation of the same
listener bookkeeping. All three are native Go constructors with a real
`.prototype` object (needed for `class X extends Event` to resolve at
all - found the hard way, the first version had no `"prototype"`
property and threw "Class extends value does not have valid prototype
property" the instant something tried to subclass it).

**`stream.go`**: real undici's `lib/web/eventsource/eventsource-stream.js`
does `class EventSourceStream extends Transform` - `node:stream`'s shim
only exported `Readable`/`Writable`/`pipeline` before this, no
`Transform` at all. Added a real Transform (a Writable+Readable joined
by a per-chunk `_transform(chunk, encoding, callback)` step a subclass
overrides to push output, plus `_flush`) as plain JS in the existing
shim - safe to extend this way (unlike the Go-native globals above)
since JS shims run inside the *same* single script execution as
everything else, not a second top-level run, so paserati#298 doesn't
apply here.

**A third real, filed paserati bug - and the one currently blocking
this chain:** past all of the above, `require()`-ing real undici's
`lib/web/fetch/response.js` or `request.js` (or `lib/web/websocket/websocket.js`)
throws `TypeError: Reflect.deleteProperty called on non-object` - both
files do `Reflect.deleteProperty(Response, 'getResponseHeaders')` etc.
at their own module top level, to remove internal helper functions they
temporarily attach to their own exported classes for cross-module
access. Bisected to a genuinely minimal repro with nothing undici-
specific left in it: `Reflect.deleteProperty` throws on *any* function
or class target (`function F(){}; F.x=1; Reflect.deleteProperty(F,
'x')` throws the identical error), while the same call on a plain
object works correctly - a real bug in `Reflect.deleteProperty`'s own
target-type check, not anything about undici's specific classes. Filed
as [paserati#297](https://github.com/nooga/paserati/issues/297).

**Verification**: 6 new Go test files
(`zlib_test.go`/`util_test.go`/`file_global_test.go`/
`message_port_global_test.go`/`event_global_test.go`, plus additions to
`stream_test.go`) drive the exact real call shapes found above - real
gzip/zlib/raw-deflate decompression against Go's own stdlib compressor
as the oracle (byte-exact, incremental multi-write, and a real corrupt-
data error path), `util/types` predicates against real typed values,
`File`/`MessagePort`/`Event`/`EventTarget`/`CustomEvent` each
subclassed and exercised through their real methods (not just
constructed), and a `Transform` subclass whose `_transform` override
actually drives `write()`'s output plus `.pipe()`-forwarding to a
downstream `Writable`. Full suite re-run 3x, `go vet` clean, and `-race`
run 3x clean *after* the eager-`errorValueFromGo` fix (not clean
before it, on the same code otherwise - a real regression this round's
own testing caught, not something carried in from round 70 undetected
until now). The real, credentialed `pi --version`/`--help`/`-p` (plain
reply and a real tool call) smoke tests still succeed, unregressed by
any of this round's five new global-installing files or the
`stream.go`/`net.go` changes.

**Status**: the require chain now clears `node:zlib`,
`node:util/types`, `File`, `MessagePort`, `Event`/`EventTarget`/
`CustomEvent`, and `stream.Transform` - confirmed by re-running the same
real-package probe after each fix. It's now blocked on paserati#297
(`Reflect.deleteProperty` on a function target), hit inside three of
real undici's own core files (`fetch/response.js`, `fetch/request.js`,
`websocket/websocket.js`) at their own module top level - not fixable
from noderati's side, filed upstream instead of worked around.
`undici.go`'s shim stays exactly as before; `net.go`/`tls.go` remain
real, verified, and still without a load-bearing consumer until this
(and whatever comes after it) closes.

## Round 74: paserati#297/#298 fixed upstream and merged same-day; real undici's require() now fully completes and real fetch() actually runs - blocked on one newly-found, precisely-isolated engine bug (filed as #302) inside real Request construction itself

User asked "299 is merged in paserati, does it help?" - PR #299 fixed
both #297 (`Reflect.deleteProperty` on a function/class target) and
#298 (the leaked top-level script frame that corrupted later exception
propagation) in one PR, plus an unrelated closure-upvalue-on-unwind bug
found along the way. Pulled `../paserati` (already at the merge commit,
`ea1f6d84`), rebuilt, full suite re-run 3x clean against the updated
engine, real credentialed `pi --version`/`--help`/`-p` still succeeded
before continuing (a plain-reply Fireworks call and a real tool call,
both unregressed) - then re-ran the same real-undici probe from round
73, fixing each new blocker exactly as it surfaced, same as every round
since 69:

- **`crypto.getHashes()` missing** - real undici's own
  `lib/web/subresource-integrity/subresource-integrity.js` calls it
  unconditionally at module load to check SRI support. Added
  ([`crypto.go`](../internal/host/crypto.go)) returning exactly (not
  more, not fewer than) the five algorithms `createHash` actually
  implements - a real, accurate answer, not Node's much larger OpenSSL-
  backed list restated as a lie.
- **`node:worker_threads`' `markAsUncloneable` missing** - real undici's
  `webidl/index.js` wires it in, then `CacheStorage`/`Cache`/`Request`/
  `Response` constructors all call it on themselves. Built for real
  ([`worker_threads.go`](../internal/host/worker_threads.go)): marks the
  object with a hidden own-property that
  [`structuredclone.go`](../internal/host/structuredclone.go)'s own
  `structuredCloneValue` now checks - a later `structuredClone()` on a
  marked object genuinely throws `DataCloneError`, not a no-op that
  merely avoids crashing on the call.
- **`Buffer.allocUnsafe` missing** - real undici's
  `websocket/constants.js` calls `Buffer.allocUnsafe(0)` at module load.
  Aliased to the same zero-filled path `Buffer.alloc` already uses - a
  legitimate implementation of "unspecified" memory (zero is one valid
  choice), not a shortcut around the real contract.
- **`DOMException` global missing** -
  [`dom_exception_global.go`](../internal/host/dom_exception_global.go):
  real undici throws `new DOMException(message, name)` directly in
  three files, and `websocket/stream/websocketerror.js` does
  `class Test extends DOMException { get reason() {...} }` at its own
  module top level (a real Node bug workaround check,
  nodejs/node#59677) before ever building a real `WebSocketError`. Built
  with the real 25-entry legacy numeric `.code` table the WHATWG spec
  still defines, and confirmed the getter-override subclassing case
  works correctly (`new Test().reason !== undefined`), not just that the
  class is constructible.
- **`Promise.withResolvers()` missing** - real undici's own
  `lib/web/fetch/index.js` does `let p = Promise.withResolvers()` at the
  very top of its exported `fetch()`, so every real fetch() call hit
  this immediately. Added
  ([`promise_with_resolvers.go`](../internal/host/promise_with_resolvers.go))
  as a real static method on the actual `Promise` constructor (built on
  `vmInst.NewPromiseFromExecutor`, the same primitive backing a real
  `new Promise()`) - not a JS-eval'd polyfill, for the same paserati#298
  reason `file_global.go` already wasn't.
- **`node:events`' `getMaxListeners`/`setMaxListeners`/
  `defaultMaxListeners` missing** - found incidentally: real undici's
  `request.js` calls `getMaxListeners(new AbortController().signal)` at
  module load, guarded by its own `try/catch` (so not itself blocking),
  but a real, separate gap worth closing anyway. Added
  ([`events.go`](../internal/host/events.go)) working generically on
  *any* object with a `_maxListeners` slot (a real `AbortSignal` is
  exactly such an object, confirmed directly, not an instance of this
  module's own `EventEmitter` class) - attached as static properties on
  the `EventEmitter` function itself, not a separate wrapper default
  export, so the existing `const EventEmitter = require("node:events")`
  bare-default shape (round 69) and the new
  `const { getMaxListeners } = require("node:events")` named shape both
  keep working side by side.

**With all of the above, real undici's `require()` completes entirely
and a real `fetch()` call actually starts running** - past every module-
load-time gap this whole investigation (rounds 69-74) has been closing
one at a time. It fails at a new point, deep inside real `Request`
construction: `requestObject.signal.aborted` throws `Cannot read
property 'aborted' of undefined` - `.signal` reads back `undefined` even
though the constructor demonstrably set it correctly moments earlier.

**A real, deeply-hidden paserati bug, isolated through an extensive
multi-stage bisection of the real ~1100-line `request.js` file - filed
as [paserati#302](https://github.com/nooga/paserati/issues/302) once
reduced to a clean, undici-free 13-line repro:**

```js
class Foo {
  #signal
  constructor() { this.#signal = "hello"; }
  get signal() { return this.#signal; }
}
Object.defineProperties(Foo.prototype, {
  signal: { enumerable: true },   // no get/set/value - should just flip enumerable
});
const f = new Foo();
console.log(f.signal); // "undefined" here - should still be "hello"
```

`Object.defineProperty`/`defineProperties`, given a *partial* descriptor
that omits `get`/`set`/`value` when redefining an existing accessor,
silently replaces the getter instead of merging into it (per spec,
`ValidateAndApplyPropertyDescriptor` must preserve every attribute the
new descriptor doesn't mention). Real undici's `request.js` does exactly
this after its `Request` class declaration - a genuinely common
real-world idiom for making class-declared getters enumerable (they're
non-enumerable by default) without restating each one as a full
descriptor:

```js
const kEnumerableProperty = { enumerable: true }
...
Object.defineProperties(Request.prototype, {
  method: kEnumerableProperty, url: kEnumerableProperty, ...,
  signal: kEnumerableProperty, ...
})
```

This silently destroys *every* one of `Request`'s real accessors the
instant it runs, not just `signal` - `method`, `url`, `headers`, `body`,
etc. are all equally broken, `signal` just happened to be the first one
a real `fetch()` call actually reads.

**The bisection itself, documented here because it's the actual
verification work, not just the conclusion**: many real, concrete
hypotheses were tested and ruled out along the way, each with a direct
repro before moving on - constructor length/complexity (a 150-local-
variable, 490-line constructor: fine), private field count (5 fields:
fine), "friend" static methods reading another instance's private field
via a parameter rather than `this` (fine), `mixinBody`'s prototype
mutation (fine), two sequential instances of the same private-field
class constructed inside one outer constructor (fine), a real
`getMaxListeners` failure silently caught by `request.js`'s own
`try/catch` (fine, and is what surfaced the gap fixed above), module-
load-time `AbortController`/`FinalizationRegistry`/`WeakMap`
construction (fine). Systematically re-adding chunks of the real,
unmodified file to an otherwise-working minimal reduction (rather than
guessing at a synthetic shape) is what actually found it: the file's own
tail-end `Object.defineProperties(Request.prototype, {...})` call was
the one addition that flipped a working reduction to a broken one, and
reducing *that* in isolation is what produced the 13-line repro above.

**Verification**: 8 new Go test files/additions
(`crypto_test.go`/`worker_threads_test.go`/`buffer_test.go`/
`dom_exception_global_test.go`/`promise_with_resolvers_test.go`/
`events_test.go`) drive the exact real call shapes found above -
`getHashes()` cross-checked against `createHash` actually accepting
every name it lists, `markAsUncloneable` verified to actually block a
later `structuredClone()` (not just exist), `DOMException`'s getter-
override subclassing case, `Promise.withResolvers()` exercised through
both its resolve and reject paths, and `getMaxListeners`/
`setMaxListeners` against a real non-`EventEmitter` object. Full suite
re-run 3x plus once under `-race`, `go vet` clean. Real credentialed `pi
--version`/`--help` still succeed; the live Fireworks `-p` calls
themselves returned `412 Account ... is suspended` during this round's
final check - a real Fireworks billing/account state, not a code
regression (confirmed by the identical failure on both the plain-reply
and tool-call prompts, and by `--version`/`--help` both still working
normally) - so the tool-call and plain-reply paths are unverified live
*this specific round*, pending the account being restored, though
nothing in this round's changes touches that code path at all.

**Status**: real undici's `require()` chain is now fully clear -
confirmed by re-running the same real-package probe (`declareUndici()`
disabled, restored immediately after) one final time against a freshly-
copied, completely unmodified copy of the real npm package. A real
`fetch()` call now actually starts executing, reaching real `Request`
construction before failing on paserati#302. `undici.go`'s shim stays
exactly as before - deleting it now would still be a regression, since
real undici cannot yet complete an actual request. `net.go`/`tls.go`
remain real, verified, and still without a load-bearing consumer.
Four real paserati bugs have now been found and filed by this
investigation (#292, #297, #298 fixed and merged same-day each time;
#302 open) - each isolated to a minimal, undici-free repro before
filing, per this project's own standing discipline.

## Round 75: paserati#302 fixed and pulled - real undici's fetch() now runs its actual network path, blocked on a hard structural gap (no WebAssembly at all)

Prompted by paserati landing on `main` with "millions of fixes" since Round
74 - pulled it, rebuilt, and confirmed directly (not assumed) that
`5d656722 fix(builtins): preserve existing accessor when defineProperty
gets a generic descriptor` is exactly paserati#302 (`Fixes #302` in its own
commit message) - the bug isolated last round that destroyed `Request`'s
class-getter accessors the instant undici's `Object.defineProperties(
Request.prototype, { signal: kEnumerableProperty, ... })` ran.

**Recovery note before any of the above could be verified**: the entire
Round 70-74 diff (net.go/tls.go/async_hooks.go/... - everything documented
in those rounds' own entries) turned out to have been sitting uncommitted
in the main checkout (`/Users/nooga/lab/noderati`) the whole time, not in
the fresh worktree this session started in (which had branched off
`origin/main` at Round 68, before even Round 69 was pushed). Confirmed the
uncommitted diff still built and passed the full suite against paserati's
new `main` before committing it as-is (one combined commit covering all
five rounds, since that's genuinely how it happened - see that commit's
own message for the per-round breakdown); continued all of this round's
work in the main checkout from there, per the user's own explicit choice
when asked.

**Re-running the real undici probe (`declareUndici()` disabled, a fresh
unmodified copy of the real npm package, the exact same
`EnvHttpProxyAgent({allowH2:false,...})` + `setGlobalDispatcher` +
`install()` + real `fetch()` call pattern as every prior round) against a
real local Go `net/http` test server** turned up four more real, precisely
isolated gaps, each fixed and each moving the failure measurably further
into undici's own real request pipeline before the next one surfaced:

1. **`node:events`'s `addAbortListener` missing entirely.** Real undici's
   `lib/core/util.js` destructures it off `require("node:events")` at
   module load time and calls it unconditionally on every request that
   carries a signal. Added a real implementation (`events.go`): the
   already-aborted branch queues the listener via a new real
   `queueMicrotask` global (see next item); the common branch does a real
   `signal.addEventListener('abort', listener, {once:true})` and returns
   a real disposer keyed by the real `Symbol.dispose` (confirmed present
   in paserati directly - `typeof Symbol.dispose === "symbol"`).

2. **Global `queueMicrotask` missing entirely** (`queue_microtask.go`,
   new file) - needed by (1)'s already-aborted branch. Built on the same
   real primitive as `Promise.withResolvers` (`vmInst.NewPromiseFromExecutor`),
   not anything synthetic: resolves a real Promise and hands the callback
   to its real `.then`, which schedules it on the engine's own real
   promise-reaction-job queue. Measured directly against both
   `Promise.resolve()` and `setTimeout` rather than assumed: correctly
   ordered *before* any macrotask, though it takes one extra microtask
   tick versus V8's native fast path (documented in the file's own
   comment, not glossed over) - a real, honest deviation, not a
   correctness bug, since no real call site found so far depends on
   tick-exact timing.

3. **While testing (2), found and filed a genuine, separate paserati
   engine bug**: `AbortController.abort()` never dispatches the `'abort'`
   event to any listener registered via `addEventListener` (nor calls
   `.onabort`, which doesn't even exist as a property), and
   `removeEventListener` on an AbortSignal is a complete no-op. Isolated
   to a 5-line repro (`ac.signal.addEventListener('abort', fn);
   ac.abort(); // fn never runs`) with the exact `abort_controller_init.go`
   root cause identified (the `abort()` method flips `.aborted`/`.reason`
   but never reads or calls `signalRef.listeners`) before filing as
   [paserati#372](https://github.com/nooga/paserati/issues/372). Not
   fixable on our side per this project's standing rule; (1)'s
   already-aborted branch doesn't depend on it and keeps working, so
   `addAbortListener`'s own test locks in today's honest (not
   spec-correct) behavior with a comment pointing at #372, to be flipped
   the moment it's fixed upstream.

4. **`new URL("http://host").pathname` was `""`, not `"/"`** - a real bug
   in noderati's *own* `url.go` (not paserati; the URL class lives here),
   found because real undici's `lib/core/util.js#parseOrigin` re-parses
   a dispatcher's origin URL and throws `InvalidArgumentError('invalid
   url')` unless `pathname === '/'` exactly, on every single Pool/Client
   construction. Root cause: Go's `net/url.Parse("http://host")` leaves
   `.Path` empty (no error), and nothing normalized that per WHATWG's own
   rule that a special-scheme URL's path is never empty. Fixed by
   normalizing `parsed.Path` to `"/"` before computing both `Pathname`
   and `Href` (real Node: the same URL's `.href` is also `"http://host/"`,
   not `"http://host"` - fixed together, not just the one field the crash
   happened to hit first).

5. **`http.maxHeaderSize` missing from the `node:http` shim.** Real
   undici's `lib/dispatcher/client.js` reads it unconditionally at
   module-load time, throwing `InvalidArgumentError('http module not
   available or http.maxHeaderSize invalid')` the instant any
   Client/Pool is constructed without it. Added as `16384` (real Node's
   own default since v13.13.0) to `http.go`'s shim only, not `https` -
   real Node doesn't expose it there either, confirmed rather than
   copy-pasted across both for convenience.

6. **Global `setTimeout` returning a plain number instead of a real
   `Timeout`-shaped object** - the deepest and most structurally
   interesting fix this round. Real undici's `lib/util/timers.js#refreshTimeout`
   does `fastNowTimeout = setTimeout(onTick, TICK_MS);
   fastNowTimeout?.unref()` on every single `FastTimer` construction (hit
   on every request). A plain number has no `.unref` property - the `?.`
   only guards the *receiver* (`fastNowTimeout`) being nullish, not the
   looked-up property itself being absent - so calling it throws
   "undefined is not a function", a real and unconditional crash, not an
   edge case. Wrote `timeout_object.go`: a real Go-native `Timeout` object
   wrapping paserati's own real numeric timer id in a closure, with real
   `.ref()`/`.unref()` (honest no-ops - paserati's timer initializer has
   no ref-counted keep-alive concept to hook into at all, so there's
   nothing for them to toggle) and a real `.refresh()`/`.close()` that
   clear-and-reschedule/clear the real underlying paserati timer, not
   stubs.

   **A real architectural discovery along the way, found by directly
   testing rather than assuming the established `gobj.SetOwn(...)`
   pattern from every prior global-install file would just work here
   too**: it silently didn't. `globalThis.GetOwn("setTimeout")` (the Go
   PlainObject API) and `vmInst.GetGlobal("setTimeout")` (paserati's
   internal by-name heap lookup) returned two *different* underlying
   function values immediately after `New()`, before any of this round's
   code ran - proof that core compile-time-known globals like
   `setTimeout`/`clearTimeout` (registered by paserati's own
   `HostTimerInitializer`) are resolved by bare identifiers through a
   dedicated heap slot, entirely separate from `globalThis`'s own-property
   map. Writing to the latter (what every prior global-install file in
   this codebase does - `file_global.go`, `promise_with_resolvers.go`,
   this round's own `queue_microtask.go`) only ever touches a cold copy
   for names that already have a reserved slot; it works for *brand-new*
   names precisely because those have no such slot and correctly fall
   back to a `globalThis` property lookup. A genuine
   `globalThis.setTimeout = ...` assignment *executed as real bytecode*,
   by contrast, measurably does update what bare identifiers subsequently
   resolve to (confirmed directly, not assumed) - so the actual override
   in `timeout_object.go` is done through one `p.EvalCode(...)` call
   rather than the raw Go property-API pattern this file otherwise
   follows throughout. Safe now specifically because paserati#298 (the
   second-eval exception-propagation bug that forced `file_global.go` and
   friends onto pure-Go construction in Round 73) is fixed upstream -
   confirmed by a standalone repro test (`EvalCode` then a later
   `RunCode` whose thrown exception still propagates correctly) before
   relying on it here.

**The wall this round actually hit, past all six of the above**: real
undici's `lib/dispatcher/client-h1.js` (the real HTTP/1.1 connection
handler `connectH1` reaches) unconditionally instantiates its bundled
`llhttp` HTTP parser as a **WebAssembly** module
(`new WebAssembly.Module(...)`, `new WebAssembly.Instance(...)`) the
moment any real HTTP/1.1 request tries to parse a response - this specific
undici version ships no pure-JS parser fallback at all
(`/* global WebAssembly */` right at the top of the file, not a
conditionally-reached corner). Checked paserati directly rather than
assumed: `grep -rl WebAssembly pkg/` across the whole engine returns
nothing - there is no `WebAssembly` global, no WASM bytecode interpreter,
nothing to build on at all. This is not a small, isolable engine bug like
#302/#372 above; it's an entire missing engine capability (a WASM
runtime), structurally different in scope from every other gap this
investigation has found and fixed or filed so far. Initially left
unfiled this round - a five-line repro and a root-cause diff isn't the
right shape for "please add a WebAssembly interpreter," and taking that
on is the project's decision to make deliberately, not something to
request via the same routine channel as an accessor bug. The user then
asked for it to be filed anyway, with the exact JS-visible surface
undici's real call site needs spelled out, since a separate agent is
being tasked with wiring in [wazero](https://github.com/tetratelabs/wazero)
(a pure-Go WASM runtime) as the actual execution engine - filed as
[paserati#375](https://github.com/nooga/paserati/issues/375), scoped
explicitly to core WebAssembly 1.0 (`Module`/`Instance`/`Memory`, the
exact import/export shape `lazyllhttp()` uses), not a request to
reimplement a WASM VM from scratch.

**Verification**: `go build`/`go vet` clean. New Go tests
(`url_test.go`/`http_test.go`/`events_test.go`/`queue_microtask_test.go`/
`timeout_object_test.go`) drive every real call shape found above,
including one (`TestEventsAddAbortListener`) that deliberately asserts
today's honest pre-#372-fix behavior rather than the spec-correct one, so
it fails loudly (in the right direction) the moment that issue closes
upstream instead of silently drifting. Full suite green 3x plus once under
`-race`. `pi --version`/`--help` (real, unmodified `pi-coding-agent@0.80.2`)
both still succeed; a live Fireworks `-p` smoke test could not be attempted
this round - no API key configured in this environment (not the account
suspension noted last round; simply nothing to authenticate with here) -
so the tool-call/plain-reply paths are unverified live this specific round
too, for an unrelated reason each time now.

**Status**: real undici's `fetch()` now gets past every prior blocker
(#302's accessor destruction, `addAbortListener`, `queueMicrotask`,
`http.maxHeaderSize`, the URL pathname bug, and the `Timeout` object
shape) and reaches its actual real network/parsing path before failing -
a genuinely different kind of blocker than every previous round's, and
one this project cannot build its way past: it needs paserati to grow a
WebAssembly runtime, not another host-layer gap fix. `undici.go`'s shim
stays in place; deleting it now would still be a regression. `net.go`/
`tls.go` remain real and verified, still without a load-bearing consumer
(undici's `Client`/`Pool` construct real sockets via `node:net.connect`
that these files provide, but never gets to use one - it fails inside
`connectH1`'s WASM setup before a socket write would happen). Five real
paserati bugs found and filed by this investigation across all rounds now
(#292, #297, #298, all fixed and merged same-day each time; #302 fixed
and merged; #372 open) - each isolated to a minimal, undici-free repro
before filing, per this project's own standing discipline.

## Round 76: WebAssembly, pivoted to noderati-only (paserati#375 needs no paserati changes); Phase 0 - Buffer made real

Re-read paserati#375's own scoped surface against `pkg/vm`'s already
*exported* API before writing a line of implementation, per this
project's habit of checking rather than assuming: `ArrayBufferObject`/
`TypedArrayObject` (real `[]byte`-backed, `AsArrayBuffer()`/
`AsTypedArray()`, `NewArrayBuffer`/`NewTypedArray`), `vm.Call(fn, this,
args)` for a Go callback to re-enter JS (already the exact mechanism
`host_timers.go`'s `setTimeout` and this codebase's own `emitter.go` use
for every other JS-callback bridge), `NewConstructorWithProps`, and
`vmInstance.NewExceptionError` for throwing a real, catchable JS
exception from Go. That's the entire toolkit `WebAssembly.Module`/
`Instance`/`Memory` (wazero-backed) needs - nothing paserati doesn't
already expose. So this is being built as a host-injected global here in
noderati, the same way `File`/`MessagePort`/`DOMException` already are,
not as a paserati core feature - commented on paserati#375 to that
effect rather than leaving it looking like still-pending paserati work.

**Before any of the WebAssembly bridge itself, a blocking prerequisite
found by reading the actual real call site again, not just the issue's
own summary of it**: real undici's `lazyllhttp()` does `new
WebAssembly.Module(require('../llhttp/llhttp-wasm.js'))`, and that
`require()` returns a real Node `Buffer` (`Buffer.from('<base64>',
'base64')` in the vendored file itself). `internal/host/buffer.go`'s
`Buffer` was a complete fake through Round 75: a `PlainObject` carrying
its bytes hidden inside a Go string closure, no indexed access, no
`ArrayBuffer` backing at all - `AsTypedArray()`/`AsArrayBuffer()` both
failed on it. A `WebAssembly.Module` constructor that (correctly, per
spec) only accepts real `ArrayBuffer`/`TypedArray` input could not read
this fake Buffer's bytes at all - the actual motivating call site would
stay broken under a perfectly-implemented WebAssembly bridge. The same
gap was latent for any binary data ever routed through the old
`wrapBuffer(vmInst, string)` generally: forcing arbitrary bytes through
a Go string and back through paserati's own JS string representation
isn't a safe carrier once those bytes aren't valid UTF-8.

Rebuilt `Buffer` as a real `Uint8Array` subclass - real Node's own
`Buffer` literally is one. `Buffer.prototype`'s `[[Prototype]]` is the
real `Uint8Array.prototype` (same reparenting trick `file_global.go`
uses to build `File` on top of the real `Blob`), so indexed byte access,
`.length`, iteration, `.buffer`, and paserati's own real ES2024
`toBase64`/`fromBase64`/`toHex`/`fromHex` typed-array methods all come
free, for real, off the actual shared prototype chain - only the
genuinely Buffer-specific surface (`from`/`alloc`/`allocUnsafe`/
`isBuffer`/`byteLength`/`concat` statics, `toString(encoding)`/`write()`
instance methods) needed writing. `subarray()`/`slice()` needed an
explicit wrap-the-result step: confirmed directly (a vanilla, Buffer-free
`new Uint8Array(...).subarray()` repro) that paserati's generic
`%TypedArray%.prototype` implementation doesn't species-construct
through `this.constructor`, so those two are overridden on
`Buffer.prototype` to call the real inherited method via
`vmInst.GetProperty`+`vmInst.Call` and reparent just the *result* back
onto `Buffer.prototype` - not a paserati bug, just a real behavioral gap
this file has to close itself since the fix belongs entirely in
noderati's own control.

**A second, genuine paserati bug found in passing**: `new
Uint8Array(4).buffer instanceof ArrayBuffer` is `false` - confirmed with
a minimal repro with zero noderati or Buffer involvement at all
(`constructor.name` correctly reports `"ArrayBuffer"`, but `instanceof`
itself fails; a second repro narrowed it further - `DataView` has the
same bug, `Uint8Array`/`Object`/`Array`/`Map` don't). Flagged to the
user, filed as
[paserati#377](https://github.com/nooga/paserati/issues/377).

Only three other files touched `wrapBuffer`/the old string-carrier
model - `crypto.go` (`randomBytes`), `net.go` (socket `data` chunks,
plus `valueToBytes` which used to invoke the fake Buffer's `toString()`
closure to get bytes back out and now reads real `TypedArray` bytes
directly), `zlib.go` (decompressed chunks) - all three already held real
`[]byte` at their call sites and needed only the signature change
(`wrapBuffer` now takes `[]byte`, not `string`).

**Verification**: `go build`/`go vet ./...` clean. New Go tests
(`TestBufferIsRealUint8Array`, `TestBufferBase64RoundTrip` - the exact
`Buffer.from(bytes).toString('base64')`/`Buffer.from(b64,
'base64')` round trip real undici's own vendored wasm-embedding file
uses - `TestBufferSubarrayStaysBuffer`) added alongside the existing
Buffer suite, all passing. Full `internal/host` suite re-run before and
after: identical to baseline except this round's own additions - the one
still-failing test (`TestEventsAddAbortListener`) reproduces byte-for-byte
on a stash of this round's diff too, confirming it's the same
already-documented pre-#372-fix sentinel from Round 74, unrelated to
this round's work.

**Status**: Phase 0 (real `Buffer`) done. Phase 1 (the actual
`WebAssembly.Module`/`Instance`/`Memory` bridge, wazero-backed) not
started yet this round.

## Round 76 (cont.): Phase 1 - the real WebAssembly.Module/Instance/Memory bridge, wazero-backed

Added `github.com/tetratelabs/wazero` (pure Go, no cgo, MIT) and built
`internal/host/webassembly_global.go`: `WebAssembly.Module`/`Instance`/
`Memory` plus real `CompileError`/`LinkError`/`RuntimeError` subclasses,
scoped exactly to what paserati#375's own investigation found real
undici's `lazyllhttp()` needs (plain-number i32/i64/f32/f64 function
imports, `instance.exports`, `.memory.buffer` reflecting current wasm
memory including after growth) - no `Table`, no `Global`, no streaming/
async instantiate, no multi-value returns.

Before writing any bridge code, validated every load-bearing assumption
empirically rather than trusting docs or the issue's own summary of
them, per this project's standing discipline:

1. **wazero's dynamic host-function API** (`WithGoFunction` +
   `[]api.ValueType`, no compile-time Go signature needed) - confirmed
   against a real, hand-built `.wasm` fixture (checked in as
   `internal/host/testdata/wasm_fixture.wasm`/`.wat`: one env import,
   four exported functions, one exported growable memory).
2. **`api.Memory.Read` is a genuine bidirectional alias**, not a copy -
   confirmed by writing through wasm and reading via the Go slice, then
   writing via the Go slice and reading back through wasm. Also
   confirmed the docs' "disconnects on grow" note empirically: after
   `memory.grow`, the old slice keeps its last value rather than
   panicking or auto-updating - it just silently stops reflecting
   further changes, which is exactly why the bridge never caches a Go
   slice across a boundary crossing.
3. **A `wazero.CompiledModule` cannot cross runtimes** - a two-runtime
   repro (compile on runtime A, try to instantiate on runtime B) fails
   outright ("source module must be compiled before instantiation").
   This directly shaped the architecture: `WebAssembly.Module` cannot
   own one persistent runtime that every `Instance` shares (two
   Instances of the same Module, each wanting its *own* `"env"` pointing
   at different JS import functions, would collide on one shared
   runtime's import namespace anyway) - so `Module` only validates
   bytes at construction (compiled once against a scratch runtime,
   purely to surface a real `CompileError`, then discarded) and stores
   the raw validated bytes; each `Instance` gets its own fresh
   `wazero.Runtime` and recompiles from those bytes. One avoidable
   recompile per `Instance`, traded for correctness - and each of those
   Runtimes is never closed (no lifecycle hook exists to know when an
   `Instance` JS object becomes unreachable), so it's not just a handle
   leak, it's a compiled-code-holding runtime per `Instance`. Fine at
   `lazyllhttp()`'s actual real usage (its result is memoized, so this
   is 1-2 `Instance`s for a process's whole life); would matter a lot if
   something ever instantiated per-request instead. Named here so a
   future round hits this as an already-known tradeoff, not a surprise
   under load.
4. **A host function's Go panic survives through `fn.Call` as a
   recoverable error** - confirmed with a custom error type: wazero
   recovers the panic and wraps it, retrievable via `errors.As`. This is
   what lets a real thrown JS import callback (llhttp's `wasm_on_*`
   callbacks do throw in real use, e.g. on `maxHeaderSize` exceeded)
   cross back out of wasm as the *original* JS exception rather than
   being flattened into a generic message or silently swallowed.
5. **Error subclassing** - confirmed a real `Error` instance is a
   `PlainObject` (same as `Blob`), so `CompileError`/`LinkError`/
   `RuntimeError` can be built with the same Construct-then-reparent
   trick `file_global.go` uses for `File` on `Blob`. One catch found
   only by testing, not by reading: the base `Error` constructor sets
   its own `name` as an *own* property on the instance, which shadows
   whatever a subclass's own prototype says unless the instance's own
   copy is overridden too - without that fix, every thrown
   `CompileError` reported `.name === "Error"`.

The JS<->wasm memory bridge (`wasmMemoryBridge`) is copy-based, not
zero-copy: paserati's `ArrayBufferObject.data` is unexported (no public
way to alias an externally-owned `[]byte` as its backing store - the
same limitation buffer.go's own doc comment already hit), and wazero's
aliasing view disconnects on grow regardless. Bridges at exactly the two
points JS and wasm can observe each other's writes: `syncIn()` (JS's
cached `ArrayBuffer` -> real wasm memory, immediately before any
exported-function call) and a lazy, dirty-flagged `.buffer` getter (real
wasm memory -> a fresh `ArrayBuffer`, only when wasm ran since the last
vend or wasm's own size changed) - matching real undici's own call
pattern of always re-reading `.memory.buffer` fresh rather than caching
it, so an uncommitted JS-side write is never silently clobbered by an
eager sync-out.

**Two review passes caught real gaps before any of this reached
undici**: a first review flagged (all fixed) - `bytesFromArrayLike`'s
per-element property lookup cost (documented, not hit by any real
BufferSource/TypedArray path); `subarray`/`slice`'s reparenting touching
only the *returned* view, not the receiver (added
`TestBufferSubarrayDoesNotMutateReceiver` to pin it down); the
`instanceof ArrayBuffer` paserati bug's relevance to `Memory.buffer`
specifically (filed as #377 rather than left as a curiosity). A second
review, after the bridge was written and passing, caught three real
correctness gaps that unit tests alone hadn't: (1) the import trampoline
was silently zeroing results on a thrown JS callback instead of
propagating it - fixed via the `wasmJSCallPanic` mechanism in point 4
above, verified by `TestWebAssemblyImportThrowPropagates`; (2) multi-
value wasm results were silently truncated to the first value instead of
refusing - both the export-wrapper and the import-trampoline now throw a
clear, honest "not supported" error instead; (3) `mem.Read`'s own
success flag was being dropped in the `.buffer` getter, which could have
hidden a real read failure behind a plausible-looking zero-filled
buffer - now checked, falling back to the last-known-good buffer instead
of fabricating new state.

**Verification**: `go build`/`go vet ./...` clean. New Go tests in
`internal/host/webassembly_global_test.go`: globals exist; a real
`CompileError` is catchable (the exact shape real undici's `try { new
WebAssembly.Module(simd) } catch {}` fallback needs); two `Instance`s
built from the same `Module` with different import objects each route
correctly to their own JS functions (written *before* the bridge code,
per review); the full JS -> native export -> wazero -> Go trampoline ->
`vm.Call` -> JS reentrancy path returns the right value; memory
read/write in both directions, including the offset-view form
(`new Uint8Array(mem.buffer, ptr, len).set(data)`) real undici's own
`Parser.execute` actually uses, not just the whole-buffer form; memory
growth (byte length, previous-page-count return value, data surviving
the grow); a thrown JS import callback propagates as the same real
exception; a non-BufferSource argument to `Module` throws a real
`CompileError`. Full `internal/host` suite green except the same
pre-existing, unrelated `TestEventsAddAbortListener`.

**Status**: core bridge real and unit-verified end to end, including the
hard parts (cross-instance isolation, deep reentrancy, memory growth,
catchable errors, a thrown-callback round trip). Real-undici end-to-end
verification (actually running real undici's `fetch()` through
`lazyllhttp()`) **not attempted this round** - every prior round that
probed real undici surfaced at least one new engine bug with its own
debugging tail, and starting that probe now risked an unbounded session
rather than a clean stopping point. Left for a following round.

## Round 76 (cont.): real-undici E2E probe - a real noderati `this`-binding bug fixed, real undici's actual async compile/instantiate usage discovered and implemented, real llhttp wasm parses a real HTTP response end to end

Took on the E2E verification explicitly deferred above, installed a real
`undici@7.11.0` (npm, into a scratch dir - not committed) and ran the
same `EnvHttpProxyAgent({allowH2:false})` + `setGlobalDispatcher` +
`install()` + real `fetch()` pattern every prior round has used, against
a real local Go `net/http` test server, with `declareUndici()`
temporarily disabled (restored before every commit, per this project's
own standing procedure for these probes).

**First real finding, entirely unrelated to WASM**: the very first run
crashed inside `Pool` construction itself, long before any request -
`Cannot set index on non-array/object/typedarray type 'undefined'` in
`dispatcher/pool-base.js`'s `onDrain`, which does `this[kNeedDrain] =
needDrain`. Isolated to a minimal, undici-free repro before touching
anything (per standing discipline): a class extending `node:events`'
`EventEmitter` that assigns a plain (non-arrow) function to an instance
property inside its own constructor, later used as *another* emitter's
listener - `this` inside it was `undefined` instead of the emitter that
dispatched the event. A plain, non-subclassed `EventEmitter` instance
doing the exact same thing was unaffected, which is what pointed at
noderati's own `events.go` shim rather than paserati: its `emit()` called
listeners via a bare `fn(...args)` instead of `fn.call(this,
...args)` - real Node's own documented behavior is that a plain-function
listener's `this` is bound to the emitter. One-line fix
(`internal/host/events.go`), full suite re-verified green before
continuing.

**Second real finding**: with that fixed, the crash moved forward into
`lazyllhttp()` itself - `undefined is not a function`. Reading the real
installed file directly (not re-reading the issue's own quoted code)
showed why: `undici@7.11.0`'s actual `lazyllhttp()` does `await
WebAssembly.compile(require('../llhttp/llhttp_simd-wasm.js'))` then
`await WebAssembly.instantiate(mod, {...})` - the *async* statics, not
`new WebAssembly.Module(...)`/`new WebAssembly.Instance(...)` directly.
paserati#375's own issue text quoted an older undici version using the
synchronous constructors only; this round's own earlier work (this same
Round 76 entry, above) had deliberately scoped those statics out on that
basis. Corrected by real evidence rather than defended on the issue's
own authority: refactored the synchronous constructors' bodies into two
shared functions (`compileWasmModule`/`instantiateWasmModule`) and added
`WebAssembly.compile`/`instantiate` as thin async wrappers around them
(genuinely synchronous under the hood - wazero itself is synchronous -
so these just return an already-resolved-or-rejected Promise; both
`instantiate` overloads per spec are implemented: `instantiate(module,
imports)` resolves to just the `Instance`, `instantiate(bufferSource,
imports)` compiles first and resolves to `{module, instance}`).

That refactor's own review (writing `TestWebAssemblyAsyncCompileAndInstantiate`
immediately after, not waiting for the next probe to find it) caught two
real bugs in this file's own code, neither paserati's fault:

1. `WebAssembly.Module.prototype`/`WebAssembly.Instance.prototype` were
   never actually wired up - `moduleProto`/`instanceProto` had their
   `constructor` property pointed at the right constructors, but the
   reverse link (`moduleCtor.prototype = moduleProtoVal`) was never set.
   Every `x instanceof WebAssembly.Module`/`Instance` check was silently
   guaranteed to throw ("Function has non-object prototype in instanceof
   check") - just never exercised by this file's own tests until the new
   one checked it.
2. `isWasmModuleValue`/`instantiateWasmModule`'s module-type check called
   `Value.AsPlainObject()` unconditionally on a raw, arbitrary caller
   argument. Confirmed directly (not assumed) that `AsPlainObject` panics
   on anything whose type tag isn't exactly `TypeObject` (`pkg/vm/value.go`) -
   so `WebAssembly.instantiate(someTypedArray, imports)` (a real, spec-legal
   call shape, and exactly the second `instantiate` overload's own first
   argument) crashed the whole VM instead of throwing a catchable
   `TypeError`. Fixed with a small `asPlainObjectSafe` guard;
   `TestWebAssemblyInstanceRejectsNonModuleArgument` pins it down.

**With all of the above fixed, the actual target**: real undici's
`fetch()` against the local Go server got further than any prior
round - past module load, past `Pool`/`Client` construction, past
`lazyllhttp()`'s real `WebAssembly.compile`/`instantiate` calls
completing successfully - and then the *process itself* hung (near-zero
CPU, not a busy loop) partway through the actual request. A `SIGQUIT`
didn't produce a dump (noderati installs its own signal handling), so
diagnosed via `NODERATI_PPROF`'s pprof endpoint instead: a goroutine
dump showed the VM's single interpreter goroutine blocked in
`DefaultAsyncRuntime.WaitForExternalOp`, and a real, still-open
`net.Conn` read blocked waiting for more bytes from the (idle,
keep-alive) server connection - i.e. something in the `Client`/socket
dispatch layer above the parser never told the async runtime the
response was actually complete, so the event loop is correctly waiting
on a genuinely pending (if permanently idle) external op forever. This
is a *different*, separate bug from anything WASM-related - almost
certainly somewhere in the `Client`/`Dispatcher`/socket coordination
layer, not in `lazyllhttp()` or the WASM bridge itself (confirmed next).
Not isolated or filed this round - a real further investigation of its
own, out of scope for what this round set out to verify.

**To settle definitively whether the WASM bridge itself is sound**,
independent of that separate hang: wrote a standalone script that
bypasses undici's `Client`/socket layer entirely and drives the real
`llhttp-wasm.js` binary directly, replicating `client-h1.js`'s own exact
call pattern (`WebAssembly.compile`/`instantiate` with real
`wasm_on_*` import callbacks, `llhttp_alloc(TYPE.RESPONSE)`,
`malloc`/`new Uint8Array(memory.buffer, ptr, len).set(chunk)`/
`llhttp_execute`) against a real, complete, hand-written HTTP/1.1
response. **Every callback fired, in the exact right order, with the
exact right argument values, and `llhttp_execute` returned 0 (`HPE_OK`)**:
`on_message_begin` -> `on_status` -> two `on_header_field`/`on_header_value`
pairs -> `on_headers_complete` (status 200, no upgrade, keep-alive) ->
`on_body` (5 bytes) -> `on_message_complete`. This is the actual hard
technical risk this whole feature existed to resolve, proven against
production wasm bytes, not a synthetic fixture - checked in as a
permanent Go test (`TestWebAssemblyRealLLHTTPParsesRealHTTPResponse`,
`internal/host/testdata/llhttp-real.wasm` - a real, unmodified,
MIT-licensed copy of undici@7.11.0's own vendored llhttp wasm binary,
extracted once via `Buffer.from(base64, 'base64')` in a real Node
process).

**Verification**: `go build`/`go vet ./...` clean. Full `internal/host`
suite green except the same pre-existing, unrelated
`TestEventsAddAbortListener`. `declareUndici()` restored (was only ever
disabled locally during the probe itself, per standing procedure).

**Status**: the WASM<->JS bridge this whole feature exists for is now
proven correct against real, production `llhttp` wasm bytes end to end,
independent of the rest of the HTTP stack. Real `fetch()` through real
undici all the way to a real response is still blocked - now on a
different, newly-found bug in the `Client`/socket dispatch layer above
the parser, not on WebAssembly - left for a following round to isolate
and file properly rather than rushed here.

**Round 76 (cont.): a real reentrancy bug in the memory bridge, caught
before it shipped, plus one flagged concern ruled out.** A post-hoc
review of the export-function wrapper (`wrapWasmExportedFunction`)
found that `markDirty()` was only called *after* `fn.Call(...)`
returned, not before it started. That's wrong for exactly the call
pattern real llhttp uses: a wasm export can call back into a JS host
import *during* its own execution (llhttp's `wasm_on_status`/
`wasm_on_body`/etc. are called mid-`llhttp_execute`, each handed a
pointer into memory llhttp just populated), and if that JS callback
reads `memory.buffer` before the wrapper's own post-call `markDirty()`
runs, `bufferValue()` would hand back the *stale* cached ArrayBuffer
(whatever was last vended, reflecting JS's pre-call writes only) rather
than a fresh read of wasm's current memory. The capstone llhttp test
didn't catch this because every `wasm_on_*` callback in it just logged
offsets - it never actually dereferenced them via `memory.buffer`
inside the callback, so the exact seam real undici's own callbacks
exercise (`new FastBuffer(currentBufferRef.buffer, start, len)`) went
untested. Fixed with a one-line change: `markDirty()` now runs
immediately after `syncIn()`, before `fn.Call`, on the reasoning that
`syncIn()` just committed every pending JS write into wasm memory, so
wasm's own memory is authoritative from that instant regardless of
whether or how it calls back into JS mid-execution; the post-call
`markDirty()` stays too (harmless, covers a `memory.grow` mid-call).
Strengthened `TestWebAssemblyRealLLHTTPParsesRealHTTPResponse` itself
to close the gap that let this ship unnoticed: `wasm_on_status` and
`wasm_on_body` now actually decode `new Uint8Array(llhttp.memory.buffer,
at, len)` inside the callback and assert on the resulting text ("OK"
and "hello" respectively), rather than only logging pointer/length
pairs - this is the assertion that would have failed under the old
code and now passes under the fix.

Separately investigated and ruled out: whether `events.go`'s
`fn.call(this, ...args)` fix (this same round, upstream in this log)
could have broken arrow-function listeners, since a spec-compliant
arrow function ignores a `.call()`-supplied receiver entirely - if
paserati's `.call()` didn't honor that, every arrow-function
`EventEmitter` listener in the runtime (including `once()`'s own
wrapper, itself an arrow) would silently start seeing the wrong `this`.
Checked directly with a minimal repro (`arrow.call(obj) === <arrow's
own lexical this>`, not `obj`) run against the current paserati: it
correctly ignores the passed receiver for arrow functions. No latent
bug here - the `this`-binding fix from earlier in this round only
affects plain-function listeners, exactly as intended.

**Re-verification**: `go build ./...` clean, `gofmt` clean, full
`internal/host` suite green except the same pre-existing, unrelated
`TestEventsAddAbortListener` (confirmed via `git stash` to fail
identically with none of this round's changes applied).

## Round 77: the Round 76 hang, actually diagnosed - Socket never implements paused-mode Readable, so real undici's parser is never fed a single byte

Picked up the one item Round 76 left open: the process hang found
during the full real-undici `fetch()` E2E attempt (VM's interpreter
goroutine parked in `DefaultAsyncRuntime.WaitForExternalOp`, diagnosed
last round via a `pprof` goroutine dump against a real, idle keep-alive
`net.Conn` read). Root-caused this round, confirmed with a minimal,
undici-free repro, and fixed the one real bug found along the way
(though - see below - it turned out not to be the cause of this
specific hang).

**The trap that ate most of this round: `declareUndici()` is a fake
shim, and forgetting to disable it means testing nothing.**
`internal/host/undici.go` registers a ~20-line JS shim under the
module name `"undici"` - a fake `EnvHttpProxyAgent`, a no-op
`setGlobalDispatcher`, and an `install()` that does nothing but
reassign `globalThis.fetch` to itself. Because noderati's own
module resolver checks registered shims before `node_modules`, `import
* as undici from "undici"` resolves to this fake, *even with the real
npm package sitting right there in `node_modules`*, unless
`declareUndici()`'s call in `host.go` is temporarily commented out
first. Round 75's own log mentions doing this in passing; nothing
spelled out what happens if you forget. This round forgot, for a good
hour: every repro of the Round 76 hang "succeeded" - small bodies,
1.3MB streamed bodies, chunked encoding, three requests reusing a
pooled connection, even a real HTTPS request to `https://example.com`
- all cleanly, because every one of them was exercising noderati's own
native Go-backed `fetch()` (the real `http`/`https` module from Round
68), not real undici, not `WebAssembly`, not `lazyllhttp()` at all. A
live `pprof` goroutine dump during one of these "successes" showed
`net/http.(*persistConn).readLoop` - Go's own standard-library HTTP
client - which is what actually caught the mistake: that stack has
nothing to do with anything this whole investigation built. Disabling
`declareUndici()` (matching Round 75's own method) immediately
reproduced the exact Round 76 hang, goroutine-for-goroutine (same
`WaitForExternalOp`, same blocked `socketState.readerLoop`, same
never-returning `wg.Wait()`), confirming it's real and current.

**Root cause, confirmed by a 20-line undici-free repro**: real undici's
HTTP/1.1 client (`client-h1.js`) drives its parser *exclusively*
through Node's paused-mode `Readable` protocol - `socket.on('readable',
onHttpSocketReadable)` at module setup, then `onHttpSocketReadable`
calling `this[kParser].readMore()`, which calls `this.socket.read()` in
a loop and feeds whatever comes back into `llhttp_execute`. It never
listens for `'data'` at all (confirmed by reading the file directly -
the only `'data'` listener in `client-h1.js` is on request bodies, not
the socket). noderati's `net.Socket` (`net.go`) implements only
push-mode streaming: `readerLoop` reads bytes off the real OS socket
and unconditionally emits `'data'`; there is no `.read()` method at
all and `'readable'` is never emitted. Confirmed directly with a
minimal script (`net.connect()`, listen for both `'readable'` and
`'data'`, log `typeof socket.read`): `typeof socket.read === "undefined"`,
`'readable'` never fires, and `'data'` fires once with the full
151-byte response - proving the bytes really do arrive over the wire
and really are drained off the OS socket by our own `readerLoop`, they
just have zero subscribers once emitted, because real undici was never
listening for that event in the first place.

**Full causal chain, now completely explained** (correcting Round 76's
own characterization, which described this as "something never told
the async runtime the response was complete" - too generous; the
response was never *parsed* at all, because it was never *delivered*
to anything that could parse it): bytes arrive -> `readerLoop` drains
them off the real OS socket -> emits `'data'` to zero listeners (real
undici only ever attached a `'readable'` listener) -> `readMore()`/
`execute()` is never called -> `llhttp_execute` never runs -> the
in-flight request's promise never resolves -> `readerLoop` blocks on
its next `conn.Read()` (nothing more coming - the server is correctly
idling on a keep-alive connection) -> `writerLoop` is idle too (its one
write already flushed) -> the `wg.Wait()` goroutine that would call
`EndExternalOp()` never returns -> `DrainUntilIdle`'s
`WaitForExternalOp()` waits forever, exactly matching both this
round's and Round 76's goroutine dumps down to the line number.

**Also ruled out along the way, worth stating since the capstone test
from Round 76 only covers one of two cases**: real undici's own
`lazyllhttp()` tries `llhttp_simd-wasm.js` (a SIMD-optimized variant)
*first*, falling back to the plain `llhttp-wasm.js` only if compiling
the SIMD variant throws. `TestWebAssemblyRealLLHTTPParsesRealHTTPResponse`
only exercises the plain variant. Checked the SIMD variant directly,
same methodology as that test (a standalone script bypassing
`Client`/socket layer entirely): it compiles and instantiates cleanly
under wazero and produces the identical, correct callback sequence.
The WASM bridge is sound for both variants real undici might load -
this hang has nothing to do with WebAssembly, confirming Round 76's
own suspicion.

**Fixed one real bug found while chasing an initially-wrong hypothesis,
kept it, but only with a test**: before finding the actual root cause
above, `Socket.ref()`/`.unref()` (`net.go`) were noticed to be pure
no-op stubs - a real, separate, genuine gap, since real undici's
`resumeH1` (`client-h1.js`) calls `socket.unref()` the instant a
keep-alive connection has no in-flight request, exactly mirroring real
Node's own mechanism for not letting a pooled idle connection keep the
process alive by itself. Implemented it for real: `socketState` now
tracks whether its one `BeginExternalOp()` registration (made at
connect time) is currently "active," and `unref()`/`ref()` toggle it
via a new `setExternalOpActive`, calling the matching `EndExternalOp()`/
`BeginExternalOp()` exactly once per real transition - the connection's
own eventual teardown (`wg.Wait()` finishing) releases whichever
registration is still outstanding, a no-op if `unref()` already did.
This was written and initially believed to *be* the fix for the Round
76 hang; a direct causal ablation (temporarily reverting just this
change and re-running the exact hang repro) proved it made no
difference either way - the real cause is the missing-Readable-protocol
bug above, which stops the request from ever completing long before
`resumeH1`/`unref()` would even run. Kept the fix anyway (it's real and
correct on its own terms) but only after adding a direct test for it
rather than shipping it on the strength of a hypothesis that turned out
to be wrong: `TestNetSocketUnrefLetsProcessDrainWithConnectionStillOpen`
opens a real connection to a server that never closes it, calls
`socket.unref()`, then asserts `VM.DrainUntilIdle()` returns within 5s
anyway - confirmed to genuinely catch a regression (reverting just the
ref/unref change makes this exact test hang until Go's own test timeout,
producing the identical blocked-`readerLoop` goroutine dump).

**Not fixed this round, deliberately**: implementing real paused-mode
`Readable` semantics on `Socket` (an internal buffer, a real
`.read([size])`, correct `'readable'` emission timing, and the
mode-exclusivity rule a real `Readable` enforces - a stream cannot be
in both paused and flowing mode at once, and `net.go`'s `readerLoop`
currently just always pushes `'data'` unconditionally) is a real
feature of its own, not a quick fix, and is exactly the next round's
clean, fully-specified starting point. This round's job was diagnosis;
diagnosis is now complete and confirmed by a minimal repro, with a
precise, small, well-understood target for the fix.

**Verification**: `go build ./...` clean, `gofmt` clean, full
`internal/host` suite green (including the new
`TestNetSocketUnrefLetsProcessDrainWithConnectionStillOpen`) except the
same pre-existing, unrelated `TestEventsAddAbortListener`.
`declareUndici()` restored (was only ever disabled locally during this
round's own probing, per standing procedure - see the trap described
above for exactly why forgetting this is so easy to do silently).

## Round 78: paused-mode Readable implemented on Socket - real undici's parser now genuinely receives its bytes, next blocker isolated and filed

Implemented the fix Round 77 diagnosed and deliberately left undone:
real `Readable` semantics on `net.Socket` (`internal/host/net.go`,
shared with `tls.go`). Scoped to exactly what real undici's HTTP/1.1
client actually uses (confirmed by reading `client-h1.js` directly,
same discipline as every round before this one) - `.read([size])` and
the `'readable'` event - rather than the full Node `Readable` surface
(no `readableFlowing`/`readableLength`/`pipe()` on `Socket`, none of
which any real call site here touches).

**Design**: `socketState` gained a `flowing` bool mirroring real Node's
flowing/paused stream mode, a flat `readBuf []byte` accumulator, and
`sourceEnded`/`endEmitted` bookkeeping. `readerLoop` now routes every
chunk through `pushReadData`: while flowing, it emits `'data'`
immediately - this implementation's entire prior behavior, so every
existing `'data'`-based caller in this codebase keeps working
unchanged (verified: not one existing test needed to change). While
not flowing, bytes accumulate in `readBuf` and a coalesced `'readable'`
fires (`maybeScheduleReadable`, reset just before it actually emits so
a later arrival still gets its own follow-up rather than being
silently swallowed). `read(size)` pulls from `readBuf` - all of it if
no size is given (real undici's own call shape, `readMore()`'s
`this.socket.read()` with no arguments, in a loop until `null`) or up
to `size` bytes, leaving the remainder queued.

Mode selection: `buildSocketObject` overrides `on`/`addListener`/
`once`/`prependListener`/`prependOnceListener` (installed after
`newEventEmitterObject`'s generic versions) to auto-switch into flowing
mode - draining anything already buffered as one `'data'` event - the
instant a `'data'` listener is registered, exactly matching real Node's
own behavior; `pause()`/`resume()` also toggle it (in addition to their
existing job of gating the OS-level read pump for real backpressure).
A socket that only ever registers `'readable'` - real undici's own
usage, and nothing else in this codebase yet - stays in paused mode for
its whole life, which is exactly the case that was completely broken
before this round.

The trickiest correctness edge, gotten right on the first pass thanks
to advisor review before committing: EOF arriving while `readBuf` still
has unread bytes in it. Real Node's own contract is that `'end'` never
fires while there's still unread buffered data - a paused-mode consumer
needs `read()` to see every last byte first. `readerLoop`'s EOF branch
now only fires `'end'` (and destroys) immediately if the buffer already
happens to be empty; otherwise it defers to whichever of `read()` or
`startFlowing` next drains the buffer to empty, which is where the
actual `'end'` emission (and deferred destroy) now lives too, guarded
by `endEmitted` so the two possible triggers can't double-fire it. This
isn't a hypothetical edge case, either - a server that writes its whole
response and closes immediately (the common case for a small response)
routinely delivers the final chunk and EOF back to back, so a
paused-mode consumer that hasn't gotten around to calling `read()` yet
when EOF lands is the *normal* case, not a rare race. Verified directly
with a test built for exactly this ordering
(`TestNetSocketReadableEndDeferredUntilBufferDrained` - a server that
writes and closes immediately, a client that deliberately delays
calling `read()` past that point via `setTimeout`) and, since a test
that merely didn't fail isn't the same as a test that would have caught
a regression, confirmed causally: temporarily reverting just this
deferred-end logic back to firing `'end'` unconditionally on EOF made
this exact test fail (`{"result":"","endFired":true}` instead of the
full payload) before the revert was itself reverted.

New tests, alongside the one above:
`TestNetSocketReadableProtocolDrivesParserStyleConsumer` (drives a real
socket with real undici's *exact* protocol - `'readable'` + `read()`
loop, never `'data'` - and asserts the bytes arrive byte-exact, the
direct regression guard for the Round 76/77 hang's actual root cause),
`TestNetSocketReadWithSizeLeavesRemainderQueued` (a sized `read(n)`
leaves the correct remainder queued for the next call), and
`TestNetSocketDataListenerStillWorksAlongsideReadableFix` (the
opposite-direction regression guard - adding paused-mode support must
not break existing push-mode/`'data'` consumers, and once a `'data'`
listener switches a socket to flowing, `'readable'` correctly stops
firing at all, matching real Node).

**Verified against the real thing, not just unit tests**: with
`declareUndici()` disabled (per the Round 77 entry's own documented
trap - forgotten and immediately re-caught once via a `pprof` dump
showing Go's own `net/http.persistConn` again this round; the fix for
forgetting this is apparently "read your own docs before probing," not
a code change) and a fresh, unmodified real undici@7.11.0, the exact
same repro that hung in Round 76/77 now genuinely delivers bytes to the
parser. Traced directly by temporarily instrumenting the vendored
`client-h1.js` (restored byte-for-byte afterward, confirmed via `diff`
against a saved copy) with `console.error` at `onHttpSocketReadable`,
`readMore()`, and the `socket.read()` call site: `typeof socket.read`
is now `"function"` (was `"undefined"`), `'readable'` fires, and
`socket.read()` returns the real 151-byte response - all previously
impossible. This is the concrete, traced proof that this round's fix
is correct and addresses the actual documented root cause, not just a
plausible-sounding one.

**The next blocker, isolated and filed rather than guessed at**: with
bytes now reaching the parser, `llhttp_execute` itself started
throwing, from inside the `wasm_on_status` host-import callback - a
new failure mode, one layer deeper than anything reached before. Traced
the actual thrown error (not assumed): `TypeError: undefined is not a
constructor`, from `client-h1.js`'s own `const FastBuffer =
Buffer[Symbol.species]` followed by `new FastBuffer(...)` inside that
callback. Isolated to a real, minimal, two-line, undici-free,
noderati-free repro directly in a real `paserati` checkout:
`Uint8Array[Symbol.species]` (and every other built-in constructor's)
is `undefined` - confirmed directly against real Node first
(`Object.getOwnPropertyDescriptor(Buffer, Symbol.species)` is a real
accessor there, returning an internal `FastBuffer extends Uint8Array`
class) before concluding paserati's behavior is wrong rather than
assuming it. This is genuine paserati core engine surface (`Symbol.species`
entirely unimplemented on any built-in constructor), not anything
`buffer.go` can work around correctly - a plausible-looking downstream
patch (defining `Buffer[Symbol.species]` to return `Buffer` itself) was
considered and deliberately rejected per advisor review: real Node's
own `Buffer[Symbol.species]` is provably *not* `Buffer` (verified
directly against real Node), and undici's whole reason for reading it
is to get something that specifically isn't the slower, more-checked
public `Buffer` constructor - patching around this one call site with
the wrong semantics would risk quietly breaking in a different way
later, for one probe iteration's worth of progress. Filed as
[paserati#381](https://github.com/nooga/paserati/issues/381) with the
minimal repro and the real-world undici call site that surfaces it.

One more finding worth recording precisely because it *didn't* need
fixing: the `resumeH1`/`socket.unref()` trace captured during this same
probe showed `kSize= 1` both times it was called - meaning
`socket.unref()` (the thing Round 76/77's ref/unref fix was originally,
wrongly, suspected of fixing) never actually ran in this exact repro at
all (it's gated on `kSize === 0`, no in-flight request). This
independently reconfirms last round's causal-ablation finding that the
ref/unref fix was always orthogonal to the Round 76 hang, from a
completely different angle (this round's trace) than the one that
found it originally (the ablation itself).

**Status**: real undici's own parser is now proven to receive every
byte of a real HTTP response through the exact protocol it actually
uses, in the exact real, unmodified npm package - the Readable-protocol
gap this whole diagnosis chain (Round 76 -> 77 -> 78) set out to close
is closed. Full real-undici `fetch()` end-to-end remains blocked, now
on paserati#381 (`Symbol.species`), one layer further into the same
onion this whole investigation has been peeling one bug at a time since
Round 76. Not pursued further this round - diagnosing and fixing the
Readable gap was this round's actual scope, and paserati#381 is now a
clean, filed, upstream-owned blocker exactly like #302/#372/#377 before
it, not something to chase further downstream.

**Verification**: `go build ./...`/`go vet ./...` clean, `gofmt`
clean. Full `internal/host` suite green (including all four new tests
above) except the same pre-existing, unrelated
`TestEventsAddAbortListener`. `declareUndici()` restored; the vendored
undici copy under the scratchpad used for tracing was diffed back to
byte-identical with its pre-instrumentation copy before being discarded
(it was never part of this repo to begin with).

## Round 79: paserati#381 pulled (Symbol.species, via #383) - Buffer's static prototype chain and setImmediate fixed, parser now completes a full response - blocked next on a new engine bug, paserati#384 (filed)

Picked back up exactly where Round 78 left off: paserati#381
(`Symbol.species` entirely unimplemented on any built-in constructor)
was fixed upstream by a large merged PR,
[paserati#383](https://github.com/nooga/paserati/issues/383)
(`79bcb5fc`), which also fixed subclass static-prototype propagation,
an `instanceof Function` gap, and Promise resolution/thenable
assimilation along the way. Pulled the sibling checkout, confirmed
directly before proceeding: `Uint8Array[Symbol.species] ===
Uint8Array` now holds and `new FastBuffer(new ArrayBuffer(8), 0, 4)` no
longer throws in vanilla paserati.

**First surprise: `Buffer[Symbol.species]` was still `undefined` after
the upstream fix landed.** Not a paserati regression - root-caused
directly (not assumed) by inspecting `Buffer.__proto__`, which was an
anonymous function, not `Uint8Array`. `buffer.go` had only ever wired
the *instance*-side prototype chain (`Buffer.prototype.__proto__ =
Uint8Array.prototype`, from an earlier round); the *constructor's own*
static `[[Prototype]]` (real Node's `Object.getPrototypeOf(Buffer) ===
Uint8Array`) was never set, so `Symbol.species` - inherited via static,
not instance, prototype chain - had nothing to inherit from. Fixed with
one call once the right paserati API was found by reading
`pkg/vm/proto.go`'s `TypeNativeFunctionWithProps` case:
`props.Properties.SetPrototype(uint8ArrayCtorVal)` on the constructor's
own `Properties` table (see [buffer.go](../internal/host/buffer.go)).
Verified directly (`Buffer.__proto__ === Uint8Array` now `true`) before
moving on.

Worth recording precisely because it's a deliberate non-fix: real
Node's actual `Buffer[Symbol.species]` is an internal, non-exported
`FastBuffer extends Uint8Array` class (confirmed directly against real
Node), not `Buffer` itself. After this fix, noderati's `Buffer`
inherits the default `%TypedArray%[Symbol.species]` getter (`return
this`), so `Buffer[Symbol.species] === Buffer` - a known, real semantic
gap from Node, left open rather than patched, because the actual real
undici call site (`new FastBuffer(arrayBuffer, offset, length)`, which
resolves to `new Buffer(...)` under this divergence) was confirmed
empirically to work fine for the real E2E trace below. Patching this to
fake a `FastBuffer`-shaped species was considered and rejected for the
same reason Round 78 rejected doing so before #381 was even filed:
guessing at a plausible-looking value without a real call site forcing
the exact right shape risks a subtler break later.

**Second gap: `setImmediate`/`clearImmediate` didn't exist as globals
at all.** Genuinely absent from paserati (confirmed via grep - unlike
`setTimeout`/`clearTimeout`, which come from paserati's own
`HostTimerInitializer`, no such global existed anywhere). Found because
real undici's `client-h1.js` calls the bare global
`setImmediate(() => client[kResume]())` directly and unconditionally
from `onMessageComplete()`, once a response finishes parsing - a real,
unavoidable call site, reachable for the first time only once the
Buffer fix above let parsing complete at all. Implemented in the new
[immediate_object.go](../internal/host/immediate_object.go) on top of a
paserati primitive that already existed but noderati had never used:
`AsyncRuntime.ScheduleMacrotask()`/`RunMacrotasks()`, already driven by
`DrainUntilIdle` internally. `clearImmediate()` uses the standard
cancelled-flag technique, since `ScheduleMacrotask` has no native
cancel. `timers.go`'s `node:timers` shim now re-exports both real
functions instead of the stale gap it used to document. Two new tests
in
[immediate_object_test.go](../internal/host/immediate_object_test.go)
guard the callback/args/cancellation contract without over-pinning the
one ordering real Node itself doesn't guarantee outside an I/O callback
(`setTimeout(fn,0)` vs `setImmediate()` from top-level scope) - the
first version of this test asserted an exact interleaving and failed
nondeterministically for exactly that reason; rewritten once diagnosed
to assert only what's actually guaranteed.

**Third gap, found immediately after: `Readable.push()`/`destroy()`/
`[Symbol.asyncIterator]` didn't exist on this codebase's own
`stream.js` shim's `Readable`.** Real undici's own
`lib/web/fetch/index.js` builds every fetch() response body as `new
Readable({ read: resume })`, pushes bytes into it directly from the
dispatch handler's `onData`/`onComplete` callbacks
(`this.body.push(bytes)` / `this.body.push(null)`), and consumes it via
`body[Symbol.asyncIterator]()` - all real, unavoidable call sites, not
hypothetical ones. Implemented the standard queue+waiters pattern for a
push-driven async iterator in [stream.go](../internal/host/stream.go):
`push()` either hands a chunk straight to a parked `next()` or buffers
it; `next()` returns a buffered chunk immediately or parks a Promise.
`'data'`/`'end'` still fire unchanged, so `pipe()` keeps working.
Deliberately not wired: the constructor's `read` option (real Node's
pull-based backpressure hook) - every real body exercised so far pushes
its entire content synchronously before anything awaits a chunk, so
there's never yet been a real gap to pull against; flagged honestly as
a known limit rather than glossed over.

**Verified against the real thing**: with `declareUndici()` disabled
per the Round 77 trap and a fresh, unmodified real undici@7.11.0
(`client-h1.js` traced via temporary `console.error` instrumentation,
restored byte-identical afterward - confirmed via `diff` against the
saved pre-instrumentation copy), the same repro that's been climbing
one layer per round since Round 76 got further than ever before: full
response parsing now genuinely completes -
`onHeadersComplete(200, false, true)` -> `onBody(len=49)` ->
`onMessageComplete()` -> `llhttp_execute returned 0` - the first time
in this entire investigation chain that a real HTTP response has been
fully parsed end-to-end by real undici's own wasm llhttp against
noderati.

**The next blocker: a genuine Go-level VM panic, not a JS-catchable
exception.** Immediately after the parse completed,
`events.go`'s own `EventEmitter.emit()` shim
(`for (const fn of list.slice()) fn.call(this, ...args);`, reached from
`onMessageComplete`'s listener dispatch) crashed the interpreter:
```
[VM PANIC] recovered: runtime error: index out of range [143] with length 35
```
recovered by the VM's own top-level `recover()` at `pkg/vm/vm.go:1591`,
originating at `pkg/vm/vm.go:1818`. This is a qualitatively different
class of bug from every other finding in this whole investigation - a
Go-level crash inside the interpreter (recovered, not a catchable JS
`TypeError`), in a frame with `RegisterSize=35`, crashing during the
for-of loop's exception-cleanup path (`OpIteratorCleanupAbrupt`/
`OpHandlePending`, `Handler 0: TryStart=155, TryEnd=189, HandlerPC=197`).

Rather than guess at the cause from this one large, deeply-nested
trace, isolated it methodically per advisor review, minimizing one
variable at a time until only the essential shape remained:
- A `for...of` + `.call(this, ...args)` + throwing-listener repro,
  first tried standalone in vanilla paserati - propagated as a normal
  catchable `PS4001 VM exception`, no panic, no swallow. Not it alone.
- The same shape wrapped in an outer `try/catch` and driven through
  `setImmediate` (matching the real call chain's Go->VM re-entry via
  `vmInst.Call` from a macrotask closure) - the `catch` block never
  ran, and *nothing* printed afterward: no panic, no exit code, no
  error, just silent truncation of everything after the throw.
- Reproduced identically via plain `Promise.resolve().then(...)`
  instead of `setImmediate` - ruling out anything specific to this
  round's own new `setImmediate` implementation.
- Reproduced identically in vanilla `paserati -no-typecheck`, no
  noderati involved at all.
- Stripped the `for...of` loop entirely, then the microtask/Promise
  context entirely - both fell away without affecting the outcome.
- Landed on a 6-line minimal repro: a bare top-level `try { fn.call(null,
  ...args); } catch (e) { ... }`, where `fn` throws and `args` is a
  spread array, **never enters the `catch` block at all** - the
  exception escapes past a handler whose try-range provably contains
  the throwing call site (confirmed directly from the `-bytecode`
  dump's own exception table). Bisected the exact trigger by toggling
  one dimension at a time: `.call(null)` (no args), `.call(null, 1)`
  (literal arg), `.apply(null, args)` (spread via apply instead of
  call), and `obj.method(...args)` (spread method call, non-`.call`
  form) **all catch correctly** - only the `.call(receiver,
  ...spreadArgs)` combination specifically breaks. Checked for a #383
  regression by checking out the commit immediately before it
  (`c6a66eda`) and rebuilding: reproduces there too, so this is a
  longstanding, pre-existing engine bug, not something #383 introduced.

Filed as
[paserati#384](https://github.com/nooga/paserati/issues/384) with the
minimal repro, the full bisection table, the bytecode/exception-table
evidence, and a flagged (not asserted) hypothesis connecting it to the
larger Go panic: both involve `OpSpreadCallMethod` throwing from inside
a call reached via this opcode, inside a live try range; the minimal
repro's smaller frame just skips the handler cleanly where the larger,
more complex real-world frame (for-of iterator state, abrupt-completion
cleanup) apparently corrupts a register index instead. Not asserted as
proven, since the panic hasn't itself been minimized to the same degree
- flagged honestly as a plausible shared root cause for the maintainer
to weigh, not stated as fact.

**Status**: real undici's own parser now genuinely completes a full
response parse against noderati for the first time ever, closing the
Readable-protocol and Symbol.species gaps this whole chain (Round 76 ->
77 -> 78 -> 79) has been peeling one layer at a time. Full real-undici
`fetch()` end-to-end remains blocked, now on paserati#384
(`.call()`+spread exception-handling bug), a clean, filed,
upstream-owned blocker exactly like #302/#372/#377/#381 before it. Not
pursued further this round - isolating and filing #384 was this
round's actual scope.

**Two follow-up checks, done before calling this round closed:**
1. The Buffer static-prototype-chain divergence noted above (species
   resolving to `Buffer` rather than a `FastBuffer`-shaped class) was
   verified for more than "didn't throw": `new
   Buffer[Symbol.species](arrayBuffer, 2, 4)` on an 8-byte buffer
   populated `[1..8]` returns a length-4 view of `[3,4,5,6]` - the
   offset/length slicing itself is correct, not just non-throwing.
2. `setImmediate`'s callback-invocation error is deliberately
   discarded (`immediate_object.go`) - checked directly whether that's
   a new gap or an existing one: a throwing `setTimeout(fn, 0)`
   callback exhibits the exact same silent behavior (no
   `uncaughtException`, no nonzero exit, nothing printed) via
   paserati's own `RunDueTimers()`. Same engine-level house pattern for
   every `DrainUntilIdle`-driven callback, not something this round's
   `setImmediate` introduced - documented in place rather than papered
   over with a one-off fix that would leave `setTimeout` still silently
   broken the same way.

**Verification**: `go build ./...` clean. Full `internal/host` suite
green (including the two new `setImmediate` tests) except the same
pre-existing, unrelated `TestEventsAddAbortListener`.
`declareUndici()` restored (was temporarily disabled for E2E probing,
per the Round 77-documented trap); the vendored undici copy under the
scratchpad used for tracing was diffed back to byte-identical with its
pre-instrumentation copy before being discarded (it was never part of
this repo to begin with).

## Round 80: paserati#385 pulled (fixes #382/#384) - implemented node:stream's isDisturbed, real undici's fetch() gets past exception handling and body-read guards - blocked next on a core TypedArray engine bug, paserati#386 (filed)

Picked up immediately where Round 79 left off:
[paserati#385](https://github.com/nooga/paserati/issues/385) merged,
closing both #382 (async function `[[Prototype]]`, only the
`.constructor`/`toString` half - the `.caller`/`.arguments`
compensation was deliberately left as real behavior, not a mask, per
the PR's own writeup) and #384 (the `.call(receiver, ...spreadArgs)`
exception-routing bug this project filed last round). Pulled the
sibling checkout (`79bcb5fc` -> `0522531c`) and verified #384's own
fix directly before doing anything else: last round's exact 6-line
minimal repro (`try { fn.call(null, ...args); } catch {...}`, `args` a
spread array) now correctly reaches its `catch` block.

**Re-ran the standing E2E probe with `declareUndici()` disabled** (per
the Round 77 trap) against real, unmodified undici@7.11.0: the Go-level
VM panic from Round 79 is gone, as expected. Progress moved one layer
further than ever before - the `fetch()` call itself now completes and
returns a response, and execution reaches `res.text()` before hitting
the next wall.

**Immediate next gap: `stream.isDisturbed` didn't exist.** Real
undici's `lib/core/util.js#isDisturbed` does `stream.isDisturbed(body)
|| body[kBodyUsed]` as part of every `consumeBody()` call (the shared
implementation behind `.text()`/`.json()`/`.arrayBuffer()`/`.blob()`) -
a real, unavoidable call on the very first body read. Missing it
entirely threw `undefined is not a function` immediately. Implemented
in [stream.go](../internal/host/stream.go): added a `_disturbed` flag
to this file's own `Readable` class (true once its async iterator has
actually been consumed, or once a live `'data'` listener was attached
at push time - not merely once bytes have been buffered), and exported
a module-level `isDisturbed(stream)` reading that flag. This doesn't
reproduce real Node's exact `readableDidRead` semantics in every
corner case, but is correct for every real call site exercised so far
(`consumeBody` checks it once, before its own read begins, as a guard
against the body already having been disturbed by something *other*
than the read it's about to do).

**Next gap after that: response bodies read back empty.** With
`isDisturbed` in place, `res.text()` no longer threw - but the returned
text was consistently empty, traced (not assumed) down to
`ReadableStreamFrom` (undici's own `core/util.js`, used to wrap this
project's Node-style `Readable` into a real WHATWG `ReadableStream` for
consumption via `.getReader()`) doing `new Uint8Array(buf)` on each
chunk read from the async iterator, where `buf` is a `Buffer` (a
`Uint8Array` subclass). Isolated with a minimal, undici-free,
noderati-free repro directly in vanilla `paserati`:

```js
const src = new Uint8Array([1, 2, 3]);
const copy = new Uint8Array(src);
console.log(copy.length); // 0 - every engine but this one prints 3
```

Bisected the same way as #384: this is neither a `Buffer`-specific
issue nor a `Uint8Array`-specific one - `new
AnyTypedArray(otherTypedArray)` (same-type *and* cross-type: tried
`Uint8Array`/`Int8Array`/`Uint16Array`/`Int32Array`/`Float64Array` in
combination) always returns a zero-length array, while the `number`,
plain-`Array`, and `ArrayBuffer` constructor overloads all work
correctly - only the "construct from another typed array" spec
overload (`%TypedArray%(typedArray)`) is broken. Filed as
[paserati#386](https://github.com/nooga/paserati/issues/386) with the
bisection table; flagged in the report as likely to affect anything
else in this codebase doing ordinary typed-array copying, not just
this one call site, since it fails silently (empty, not a throw)
rather than loudly.

**Follow-up checks, done before calling this round closed** (per
advisor review of the draft above):

1. **Which object actually reaches `isDisturbed` on the real path -
   traced, not assumed.** Temporarily logged the argument's
   `constructor.name`: on the real `fetch()`-response path it's
   paserati's own built-in WHATWG `ReadableStream`, not this file's
   `Readable` - undici's `ReadableStreamFrom` wraps a `Readable` into
   one before any of this project's own code sees it again, and
   `consumeBody()`/`bodyUnusable()` both check the wrapped web stream.
   A bare `ReadableStream` has no `_disturbed` property, so the
   function always answers `false` there - the right answer for an
   undisturbed body, but it means *existence* of the function is what
   actually unblocked `consumeBody()` this round, not the flag-tracking
   logic. That logic isn't dead code, though: `extractBody()` (building
   a body *from* a `Readable`, e.g. as `RequestInit.body`) checks the
   pre-wrap async-iterable directly, which does see this file's real
   flag - not exercised by this round's GET-only probe, left in place
   for that real, just not-yet-hit, call site. Corrected the code
   comment in [stream.go](../internal/host/stream.go) to say this
   precisely instead of implying both paths were verified.
2. **`isErrored` was the other half of the same `require('node:stream')`
   destructure** (`const { isErrored, isDisturbed } =
   require('node:stream')` in undici's own `core/util.js`) - not yet
   hit (paserati#386 blocks first), but the same class of gap one step
   ahead. Added alongside `isDisturbed` rather than waiting to
   rediscover it in Round 81.
3. **`.set()`/`.slice()` checked against paserati#386's typed-array-copy
   bug**, since they're the same "read elements out of a typed-array
   source" operation as the broken constructor overload - both work
   correctly. Confirmed the bug is narrowly scoped to the constructor
   overload and recorded that on the issue.
4. **A real diagnostic side-finding, chased down rather than left as a
   loose end**: `JSON.stringify({v: someBuffer})` was silently
   producing `{"v":null}` instead of real Node's `{"v":{"type":"Buffer",
   "data":[...]}}`. Discriminated which repo owns it with one command
   (`typeof buf.toJSON`): `undefined` - a genuine noderati gap, `Buffer`
   never had a `toJSON()` at all. Fixed directly in
   [buffer.go](../internal/host/buffer.go) (matches real Node's
   `{type: "Buffer", data: [...]}` shape exactly). But `toJSON()`
   existing wasn't enough - `JSON.stringify({v: bufferWithToJSON})`
   *still* printed `null` even with a real `toJSON()` defined, which
   moved the question upstream: verified directly against real Node
   (`node -e`) that a typed array both without `toJSON()` (serializes
   by its own indexed properties, `{"0":1,"1":2,...}`) and with one
   (calls it, like any other object) behave nothing like paserati's
   `null` in either case, and confirmed in vanilla `paserati` this
   isn't Buffer-specific - `JSON.stringify()` on any typed array
   (subclass or not) always returns `null`, ignoring `toJSON()`
   entirely. Filed as
   [paserati#387](https://github.com/nooga/paserati/issues/387).

**Status**: two more real layers of the same onion peeled this round -
exception handling through spread `.call()` and the body-read
disturbed-check guard are both now correct against real undici. Full
real-undici `fetch()` end-to-end remains blocked, now on paserati#386
(typed-array copy constructor), a clean, filed, upstream-owned blocker
exactly like #302/#372/#377/#381/#384 before it. Not pursued further
this round - isolating and filing #386 (plus the #387 side-finding it
led to) was this round's actual scope.

**Verification**: `go build ./...` clean. Full `internal/host` suite
green, same pre-existing unrelated `TestEventsAddAbortListener`
excluded. `declareUndici()` restored (was temporarily disabled for E2E
probing, per the Round 77-documented trap).

## Round 81: paserati#388 pulled (fixes #386/#387) - real undici's fetch() works end to end for the first time ever - a new, high-impact engine bug found on the very next probe, paserati#389 (filed)

[paserati#388](https://github.com/nooga/paserati/issues/388) merged,
fixing both #386 (`new TypedArray(otherTypedArray)` always zero-length)
and #387 (`JSON.stringify(typedArray)` always `null`) in one PR. Pulled
the sibling checkout (`0522531c` -> `07014ab3`) and verified both fixes
directly against last round's own stored minimal repros before doing
anything else - both now produce the exact expected output.

**Re-ran the standing E2E probe** (`declareUndici()` disabled per the
Round 77 trap, real unmodified undici@7.11.0) against the local Go test
server: **`STATUS=200` and the correct response body text** - real
undici's own `fetch()` now works end-to-end against noderati, for the
first time in this entire investigation chain (Round 76 -> 77 -> 78 ->
79 -> 80 -> 81). Every layer this chain has peeled - the Readable
push-protocol gap, `Symbol.species`, the `.call(...spread)`
exception-routing bug, the body-read guards, the typed-array-copy
constructor - was a real, necessary link in the same chain; this is the
first round where the chain actually completed for a basic request.

**Immediately stress-tested rather than declaring victory on one
request**: wrote a script covering repeated `GET`s, a 100KB body,
`res.json()`, and a `POST` with a request body, all against the same
test server. Execution never reached past the third call in that
script, though - correcting an overstatement in an earlier draft of
this entry: only the first two repeated `GET`s are what actually ran
and succeeded (identically to the first probe) before the third request
triggered a genuine **new bug** - a VM stack overflow, thousands of
frames deep, every frame named `clearTimeout` at the exact same source
line (undici's own `lib/util/timers.js:361`). The 100KB body,
`res.json()`, and `POST`-with-body cases were written but not yet
exercised by that run; each is a distinct code path that could hide its
own blocker the same way `isDisturbed`/#386/#389 each hid behind the
one before it, so none of the three should be assumed clear until
checked directly (they are, below).

**Traced (not assumed) straight to the real source line**, then
isolated methodically rather than guessed at:

```js
// undici's own lib/util/timers.js, module.exports:
clearTimeout (timeout) {
  if (timeout[kFastTimer]) {
    timeout.clear()
  } else {
    clearTimeout(timeout) // intends the OUTER/global clearTimeout
  }
}
```

This is an ES6 object-literal **shorthand method** named `clearTimeout`
whose body calls the bare identifier `clearTimeout` as a deliberate
fallback to the real global timer function - a shorthand method has no
self-binding by spec (unlike a *named* function expression, which
does), so this bare reference should skip right past this method and
reach the actual global. Reproduced the exact shape standalone in
vanilla `paserati`, no undici/noderati involved:

```js
function foo(x) { return "outer foo called with " + x; }
const obj = { foo(x) { return foo(x); } }; // should call the OUTER foo
obj.foo(42); // hangs - infinite recursion into itself instead
```

Bisected against the two other property-value forms that could
plausibly carry the same self-binding behavior: a regular (non-arrow,
non-shorthand) `function` expression assigned as a property value, and
an arrow function assigned as a property value. Both correctly throw
`ReferenceError: foo is not defined` when no real outer `foo` exists -
proving the compiler does *not* generally invent a self-binding for a
property's own name in those forms. Only the ES6 shorthand-method
syntax specifically does it wrongly. Filed as
[paserati#389](https://github.com/nooga/paserati/issues/389) with the
full bisection and the real undici call site - flagged as likely to
affect other real npm packages too, since "a shorthand method that
falls back to an outer/global function of the same name" (exactly
undici's own pattern here) is a common, unremarkable idiom, not
something unusual to this one file.

**Checked the three not-yet-exercised cases directly** (per advisor
review of an earlier draft, rather than leaving them as an assumption):
ran `/big`, `/json`, and the `POST`-with-body case each as its own
single-request process (isolating them from whatever accumulated state
across requests made `clearTimeout`'s bad branch reachable only on the
third `GET`). `/big` and `/json` didn't just avoid #389's bad branch -
they hit a **second, different infinite recursion**, this time
genuinely noderati's own bug, not paserati's: a VM stack overflow with
every frame alternating `emit` -> `destroy` -> `onError`. Traced to
real undici's own `lib/web/fetch/index.js`, which registers a body's
own error handler as `this.body.on('error', onError)`, where `onError`
itself calls `this.body.destroy(error)` - a real, unavoidable pairing.
This project's own `Readable.destroy()`
([stream.go](../internal/host/stream.go)) had no re-entrancy guard:
every call unconditionally re-emitted `'error'`, which re-invoked the
very listener that had just called `destroy()`, which called it again
- forever. Real Node's own `Readable.destroy()` has exactly this guard
(a `destroyed` flag that makes every call after the first a no-op for
emission purposes) for exactly this reason - a stream's own error/close
handling calling `destroy()` again on an already-destroyed stream is
normal, expected usage, not misuse. Fixed directly (this is noderati's
own code, not an engine bug) with a `destroyed` flag, and added
[`TestReadableDestroyIsReentrancySafe`](../internal/host/stream_test.go)
driving the exact real shape (a listener that calls `destroy()` again
from inside the handler `destroy()` itself invoked) with a hard test
timeout, so a regression here fails loudly instead of hanging the
suite.

With that fixed, `/big` and `/json` (as isolated single-request
processes) now both genuinely pass: the 100KB body round-trips byte-for
-byte (`len=100000`, content verified, not just length), and
`res.json()` correctly parses a real JSON response body. The `POST`
-with-body case hit a **third, distinct blocker** - not a noderati bug
this time: `e.cause.message` traced directly to `TypeError: undefined
is not a function` at undici's own `cloneBody` (`body.js:294`,
`body.stream.tee()`), called from `cloneRequest`, which
`httpNetworkOrCacheFetch` runs on every real fetch() carrying a request
body. Confirmed directly and minimally in vanilla `paserati`:
`ReadableStream.prototype.tee` doesn't exist at all - `typeof
rs.tee === "undefined"` on any instance, not a subtle behavioral gap
like every other finding this investigation has hit, just an entirely
unimplemented spec method. Filed as
[paserati#390](https://github.com/nooga/paserati/issues/390).

**Status**: a genuine end-to-end milestone this round - real undici's
`fetch()` completing a full request/response cycle against noderati for
the first time. Two *independent* bugs were found by deliberately
stress-testing past that first success rather than stopping there, and
they don't compose the way an earlier draft of this entry implied - the
`destroy()` fix did not "clear a path blocked by #389"; the two are
unrelated bugs on different code paths that happened to both be
reachable from this round's stress script. Precisely, as of this
round's end: a single-request `/big` fetch (100KB, content-verified)
and a single-request `/json` fetch both now genuinely work, thanks to
this round's own `destroy()` fix. Sending a request body is blocked on
paserati#390 (`ReadableStream.tee()`, unimplemented). Repeated
sequential requests *within one process* are separately blocked on
paserati#389 (`clearTimeout` self-recursion, filed, not yet fixed) -
the third of three sequential `GET`s in one process still overflows;
this was not resolved by anything done this round. Deliberately
stress-tested past the first success and past an inaccurate first draft
of this very entry (corrected here, and via a follow-up comment on
#389 itself, after advisor review caught both), in the same "verify
against the real thing, not just the first result" spirit every round
in this chain has used.

**Verification**: `go build ./...` clean. Full `internal/host` suite
green (including the new re-entrancy test, timeout-guarded), same
pre-existing unrelated `TestEventsAddAbortListener` excluded.
`declareUndici()` restored (was temporarily disabled for E2E probing,
per the Round 77-documented trap). One real noderati-side fix this
round (`Readable.destroy()`'s re-entrancy guard); two upstream paserati
bugs found and filed (#389, #390); the pull of #388 itself needed no
downstream adjustment.
