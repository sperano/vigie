package board

import (
	"cmp"
	"slices"
	"time"

	"github.com/sperano/vigie/internal/hidden"
	"github.com/sperano/vigie/internal/source"
)

// isUsage reports whether r is an account-usage reading, or the usage
// source's error row: front ends show those in the header, not the table.
func isUsage(r source.Row) bool {
	return r.Source == source.SourceUsage
}

// splitUsage separates the usage rows from the others, keeping the order of
// both.
func splitUsage(rows []source.Row) (others, usage []source.Row) {
	for _, r := range rows {
		if isUsage(r) {
			usage = append(usage, r)
		} else {
			others = append(others, r)
		}
	}
	return others, usage
}

// usageEntries decorates the usage rows, sorted by name so that each
// account keeps its place in the header. They are never hidden: the header
// has no selection to hide or unhide them with.
func usageEntries(rows []source.Row, now time.Time) []Entry {
	entries := make([]Entry, 0, len(rows))
	for _, r := range rows {
		e := Entry{Row: r}
		decorate(&e, hidden.Set{}, now)
		entries = append(entries, e)
	}
	slices.SortStableFunc(entries, func(a, b Entry) int { return cmp.Compare(a.Row.Name, b.Row.Name) })
	return entries
}
