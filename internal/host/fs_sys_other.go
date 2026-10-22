//go:build !darwin && !linux

package host

import (
	"errors"
	"os"
	"syscall"
	"time"
)

func errnoName(syscall.Errno) string { return "" }

func fsStatfsRaw(string) ([8]uint64, error) {
	return [8]uint64{}, errors.New("statfs is not supported on this platform")
}

func fsLutimes(path string, atimeNs, mtimeNs int64) error {
	return os.Chtimes(path, time.Unix(0, atimeNs), time.Unix(0, mtimeNs))
}

const fsHasLchmod = false

func fsLchmod(string, uint32) (string, error) { return "lchmod", errors.New("lchmod is not supported") }

var fsOpenFlagConsts = []fsConst{{"O_TRUNC", syscall.O_TRUNC}, {"O_APPEND", syscall.O_APPEND}}
var fsBaseOpenConsts = []fsConst{{"O_RDONLY", syscall.O_RDONLY}, {"O_WRONLY", syscall.O_WRONLY}, {"O_RDWR", syscall.O_RDWR}}
var fsCreatConsts = []fsConst{{"O_CREAT", syscall.O_CREAT}, {"O_EXCL", syscall.O_EXCL}}
