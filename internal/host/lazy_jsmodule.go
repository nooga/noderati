package host

import (
	"fmt"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// lazyJSModule is a JS shim (registerJSShim) whose exports extend a
// Go-declared module but which is only loaded and run the first time one
// of them is used, so the common case pays nothing at startup (loading
// node:stream alone costs ~9ms, a third of a trivial script's run).
type lazyJSModule struct {
	p       *driver.Paserati
	spec    string
	loaded  bool
	exports map[string]vm.Value
	err     error
}

func (l *lazyJSModule) get(name string) (vm.Value, error) {
	if !l.loaded {
		l.loaded = true
		rec, err := l.p.LoadModule(l.spec, ".")
		if err == nil && rec.GetError() != nil {
			err = rec.GetError()
		}
		var vals map[string]vm.Value
		if err == nil {
			vals, err = shimModuleExports(l.p, l.spec, rec)
		}
		if err != nil {
			l.err = fmt.Errorf("noderati: loading %s: %v", l.spec, err)
		} else {
			l.exports = vals
		}
	}
	if l.err != nil {
		return vm.Undefined, l.err
	}
	return l.exports[name], nil
}

// shimModuleExports returns the exports of a text module the host loads
// directly (require() of a JS shim, lazily loaded shim exports), running it
// first if nothing has yet. It goes through the VM's own module table
// (GetModuleExport executes a module at most once) rather than
// RunModuleWithValue, which interprets the chunk without recording it
// there: with that, a shim reached both by require() and by an ESM import
// (the stream/promises shim's own `import Stream from "stream"`, or an ESM
// app whose CJS dependencies require() the same builtin) had its body run
// twice over the same bindings - re-creating every top-level function, so
// instances made from the first run's classes stopped being instanceof the
// "same" class.
func shimModuleExports(p *driver.Paserati, spec string, rec vm.ModuleRecord) (map[string]vm.Value, error) {
	if vals := rec.GetExportValues(); len(vals) > 0 {
		return vals, nil
	}
	vmInst := p.GetVM()
	names := rec.GetExportNames()
	if len(names) == 0 {
		for name := range rec.GetExportIndices() {
			names = append(names, name)
		}
	}
	vals := make(map[string]vm.Value, len(names))
	defined := false
	for _, name := range names {
		v := vmInst.GetModuleExport("", spec, name)
		vals[name] = v
		if !v.IsUndefined() {
			defined = true
		}
	}
	if len(names) > 0 && !defined {
		return nil, fmt.Errorf("module %s failed to evaluate", spec)
	}
	return vals, nil
}

// installLazyJSExports adds each of names, implemented by the JS shim spec,
// to the Go-declared module. The default export object (what default
// imports and require() see) gets an accessor that resolves to the real
// value on first read; the named export, which must exist before any code
// runs, is a forwarding function (constructing, for classes).
func installLazyJSExports(p *driver.Paserati, module, spec string, functions, classes []string) {
	installLazyJSExportsFrom(p, module, &lazyJSModule{p: p, spec: spec}, functions, classes)
}

// installLazyJSExportsFrom is installLazyJSExports with a caller-owned
// lazyJSModule, for callers that also read the shim's exports themselves.
func installLazyJSExportsFrom(p *driver.Paserati, module string, lazy *lazyJSModule, functions, classes []string) {
	vmInst := p.GetVM()
	rec, err := p.LoadModule(module, ".")
	if err != nil {
		return
	}
	exports := rec.GetExportValues()
	var def *vm.PlainObject
	if d, ok := exports["default"]; ok && d.Type() == vm.TypeObject {
		def = d.AsPlainObject()
	} else if d.Type() == vm.TypeNativeFunctionWithProps {
		def = d.AsNativeFunctionWithProps().Properties
	}
	add := func(name string, forward vm.Value) {
		exports[name] = forward
		if def == nil {
			return
		}
		getter := vm.NewNativeFunction(0, false, "get "+name, func(_ []vm.Value) (vm.Value, error) {
			return lazy.get(name)
		})
		setter := vm.NewNativeFunction(1, false, "set "+name, func(args []vm.Value) (vm.Value, error) {
			def.DeleteOwn(name)
			def.SetOwn(name, argAt(args, 0))
			return vm.Undefined, nil
		})
		yes := true
		def.DefineAccessorProperty(name, getter, true, setter, true, &yes, &yes)
	}
	for _, name := range functions {
		name := name
		add(name, vm.NewNativeFunction(0, true, name, func(args []vm.Value) (vm.Value, error) {
			fn, err := lazy.get(name)
			if err != nil {
				return vm.Undefined, err
			}
			return vmInst.Call(fn, vmInst.GetThis(), args)
		}))
	}
	for _, name := range classes {
		name := name
		var forward vm.Value
		forward = vm.NewConstructorWithProps(0, true, name, func(args []vm.Value) (vm.Value, error) {
			cls, err := lazy.get(name)
			if err != nil {
				return vm.Undefined, err
			}
			// Share the real prototype so instanceof against the
			// forwarder (a named ESM import) holds.
			if fp := forward.AsNativeFunctionWithProps(); fp != nil && fp.Properties != nil {
				if proto, err := vmInst.GetProperty(cls, "prototype"); err == nil {
					fp.Properties.SetOwnNonEnumerable("prototype", proto)
				}
			}
			return vmInst.Construct(cls, args)
		})
		add(name, forward)
	}
}
