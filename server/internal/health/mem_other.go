//go:build !linux && !darwin

package health

func memory() (rss, heap uint64)       { return goMemory() }
func hostMemory() (total, free uint64) { return 0, 0 }
