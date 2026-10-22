package host

import (
	"errors"
	"fmt"
	"sync"

	"github.com/nooga/paserati/pkg/builtins"
	"github.com/nooga/paserati/pkg/driver"
	perrors "github.com/nooga/paserati/pkg/errors"
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
		m.Function("createContext", func(sandbox vm.Value, _ vm.Value) (vm.Value, error) {
			return vmCreateContext(p, vmInstance, sandbox)
		})
		m.Function("isContext", func(v vm.Value) bool {
			return vmLookupContext(v) != nil
		})
		// The trailing `_ vm.Value` options parameters below exist only so
		// a real caller passing real Node's actual optional second/third
		// argument (filename, lineOffset, timeout, ...) doesn't panic:
		// paserati's native-module reflection binding has no arity
		// tolerance (a Go function's parameter count must exactly match
		// the call site - found the hard way via util.deprecate and this
		// same class of bug, see docs/real-node-plan.md's round 49 entry)
		// so an omitted parameter here would crash on the very first real
		// options object passed, not silently ignore it. The options
		// themselves are still unimplemented, same scope note as this
		// file's declareVM doc comment already states.
		m.Function("runInThisContext", func(code string, _ vm.Value) (vm.Value, error) {
			return vmEval(p, code)
		})
		m.Function("runInContext", func(code string, contextObj vm.Value, _ vm.Value) (vm.Value, error) {
			realm := vmLookupContext(contextObj)
			if realm == nil {
				return vm.Undefined, fmt.Errorf("contextified sandbox argument is not a vm.Context")
			}
			return vmRunInRealm(p, vmInstance, realm, code)
		})
		m.Function("runInNewContext", func(code string, sandbox vm.Value, _ vm.Value) (vm.Value, error) {
			ctxVal, err := vmCreateContext(p, vmInstance, sandbox)
			if err != nil {
				return vm.Undefined, err
			}
			return vmRunInRealm(p, vmInstance, vmLookupContext(ctxVal), code)
		})

		m.Class("Script", &vmScript{}, newVMScript(p, vmInstance))
		m.Const("constants", vmConstants(vmInstance))

		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:vm", "vm")
	registerJSShim("noderati-internal:vm-extra", vmExtraShim)
}

// vmConstants is vm.constants: a frozen null-prototype object of the two
// importModuleDynamically sentinels.
func vmConstants(vmInstance *vm.VM) vm.Value {
	obj := vm.NewObject(vm.Null).AsPlainObject()
	obj.SetOwn("USE_MAIN_CONTEXT_DEFAULT_LOADER", vm.NewSymbol("vm_dynamic_import_main_context_default"))
	obj.SetOwn("DONT_CONTEXTIFY", vm.NewSymbol("vm_context_no_contextify"))
	v := vm.NewValueFromPlainObject(obj)
	if objectCtor, ok := vmInstance.GetGlobal("Object"); ok {
		if freeze, err := vmInstance.GetProperty(objectCtor, "freeze"); err == nil {
			_, _ = vmInstance.Call(freeze, objectCtor, []vm.Value{v})
		}
	}
	return v
}

// installVMExtras adds the JS-implemented parts of node:vm.
func installVMExtras(p *driver.Paserati) {
	vmInstance := p.GetVM()
	if gt, ok := vmInstance.GetGlobal("globalThis"); ok && gt.AsPlainObject() != nil {
		// __noderatiVMCallIn(context, fn, thisArg, args): a function
		// compiled in a context looks its globals up in that context's
		// realm, so it has to be called inside it too.
		gt.AsPlainObject().SetOwnNonEnumerable("__noderatiVMCallIn", vm.NewNativeFunction(4, false, "callIn", func(args []vm.Value) (vm.Value, error) {
			realm := vmLookupContext(argAt(args, 0))
			var callArgs []vm.Value
			if a := argAt(args, 3); a.Type() == vm.TypeArray {
				for i := 0; i < a.AsArray().Length(); i++ {
					callArgs = append(callArgs, a.AsArray().Get(i))
				}
			}
			if realm == nil {
				return vmInstance.Call(argAt(args, 1), argAt(args, 2), callArgs)
			}
			var callErr error
			res := vmInstance.WithRealmValue(realm, func() vm.Value {
				v, err := vmInstance.Call(argAt(args, 1), argAt(args, 2), callArgs)
				callErr = err
				return v
			})
			return res, callErr
		}))
	}
	installLazyJSExports(p, "vm", "noderati-internal:vm-extra",
		[]string{"compileFunction", "createScript", "measureMemory"}, nil)
}

