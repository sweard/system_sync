package packageedit

import (
	"fmt"
	"strings"
)

// UnifiedDiff shows the actual plan. A single hunk also covers distant edits
// (e.g. adding a tap and its cask) without depending on an external diff tool.
func UnifiedDiff(before, after, path string) string {
	if before == after {
		return ""
	}
	a, b := lines(before), lines(after)
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	start := prefix - 3
	if start < 0 {
		start = 0
	}
	context := suffix
	if context > 3 {
		context = 3
	}
	endA, endB := len(a)-suffix+context, len(b)-suffix+context
	rangeText := func(start, count int) string {
		if count == 1 {
			return fmt.Sprint(start + 1)
		}
		if count == 0 {
			return fmt.Sprintf("%d,0", start)
		}
		return fmt.Sprintf("%d,%d", start+1, count)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n@@ -%s +%s @@\n", path, path, rangeText(start, endA-start), rangeText(start, endB-start))
	write := func(marker byte, line string) {
		out.WriteByte(marker)
		out.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			out.WriteString("\n\\ No newline at end of file\n")
		}
	}
	for _, line := range a[start:prefix] {
		write(' ', line)
	}
	for _, line := range a[prefix : len(a)-suffix] {
		write('-', line)
	}
	for _, line := range b[prefix : len(b)-suffix] {
		write('+', line)
	}
	for _, line := range a[len(a)-suffix : endA] {
		write(' ', line)
	}
	return out.String()
}
