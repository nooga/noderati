package host

import "testing"

// Expected values below were produced by real Node v26.

func TestVMCompileFunctionAndExtrasMatchNode(t *testing.T) {
	got := runScriptString(t, `
		import vm from "node:vm";
		const out = [Object.keys(vm).sort().join(" ")];
		out.push(String(vm.constants.USE_MAIN_CONTEXT_DEFAULT_LOADER), String(vm.constants.DONT_CONTEXTIFY), Object.isFrozen(vm.constants), Object.getPrototypeOf(vm.constants));
		const f = vm.compileFunction("return a+b+x", ["a", "b"], { contextExtensions: [{ x: 10 }] });
		out.push(f(1, 2), f.name, f.length, vm.compileFunction("return 7")());
		out.push(vm.createScript("1+1").runInThisContext(), vm.createScript("1") instanceof vm.Script);
		out.push(Buffer.isBuffer(new vm.Script("1+1").createCachedData()));
		const err = (fn) => { try { fn(); return "ok"; } catch (e) { return e.name + "|" + e.code; } };
		out.push(err(() => vm.measureMemory({ mode: "x" })), err(() => vm.compileFunction(1)), err(() => vm.compileFunction("return (")));
		const c = vm.createContext({ y: 5 });
		out.push(vm.compileFunction("return y*2", [], { parsingContext: c })());
		out.push(Object.keys(await vm.measureMemory()));
		JSON.stringify(out)
	`)
	want := `["Script compileFunction constants createContext createScript isContext measureMemory runInContext runInNewContext runInThisContext",` +
		`"Symbol(vm_dynamic_import_main_context_default)","Symbol(vm_context_no_contextify)",true,null,13,"",2,7,2,true,true,` +
		`"TypeError|ERR_INVALID_ARG_VALUE","TypeError|ERR_INVALID_ARG_TYPE","SyntaxError|undefined",10,["total"]]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
