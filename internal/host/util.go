package host

import (
	"strconv"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

func declareUtil(p *driver.Paserati) {
	vmInst := p.GetVM()
	p.DeclareModule("util", func(m *driver.ModuleBuilder) {
		// format(fmt, ...args): real util.format %-directive substitution
		// (%s %d %i %f %j %o %O %%), with any args left over (either
		// beyond the directives, or all of them when the first argument
		// isn't a string) appended space-separated. This is what
		// `debug`'s node.js backend actually needs `formatWithOptions`
		// (below) to do for it - it deliberately leaves directives like
		// %s unconsumed in its own %-scan specifically so this call
		// expands them against the trailing args.
		m.Function("format", func(args ...vm.Value) string {
			return formatValues(args)
		})
		m.Function("inspect", func(v string) string {
			return v
		})
		// formatWithOptions(inspectOptions, ...args): real Node applies
		// inspectOptions only to how %o/%O render objects. We don't have
		// a real util.inspect here (see `inspect` above - a passthrough),
		// so inspectOptions is accepted and ignored; the %-directive
		// substitution itself is real and shared with `format`.
		m.Function("formatWithOptions", func(_ vm.Value, args ...vm.Value) string {
			return formatValues(args)
		})
		// deprecate(fn, msg): real Node wraps fn so the first call emits
		// a one-time warning before running fn. We deliberately don't
		// emit anything here (msg is accepted only to match the real
		// signature) - this host has no `process.emitWarning`/`warning`
		// event machinery for anything to consume, and printing our own
		// ad-hoc line to stderr on first call would just be surprise
		// output in the middle of an otherwise-quiet CLI run for
		// whichever caller happens to trigger it first. What matters for
		// callers (e.g. `debug`'s `t.destroy`, which calls this just to
		// build a wrapper, not to warn anyone) is that the returned
		// value stays callable and behaves exactly like fn - which it
		// does.
		m.Function("deprecate", func(fn vm.Value, _ string) vm.Value {
			return vm.NewNativeFunction(-1, true, "deprecated", func(args []vm.Value) (vm.Value, error) {
				return vmInst.Call(fn, vm.Undefined, args)
			})
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:util", "util")
}

// formatValues implements util.format's actual semantics: %s/%d/%i/%f/%j/%o/%O/%%
// substitution against a leading string template, consuming one trailing
// argument per directive, with anything left over (unconsumed directive
// args, or every arg when the template isn't a string) appended
// space-separated.
func formatValues(args []vm.Value) string {
	if len(args) == 0 {
		return ""
	}
	first := args[0]
	if !first.IsString() {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.ToString()
		}
		return strings.Join(parts, " ")
	}

	template := first.AsString()
	rest := args[1:]
	var sb strings.Builder
	argIdx := 0
	for i := 0; i < len(template); i++ {
		c := template[i]
		if c != '%' || i+1 >= len(template) {
			sb.WriteByte(c)
			continue
		}
		spec := template[i+1]
		if spec == '%' {
			sb.WriteByte('%')
			i++
			continue
		}
		if strings.IndexByte("sdifjoO", spec) < 0 || argIdx >= len(rest) {
			sb.WriteByte(c)
			continue
		}
		v := rest[argIdx]
		argIdx++
		i++
		switch spec {
		case 's':
			sb.WriteString(v.ToString())
		case 'd', 'i':
			sb.WriteString(strconv.FormatInt(int64(v.ToFloat()), 10))
		case 'f':
			sb.WriteString(strconv.FormatFloat(v.ToFloat(), 'g', -1, 64))
		case 'j', 'o', 'O':
			// No real JSON.stringify/util.inspect available from Go
			// here; best-effort stringification is enough for what
			// currently reaches this (debug's own formatters handle
			// %o/%O themselves before this ever sees them).
			sb.WriteString(v.ToString())
		}
	}
	for ; argIdx < len(rest); argIdx++ {
		sb.WriteByte(' ')
		sb.WriteString(rest[argIdx].ToString())
	}
	return sb.String()
}
