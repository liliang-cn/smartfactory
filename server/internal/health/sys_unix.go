//go:build unix

package health

import (
	"time"

	"golang.org/x/sys/unix"
)

// cpuTime is the CPU time the process has used, user and system.
func cpuTime() (time.Duration, bool) {
	var ru unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), true
}

// disk is the free space (for unprivileged users) and the size of the volume dir is on.
func disk(dir string) (free, size uint64) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize)
}
