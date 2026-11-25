package ui

import (
	"strings"

	"github.com/sperano/vigie/internal/board"
	"github.com/sperano/vigie/internal/source"
)

const (
	// usagePrefix starts the header line of the account usage readings.
	usagePrefix = "usage: "
	// usageSep separates the accounts on that line.
	usageSep = " · "
)

// usageLine is the header line of the account usage readings, each coloured
// by its severity and marked when it needs you; empty when there are none.
func usageLine(st styles, usage []board.Entry) string {
	if len(usage) == 0 {
		return ""
	}
	items := make([]string, 0, len(usage))
	for _, e := range usage {
		items = append(items, st.entry(e).Render(usageItem(e)))
	}
	return usagePrefix + strings.Join(items, usageSep)
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

// hasUsage reports whether rows hold a usage row, which takes a header line.
func hasUsage(rows []source.Row) bool {
	for _, r := range rows {
		if r.Source == source.SourceUsage {
			return true
		}
	}
	return false
}
