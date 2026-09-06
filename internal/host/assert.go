package host

import (
	"fmt"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

func declareAssert(p *driver.Paserati) {
	p.DeclareModule("assert", func(m *driver.ModuleBuilder) {
		m.Function("ok", func(v bool) (interface{}, error) {
			if !v {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: false == true")
			}
			return nil, nil
		})
		m.Function("equal", func(actual, expected string) (interface{}, error) {
			if actual != expected {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: %s != %s", actual, expected)
			}
			return nil, nil
		})
		m.Function("strictEqual", func(actual, expected string) (interface{}, error) {
			if actual != expected {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: %s !== %s", actual, expected)
			}
			return nil, nil
		})
		m.Function("notEqual", func(actual, expected string) (interface{}, error) {
			if actual == expected {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: %s == %s", actual, expected)
			}
			return nil, nil
		})
		m.Function("notStrictEqual", func(actual, expected string) (interface{}, error) {
			if actual == expected {
				return nil, fmt.Errorf("AssertionError [ERR_ASSERTION]: %s === %s", actual, expected)
			}
			return nil, nil
		})
		m.Function("fail", func(message string) (interface{}, error) {
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
