package main

import (
	"fmt"
	"strings"
	"time"
)

var sparkLevels = []rune("▁▂▃▄▅▆▇█")

// sparkline compresses a series into one line. An all-zero series still gets a
// baseline so the shape of the layout does not jump around.
func sparkline(vals []int64) string {
	if len(vals) == 0 {
		return ""
	}
	var max int64
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	var b strings.Builder
	for _, v := range vals {
		if max == 0 {
			b.WriteRune(sparkLevels[0])
			continue
		}
		i := int(v * int64(len(sparkLevels)-1) / max)
		if i < 0 {
			i = 0
		}
		if i >= len(sparkLevels) {
			i = len(sparkLevels) - 1
		}
		b.WriteRune(sparkLevels[i])
	}
	return b.String()
}

// bar draws a proportional bar. A non-zero value always gets one cell, so the
// tail of a distribution stays visible.
func bar(v, max, width int) string {
	if max <= 0 || width <= 0 || v <= 0 {
		return ""
	}
	n := v * width / max
	if n < 1 {
		n = 1
	}
	return strings.Repeat("█", n)
}

func humanN(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func humanDur(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%.1fh", d.Hours())
	}
}

// ago renders a unix timestamp as a compact distance from now.
func ago(ts int64) string {
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < 0:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func clock(ts int64) string {
	return time.Unix(ts, 0).Format("01-02 15:04")
}

func pct(part, whole int) string {
	if whole <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(part)/float64(whole))
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

func pad(s string, w int) string {
	n := w - len([]rune(s))
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}

func padLeft(s string, w int) string {
	n := w - len([]rune(s))
	if n <= 0 {
		return s
	}
	return strings.Repeat(" ", n) + s
}
