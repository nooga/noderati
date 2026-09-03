package host

import (
	"fmt"
	"sync"

	"github.com/nooga/paserati/pkg/builtins"
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// vmContexts maps a "contextified" sandbox object's identity (its
// *vm.PlainObject pointer, stable across every vm.Value wrapper of the
// same object) to the paserati Realm backing it. Node's own
// vm.createContext(sandbox) returns the very same sandbox reference
// (=== sandbox) with the engine internally treating it as the context's
// global object — since paserati's Realm is a genuinely separate global
// object/heap, not something we can graft directly onto an arbitrary JS
// object, this identity map is what lets isContext()/runInContext() later
// recognize "this object IS a context" and find its Realm.
//
// Entries are never removed: a context created and dropped by JS leaks its
// Realm for the life of the process. Real V8 contexts get collected when
// unreachable; matching that would need finalizers tied to the sandbox
// object's own GC lifetime, which paserati's object model doesn't expose.
// Acceptable for real usage found so far (jiti creates at most a handful of
// long-lived contexts, not one per request) — revisit if a real caller
// creates contexts in a hot loop.
var (
	vmContextMu sync.Mutex
	vmContexts  = map[*vm.PlainObject]*vm.Realm{}
)

// declareVM implements Node's `vm` module on top of paserati's own Realm
// and indirect-eval primitives — both already public driver/vm APIs, so
// this needs zero new paserati-core work. Found missing via noderati's
// `jiti` group-B fake: jiti's real `dist/jiti.cjs` calls
// `vm.runInThisContext(code)` (webpack-wrapped as `Ii().runInThisContext`)
// as its actual TypeScript-execution mechanism, once #203/#204 (parser)
// and the OSPathResolver/node:v8 fixes cleared everything before it.
//
// Mapping (see docs/real-node-plan.md's thirty-second round for the design
// discussion this followed):
//   - runInThisContext(code)   -> p.IndirectEvalCode(code) directly. Real
//     Node's own docs describe runInThisContext as behaving like indirect
//     eval() (no access to the caller's local scope); IndirectEvalCode's
//     own doc comment says exactly that (let/const/class stay local to the
//     eval, var goes to the global environment, no inherited strict mode)
//   - this is a real semantic match, not an approximation.
//   - createContext(sandbox)   -> vmInstance.CreateRealm() +
//     p.InitializeRealmBuiltins(realm, standard initializers) to populate
//     ordinary JS globals (no Node-specific globals like `require`/
//     `process`/`console` are injected — real vm contexts don't get those
//     either unless the caller adds them), then the sandbox's own
//     properties are copied onto the realm as globals.
//   - runInContext(code, ctx)  -> vmInstance.WithRealmValue(realm, ...)
//     around the same IndirectEvalCode call, so it executes against the
//     target realm's globals/heap instead of the current one.
//   - runInNewContext(code, sandbox) -> runInContext(code,
//     createContext(sandbox)).
//   - Script -> a thin wrapper holding the source string; its three
//     run*() methods delegate to the module-level functions above.
//
// Scoped deliberately narrower than every corner of real Node's `vm`:
//   - No `vm.compileFunction`, no `Script` constructor options
//     (`filename`, `timeout`, `displayErrors`, ...), no cached-data/V8
//     snapshot features — none of these are exercised by any real call
//     site found, and several (V8 code caching) have no meaningful
//     equivalent without a real V8 underneath.
//   - `new vm.Script(code)` doesn't eagerly parse/validate at construction
//     time the way real Node does (a syntax error only surfaces on the
//     first run* call, not at `new`) — IndirectEvalCode compiles and runs
//     in one step, so there's no "compile once, validate now, run later"
//     split to hook a construction-time check into without duplicating
//     the parse. A real gap from spec fidelity, not a silent one.
//   - Sandbox-to-context linkage is one-directional at creation time
//     (sandbox's *existing* properties become context globals); a script
//     defining a *new* global inside the context is not synced back onto
//     the original sandbox object afterward, unlike real V8's live
//     two-way binding. No real call site found needing that; the honest
//     one-way copy is what's implemented here rather than a fragile
//     partial simulation of the two-way case.
func declareVM(p *driver.Paserati) {
	vmInstance := p.GetVM()

	p.DeclareModule("vm", func(m *driver.ModuleBuilder) {
		m.Function("createContext", func(sandbox vm.Value) (vm.Value, error) {
			return vmCreateContext(p, vmInstance, sandbox)
		})
		m.Function("isContext", func(v vm.Value) bool {
			return vmLookupContext(v) != nil
		})
		m.Function("runInThisContext", func(code string) (vm.Value, error) {
			return vmEval(p, code)
		})
		m.Function("runInContext", func(code string, contextObj vm.Value) (vm.Value, error) {
			realm := vmLookupContext(contextObj)
			if realm == nil {
				return vm.Undefined, fmt.Errorf("contextified sandbox argument is not a vm.Context")
			}
			return vmRunInRealm(p, vmInstance, realm, code)
		})
		m.Function("runInNewContext", func(code string, sandbox vm.Value) (vm.Value, error) {
			ctxVal, err := vmCreateContext(p, vmInstance, sandbox)
			if err != nil {
				return vm.Undefined, err
			}
			return vmRunInRealm(p, vmInstance, vmLookupContext(ctxVal), code)
		})

		m.Class("Script", &vmScript{}, newVMScript(p, vmInstance))

		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:vm", "vm")
}

func vmEval(p *driver.Paserati, code string) (vm.Value, error) {
	val, errs := p.IndirectEvalCode(code)
	if len(errs) > 0 {
		return vm.Undefined, errs[0]
	}
	return val, nil
}

func vmLookupContext(v vm.Value) *vm.Realm {
	if v.Type() != vm.TypeObject {
		return nil
	}
	obj := v.AsPlainObject()
	if obj == nil {
		return nil
	}
	vmContextMu.Lock()
	defer vmContextMu.Unlock()
	return vmContexts[obj]
}

func vmCreateContext(p *driver.Paserati, vmInstance *vm.VM, sandbox vm.Value) (vm.Value, error) {
	var sandboxObj *vm.PlainObject
	switch {
	case sandbox.IsUndefined(), sandbox.IsNull():
		sandboxObj = vm.NewObject(vmInstance.ObjectPrototype).AsPlainObject()
	case sandbox.Type() == vm.TypeObject:
		sandboxObj = sandbox.AsPlainObject()
	default:
		return vm.Undefined, fmt.Errorf("vm.createContext: contextObject must be an object, undefined, or null")
	}
	if sandboxObj == nil {
		return vm.Undefined, fmt.Errorf("vm.createContext: contextObject must be an object, undefined, or null")
	}

	realm := vmInstance.CreateRealm()
	if err := p.InitializeRealmBuiltins(realm, builtins.GetStandardInitializers()); err != nil {
		return vm.Undefined, fmt.Errorf("vm.createContext: %w", err)
	}

	for _, name := range sandboxObj.OwnPropertyNames() {
		if val, ok := sandboxObj.GetOwn(name); ok {
			_ = realm.DefineGlobal(name, val)
		}
	}

	vmContextMu.Lock()
	vmContexts[sandboxObj] = realm
	vmContextMu.Unlock()

	return vm.NewValueFromPlainObject(sandboxObj), nil
}

func vmRunInRealm(p *driver.Paserati, vmInstance *vm.VM, realm *vm.Realm, code string) (vm.Value, error) {
	if realm == nil {
		return vm.Undefined, fmt.Errorf("contextified sandbox argument is not a vm.Context")
	}
	var runErr error
	result := vmInstance.WithRealmValue(realm, func() vm.Value {
		val, err := vmEval(p, code)
		if err != nil {
			runErr = err
			return vm.Undefined
		}
		return val
	})
	if runErr != nil {
		return vm.Undefined, runErr
	}
	return result, nil
}

// vmScript backs `new vm.Script(code)`. See declareVM's doc comment for
// the deliberate scope of what this does and doesn't cover.
type vmScript struct {
	p          *driver.Paserati
	vmInstance *vm.VM
	code       string
}

func newVMScript(p *driver.Paserati, vmInstance *vm.VM) func(code string) (*vmScript, error) {
	return func(code string) (*vmScript, error) {
		return &vmScript{p: p, vmInstance: vmInstance, code: code}, nil
	}
}

// vmThrow makes a Go error surface as a catchable JS exception from a
// vmScript method. Filed as paserati#221: unlike a module-level Function
// (m.Function) or a class constructor - both of which special-case a
// trailing (T, error) return and turn a non-nil error into a real throw -
// a bound instance method's wiring (driver's createClassConstructor ->
// bindStructMethods -> createBoundMethod) only ever reads results[0] and
// hardcodes a nil error back to the VM, silently discarding whatever
// vmScript.RunInThisContext/RunInContext/RunInNewContext actually returned.
// Confirmed empirically: without this, `new vm.Script("(((").runInThisContext()`
// returned undefined instead of throwing.
//
// The workaround calls the VM's own throwException machinery directly
// (vm.ThrowExceptionValue / vm.ThrowTypeError) instead of relying on the
// return value, mirroring exactly what the module-level Function path does
// with a returned error: an ExceptionError already carries the real thrown
// value (a SyntaxError from IndirectEvalCode, say) and that value is reused
// as-is; anything else gets wrapped in a real Error instance via the global
// Error constructor, falling back to a TypeError only if that constructor is
// somehow unavailable. A throw made this way still needs the
// EnterHelperCall/ExitHelperCall bracket below to actually reach the calling
// try/catch - see vmThrow's own comment for why the naive version (throw,
// then return) silently failed to propagate the first time this was tried.
func vmThrow(vmInstance *vm.VM, err error) {
	// EnterHelperCall/ExitHelperCall bracket a synchronous throw made from
	// inside a native call rather than as its returned error (see call.go's
	// own doc comment on EnterHelperCall: "should be called before native
	// functions call helpers like ToPrimitive that might throw exceptions
	// which need to be caught by try/catch blocks"). Confirmed necessary
	// empirically, not just by reading the doc comment: without this bracket,
	// handleCatchBlock (exceptions.go) finds the in-frame catch handler and
	// correctly repoints frame.ip at it, but only sets vm.handlerFound when
	// vm.helperCallDepth > 0 - and OpCallMethod's own "exception caught in
	// the same frame, jump to handler" fallback check is gated behind
	// !calleeVal.IsCallable(), which never applies to a bound method like
	// this one. Without helperCallDepth > 0, neither path notices the
	// pending jump and the call site just falls through as if nothing were
	// thrown, using createBoundMethod's bogus discarded-error return value.
	vmInstance.EnterHelperCall()
	defer vmInstance.ExitHelperCall()

	if ee, ok := err.(vm.ExceptionError); ok {
		vmInstance.ThrowExceptionValue(ee.GetExceptionValue())
		return
	}
	if errCtor, ok := vmInstance.GetGlobal("Error"); ok {
		if res, callErr := vmInstance.Call(errCtor, vm.Undefined, []vm.Value{vm.NewString(err.Error())}); callErr == nil {
			vmInstance.ThrowExceptionValue(res)
			return
		}
	}
	vmInstance.ThrowTypeError(err.Error())
}

func (s *vmScript) RunInThisContext() (vm.Value, error) {
	val, err := vmEval(s.p, s.code)
	if err != nil {
		vmThrow(s.vmInstance, err)
		return vm.Undefined, err
	}
	return val, nil
}

func (s *vmScript) RunInContext(contextObj vm.Value) (vm.Value, error) {
	val, err := vmRunInRealm(s.p, s.vmInstance, vmLookupContext(contextObj), s.code)
	if err != nil {
		vmThrow(s.vmInstance, err)
		return vm.Undefined, err
	}
	return val, nil
}

func (s *vmScript) RunInNewContext(sandbox vm.Value) (vm.Value, error) {
	ctxVal, err := vmCreateContext(s.p, s.vmInstance, sandbox)
	if err != nil {
		vmThrow(s.vmInstance, err)
		return vm.Undefined, err
	}
	val, err := vmRunInRealm(s.p, s.vmInstance, vmLookupContext(ctxVal), s.code)
	if err != nil {
		vmThrow(s.vmInstance, err)
		return vm.Undefined, err
	}
	return val, nil
}
