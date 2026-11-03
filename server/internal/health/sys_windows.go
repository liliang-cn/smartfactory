//go:build windows

package health

import (
	"time"

	"golang.org/x/sys/windows"
)

func cpuTime() (time.Duration, bool) {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u); err != nil {
		return 0, false
	}
	// Filetime counts 100 ns intervals
	ticks := int64(k.HighDateTime)<<32 | int64(k.LowDateTime) + int64(u.HighDateTime)<<32 | int64(u.LowDateTime)
	return time.Duration(ticks * 100), true
}

func disk(dir string) (free, size uint64) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0
	}
	var avail, total, all uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &all); err != nil {
		return 0, 0
	}
	return avail, total
}
