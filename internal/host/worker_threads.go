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

func buildWorkerConstructor(vmInst *vm.VM) vm.Value {
	return vm.NewNativeFunction(0, false, "Worker", func(_ []vm.Value) (vm.Value, error) {
		proto := vm.Undefined
		if vmInst != nil {
			proto = vmInst.ObjectPrototype
		}
		obj := vm.NewObject(proto).AsPlainObject()
		obj.SetOwn("postMessage", vm.NewNativeFunction(0, true, "postMessage", func(_ []vm.Value) (vm.Value, error) {
			return vm.Undefined, nil
		}))
		obj.SetOwn("terminate", vm.NewNativeFunction(0, false, "terminate", func(_ []vm.Value) (vm.Value, error) {
			return vm.Undefined, nil
		}))
		return vm.NewValueFromPlainObject(obj), nil
	})
}
