package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// promise_with_resolvers.go implements the ES2024 `Promise.withResolvers()`
// static method - found missing while probing real undici (round 74,
// docs/real-node-plan.md): real undici's own lib/web/fetch/index.js does
// `let p = Promise.withResolvers()` at the very top of its exported
// `fetch()` function, so every real fetch() call hit "undefined is not a
// function" immediately. This is a real, spec-compliant addition, not a
// stand-in: `{ promise, resolve, reject }` built on
// vmInst.NewPromiseFromExecutor - the same primitive backing a real
// `new Promise((resolve, reject) => {...})` - added as a static method
// directly onto the real, existing Promise constructor (a
// vm.NewConstructorWithProps value, same shape as Blob/File/Event) via
// Go, not JS run through EvalCode/RunCode, for the same reason
// file_global.go is (see its own doc comment - paserati#298 already
// showed a second top-level script run corrupts later exception
// propagation on the same instance).
func installPromiseWithResolvers(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	promiseVal, ok := vmInst.GetGlobal("Promise")
	if !ok {
		return
	}
	props := promiseVal.AsNativeFunctionWithProps()
	if props == nil || props.Properties == nil {
		return
	}
	if _, exists := props.Properties.GetOwn("withResolvers"); exists {
		return
	}

	props.Properties.SetOwn("withResolvers", vm.NewNativeFunction(0, false, "withResolvers", func(_ []vm.Value) (vm.Value, error) {
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
		promise, err := vmInst.NewPromiseFromExecutor(executor)
		if err != nil {
			return vm.Undefined, err
		}
		out := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		out.SetOwn("promise", promise)
		out.SetOwn("resolve", resolveFn)
		out.SetOwn("reject", rejectFn)
		return vm.NewValueFromPlainObject(out), nil
	}))
}
