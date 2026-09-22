//go:build darwin

package host

import (
	"os"
	"syscall"
	"time"
)

func statSysFields(info os.FileInfo) (statSys, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return statSys{}, false
	}
	return statSys{
		dev:       uint64(st.Dev),
		ino:       st.Ino,
		mode:      uint32(st.Mode),
		nlink:     uint64(st.Nlink),
		uid:       st.Uid,
		gid:       st.Gid,
		rdev:      uint64(st.Rdev),
		blksize:   int64(st.Blksize),
		blocks:    st.Blocks,
		atime:     time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec),
		ctime:     time.Unix(st.Ctimespec.Sec, st.Ctimespec.Nsec),
		birthtime: time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec),
	}, true
}
