package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestVMRunInThisContext(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import vm from "node:vm";
		const result = vm.runInThisContext("1 + 2");
		result
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "3" {
		t.Errorf("runInThisContext(\"1 + 2\") = %q, want 3", val.ToString())
	}
}

// TestVMRunInThisContextSyntaxError guards that a real compile error from
// the sandboxed code surfaces as a catchable JS exception, not a Go panic
// or a silently-swallowed failure.
func TestVMRunInThisContextSyntaxError(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import vm from "node:vm";
		try {
			vm.runInThisContext("this is not valid js (((");
			"no error"
		} catch (e) {
			"caught"
		}
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "caught" {
		t.Errorf("expected the syntax error to be caught, got %q", val.ToString())
	}
}

func TestVMCreateContextAndRunInContext(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import vm from "node:vm";
		const sandbox = { x: 10 };
		const ctx = vm.createContext(sandbox);
		[
			ctx === sandbox,
			vm.isContext(ctx),
			vm.isContext({}),
			vm.runInContext("x + 5", ctx),
		].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "true|true|false|15"
	if val.ToString() != want {
		t.Errorf("createContext/runInContext = %q, want %q", val.ToString(), want)
	}
}

func TestVMRunInNewContext(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import vm from "node:vm";
		vm.runInNewContext("a + b", { a: 3, b: 4 })
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "7" {
		t.Errorf("runInNewContext = %q, want 7", val.ToString())
	}
}

// TestVMContextIsolation guards that a context's globals don't leak into
// (or read from) the main realm's globals — the whole point of a separate
// Realm, not just a scoping trick.
func TestVMContextIsolation(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import vm from "node:vm";
		globalThis.leakCheck = "main-realm-value";
		const ctx = vm.createContext({});
		const sawMainGlobalInContext = vm.runInContext("typeof leakCheck", ctx);
		vm.runInContext("contextOnlyVar = 'set-inside-context'", ctx);
		[
			sawMainGlobalInContext,
			typeof globalThis.contextOnlyVar,
		].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "undefined|undefined"
	if val.ToString() != want {
		t.Errorf("context isolation = %q, want %q", val.ToString(), want)
	}
}

// TestVMRunInContextRejectsNonContext guards the "not a vm.Context" error
// path on both the module-level function and the Script method — a caller
// passing a plain object (never returned from createContext) must get a
// catchable error, not a silent run against the current realm.
func TestVMRunInContextRejectsNonContext(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import vm from "node:vm";
		function caught(fn) {
			try { fn(); return "ran"; } catch (e) { return "caught"; }
		}
		[
			caught(() => vm.runInContext("1", {})),
			caught(() => new vm.Script("1").runInContext({})),
		].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "caught|caught"
	if val.ToString() != want {
		t.Errorf("runInContext on a non-context object = %q, want %q", val.ToString(), want)
	}
}

// TestVMContextIsolationRealBinding is the discriminating version of
// TestVMContextIsolation: that test only proved a *property* assigned via
// `globalThis.x = ...` doesn't leak, which could pass even if realms shared
// heap storage (property-vs-slot are different storage). This declares a
// real global binding with `var` at module top level in each direction and
// checks it's genuinely invisible across the realm boundary either way.
func TestVMContextIsolationRealBinding(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import vm from "node:vm";
		var mainBinding = "main-realm-binding";
		const ctx = vm.createContext({});
		const sawMainBindingInContext = vm.runInContext("typeof mainBinding", ctx);
		vm.runInContext("var contextBinding = 'from-context';", ctx);
		[
			sawMainBindingInContext,
			typeof contextBinding,
		].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "undefined|undefined"
	if val.ToString() != want {
		t.Errorf("real-binding isolation = %q, want %q (would be \"main-realm-binding|string\" if realms shared heap storage)", val.ToString(), want)
	}
}

func TestVMScriptClass(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import vm from "node:vm";
		const script = new vm.Script("6 * 7");
		[
			script.runInThisContext(),
			script.runInNewContext({}),
			script instanceof vm.Script,
		].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "42|42|true"
	if val.ToString() != want {
		t.Errorf("Script class = %q, want %q", val.ToString(), want)
	}
}

// TestVMScriptRunInThisContextSyntaxError is the Script-class counterpart of
// TestVMRunInThisContextSyntaxError. It guards against paserati#221: a Class
// instance method's (T, error) return is silently discarded by the driver's
// bound-method wiring, so without vmThrow's workaround this would evaluate
// to undefined instead of throwing.
func TestVMScriptRunInThisContextSyntaxError(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `
		import vm from "node:vm";
		try {
			new vm.Script("this is not valid js (((").runInThisContext();
			"no error"
		} catch (e) {
			"caught"
		}
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "caught" {
		t.Errorf("expected the syntax error to be caught, got %q", val.ToString())
	}
}

// TestVMRequireViaCJS guards cjs.go's nativeRequireNames entry — the same
// import-vs-require split node:v8 needed both an entry for.
func TestVMRequireViaCJS(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	js := `module.exports = require("node:vm").runInThisContext("2 + 2");`
	val, errs := RunCJS(p, js, "/virtual/entry.cjs")
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	if val.ToString() != "4" {
		t.Errorf("require(\"node:vm\").runInThisContext(\"2 + 2\") = %q, want 4", val.ToString())
	}
}
