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
		if err == nil && len(rec.GetExportValues()) == 0 {
			if _, loadErrs, runErrs := l.p.RunModuleWithValue(l.spec); len(loadErrs) > 0 {
				err = loadErrs[0]
			} else if len(runErrs) > 0 {
				err = runErrs[0]
			}
		}
		if err != nil {
			l.err = fmt.Errorf("noderati: loading %s: %v", l.spec, err)
		} else {
			l.exports = rec.GetExportValues()
		}
	}
	if l.err != nil {
		return vm.Undefined, l.err
	}
	return l.exports[name], nil
}

// installLazyJSExports adds each of names, implemented by the JS shim spec,
// to the Go-declared module. The default export object (what default
// imports and require() see) gets an accessor that resolves to the real
// value on first read; the named export, which must exist before any code
// runs, is a forwarding function (constructing, for classes).
func installLazyJSExports(p *driver.Paserati, module, spec string, functions, classes []string) {
	vmInst := p.GetVM()
	lazy := &lazyJSModule{p: p, spec: spec}
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
		add(name, vm.NewConstructorWithProps(0, true, name, func(args []vm.Value) (vm.Value, error) {
			cls, err := lazy.get(name)
			if err != nil {
				return vm.Undefined, err
			}
			return vmInst.Construct(cls, args)
		}))
	}
}
