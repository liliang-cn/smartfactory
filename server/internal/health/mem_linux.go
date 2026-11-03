package health

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// memory is the process's resident memory (VmRSS) and its Go heap.
func memory() (rss, heap uint64) {
	total, heap := goMemory()
	if kb := procField("/proc/self/status", "VmRSS:"); kb > 0 {
		return kb * 1024, heap
	}
	return total, heap
}

// hostMemory is the machine's memory and what is still available to programs.
func hostMemory() (total, free uint64) {
	return procField("/proc/meminfo", "MemTotal:") * 1024, procField("/proc/meminfo", "MemAvailable:") * 1024
}

// procField reads "Name:   1234 kB" from a /proc file.
func procField(path, name string) uint64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), name); ok {
			n, _ := strconv.ParseUint(strings.Fields(rest)[0], 10, 64)
			return n
		}
	}
	return 0
}