// compileFunction builds the function from source (with one nested
// "with" per contextExtensions entry, which is how those objects' members
// come into scope) and evaluates it in the parsing context.
const vmExtraShim = `import vm from "vm";

function received(v) {
  if (v == null) return " Received " + v;
  if (typeof v === "function") return " Received function " + (v.name || "<anonymous>");
  if (typeof v === "object") return " Received an instance of " + ((v.constructor && v.constructor.name) || "Object");
  return " Received type " + typeof v + " (" + (typeof v === "string" ? "'" + v + "'" : String(v)) + ")";
}
function codedError(Ctor, code, message) {
  const err = new Ctor(message);
  err.code = code;
  return err;
}
function invalidArgType(name, expected, v) {
  const what = '"' + name + '" ' + (name.includes(".") ? "property" : "argument");
  return codedError(TypeError, "ERR_INVALID_ARG_TYPE", "The " + what + " must be " + expected + "." + received(v));
}

export function compileFunction(code, params, options = {}) {
  if (typeof code !== "string") throw invalidArgType("code", "of type string", code);
  if (params === undefined) params = [];
  if (!Array.isArray(params)) throw invalidArgType("params", "an instance of Array", params);
  params.forEach((p, i) => { if (typeof p !== "string") throw invalidArgType("params[" + i + "]", "of type string", p); });
  if (options === null || typeof options !== "object") throw invalidArgType("options", "of type object", options);
  const { contextExtensions = [], parsingContext } = options;
  if (!Array.isArray(contextExtensions)) throw invalidArgType("options.contextExtensions", "an instance of Array", contextExtensions);
  contextExtensions.forEach((e, i) => {
    if (e === null || typeof e !== "object") throw invalidArgType("options.contextExtensions[" + i + "]", "of type object", e);
  });
  if (parsingContext !== undefined && !vm.isContext(parsingContext)) {
    throw invalidArgType("options.parsingContext", "an instance of Context", parsingContext);
  }
  let src = "(function (__noderatiExt) {";
  for (let i = 0; i < contextExtensions.length; i++) src += " with (__noderatiExt[" + i + "])";
  src += " { return function (" + params.join(", ") + ") {\n" + code + "\n}; } })";
  if (parsingContext === undefined) return vm.runInThisContext(src)(contextExtensions);
  const callIn = globalThis.__noderatiVMCallIn;
  const fn = callIn(parsingContext, vm.runInContext(src, parsingContext), undefined, [contextExtensions]);
  const wrapped = function (...args) { return callIn(parsingContext, fn, this, args); };
  Object.defineProperties(wrapped, {
    name: { value: "", configurable: true },
    length: { value: params.length, configurable: true },
  });
  return wrapped;
}

export function createScript(code, options) {
  return new vm.Script(code, options);
}

export function measureMemory(options = {}) {
  if (options === null || typeof options !== "object") throw invalidArgType("options", "of type object", options);
  const { mode = "summary", execution = "default" } = options;
  if (mode !== "summary" && mode !== "detailed") {
    throw codedError(TypeError, "ERR_INVALID_ARG_VALUE", "The property 'options.mode' must be one of: 'summary', 'detailed'. Received " + (typeof mode === "string" ? "'" + mode + "'" : String(mode)));
  }
  if (execution !== "default" && execution !== "eager") {
    throw codedError(TypeError, "ERR_INVALID_ARG_VALUE", "The property 'options.execution' must be one of: 'default', 'eager'. Received '" + execution + "'");
  }
  const used = process.memoryUsage().heapUsed;
  const estimate = { jsMemoryEstimate: used, jsMemoryRange: [used, process.memoryUsage().heapTotal] };
  const result = mode === "summary" ? { total: estimate } : { total: estimate, current: estimate, other: [] };
  return Promise.resolve(result);
}

export default { compileFunction, createScript, measureMemory };
`

func vmEval(p *driver.Paserati, code string) (vm.Value, error) {
	val, errs := p.IndirectEvalCode(code)
	if len(errs) > 0 {
		var syn *perrors.SyntaxError
		if errors.As(errs[0], &syn) {
			// Thrown as a real JS SyntaxError, as V8 does when compiling.
			vmInst := p.GetVM()
			if ctor, ok := vmInst.GetGlobal("SyntaxError"); ok {
				if exc, err := vmInst.Construct(ctor, []vm.Value{vm.NewString(syn.Msg)}); err == nil {
					return vm.Undefined, &fsSystemError{exception: exc, message: syn.Msg}
				}
			}
		}
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

func newVMScript(p *driver.Paserati, vmInstance *vm.VM) func(code string, _ vm.Value) (*vmScript, error) {
	return func(code string, _ vm.Value) (*vmScript, error) {
		return &vmScript{p: p, vmInstance: vmInstance, code: code}, nil
	}
}

// RunInThisContext/RunInContext/RunInNewContext return their error
// naturally, the same way any other bound Class instance method does.
// Until paserati#221 landed (fixed 2026-09-03, see docs/real-node-plan.md),
// that path silently discarded a bound method's returned error -
// `new vm.Script("(((").runInThisContext()` evaluated to `undefined`
// instead of throwing - and these methods worked around it by throwing
// directly via the VM's exception machinery. Removed now that #221's real
// fix makes the plain (T, error) return propagate correctly on its own;
// re-verified against the exact syntax-error repro these methods'
// tests use before removing the workaround.

// CreateCachedData returns the script's "code cache". There is no V8
// bytecode cache to serialize, so it's the source itself - enough for a
// caller that stores and later passes it back as options.cachedData.
func (s *vmScript) CreateCachedData() vm.Value {
	return wrapBuffer(s.vmInstance, []byte(s.code))
}

func (s *vmScript) RunInThisContext(_ vm.Value) (vm.Value, error) {
	return vmEval(s.p, s.code)
}

func (s *vmScript) RunInContext(contextObj vm.Value, _ vm.Value) (vm.Value, error) {
	return vmRunInRealm(s.p, s.vmInstance, vmLookupContext(contextObj), s.code)
}

func (s *vmScript) RunInNewContext(sandbox vm.Value, _ vm.Value) (vm.Value, error) {
	ctxVal, err := vmCreateContext(s.p, s.vmInstance, sandbox)
	if err != nil {
		return vm.Undefined, err
	}
	return vmRunInRealm(s.p, s.vmInstance, vmLookupContext(ctxVal), s.code)
}
