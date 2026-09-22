//go:build linux

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
	ctime := time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec))
	return statSys{
		dev:     uint64(st.Dev),
		ino:     uint64(st.Ino),
		mode:    uint32(st.Mode),
		nlink:   uint64(st.Nlink),
		uid:     st.Uid,
		gid:     st.Gid,
		rdev:    uint64(st.Rdev),
		blksize: int64(st.Blksize),
		blocks:  int64(st.Blocks),
		atime:   time.Unix(int64(st.Atim.Sec), int64(st.Atim.Nsec)),
		ctime:   ctime,
		// stat(2) has no birth time; Node documents ctime as an allowed
		// stand-in where the filesystem doesn't provide one.
		birthtime: ctime,
	}, true
}
