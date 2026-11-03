package health

import "golang.org/x/sys/unix"

// memory is what the Go runtime has mapped (macOS gives a process no cheap resident size) and
// its heap.
func memory() (rss, heap uint64) { return goMemory() }

// hostMemory is the machine's memory; how much is free macOS does not say simply.
func hostMemory() (total, free uint64) {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, 0
	}
	return n, 0
}
