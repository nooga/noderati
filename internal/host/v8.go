package host

import (
	"math"
	"runtime"
	"runtime/debug"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// declareV8 implements a small, honest slice of Node's `v8` module — not a
// full port (there is no V8 engine underneath paserati to introspect), but
// real where a real answer exists and clearly-labeled no-ops where the
// underlying V8 concept genuinely doesn't apply, rather than a silent
// "module not found".
//
// Found missing via noderati's `jiti` group-B fake: jiti's real, unmodified
// `dist/jiti.cjs` does `require("node:v8")` at module scope purely to check
// `v8.startupSnapshot.isBuildingSnapshot()` (guarding a stack-trace-limit
// tweak that only matters while building a V8 startup snapshot) — with the
// whole module missing, `require("node:v8")` itself throws before that
// call's own try/catch ever gets a chance to run.
func declareV8(p *driver.Paserati) {
	p.DeclareModule("v8", func(m *driver.ModuleBuilder) {
		// getHeapStatistics(): real numbers, sourced from Go's own runtime
		// memory stats rather than invented zeros — not V8's actual heap
		// layout (there isn't one), but genuinely reflects live process
		// memory. `heap_size_limit` in particular is a *ceiling* real
		// callers branch on ("am I near the cap?"), so it has to actually
		// behave like one rather than just echoing current usage (which
		// would make used/limit never approach 1, and available always
		// read as near-zero on a perfectly healthy process): Go's own
		// soft memory limit (`debug.SetMemoryLimit(-1)` reads without
		// changing it) is the closest real equivalent, falling back to
		// "no limit configured" (`math.MaxInt64`, same as Go itself
		// reports when GOMEMLIMIT/SetMemoryLimit was never set) rather
		// than inventing one.
		m.Function("getHeapStatistics", func() map[string]float64 {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			limit := debug.SetMemoryLimit(-1)
			if limit <= 0 || limit == math.MaxInt64 {
				limit = math.MaxInt64
			}
			available := float64(limit) - float64(ms.HeapAlloc)
			if limit == math.MaxInt64 || available < 0 {
				available = float64(ms.HeapSys - ms.HeapInuse)
			}
			return map[string]float64{
				"total_heap_size":             float64(ms.HeapSys),
				"total_heap_size_executable":  0,
				"total_physical_size":         float64(ms.HeapInuse),
				"total_available_size":        available,
				"used_heap_size":              float64(ms.HeapAlloc),
				"heap_size_limit":             float64(limit),
				"malloced_memory":             float64(ms.HeapAlloc),
				"peak_malloced_memory":        float64(ms.HeapInuse),
				"does_zap_garbage":            0,
				"number_of_native_contexts":   1,
				"number_of_detached_contexts": 0,
			}
		})

		// setFlagsFromString(): a genuine no-op, not a stub standing in for
		// unfinished work — there is no V8 to pass engine flags to, so
		// "accept the call, change nothing" is the only honest behavior
		// (matching what real Node itself does for a flag it doesn't
		// recognize: silently ignore it, never throw).
		m.Function("setFlagsFromString", func(flags string) {})

		// startupSnapshot: paserati never runs from a V8 startup snapshot,
		// so isBuildingSnapshot() is always, correctly, false — and every
		// snapshot-lifecycle callback registrar is a real no-op for the
		// same reason (nothing will ever invoke a callback that would only
		// fire during snapshot serialization).
		m.Namespace("startupSnapshot", func(ns *driver.NamespaceBuilder) {
			ns.Function("isBuildingSnapshot", func() bool { return false })
			ns.Function("addSerializeCallback", func(vm.Value) {})
			ns.Function("addDeserializeCallback", func(vm.Value) {})
			ns.Function("setDeserializeMainFunction", func(vm.Value) {})
		})

		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:v8", "v8")
}
