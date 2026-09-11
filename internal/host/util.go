package host

import (
	"fmt"
	"os"
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
		// debuglog(section) was missing entirely - found the hard way
		// while probing real undici (round 69, docs/real-node-plan.md):
		// lib/core/diagnostics.js calls it unconditionally at module load
		// (`util.debuglog('undici')`), so a missing export there threw a
		// TypeError before undici's own dispatch code ever ran. Real
		// Node's version is a genuine, honestly-gated no-op by default:
		// the returned function only actually prints when the section
		// name appears (case-insensitively) in NODE_DEBUG - checked for
		// real here, not just accepted-and-ignored - and stays silent
		// otherwise, matching every real caller's expectation that this
		// is inert unless a developer explicitly opted in.
		m.Function("debuglog", func(section string) vm.Value {
			enabled := false
			for _, s := range strings.Split(os.Getenv("NODE_DEBUG"), ",") {
				if strings.EqualFold(strings.TrimSpace(s), section) {
					enabled = true
					break
				}
			}
			fn := vm.NewNativeFunctionWithProps(-1, true, "debug", func(args []vm.Value) (vm.Value, error) {
				if enabled {
					fmt.Fprintln(os.Stderr, strings.ToUpper(section)+":", formatValues(args))
				}
				return vm.Undefined, nil
			})
			if props := fn.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
				props.Properties.SetOwn("enabled", vm.BooleanValue(enabled))
			}
			return fn
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:util", "util")
}

// installUtilNatives adds `util.types` and `util.promisify` after the
// module itself is declared - both need direct vm.Value/VM access
// ModuleBuilder's reflection-based Function() can't easily give a
// closure (raw ValueType inspection for types.*, resolve/reject wiring
// for promisify), so they're built the same post-processing way
// buffer.go's installBufferGlobal adds Buffer.byteLength: load the
// already-declared module, mutate its export map directly, then rebuild
// "default" from the mutated map so both `import util from "util"` and
// CJS `require("node:util")` (which hands back exactly this "default"
// value - see cjs.go's requireNative) see the new members too.
//
// Found missing while probing real undici (round 72, docs/real-node-plan.md):
// undici's own lib/mock/mock-utils.js does
// `const { types: { isPromise } } = require('node:util')` - a *nested*
// destructure that throws "Cannot destructure 'undefined'" the instant
// `types` itself is missing, not just a silently-undefined `isPromise`.
func installUtilNatives(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	rec, err := p.LoadModule("util", ".")
	if err != nil {
		return
	}
	exports := rec.GetExportValues()

	// util.types: only the predicates real code in this dependency tree
	// actually destructures (isPromise/isProxy/isArrayBuffer/
	// isSharedArrayBuffer/isAnyArrayBuffer/isArrayBufferView/isDataView/
	// isTypedArray - grepped directly across undici and pi-coding-agent's
	// own dist before writing this, not guessed from Node's full ~30-name
	// list). Each maps onto one of paserati's own dedicated ValueType
	// tags (pkg/vm/value.go's TypePromise/TypeProxy/TypeArrayBuffer/
	// TypeSharedArrayBuffer/TypeTypedArray/TypeDataView), so these are
	// real, exact engine-level checks - not a heuristic guess via
	// Object.prototype.toString. Node's other `types.is*` predicates
	// (isDate, isMap, isSet, isRegExp, isNativeError, isAsyncFunction,
	// the boxed-primitive checks, ...) aren't built: nothing reachable
	// here calls them, and most of them would need a real check against
	// paserati's internal-slot machinery to be honest rather than a
	// toString-tag guess - add them the same way, per-predicate, if and
	// when something reachable actually needs one.
	typesObj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	addTypePredicate := func(name string, want ...vm.ValueType) {
		typesObj.SetOwn(name, vm.NewNativeFunction(1, false, name, func(args []vm.Value) (vm.Value, error) {
			if len(args) == 0 {
				return vm.False, nil
			}
			t := args[0].Type()
			for _, w := range want {
				if t == w {
					return vm.True, nil
				}
			}
			return vm.False, nil
		}))
	}
	addTypePredicate("isPromise", vm.TypePromise)
	addTypePredicate("isProxy", vm.TypeProxy)
	addTypePredicate("isArrayBuffer", vm.TypeArrayBuffer)
	addTypePredicate("isSharedArrayBuffer", vm.TypeSharedArrayBuffer)
	addTypePredicate("isAnyArrayBuffer", vm.TypeArrayBuffer, vm.TypeSharedArrayBuffer)
	addTypePredicate("isDataView", vm.TypeDataView)
	addTypePredicate("isTypedArray", vm.TypeTypedArray)
	addTypePredicate("isArrayBufferView", vm.TypeTypedArray, vm.TypeDataView)
	// isUint8Array specifically (not "any typed array") - real undici's
	// lib/web/fetch/util.js and body.js both destructure exactly this
	// one, not the generic isTypedArray, so element type has to be
	// checked, not just the TypeTypedArray tag alone.
	typesObj.SetOwn("isUint8Array", vm.NewNativeFunction(1, false, "isUint8Array", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.False, nil
		}
		ta := args[0].AsTypedArray()
		if ta == nil {
			return vm.False, nil
		}
		return vm.BooleanValue(ta.GetElementType() == vm.TypedArrayUint8), nil
	}))
	exports["types"] = vm.NewValueFromPlainObject(typesObj)

	// Also exposed as globalThis.__noderatiUtilTypes so the separate
	// "node:util/types" module (util_types.go - a real, distinct Node
	// module of its own, not just util.go's own `.types` property; real
	// undici's lib/web/websocket/websocket.js does
	// `const { isArrayBuffer } = require('node:util/types')` directly)
	// can reuse the exact same predicate functions instead of
	// duplicating this logic.
	if gt, ok := vmInst.GetGlobal("globalThis"); ok {
		if gobj := gt.AsPlainObject(); gobj != nil {
			gobj.SetOwn("__noderatiUtilTypes", vm.NewValueFromPlainObject(typesObj))
		}
	}

	// promisify(original): a real implementation, not a stand-in -
	// wraps a Node-style `(...args, (err, ...results) => {})` callback
	// function into one returning a genuine Promise (built via
	// vmInst.NewPromiseFromExecutor, the same primitive paserati's own
	// `new Promise((resolve, reject) => {...})` uses), resolving with a
	// single value for one callback result or an array for more than
	// one, matching real util.promisify's own documented behavior.
	// Nothing reachable in this codebase's own tests currently calls the
	// wrapped function (undici's only real call site,
	// lib/mock/mock-client.js's `close()`, is mock-only and never
	// exercised by a real fetch), but this needs to actually work rather
	// than merely exist, so it's built for real rather than guessed at.
	exports["promisify"] = vm.NewNativeFunction(1, false, "promisify", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsCallable() {
			return vm.Undefined, vmInst.NewTypeError("The \"original\" argument must be of type function")
		}
		original := args[0]
		wrapped := vm.NewNativeFunction(-1, true, "promisified", func(callArgs []vm.Value) (vm.Value, error) {
			resolveFn := vm.Undefined
			rejectFn := vm.Undefined
			executor := vm.NewNativeFunction(2, false, "executor", func(rr []vm.Value) (vm.Value, error) {
				if len(rr) > 0 {
					resolveFn = rr[0]
				}
				if len(rr) > 1 {
					rejectFn = rr[1]
				}
				return vm.Undefined, nil
			})
			promise, perr := vmInst.NewPromiseFromExecutor(executor)
			if perr != nil {
				return vm.Undefined, perr
			}
			cb := vm.NewNativeFunction(-1, true, "callback", func(cbArgs []vm.Value) (vm.Value, error) {
				if len(cbArgs) > 0 && !cbArgs[0].IsUndefined() && cbArgs[0].Type() != vm.TypeNull {
					_, _ = vmInst.Call(rejectFn, vm.Undefined, []vm.Value{cbArgs[0]})
					return vm.Undefined, nil
				}
				result := vm.Undefined
				switch {
				case len(cbArgs) == 2:
					result = cbArgs[1]
				case len(cbArgs) > 2:
					arr := vm.NewArray()
					a := arr.AsArray()
					for _, v := range cbArgs[1:] {
						a.Append(v)
					}
					result = arr
				}
				_, _ = vmInst.Call(resolveFn, vm.Undefined, []vm.Value{result})
				return vm.Undefined, nil
			})
			fullArgs := append(append([]vm.Value{}, callArgs...), cb)
			if _, err := vmInst.Call(original, vm.Undefined, fullArgs); err != nil {
				if rejectFn.IsCallable() {
					_, _ = vmInst.Call(rejectFn, vm.Undefined, []vm.Value{errorValueFromGo(vmInst, err)})
				}
			}
			return promise, nil
		})
		return wrapped, nil
	})

	// util.TextEncoder/util.TextDecoder: real Node re-exports the global
	// WHATWG constructors here too (a long-standing legacy alias -
	// `require('util').TextEncoder === TextEncoder` is `true` in real
	// Node), and real code still reaches for them this way rather than
	// the global. Found the hard way probing real
	// `@silvia-odwyer/photon-node` (the WASM image-processing package
	// pi-coding-agent's real image-resize path depends on, per
	// docs/real-node-plan.md's "WASM-backed image resizing" open item):
	// its wasm-bindgen-generated glue does
	// `const { TextEncoder, TextDecoder } = require('util')` at module
	// top level, so a missing pair here threw "undefined is not a
	// constructor" before the module's own WASM instantiation ever ran -
	// the global constructors were never in question, only their
	// absence from this module's own exports.
	if te, ok := vmInst.GetGlobal("TextEncoder"); ok {
		exports["TextEncoder"] = te
	}
	if td, ok := vmInst.GetGlobal("TextDecoder"); ok {
		exports["TextDecoder"] = td
	}

	proto := vm.Undefined
	if vmInst != nil {
		proto = vmInst.ObjectPrototype
	}
	ns := vm.NewObject(proto).AsPlainObject()
	for name, val := range exports {
		if name == "default" {
			continue
		}
		ns.SetOwn(name, val)
	}
	exports["default"] = vm.NewValueFromPlainObject(ns)
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
