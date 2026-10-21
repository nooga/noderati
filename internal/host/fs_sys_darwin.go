//go:build darwin

package host

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func errnoName(e syscall.Errno) string { return unix.ErrnoName(e) }

// fsStatfsRaw returns real Node's StatFs fields in order: type, bsize,
// frsize, blocks, bfree, bavail, files, ffree. macOS has no f_frsize;
// libuv reports f_bsize for it.
func fsStatfsRaw(path string) ([8]uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return [8]uint64{}, err
	}
	return [8]uint64{uint64(st.Type), uint64(st.Bsize), uint64(st.Bsize), st.Blocks, st.Bfree, st.Bavail, st.Files, st.Ffree}, nil
}

func fsLutimes(path string, atimeNs, mtimeNs int64) error {
	return unix.Lutimes(path, []unix.Timeval{unix.NsecToTimeval(atimeNs), unix.NsecToTimeval(mtimeNs)})
}

// fsHasLchmod: real Node only defines fs.lchmod on macOS.
const fsHasLchmod = true

// fsLchmod mirrors real Node's own lchmod: open the link itself
// (O_SYMLINK) and fchmod it, so failures report syscall "open".
func fsLchmod(path string, mode uint32) (string, error) {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_SYMLINK, 0)
	if err != nil {
		return "open", err
	}
	defer unix.Close(fd)
	return "fchmod", unix.Fchmod(fd, mode)
}

// fsOpenFlagConsts is the platform-specific slice of real Node's
// fs.constants, between O_EXCL and S_IRWXU, in Node's own order.
var fsOpenFlagConsts = []fsConst{
	{"UV_FS_O_FILEMAP", 0}, {"O_NOCTTY", unix.O_NOCTTY}, {"O_TRUNC", unix.O_TRUNC}, {"O_APPEND", unix.O_APPEND},
	{"O_DIRECTORY", unix.O_DIRECTORY}, {"O_NOFOLLOW", unix.O_NOFOLLOW}, {"O_SYNC", unix.O_SYNC},
	{"O_DSYNC", unix.O_DSYNC}, {"O_SYMLINK", unix.O_SYMLINK}, {"O_NONBLOCK", unix.O_NONBLOCK},
}

var fsBaseOpenConsts = []fsConst{{"O_RDONLY", unix.O_RDONLY}, {"O_WRONLY", unix.O_WRONLY}, {"O_RDWR", unix.O_RDWR}}
var fsCreatConsts = []fsConst{{"O_CREAT", unix.O_CREAT}, {"O_EXCL", unix.O_EXCL}}
