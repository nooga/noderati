package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// uncloneableMarker is a hidden own-property structuredclone.go's own
// structuredCloneValue checks for before cloning an object - shared here
// so markAsUncloneable (below) and the actual clone-time check agree on
// the same name without either file needing to know the other's
// internals beyond this one constant.
const uncloneableMarker = "__noderatiUncloneable"

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
	installWorkerNative(p)
	exports := rec.GetExportValues()
	api := messageChannelFor(vmInst)
	exports["MessageChannel"] = api.channel
	exports["MessagePort"] = api.port
	exports["receiveMessageOnPort"] = api.receive
	exports["threadId"] = vm.NumberValue(0)
	exports["threadName"] = vm.NewString("")
	exports["isInternalThread"] = vm.False
	exports["workerData"] = vm.Null
	exports["SHARE_ENV"] = workerShareEnvSymbol(vmInst)
	exports["resourceLimits"] = vm.NewValueFromPlainObject(vm.NewObject(vmInst.ObjectPrototype).AsPlainObject())
	// markAsUncloneable(obj): real Node marks obj so a later
	// structuredClone()/postMessage attempt on it throws DataCloneError.
	// Real undici's webidl marks its Request/Response/Cache objects with it;
	// structuredclone.go's structuredCloneValue checks the marker.
	exports["markAsUncloneable"] = vm.NewNativeFunction(1, false, "markAsUncloneable", func(args []vm.Value) (vm.Value, error) {
		if len(args) > 0 && args[0].Type() == vm.TypeObject {
			if obj := args[0].AsPlainObject(); obj != nil {
				obj.SetOwnNonEnumerable(uncloneableMarker, vm.True)
			}
		}
		return vm.Undefined, nil
	})

	ns := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	for name, val := range exports {
		if name == "default" {
			continue
		}
		ns.SetOwn(name, val)
	}
	exports["default"] = vm.NewValueFromPlainObject(ns)

	// Worker, BroadcastChannel and the rest live in the JS shim
	// (worker_threads_shim.go), loaded on first use.
	lazy := threadOf(vmInst).lazy
	installLazyJSExportsFrom(p, "worker_threads", lazy,
		[]string{"getEnvironmentData", "setEnvironmentData", "markAsUntransferable", "isMarkedAsUntransferable", "moveMessagePortToContext", "postMessageToThread"},
		[]string{"Worker", "BroadcastChannel"})
	// locks is an object, resolved on first read of the default export.
	yes := true
	ns.DefineAccessorProperty("locks", vm.NewNativeFunction(0, false, "get locks", func(_ []vm.Value) (vm.Value, error) {
		return lazy.get("locks")
	}), true, vm.Undefined, false, &yes, &yes)
	exports["locks"] = vm.Undefined
	// BroadcastChannel is also a global, resolved to the real class on
	// first read so it's identical to the module's.
	g := vmInst.GlobalObject
	no := false
	g.DefineAccessorProperty("BroadcastChannel", vm.NewNativeFunction(0, false, "get BroadcastChannel", func(_ []vm.Value) (vm.Value, error) {
		return lazy.get("BroadcastChannel")
	}), true, vm.NewNativeFunction(1, false, "set BroadcastChannel", func(args []vm.Value) (vm.Value, error) {
		g.DeleteOwn("BroadcastChannel")
		g.SetOwnNonEnumerable("BroadcastChannel", argAt(args, 0))
		return vm.Undefined, nil
	}), true, &no, &yes)
}
