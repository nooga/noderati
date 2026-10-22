package host

import (
	"github.com/nooga/paserati/pkg/driver"
)

// fsConstantEntries is the shared list behind both `fs.constants` and the
// standalone (deprecated in real Node, but still `require()`d directly by
// real packages — e.g. graceful-fs) `constants` module. Real Node's
// `constants`/`fs.constants` also cover errno codes and signal numbers;
// this only covers the file-access and open-flag constants real packages
// actually seem to need in practice. Extend when a real package needs more
// (ledger group A — fill gaps, don't fake).
type fsConst struct {
	Name  string
	Value int
}

func fsConstantEntries() []fsConst {
	entries := []fsConst{{"UV_FS_SYMLINK_DIR", 1}, {"UV_FS_SYMLINK_JUNCTION", 2}}
	entries = append(entries, fsBaseOpenConsts...)
	entries = append(entries,
		fsConst{"UV_DIRENT_UNKNOWN", 0}, fsConst{"UV_DIRENT_FILE", 1}, fsConst{"UV_DIRENT_DIR", 2},
		fsConst{"UV_DIRENT_LINK", 3}, fsConst{"UV_DIRENT_FIFO", 4}, fsConst{"UV_DIRENT_SOCKET", 5},
		fsConst{"UV_DIRENT_CHAR", 6}, fsConst{"UV_DIRENT_BLOCK", 7},
		fsConst{"S_IFMT", sIFMT}, fsConst{"S_IFREG", sIFREG}, fsConst{"S_IFDIR", sIFDIR},
		fsConst{"S_IFCHR", sIFCHR}, fsConst{"S_IFBLK", sIFBLK}, fsConst{"S_IFIFO", sIFIFO},
		fsConst{"S_IFLNK", sIFLNK}, fsConst{"S_IFSOCK", sIFSOCK},
	)
	entries = append(entries, fsCreatConsts...)
	entries = append(entries, fsOpenFlagConsts...)
	entries = append(entries,
		fsConst{"S_IRWXU", 0o700}, fsConst{"S_IRUSR", 0o400}, fsConst{"S_IWUSR", 0o200}, fsConst{"S_IXUSR", 0o100},
		fsConst{"S_IRWXG", 0o70}, fsConst{"S_IRGRP", 0o40}, fsConst{"S_IWGRP", 0o20}, fsConst{"S_IXGRP", 0o10},
		fsConst{"S_IRWXO", 0o7}, fsConst{"S_IROTH", 0o4}, fsConst{"S_IWOTH", 0o2}, fsConst{"S_IXOTH", 0o1},
		fsConst{"F_OK", 0}, fsConst{"R_OK", 4}, fsConst{"W_OK", 2}, fsConst{"X_OK", 1},
		fsConst{"UV_FS_COPYFILE_EXCL", 1}, fsConst{"COPYFILE_EXCL", 1},
		fsConst{"UV_FS_COPYFILE_FICLONE", 2}, fsConst{"COPYFILE_FICLONE", 2},
		fsConst{"UV_FS_COPYFILE_FICLONE_FORCE", 4}, fsConst{"COPYFILE_FICLONE_FORCE", 4},
	)
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
