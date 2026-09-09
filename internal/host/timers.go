package host

// timers.go implements node:timers - real undici's
// lib/mock/snapshot-recorder.js does
// `const { setTimeout, clearTimeout } = require('node:timers')`,
// confirmed by grepping every real `node:timers` call site in the
// vendored package before writing this. Only re-exports the two names
// actually destructured, from the same global `setTimeout`/`clearTimeout`
// every other real timer call in this codebase already uses (paserati's
// own HostTimerInitializer). Real Node's node:timers module also exports
// `setInterval`/`setImmediate`/`clearInterval`/`clearImmediate` - not
// re-exported here because none of those exist as globals in paserati
// at all (a separate, pre-existing, unrelated gap, not something this
// module should paper over by exporting `undefined` under a real name).
const timersShim = `const setTimeout = globalThis.setTimeout;
const clearTimeout = globalThis.clearTimeout;

export { setTimeout, clearTimeout };
export default { setTimeout, clearTimeout };
`

func declareTimers() {
	registerJSShim("timers", timersShim)
}
