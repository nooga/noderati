package host

import (
	"fmt"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// Every function below takes its real, documented Node parameters plus a
// trailing `extra ...string` catch-all - not because the extra arguments
// are used, but because paserati's non-variadic Go-function bridge
// (pkg/driver/native_module.go's goFunctionToVM) sizes its reflect.Value
// argument slice to the number of arguments the JS *caller* passed, not
// the Go function's own arity, and passes that oversized slice straight
// to reflect.Value.Call - which panics ("reflect: Call with too many
// input arguments") instead of raising a catchable JS error. Real code
// calls assert.equal/strictEqual/notEqual/notStrictEqual with an optional
// trailing message argument far more often than not (hit for real while
// running jiti's babel.cjs pipeline, filed as
// https://github.com/nooga/paserati/issues/278), so a bare 2-arg Go
// signature crashed the whole VM on the very first such call.
// The variadic branch of that same bridge function correctly slices
// extra arguments into the variadic parameter instead of overflowing a
// fixed-size Go arg list, so making the trailing parameter variadic
// (rather than fixed-arity) sidesteps the bug entirely without touching
// paserati's own code.
func declareAssert(p *driver.Paserati) {
	p.DeclareModule("assert", func(m *driver.ModuleBuilder) {
		m.Function("ok", func(v bool, extra ...string) (interface{}, error) {
			if !v {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: false == true")
			}
			return nil, nil
		})
		m.Function("equal", func(actual, expected string, extra ...string) (interface{}, error) {
			if actual != expected {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: %s != %s", actual, expected)
			}
			return nil, nil
		})
		m.Function("strictEqual", func(actual, expected string, extra ...string) (interface{}, error) {
			if actual != expected {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: %s !== %s", actual, expected)
			}
			return nil, nil
		})
		m.Function("notEqual", func(actual, expected string, extra ...string) (interface{}, error) {
			if actual == expected {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: %s == %s", actual, expected)
			}
			return nil, nil
		})
		m.Function("notStrictEqual", func(actual, expected string, extra ...string) (interface{}, error) {
			if actual == expected {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: %s === %s", actual, expected)
			}
			return nil, nil
		})
		m.Function("fail", func(message string, extra ...string) (interface{}, error) {
			if message == "" {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: Failed")
			}
			return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: %s", message)
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:assert", "assert")
}

// installAssertGlobal replaces the "assert" module's default export with a
// callable-with-properties value, mirroring installBufferGlobal's pattern.
//
// Real Node's `assert` module exports a function (`assert(value, message)`,
// shorthand for `assert.ok`) that also carries `.ok`/`.equal`/`.strictEqual`/
// etc. as properties - a classic "callable object" shape. declareAssert's
// plain m.Default(nil) instead builds a plain, non-callable namespace
// object (Node CJS interop default), so `import assert from "assert"` gave
// back an object, and calling it directly (`assert(cond)`, the form every
// real assert-based validator - including @babel/helper-validator-option's
// OptionValidator, hit while running jiti's real babel.cjs pipeline - uses
// far more often than the equivalent `assert.ok(cond)`) threw a raw
// "object is not a function" TypeError instead of the real assertion
// behavior.
func installAssertGlobal(p *driver.Paserati) {
	rec, err := p.LoadModule("assert", ".")
	if err != nil {
		return
	}
	exports := rec.GetExportValues()

	assertFn := vm.NewNativeFunctionWithProps(1, true, "assert", func(args []vm.Value) (vm.Value, error) {
		v := len(args) > 0 && args[0].IsTruthy()
		if !v {
			msg := "AssertionError [ERR_ASSERTION]: false == true"
			if len(args) > 1 && args[1].Type() != vm.TypeUndefined {
				msg = args[1].ToString()
			}
			return vm.Undefined, fmt.Errorf("%s", msg)
		}
		return vm.Undefined, nil
	})

	if props := assertFn.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		for name, val := range exports {
			if name == "default" {
				continue
			}
			props.Properties.SetOwn(name, val)
		}
	}

	exports["default"] = assertFn
}
