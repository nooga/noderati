package host

import (
	_ "embed"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// inspectShim is util.inspect / util.format (inspect.js): a port of Node's
// lib/internal/util/inspect.js, loaded on first use.
//
//go:embed inspect.js
var inspectShim string

// utilMoreShim is the rest of node:util (util_more.js): MIMEType,
// MIMEParams, parseEnv, diff, getCallSites and friends.
//
//go:embed util_more.js
var utilMoreShim string

// declareInspect registers the inspect shim and the engine lookups it
// needs that plain JS can't do: V8 exposes these to Node's inspect through
// internalBinding('util').
func declareInspect(p *driver.Paserati) {
	registerJSShim("noderati-internal:inspect", inspectShim)
	registerJSShim("noderati-internal:util-more", utilMoreShim)
	p.DeclareModule("noderati-internal:inspect-natives", func(m *driver.ModuleBuilder) {
		// getPromiseDetails(p) -> [state, result]: 0 pending, 1 fulfilled,
		// 2 rejected (V8's kPending/kFulfilled/kRejected).
		m.Const("getPromiseDetails", vm.NewNativeFunction(1, false, "getPromiseDetails", func(args []vm.Value) (vm.Value, error) {
			v := argAt(args, 0)
			if v.Type() != vm.TypePromise {
				return vm.Undefined, nil
			}
			pr := v.AsPromise()
			state := pr.GetState()
			if state == vm.PromisePending {
				return vm.NewArrayWithArgs([]vm.Value{vm.IntegerValue(0)}), nil
			}
			code := int32(1)
			if state == vm.PromiseRejected {
				code = 2
			}
			return vm.NewArrayWithArgs([]vm.Value{vm.IntegerValue(code), pr.GetResult()}), nil
		}))
		// getProxyDetails(v, full): undefined for a non-proxy; the target
		// (null once revoked), or [target, handler] when full is true.
		m.Const("getProxyDetails", vm.NewNativeFunction(2, false, "getProxyDetails", func(args []vm.Value) (vm.Value, error) {
			v := argAt(args, 0)
			if v.Type() != vm.TypeProxy {
				return vm.Undefined, nil
			}
			px := v.AsProxy()
			target, handler := px.Target(), px.Handler()
			if px.Revoked {
				target, handler = vm.Null, vm.Null
			}
			if argAt(args, 1).IsTruthy() {
				return vm.NewArrayWithArgs([]vm.Value{target, handler}), nil
			}
			return target, nil
		}))
		// previewEntries(v, isIterator): the entries a Map/Set iterator
		// has yet to yield, without advancing it. With isIterator the
		// result is [entries, isKeyValue], entries flattened for key/value
		// iterators. Weak collections aren't enumerable here: undefined.
		m.Const("previewEntries", vm.NewNativeFunction(2, false, "previewEntries", func(args []vm.Value) (vm.Value, error) {
			v := argAt(args, 0)
			if v.Type() != vm.TypeObject {
				return vm.Undefined, nil
			}
			st := v.AsPlainObject().InternalIterState()
			if st == nil {
				return vm.Undefined, nil
			}
			var out []vm.Value
			keyValue := false
			if !st.Exhausted {
				switch st.Kind {
				case vm.IterKindMapKeys, vm.IterKindMapValues, vm.IterKindMapEntries:
					keyValue = st.Kind == vm.IterKindMapEntries
					for i := st.Index; i < st.M.OrderLen(); i++ {
						k, val, ok := st.M.GetEntryAt(i)
						if !ok {
							continue
						}
						switch st.Kind {
						case vm.IterKindMapKeys:
							out = append(out, k)
						case vm.IterKindMapValues:
							out = append(out, val)
						default:
							out = append(out, k, val)
						}
					}
				case vm.IterKindSetValues, vm.IterKindSetEntries:
					keyValue = st.Kind == vm.IterKindSetEntries
					for i := st.Index; i < st.S.OrderLen(); i++ {
						val, ok := st.S.GetValueAt(i)
						if !ok {
							continue
						}
						out = append(out, val)
						if keyValue {
							out = append(out, val)
						}
					}
				default:
					return vm.Undefined, nil
				}
			}
			entries := vm.NewArrayWithArgs(out)
			if argAt(args, 1).IsTruthy() {
				return vm.NewArrayWithArgs([]vm.Value{entries, vm.BooleanValue(keyValue)}), nil
			}
			return entries, nil
		}))
		// getOwnNonIndexKeys(v): own string and symbol keys that aren't
		// array indices, for arrays and typed arrays (without listing
		// every index); undefined for anything else.
		m.Const("getOwnNonIndexKeys", vm.NewNativeFunction(1, false, "getOwnNonIndexKeys", func(args []vm.Value) (vm.Value, error) {
			v := argAt(args, 0)
			var keys []vm.Value
			switch v.Type() {
			case vm.TypeArray:
				arr := v.AsArray()
				for _, k := range arr.OwnNamedKeys() {
					keys = append(keys, vm.NewString(k))
				}
				keys = append(keys, arr.OwnSymbolKeys()...)
			case vm.TypeTypedArray:
				ta := v.AsTypedArray()
				if ta.Properties != nil {
					for _, k := range ta.Properties.OwnPropertyNames() {
						keys = append(keys, vm.NewString(k))
					}
					keys = append(keys, ta.Properties.OwnSymbolKeys()...)
				}
			default:
				return vm.Undefined, nil
			}
			return vm.NewArrayWithArgs(keys), nil
		}))
		m.Default(nil)
	})
}

// callInspectExport calls export name of the inspect shim. Loading goes
// through the module registry, which caches the evaluated shim per session.
func callInspectExport(p *driver.Paserati, name string, this vm.Value, args []vm.Value) (vm.Value, error) {
	fn, err := (&lazyJSModule{p: p, spec: "noderati-internal:inspect"}).get(name)
	if err != nil {
		return vm.Undefined, err
	}
	return p.GetVM().Call(fn, this, args)
}

// installUtilInspect wires util.inspect, util.format and
// util.formatWithOptions to the shim. The exports are forwarders so the
// shim only loads when one is first called; util.inspect also carries
// .custom up front and forwards .defaultOptions/.colors/.styles.
func installUtilInspect(p *driver.Paserati) {
	vmInst := p.GetVM()
	rec, err := p.LoadModule("util", ".")
	if err != nil {
		return
	}
	exports := rec.GetExportValues()
	lazy := &lazyJSModule{p: p, spec: "noderati-internal:inspect"}
	call := func(name string, this vm.Value, args []vm.Value) (vm.Value, error) {
		fn, err := lazy.get(name)
		if err != nil {
			return vm.Undefined, err
		}
		return vmInst.Call(fn, this, args)
	}
	forward := func(name string) vm.Value {
		return vm.NewNativeFunction(0, true, name, func(args []vm.Value) (vm.Value, error) {
			return call(name, vmInst.GetThis(), args)
		})
	}
	inspectFn := forward("inspect")
	// Defined through Object.defineProperty: accessors stored directly on
	// a NativeFunctionWithProps' property table read back as undefined.
	defineProperty := jsGlobalMethod(vmInst, "Object", "defineProperty")
	define := func(name string, desc map[string]vm.Value) {
		d := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		for k, v := range desc {
			d.SetOwn(k, v)
		}
		_, _ = vmInst.Call(defineProperty, vm.Undefined, []vm.Value{inspectFn, vm.NewString(name), vm.NewValueFromPlainObject(d)})
	}
	define("custom", map[string]vm.Value{
		"value": jsSymbolFor(vmInst, "nodejs.util.inspect.custom"), "writable": vm.True, "enumerable": vm.True, "configurable": vm.True,
	})
	for _, name := range []string{"defaultOptions", "colors", "styles"} {
		name := name
		getter := vm.NewNativeFunction(0, false, "get "+name, func(_ []vm.Value) (vm.Value, error) {
			real, err := lazy.get("inspect")
			if err != nil {
				return vm.Undefined, err
			}
			return jsReflectGet(vmInst, real, vm.NewString(name))
		})
		// Assigned from JS: Reflect.set on a function skips its setters.
		setter := vm.NewNativeFunction(1, false, "set "+name, func(args []vm.Value) (vm.Value, error) {
			return call("setInspectProperty", vm.Undefined, []vm.Value{vm.NewString(name), argAt(args, 0)})
		})
		define(name, map[string]vm.Value{"get": getter, "set": setter, "enumerable": vm.False, "configurable": vm.True})
	}
	values := map[string]vm.Value{
		"inspect":           inspectFn,
		"format":            forward("format"),
		"formatWithOptions": forward("formatWithOptions"),
	}
	for name, v := range values {
		exports[name] = v
		if def, ok := exports["default"]; ok && def.Type() == vm.TypeObject {
			def.AsPlainObject().SetOwn(name, v)
		}
	}

	// Buffer.prototype[inspect.custom] and its legacy alias .inspect.
	// (Buffer lives on globalThis's own properties, which GetGlobal
	// doesn't consult; see wrapBuffer.)
	var bufCtor vm.Value
	bufOK := false
	if gt, ok := vmInst.GetGlobal("globalThis"); ok && gt.AsPlainObject() != nil {
		bufCtor, bufOK = gt.AsPlainObject().GetOwn("Buffer")
	}
	if bufOK {
		if proto, err := jsReflectGet(vmInst, bufCtor, vm.NewString("prototype")); err == nil && !isNullish(proto) {
			hook := vm.NewNativeFunction(2, false, "inspect", func(args []vm.Value) (vm.Value, error) {
				return call("bufferInspect", vmInst.GetThis(), args)
			})
			for _, key := range []vm.Value{jsSymbolFor(vmInst, "nodejs.util.inspect.custom"), vm.NewString("inspect")} {
				d := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
				d.SetOwn("value", hook)
				d.SetOwn("writable", vm.True)
				d.SetOwn("enumerable", vm.True)
				d.SetOwn("configurable", vm.True)
				_, _ = vmInst.Call(defineProperty, vm.Undefined, []vm.Value{proto, key, vm.NewValueFromPlainObject(d)})
			}
		}
	}

	// The rest of node:util (util_more.js), also loaded on first use.
	installLazyJSExports(p, "util", "noderati-internal:util-more",
		[]string{"parseEnv", "getCallSites", "diff", "transferableAbortController", "transferableAbortSignal",
			"setTraceSigInt", "convertProcessSignalToExitCode"},
		[]string{"MIMEType", "MIMEParams"})
}
