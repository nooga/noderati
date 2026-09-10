package host

import (
	"io"
	"strings"

	"github.com/nooga/paserati/pkg/modules"
)

var jsShimRegistry = map[string]string{}

// registerJSShim adds an ESM shim served by JSShimResolver for specifier and node: alias.
func registerJSShim(specifier, source string) {
	jsShimRegistry[specifier] = source
	if !strings.HasPrefix(specifier, "node:") {
		jsShimRegistry["node:"+specifier] = source
	}
}

// JSShimResolver serves tiny ESM shims for node: builtins backed by globals.
type JSShimResolver struct {
	priority int
	shims    map[string]string
}

func NewJSShimResolver() *JSShimResolver {
	src := `const p = globalThis.process;
export default p;
export const env = p.env;
export const argv = p.argv;
export const platform = p.platform;
export const arch = p.arch;
export const version = p.version;
export const pid = p.pid;
export const stdout = p.stdout;
export const stderr = p.stderr;
export const cwd = function () { return p.cwd(); };
export const nextTick = function (fn) { return p.nextTick(fn); };
export const exit = function (code) { return p.exit(code); };
`
	shims := map[string]string{
		"process":      src,
		"node:process": src,
	}
	for k, v := range jsShimRegistry {
		shims[k] = v
	}
	return &JSShimResolver{
		priority: -50,
		shims:    shims,
	}
}

func (r *JSShimResolver) Name() string  { return "JSShim" }
func (r *JSShimResolver) Priority() int { return r.priority }

func (r *JSShimResolver) CanResolve(specifier string) bool {
	_, ok := r.shims[specifier]
	return ok
}

func (r *JSShimResolver) Resolve(specifier string, _ string) (*modules.ResolvedModule, error) {
	source, ok := r.shims[specifier]
	if !ok {
		return nil, nil
	}
	// ResolvedPath is the module registry's real cache key (per
	// pkg/driver, modules are cached/reused by ResolvedPath, not by the
	// specifier string a particular import used) - canonicalizing the
	// "node:" prefix away here means `import x from "events"` and
	// `import x from "node:events"` resolve to the exact same cached
	// module instance, not two independently-evaluated ones. Found the
	// hard way while making stream.go import the real EventEmitter from
	// "events" instead of hand-rolling its own copy (docs/real-node-plan.md):
	// without this, `new Readable() instanceof EventEmitter` came back
	// false whenever the EventEmitter on the right-hand side had been
	// imported via "node:events" instead - two textually-identical
	// `class EventEmitter` bodies, evaluated twice, are two different
	// class objects with no instanceof relationship, exactly the bug
	// real Node's own `node:` prefix is defined to never have (it's a
	// plain alias for the same builtin, not a separate module).
	canonical := strings.TrimPrefix(specifier, "node:")
	return &modules.ResolvedModule{
		Specifier:    specifier,
		ResolvedPath: "noderati-shim:" + canonical,
		Source:       io.NopCloser(strings.NewReader(source)),
		Resolver:     r.Name(),
	}, nil
}
