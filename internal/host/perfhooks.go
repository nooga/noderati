package host

const perfHooksShim = `export const performance = globalThis.performance || { now() { return Date.now(); } };
export default { performance };
`

func declarePerfHooks() {
	registerJSShim("perf_hooks", perfHooksShim)
}
