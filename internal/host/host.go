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
	installBufferGlobal(p)
	installWorkerThreadsExports(p)
	dirs := append(entryScriptDirs(argv), findPiCodingAgentNodeModulesRoots()...)
	p.AddResolver(NewNodeModulesResolver(dirs...))
	p.AddResolver(NewPackageImportsResolver())
	p.AddResolver(NewJSShimResolver())
	p.AddResolver(NewOSPathResolver())
	p.AddResolver(NewNodeMissingResolver())
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
	declareChildProcess()
	installChildProcessNatives(p)
	declareReadline()
	declareTTY(p)
	declareEvents()
	declareUndici()
	declareStream()
	declareBuffer(p)
	declareCrypto(p)
	declareFSPromises(p)
	declareModule()
	declareWorkerThreads(p)
	declarePiTui()
	declarePiAi()
	declarePiAgentCore()
	declareHostedGitInfo()
	declarePerfHooks()
	declareStringDecoder()
	declareTypeboxCompile()
	declareTypebox()
	declareTypeboxValue()
	declareDiff()
	declareJiti()
	declareGlob()
	declareMinimatch()
	declareProperLockfile()
	declareCJSInterop(p)
}
