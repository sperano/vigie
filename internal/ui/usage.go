package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/sperano/vigie/internal/board"
)

const (
	// usagePrefix starts the header line of the account usage readings.
	usagePrefix = "usage: "
	// usageSep separates the accounts on that line.
	usageSep = " · "
)

// usageLines are the header lines of the account usage readings, each
// coloured by its severity and marked when it needs you; none when there are
// no readings. The accounts wrap onto further lines, indented under the
// first, so that none is cut off at width; an account wider than a line alone
// is truncated. A width of zero or less keeps them on one line.
func usageLines(st styles, usage []board.Entry, width int) []string {
	if len(usage) == 0 {
		return nil
	}
	prefixWidth, sepWidth := ansi.StringWidth(usagePrefix), ansi.StringWidth(usageSep)
	indent := strings.Repeat(" ", prefixWidth)
	var lines []string
	line, lineWidth := usagePrefix, prefixWidth
	for i, e := range usage {
		item := usageItem(e)
		w := ansi.StringWidth(item)
		switch {
		case i == 0:
		case width <= 0 || lineWidth+sepWidth+w <= width:
			line += usageSep
			lineWidth += sepWidth
		default:
			lines = append(lines, line)
			line, lineWidth = indent, prefixWidth
		}
		if width > 0 {
			item = fit(item, width-lineWidth)
		}
		line += st.entry(e).Render(item)
		lineWidth += ansi.StringWidth(item)
	}
	return append(lines, line)
}

// usageItem is one account: its mark, name, state and detail.
func usageItem(e board.Entry) string {
	parts := make([]string, 0, 4)
	if e.Mark != "" {
		parts = append(parts, e.Mark)
	}
	parts = append(parts, e.Row.DisplayName(), e.Row.State)
	if d := oneLine(e.Row.Detail); d != "" {
		parts = append(parts, d)
	}
	return strings.Join(parts, " ")
}
