package host

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
)

// wasmCacheEnv turns the on-disk cache off when set to "0"; the in-memory
// cache stays, so a module compiled once per process is still compiled once.
const wasmCacheEnv = "NODERATI_WASM_CACHE"

// wasmCompilationCache is shared by every runtime newWasmRuntime builds.
// Sharing matters within one process as well as across them: a module is
// compiled once against a scratch runtime to validate it and again for each
// Instance, and with a shared cache the later compiles are lookups.
var wasmCompilationCache = sync.OnceValue(func() wazero.CompilationCache {
	if dir := wasmCacheDir(); dir != "" {
		if c, err := wazero.NewCompilationCacheWithDir(dir); err == nil {
			return c
		}
	}
	return wazero.NewCompilationCache()
})

// wasmCacheDir names the on-disk cache, or "" when there should not be one.
//
// wazero keys its files by its own version string, which it reads from the
// build info as the *required* version. Under a replace directive that is
// still v1.12.0 whichever fork commit was built in, so two noderati builds on
// different wazero pins would load each other's native code. The directory
// is therefore named after the wazero that was actually linked.
func wasmCacheDir() string {
	if os.Getenv(wasmCacheEnv) == "0" {
		return ""
	}
	key := wazeroBuildKey()
	if key == "" {
		return ""
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "noderati", "wasm", key+"-"+runtime.GOOS+"-"+runtime.GOARCH)
}

func wazeroBuildKey() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return wazeroBuildKeyFrom(info.Deps)
}

// wazeroBuildKeyFrom returns a path-safe key for the linked wazero, or ""
// when the build does not pin one: a replace to a local directory carries no
// version, and its contents can change between builds.
func wazeroBuildKeyFrom(deps []*debug.Module) string {
	for _, dep := range deps {
		if dep.Path != "github.com/tetratelabs/wazero" {
			continue
		}
		m := dep
		if dep.Replace != nil {
			m = dep.Replace
		}
		if m.Version == "" || m.Version == "(devel)" {
			return ""
		}
		return strings.NewReplacer("/", "_", "@", "_").Replace(m.Path + "@" + m.Version)
	}
	return ""
}
