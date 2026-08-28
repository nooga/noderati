package host

import (
	"github.com/nooga/paserati/pkg/builtins"
	"github.com/nooga/paserati/pkg/driver"
)

// New builds a Paserati session with Node-like process, timers, and builtins.
func New(argv []string) *driver.Paserati {
	inits := append(builtins.GetStandardInitializers(),
		NewProcessInitializer(argv),
		driver.NewHostTimerInitializer(),
	)
	p := driver.NewPaseratiWithInitializers(inits)
	installModules(p)
	return p
}

func installModules(p *driver.Paserati) {
	declarePath(p)
	declareOS(p)
	declareUtil(p)
}
