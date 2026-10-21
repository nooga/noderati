//go:build linux

package host

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

func errnoName(e syscall.Errno) string { return unix.ErrnoName(e) }

func fsStatfsRaw(path string) ([8]uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return [8]uint64{}, err
	}
	return [8]uint64{uint64(st.Type), uint64(st.Bsize), uint64(st.Frsize), st.Blocks, st.Bfree, st.Bavail, st.Files, st.Ffree}, nil
}

func fsLutimes(path string, atimeNs, mtimeNs int64) error {
	ts := []unix.Timespec{unix.NsecToTimespec(atimeNs), unix.NsecToTimespec(mtimeNs)}
	return unix.UtimesNanoAt(unix.AT_FDCWD, path, ts, unix.AT_SYMLINK_NOFOLLOW)
}

// fsHasLchmod: real Node only defines fs.lchmod on macOS.
const fsHasLchmod = false

func fsLchmod(string, uint32) (string, error) { return "lchmod", errors.New("lchmod is not supported") }

// fsOpenFlagConsts is the platform-specific slice of real Node's
// fs.constants, between O_EXCL and S_IRWXU, in Node's own order.
var fsOpenFlagConsts = []fsConst{
	{"UV_FS_O_FILEMAP", 0}, {"O_NOCTTY", unix.O_NOCTTY}, {"O_TRUNC", unix.O_TRUNC}, {"O_APPEND", unix.O_APPEND},
	{"O_DIRECTORY", unix.O_DIRECTORY}, {"O_NOATIME", unix.O_NOATIME}, {"O_NOFOLLOW", unix.O_NOFOLLOW},
	{"O_SYNC", unix.O_SYNC}, {"O_DSYNC", unix.O_DSYNC}, {"O_DIRECT", unix.O_DIRECT}, {"O_NONBLOCK", unix.O_NONBLOCK},
}

var fsBaseOpenConsts = []fsConst{{"O_RDONLY", unix.O_RDONLY}, {"O_WRONLY", unix.O_WRONLY}, {"O_RDWR", unix.O_RDWR}}
var fsCreatConsts = []fsConst{{"O_CREAT", unix.O_CREAT}, {"O_EXCL", unix.O_EXCL}}
