package host

import (
	"fmt"

	"github.com/nooga/paserati/pkg/driver"
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
