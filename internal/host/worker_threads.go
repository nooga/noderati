package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

func declareWorkerThreads(p *driver.Paserati) {
	p.DeclareModule("worker_threads", func(m *driver.ModuleBuilder) {
		m.Const("parentPort", nil)
		m.Const("isMainThread", true)
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:worker_threads", "worker_threads")
}

func installWorkerThreadsExports(p *driver.Paserati) {
	vmInst := p.GetVM()
	rec, err := p.LoadModule("worker_threads", ".")
	if err != nil {
		return
	}
	exports := rec.GetExportValues()
	workerCtor := buildWorkerConstructor(vmInst)
	exports["Worker"] = workerCtor

	proto := vm.Undefined
	if vmInst != nil {
		proto = vmInst.ObjectPrototype
	}
	ns := vm.NewObject(proto).AsPlainObject()
	for name, val := range exports {
		if name == "default" {
			continue
		}
		ns.SetOwn(name, val)
	}
	exports["default"] = vm.NewValueFromPlainObject(ns)
}

// buildWorkerConstructor: a real Worker needs a second JS execution context
// running concurrently with the main one, on its own goroutine, with real
// message passing between them - not a host-shim question but an engine
// one. Measured directly (round 67, docs/real-node-plan.md) rather than
// assumed: running two paserati VM instances concurrently races under `go
// test -race`, inside Paserati.PreloadAllNativeModules's own module-loader
// path, before any user code even runs - so real concurrent worker_threads
// isn't something this host layer can build on top of today. That's a
// paserati-side capability gap, not a noderati one.
//
// This constructor previously returned an object built from
// vm.NewNativeFunction, which - unlike vm.NewNativeConstructor - defaults
// IsConstructor to false. `new Worker(...)` therefore never even reached
// this function's body: paserati's own `new`-dispatch checks
// IsConstructor first and throws its own generic "Worker is not a
// constructor" TypeError before calling in. The postMessage/terminate
// no-ops the object used to return were consequently unreachable dead
// code - whatever real code did after `new Worker(...)` (pi's own
// dist/utils/image-resize.js, the one real call site in the whole pi
// dependency tree, does `worker.once("message", ...)` next) was reacting
// to that same generic message, not to anything this file wrote. Confirmed
// by testing `new Worker(...)` in isolation before assuming which bug was
// live. Fixed by switching to vm.NewNativeConstructor and throwing our own
// specific, honest error instead of leaving paserati's generic one: pi's
// own resizeImage() has a real, deliberate fallback for exactly this case
// (its own comment: "If the worker cannot be loaded ... fall back to
// in-process resizing"), so either error reaches it correctly - the
// difference is only that ours names the real reason.
func buildWorkerConstructor(vmInst *vm.VM) vm.Value {
	return vm.NewNativeConstructor(0, false, "Worker", func(_ []vm.Value) (vm.Value, error) {
		return vm.Undefined, simpleNodeError(vmInst, "ERR_WORKER_NOT_SUPPORTED",
			"worker_threads.Worker is not implemented: noderati cannot yet run a second concurrent JS execution context")
	})
}
