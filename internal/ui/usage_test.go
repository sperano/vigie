package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sperano/vigie/internal/hidden"
	"github.com/sperano/vigie/internal/source"
)

// usageRows are a PR and two account readings, out of name order.
func usageRows() []source.Row {
	return []source.Row{
		{Source: source.SourcePR, Name: "vigie#3", State: "review"},
		{Source: source.SourceUsage, Name: "codex-oauth", State: source.StateLimited, Detail: "[plus]\n5h 100% resets 2h", NeedsYou: true},
		{Source: source.SourceUsage, Name: "claude-oauth-primary", State: source.StateOK, Detail: "7d 3% resets 3d"},
	}
}

const wantUsageLine = "usage: claude-oauth-primary ok 7d 3% resets 3d · ● codex-oauth limited [plus] 5h 100% resets 2h"

func TestSnapshotShowsUsageInTheHeader(t *testing.T) {
	t.Parallel()
	lines := snapshotLines(t, usageRows(), false)
	if lines[1] != wantUsageLine {
		t.Errorf("usage line = %q\nwant         %q", lines[1], wantUsageLine)
	}
	if !strings.HasSuffix(lines[0], "0 need you · 0 busy · 0 idle") {
		t.Errorf("title = %q, want usage left out of the counts", lines[0])
	}
	if lines[2] != "" || !strings.Contains(lines[3], "TYPE") {
		t.Errorf("lines 2-3 = %q, %q, want the blank line and the column header", lines[2], lines[3])
	}
	if len(lines) != 5 || !strings.Contains(lines[4], "vigie#3") {
		t.Errorf("table = %q, want the PR row alone", lines[4:])
	}
}

func TestSnapshotWithoutUsageHasNoUsageLine(t *testing.T) {
	t.Parallel()
	lines := snapshotLines(t, named("a"), false)
	if lines[1] != "" || strings.Contains(strings.Join(lines, "\n"), usagePrefix) {
		t.Errorf("snapshot = %q, want no usage line", lines)
	}
}

func TestUsageLineColoursEachAccount(t *testing.T) {
	t.Parallel()
	st := newStyles(true)
	lines := strings.Split(Snapshot(usageRows(), hidden.Set{}, testNow, testWidth, true), "\n")
	want := usagePrefix + st.rest.Render("claude-oauth-primary ok 7d 3% resets 3d") + usageSep +
		st.blocked.Render("● codex-oauth limited [plus] 5h 100% resets 2h")
	if lines[1] != want {
		t.Errorf("usage line = %q\nwant         %q", lines[1], want)
	}
}

func TestViewUsageLineTakesABodyRow(t *testing.T) {
	t.Parallel()
	const height = 10
	m := newTestModel(&fakeOpener{}, false, usageRows()...)
	m = send(m, tea.WindowSizeMsg{Width: testWidth, Height: height})
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != height {
		t.Errorf("view has %d lines, want %d", len(lines), height)
	}
	if lines[1] != wantUsageLine {
		t.Errorf("usage line = %q\nwant         %q", lines[1], wantUsageLine)
	}
	if got, want := m.bodyHeight(), height-chromeLines-1; got != want {
		t.Errorf("body height = %d, want %d", got, want)
	}
	if len(m.entries) != 1 || m.entries[0].Row.Name != "vigie#3" {
		t.Errorf("selectable entries = %+v, want the PR alone", m.entries)
	}
}
