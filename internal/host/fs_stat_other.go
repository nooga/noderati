//go:build !darwin && !linux

package host

import "os"

func statSysFields(_ os.FileInfo) (statSys, bool) {
	return statSys{}, false
}
