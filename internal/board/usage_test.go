package board

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/sperano/vigie/internal/hidden"
	"github.com/sperano/vigie/internal/source"
)

func TestBuildPutsUsageApart(t *testing.T) {
	t.Parallel()
	const url = "https://claude.ai/settings/usage"
	rows := []source.Row{
		{Source: source.SourceUsage, Origin: source.OriginKelos, Name: "codex-oauth", State: source.StateLimited, NeedsYou: true,
			Since: testNow.Add(-time.Hour)},
		{Source: source.SourcePR, Name: "vigie#3", State: "review", NeedsYou: true},
		{Source: source.SourceUsage, Origin: source.OriginKelos, Name: "claude-oauth-primary", State: source.StateOK,
			Target: source.Target{URL: url}},
		{Source: source.SourceKelosSession, Name: "eric", State: source.StateIdle},
		source.ErrorRow(source.SourceUsage, errors.New("secret unreadable")),
	}
	// A usage row in the hidden file stays in the header: nothing there can
	// unhide it.
	set := hidden.New(hidden.Entry{Source: source.SourceUsage, Name: "codex-oauth"})
	b := Build(rows, set, false, false, testNow)

	var names []string
	for _, e := range b.Entries {
		names = append(names, e.Row.Name)
	}
	if want := []string{"vigie#3", "eric"}; !slices.Equal(names, want) {
		t.Errorf("entries = %q, want %q", names, want)
	}
	if want := (Counts{NeedYou: 1, Idle: 1}); b.Counts != want {
		t.Errorf("counts = %+v, want %+v: usage is neither counted nor hidden", b.Counts, want)
	}
	want := []Entry{
		{Row: rows[4], Mark: MarkNeedsYou, Origin: source.OriginKelos, Age: unknownAge, Severity: SeverityFailed},
		{Row: rows[2], Origin: source.OriginKelos, Age: unknownAge, Target: Target{Kind: TargetURL, Value: url}, Severity: SeverityQuiet},
		{Row: rows[0], Mark: MarkNeedsYou, Origin: source.OriginKelos, Age: "1h", Severity: SeverityBlocked},
	}
	if len(b.Usage) != len(want) {
		t.Fatalf("usage = %+v, want %d entries", b.Usage, len(want))
	}
	for i, w := range want {
		g := b.Usage[i]
		if g.Row.Name != w.Row.Name || g.Hidden || g.Mark != w.Mark || g.Origin != w.Origin || g.Age != w.Age ||
			g.Target != w.Target || g.Severity != w.Severity {
			t.Errorf("usage %d = %+v\nwant      %+v", i, g, w)
		}
	}
}

func TestBuildWithoutUsage(t *testing.T) {
	t.Parallel()
	b := Build([]source.Row{{Source: source.SourcePR, Name: "vigie#3"}}, hidden.Set{}, false, false, testNow)
	if len(b.Usage) != 0 {
		t.Errorf("usage = %+v, want none", b.Usage)
	}
}
