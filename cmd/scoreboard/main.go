// Command scoreboard is the Phase 2 regression scoreboard
// (docs/real-node-plan.md): it runs the real, unmodified
// `pi-coding-agent@0.80.2` CLI's `--version`, `--help`, and a scripted
// `-p "hello"` invocation once per configuration, and records the first
// failure signature as a single line so "shrunk the gap" is measurable, not
// felt.
//
// Configurations: a baseline (every internal/host shim on), then each
// group-B fake disabled individually (via NODERATI_DISABLE_FAKES — see
// internal/host/scoreboard_config.go), diffed against the baseline. This is
// what made the Phase 1 close-out sweep (deleting esmpatch.go's patches, one
// by one, down to zero — the last one, sdk-reexports, went 2026-09-01 once
// paserati#163 made it genuinely unneeded) a measured decision per patch
// instead of a judgment call. esmpatch.go itself is gone now that it has no
// patches left to hold; NODERATI_DISABLE_PATCHES plumbing stays in
// runOnce/config below (harmless, always "") in case a future package quirk
// needs the mechanism back.
//
// Each invocation runs as its own subprocess (the real `noderati` binary,
// built fresh into a temp dir at startup) rather than in-process: pi's own
// `--version`/`--help` paths call process.exit(), which internal/host wires
// to the real os.Exit() — in-process that would kill the scoreboard tool
// itself on the very first run.
//
// Usage: go run ./cmd/scoreboard [path/to/pi-coding-agent/dist/cli.js]
// Must be run with the noderati repo root as the working directory (it
// shells out to `go build ./cmd/noderati` from cwd).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultTarget = "/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent/dist/cli.js"

const perRunTimeout = 20 * time.Second

// fakeNames mirrors installModules()'s ledger-group-B knobs.
//
// pi-ai and pi-agent-core's fakes were deleted 2026-09-05 (round 47/48 of
// docs/real-node-plan.md) — a real Fireworks end-to-end test with both
// fakes off returned correct completions 3/3 runs, the first time this
// coupled pair worked against a live backend. Their individual
// fake-off:* rows (and the fake-off:pi-ai+pi-agent-core combined row this
// tool used to also run, back when the pair had to be tested together to
// mean anything) are gone along with them — there's no toggle left to
// flip; node_modules resolution always loads both real packages now.
var fakeNames = []string{
	"jiti",
}

// esmpatch.go held per-rewrite knobs here (patchNames) until 2026-09-01,
// when its last patch (sdk-reexports) went the way of the ten before it —
// confirmed dead once paserati#163 made it genuinely unneeded, then the
// whole file was deleted rather than left holding zero patches. Nothing
// left to toggle patch-wise; config.patches/NODERATI_DISABLE_PATCHES below
// stay as harmless (always-"") plumbing in case that changes.

type invocation struct {
	label string
	args  []string // extra argv after the script path
}

var invocations = []invocation{
	{label: "version", args: []string{"--version"}},
	{label: "help", args: []string{"--help"}},
	{label: "print", args: []string{"-p", "hello"}},
}

// result is one invocation's outcome, reduced to a comparable signature.
type result struct {
	signature string
}

type config struct {
	label   string
	fakes   string // NODERATI_DISABLE_FAKES value
	patches string // NODERATI_DISABLE_PATCHES value
}

func main() {
	flag.Parse()
	target := defaultTarget
	if flag.NArg() >= 1 {
		target = flag.Arg(0)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		fatalf("%s", err)
	}
	if _, err := os.Stat(abs); err != nil {
		fatalf("target not found: %s\n%v", abs, err)
	}

	binPath, cleanup, err := buildNoderati()
	if err != nil {
		fatalf("building ./cmd/noderati: %v", err)
	}
	defer cleanup()

	configs := []config{{label: "baseline"}}
	for _, name := range fakeNames {
		configs = append(configs, config{label: "fake-off:" + name, fakes: name})
	}
	configs = append(configs, config{label: "all-fakes-off", fakes: "all"})

	var baseline map[string]result
	rows := make(map[string]map[string]result, len(configs))

	for _, c := range configs {
		runResults := make(map[string]result, len(invocations))
		for _, inv := range invocations {
			runResults[inv.label] = runOnce(binPath, abs, inv.args, c.fakes, c.patches)
		}
		rows[c.label] = runResults
		if c.label == "baseline" {
			baseline = runResults
		}

		var parts []string
		for _, inv := range invocations {
			r := runResults[inv.label]
			mark := "="
			if base, ok := baseline[inv.label]; ok && base.signature != r.signature {
				mark = "DIFF"
			}
			parts = append(parts, fmt.Sprintf("%s[%s]=%s", inv.label, mark, r.signature))
		}
		fmt.Printf("%-28s %s\n", c.label, strings.Join(parts, "  "))
	}

	fmt.Println()
	fmt.Println("Configurations that reproduce baseline on every invocation (candidates")
	fmt.Println("to actually delete, per docs/real-node-plan.md's measure-don't-assume rule):")
	var clean []string
	for _, c := range configs {
		if c.label == "baseline" {
			continue
		}
		same := true
		for _, inv := range invocations {
			if rows[c.label][inv.label].signature != baseline[inv.label].signature {
				same = false
				break
			}
		}
		if same {
			clean = append(clean, c.label)
		}
	}
	sort.Strings(clean)
	if len(clean) == 0 {
		fmt.Println("  (none)")
	}
	for _, label := range clean {
		fmt.Println("  " + label)
	}
}

func buildNoderati() (binPath string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "noderati-scoreboard-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(dir) }
	binPath = filepath.Join(dir, "noderati")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/noderati")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		cleanup()
		return "", nil, err
	}
	return binPath, cleanup, nil
}

// runOnce shells out to the real noderati binary as its own OS process (see
// package doc for why: process.exit() maps to the real os.Exit() and would
// otherwise kill the scoreboard tool itself). The signature is exit code
// plus a trimmed tail of combined stdout+stderr, so an expected app-level
// failure (e.g. `-p` dialing a local model with nothing listening) reads as
// the same signature run over run, and only a *changed* tail counts as DIFF.
func runOnce(binPath, scriptPath string, extraArgs []string, fakes, patches string) result {
	ctx, cancel := context.WithTimeout(context.Background(), perRunTimeout)
	defer cancel()

	args := append([]string{scriptPath}, extraArgs...)
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Env = append(os.Environ(),
		"NODERATI_DISABLE_FAKES="+fakes,
		"NODERATI_DISABLE_PATCHES="+patches,
	)
	out, err := cmd.CombinedOutput()

	exitCode := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return result{signature: fmt.Sprintf("TIMEOUT tail=%q", truncate(tail(string(out), 200), 200))}
		}
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			return result{signature: "EXEC_ERROR: " + err.Error()}
		}
	}
	return result{signature: fmt.Sprintf("exit=%d tail=%q", exitCode, truncate(tail(string(out), 200), 200))}
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " | ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "scoreboard: "+format+"\n", args...)
	os.Exit(1)
}
