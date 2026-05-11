package monitor

import (
	"syscall"
)

// statFS returns total bytes, used bytes, and usage percentage for a mount point.
func statFS(path string) (total, used uint64, percent float64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return
	}

	total = stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used = total - free

	if total > 0 {
		percent = (float64(used) / float64(total)) * 100.0
	}

	return
}
