package host

import (
	"os"
	"runtime/debug"
	"testing"
)

// TestMain keeps the package's tests off the user's on-disk wasm cache:
// many of them compile modules, and a test run should leave nothing behind.
func TestMain(m *testing.M) {
	os.Setenv(wasmCacheEnv, "0")
	os.Exit(m.Run())
}

func TestWazeroBuildKeyFrom(t *testing.T) {
	const wz = "github.com/tetratelabs/wazero"
	other := &debug.Module{Path: "golang.org/x/sys", Version: "v0.44.0"}
	cases := []struct {
		name string
		deps []*debug.Module
		want string
	}{
		{"plain requirement", []*debug.Module{other, {Path: wz, Version: "v1.12.0"}},
			"github.com_tetratelabs_wazero_v1.12.0"},
		{"replaced by a fork keys on the fork, not the requirement",
			[]*debug.Module{{Path: wz, Version: "v1.12.0", Replace: &debug.Module{
				Path: "github.com/nooga/wazero", Version: "v1.12.1-0.20260911172836-0ec6142ae8c7"}}},
			"github.com_nooga_wazero_v1.12.1-0.20260911172836-0ec6142ae8c7"},
		{"replaced by a local directory has no stable key",
			[]*debug.Module{{Path: wz, Version: "v1.12.0", Replace: &debug.Module{Path: "../wazero"}}}, ""},
		{"devel build", []*debug.Module{{Path: wz, Version: "(devel)"}}, ""},
		{"wazero not linked", []*debug.Module{other}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wazeroBuildKeyFrom(c.deps); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestWasmCacheDirOff(t *testing.T) {
	t.Setenv(wasmCacheEnv, "0")
	if dir := wasmCacheDir(); dir != "" {
		t.Fatalf("%s=0 should disable the disk cache, got %q", wasmCacheEnv, dir)
	}
}
