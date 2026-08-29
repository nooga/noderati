package host

import (
	"fmt"
	"os"
	"path/filepath"

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
	if err := p.PreloadAllNativeModules(); err != nil {
		fmt.Fprintf(os.Stderr, "noderati: preload native modules: %v\n", err)
	}
	p.AddResolver(NewNodeModulesResolver(entryScriptDirs(argv)...))
	p.AddResolver(NewOSPathResolver())
	return p
}

func entryScriptDirs(argv []string) []string {
	if len(argv) < 2 {
		return nil
	}
	script := argv[1]
	if script == "-e" || script == "-p" {
		return nil
	}
	abs, err := filepath.Abs(script)
	if err != nil {
		return nil
	}
	return []string{filepath.Dir(abs)}
}

func installModules(p *driver.Paserati) {
	declarePath(p)
	declareOS(p)
	declareUtil(p)
	declareFS(p)
	declareURL(p)
	declareQuerystring(p)
	declareAssert(p)
	declareChildProcess(p)
	declareCJSInterop(p)
}
