package host

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/nooga/paserati/pkg/modules"
)

// NodeMissingResolver is the last-resort resolver in the chain (priority
// 200 - every other resolver in host.go's New() is registered at -50, -10,
// 0, or 50, so this one is always tried last, only once nothing real could
// resolve the specifier). It exists to turn paserati's own generic "no
// resolver could handle specifier: X" into the same shape of error real
// Node raises for an unresolvable import - a missing dependency should
// read like a missing dependency, not an internal engine message, and it
// should name what tried to import it the way Node's own message does.
//
// Node itself uses three different error shapes here depending on what
// kind of specifier failed to resolve, and this resolver mirrors each:
//
//   - "node:xxx" reaching here -> code ERR_UNKNOWN_BUILTIN_MODULE,
//     "No such built-in module: node:xxx" (no quoting, no "imported
//     from" - confirmed against real Node directly for a genuinely
//     nonexistent name; an earlier version of this resolver used
//     ERR_MODULE_NOT_FOUND's "Cannot find module" wording here instead,
//     which is real Node's message for a *different* case (see below)
//     and was never actually Node's message for this one - see
//     docs/real-node-plan.md's Phase 4 section). One honest caveat:
//     this resolver can't tell "node:xxx isn't a real Node builtin at
//     all" from "node:xxx is a real Node builtin noderati just hasn't
//     implemented yet" (e.g. `node:dgram`, a tracked gap; `node:net`/
//     `node:tls` were the same kind of gap until round 69, see
//     docs/real-node-plan.md) -
//     doing that would mean maintaining a list of every real builtin
//     name. Real Node would never error on the latter case at all (the
//     module genuinely exists there), so this message is only precise
//     for the former; for the latter it at least says plainly that
//     *this* runtime doesn't have it, which is still true and still an
//     improvement over the previous wrong-error-shape message, just
//     not a byte-exact match to what real Node would do (nothing).
//   - a relative (`.`/`..`) or absolute (`/`) specifier -> code
//     ERR_MODULE_NOT_FOUND, "Cannot find module '<absolute path>' imported
//     from <fromPath>" - the path is resolved relative to fromPath's
//     directory the way Node's own resolver would, since Node's message
//     names the resolved path, not the raw specifier as written.
//   - anything else (an ordinary bare package specifier nothing could
//     resolve) -> code ERR_MODULE_NOT_FOUND, "Cannot find package 'xxx'
//     imported from <fromPath>".
//
// The generated module body throws at its own top level rather than
// returning a paserati-level resolution error, matching how ES module
// linking actually surfaces this in both engines: a *static* import of an
// unresolvable specifier fails eagerly, before any of the importing
// module's own code runs (verified directly - a script that imports a
// missing module and only prints something before the import statement
// never reaches that print). A specifier reached only through a
// conditional/lazy `require()` that's never actually called correctly
// never resolves at all in either engine, which this preserves.
type NodeMissingResolver struct {
	priority int
}

func NewNodeMissingResolver() *NodeMissingResolver {
	return &NodeMissingResolver{priority: 200}
}

func (r *NodeMissingResolver) Name() string  { return "NodeMissing" }
func (r *NodeMissingResolver) Priority() int { return r.priority }

// CanResolve always returns true: by priority 200 this resolver is tried
// dead last, after every other registered resolver already had its shot.
// Anything that reaches here is genuinely unresolvable - Node itself
// doesn't distinguish specifier shapes ahead of time either, resolution
// just fails once every real strategy has been exhausted.
func (r *NodeMissingResolver) CanResolve(specifier string) bool {
	return true
}

func (r *NodeMissingResolver) Resolve(specifier string, fromPath string) (*modules.ResolvedModule, error) {
	code, msg := missingModuleError(specifier, fromPath)
	src := fmt.Sprintf(
		"const e = new Error(%q); e.code = %q; throw e;",
		msg, code,
	)
	return &modules.ResolvedModule{
		Specifier:    specifier,
		ResolvedPath: "noderati-missing:" + specifier,
		Source:       io.NopCloser(strings.NewReader(src)),
		Resolver:     r.Name(),
	}, nil
}

// missingModuleError builds the Node-shaped (code, message) pair for a
// specifier nothing could resolve. See NodeMissingResolver's doc comment
// for the three cases and how each was confirmed against real Node.
func missingModuleError(specifier, fromPath string) (code, msg string) {
	if strings.HasPrefix(specifier, "node:") {
		return "ERR_UNKNOWN_BUILTIN_MODULE", "No such built-in module: " + specifier
	}

	if strings.HasPrefix(specifier, ".") || strings.HasPrefix(specifier, "/") {
		resolved := specifier
		if strings.HasPrefix(specifier, ".") {
			dir := "."
			if fromPath != "" && fromPath != "." {
				dir = filepath.Dir(fromPath)
			}
			if abs, err := filepath.Abs(filepath.Join(dir, specifier)); err == nil {
				resolved = abs
			}
		} else if abs, err := filepath.Abs(specifier); err == nil {
			resolved = abs
		}
		msg = fmt.Sprintf("Cannot find module '%s'", resolved)
		if fromPath != "" && fromPath != "." {
			msg += " imported from " + fromPath
		}
		return "ERR_MODULE_NOT_FOUND", msg
	}

	msg = fmt.Sprintf("Cannot find package '%s'", specifier)
	if fromPath != "" && fromPath != "." {
		msg += " imported from " + fromPath
	}
	return "ERR_MODULE_NOT_FOUND", msg
}
