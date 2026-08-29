package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
	"golang.org/x/term"
)

func declareTTY(p *driver.Paserati) {
	vmInst := p.GetVM()
	p.DeclareModule("tty", func(m *driver.ModuleBuilder) {
		m.Function("isatty", func(fd float64) bool {
			return term.IsTerminal(int(fd))
		})
		m.Function("ReadStream", func() vm.Value {
			obj := newEventEmitterObject(vmInst)
			obj.SetOwn("isTTY", vm.False)
			return vm.NewValueFromPlainObject(obj)
		})
		m.Function("WriteStream", func() vm.Value {
			obj := newEventEmitterObject(vmInst)
			obj.SetOwn("isTTY", vm.False)
			return vm.NewValueFromPlainObject(obj)
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:tty", "tty")
}
