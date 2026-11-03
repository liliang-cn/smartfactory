package health

import "fmt"

// Warning is something off, said for a factory's IT person, with what to do about it.
type Warning struct {
	// Level is "bad" (act now) or "watch".
	Level string `json:"level"`
	Text  string `json:"text"`
	Hint  string `json:"hint"`
}

// StepBudgetMs is the time the simulation has for a step (it steps ten times a second).
const StepBudgetMs = 100

// Check is what is off in the numbers given.
func Check(host Host, rss uint64, dir DirUsage, last Summary, b BrowserSummary) []Warning {
	out := []Warning{}
	if dir.Size > 0 {
		pct := float64(dir.Free) / float64(dir.Size) * 100
		switch {
		case pct < 5:
			out = append(out, Warning{"bad", fmt.Sprintf("数据所在的磁盘只剩 %.1f%%（%s）", pct, Bytes(dir.Free)),
				"磁盘满了读数就存不进去。清掉磁盘上别的大文件，或把保留天数（series.retention）调短，或换一块更大的盘。"})
		case pct < 10:
			out = append(out, Warning{"watch", fmt.Sprintf("数据所在的磁盘只剩 %.1f%%（%s）", pct, Bytes(dir.Free)),
				"留意下面哪一项占得最多；读数占得多可以把保留天数（series.retention）调短。"})
		}
	}
	if host.MemTotal > 0 {
		if host.MemFree > 0 {
			used := 100 - float64(host.MemFree)/float64(host.MemTotal)*100
			if used > 90 {
				out = append(out, Warning{"bad", fmt.Sprintf("这台机器的内存用掉了 %.0f%%", used),
					"再涨系统会杀掉进程。看看机器上还跑了什么别的程序，或者加内存。"})
			} else if used > 80 {
				out = append(out, Warning{"watch", fmt.Sprintf("这台机器的内存用掉了 %.0f%%", used),
					"还够用，但余量不多了。机器上别的程序也在用内存的话，考虑加内存。"})
			}
		}
		if share := float64(rss) / float64(host.MemTotal) * 100; share > 50 {
			out = append(out, Warning{"watch", fmt.Sprintf("SmartFactory 占了这台机器 %.0f%% 的内存", share),
				"设备很多的工厂会这样。把不用的公司删掉，或者加内存。"})
		}
	}
	switch {
	case last.StepMs > StepBudgetMs*0.8:
		out = append(out, Warning{"bad", fmt.Sprintf("仿真跟不上：每步平均 %.0f ms，只有 %d ms", last.StepMs, StepBudgetMs),
			"孪生里的时间会走得比现实慢。减少设备或工厂，或换一台 CPU 更快的机器。"})
	case last.StepMaxMs > StepBudgetMs:
		out = append(out, Warning{"watch", fmt.Sprintf("仿真偶尔跟不上：最慢的一步用了 %.0f ms", last.StepMaxMs),
			"偶尔一次没关系；经常出现的话，看看机器是不是被别的程序占满了。"})
	}
	if last.CPU >= 0 && host.CPUs > 0 && last.CPU > float64(host.CPUs)*100*0.8 {
		out = append(out, Warning{"bad", fmt.Sprintf("SmartFactory 用满了机器的 CPU（%.0f%%）", last.CPU),
			"孪生和助手都会变慢。减少设备或工厂，或换一台核更多的机器。"})
	}
	if b.Count >= 3 && b.MedianFPS > 0 && b.MedianFPS < SlowFPS {
		out = append(out, Warning{"watch", fmt.Sprintf("看孪生的电脑大多很卡：中位帧率 %.0f", b.MedianFPS),
			"多半是电脑或手机的显卡弱。可以在大屏上用性能好些的电脑，或者少开几个标签页。"})
	}
	return out
}

// Bytes says a size the way people read it.
func Bytes(n uint64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1f GB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.0f MB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.0f KB", float64(n)/k)
	}
	return fmt.Sprintf("%d B", n)
}
