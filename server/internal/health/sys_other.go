//go:build !unix && !windows

package health

import "time"

func cpuTime() (time.Duration, bool)  { return 0, false }
func disk(string) (free, size uint64) { return 0, 0 }
