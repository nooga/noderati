package host

// timers.go implements node:timers - real undici's
// lib/mock/snapshot-recorder.js does
// `const { setTimeout, clearTimeout } = require('node:timers')`,
// confirmed by grepping every real `node:timers` call site in the
// vendored package before writing this. Re-exports the real globals
// every other real timer call in this codebase already uses: setTimeout/
// clearTimeout from paserati's own HostTimerInitializer, setImmediate/
// clearImmediate from this project's own immediate_object.go (added
// round 79, docs/real-node-plan.md - real undici's client-h1.js calls
// the bare global directly, not through this module, but real Node's
// node:timers genuinely does export these too). setInterval/
// clearInterval still aren't re-exported - neither exists as a global in
// paserati at all yet (a separate, pre-existing, unrelated gap, not
// something this module should paper over by exporting `undefined`
// under a real name), and no real call site in this dependency tree has
// needed them so far.
const timersShim = `const setTimeout = globalThis.setTimeout;
const clearTimeout = globalThis.clearTimeout;
const setImmediate = globalThis.setImmediate;
const clearImmediate = globalThis.clearImmediate;

export { setTimeout, clearTimeout, setImmediate, clearImmediate };
export default { setTimeout, clearTimeout, setImmediate, clearImmediate };
`

func declareTimers() {
	registerJSShim("timers", timersShim)
}
