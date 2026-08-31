package host

import (
	"runtime"
	"syscall"

	"github.com/nooga/paserati/pkg/driver"
)

// fsConstantEntries is the shared list behind both `fs.constants` and the
// standalone (deprecated in real Node, but still `require()`d directly by
// real packages — e.g. graceful-fs) `constants` module. Real Node's
// `constants`/`fs.constants` also cover errno codes and signal numbers;
// this only covers the file-access and open-flag constants real packages
// actually seem to need in practice. Extend when a real package needs more
// (ledger group A — fill gaps, don't fake).
func fsConstantEntries() []struct {
	Name  string
	Value int
} {
	entries := []struct {
		Name  string
		Value int
	}{
		{"F_OK", 0},
		{"X_OK", 1},
		{"W_OK", 2},
		{"R_OK", 4},
		{"O_RDONLY", syscall.O_RDONLY},
		{"O_WRONLY", syscall.O_WRONLY},
		{"O_RDWR", syscall.O_RDWR},
		{"O_CREAT", syscall.O_CREAT},
		{"O_EXCL", syscall.O_EXCL},
		{"O_TRUNC", syscall.O_TRUNC},
		{"O_APPEND", syscall.O_APPEND},
	}
	// O_SYMLINK (open the symlink itself, don't follow it) only exists on
	// BSD-family platforms (Darwin included) — real Node only defines it
	// there too. Packages that need it (graceful-fs among them) already
	// feature-detect via `constants.hasOwnProperty('O_SYMLINK')`, so
	// omitting it entirely on other platforms is the correct match for
	// real Node's behavior, not a gap.
	if runtime.GOOS == "darwin" {
		entries = append(entries, struct {
			Name  string
			Value int
		}{"O_SYMLINK", 0x200000})
	}
	return entries
}

// declareConstants registers the legacy, standalone `constants` module.
// Deprecated in real Node in favor of `fs.constants`/`os.constants`, but
// still `require()`d directly by real packages on disk (graceful-fs is
// the one that surfaced this gap).
func declareConstants(p *driver.Paserati) {
	p.DeclareModule("constants", func(m *driver.ModuleBuilder) {
		for _, c := range fsConstantEntries() {
			m.Const(c.Name, c.Value)
		}
	})
}
