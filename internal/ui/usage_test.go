package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

func TestUsageLinesWrapAtWidth(t *testing.T) {
	t.Parallel()
	primary := "claude-oauth-primary ok 7d 3% resets 3d"
	codex := "● codex-oauth limited [plus] 5h 100% resets 2h"
	indent := strings.Repeat(" ", len(usagePrefix))
	m := newTestModel(&fakeOpener{}, false, usageRows()...)
	usage := m.build(testNow).Usage
	tests := []struct {
		name  string
		width int
		want  []string
	}{
		{"no width keeps one line", 0, []string{wantUsageLine}},
		{"wide enough keeps one line", ansi.StringWidth(wantUsageLine), []string{wantUsageLine}},
		{"one cell short wraps", ansi.StringWidth(wantUsageLine) - 1, []string{usagePrefix + primary, indent + codex}},
		{"an account wider than a line is truncated", len(usagePrefix) + 10, []string{usagePrefix + "claude-oa…", indent + "● codex-o…"}},
		{"a width the indent fills keeps an ellipsis per account", len(usagePrefix) - 2, []string{usagePrefix + "…", indent + "…"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := usageLines(styles{}, usage, tt.width)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("usageLines(%d) = %q\nwant %q", tt.width, got, tt.want)
			}
		})
	}
}

func TestNarrowViewWrapsUsageAndShrinksTheBody(t *testing.T) {
	t.Parallel()
	const height = 10
	narrow := ansi.StringWidth(wantUsageLine) - 1
	m := newTestModel(&fakeOpener{}, false, usageRows()...)
	m = send(m, tea.WindowSizeMsg{Width: narrow, Height: height})
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != height {
		t.Errorf("view has %d lines, want %d", len(lines), height)
	}
	want := strings.Repeat(" ", len(usagePrefix)) + "● codex-oauth limited [plus] 5h 100% resets 2h"
	if lines[2] != want {
		t.Errorf("second usage line = %q\nwant              %q", lines[2], want)
	}
	if got, want := m.bodyHeight(), height-chromeLines-2; got != want {
		t.Errorf("body height = %d, want %d", got, want)
	}
}

func TestSnapshotWrapsUsageAtWidth(t *testing.T) {
	t.Parallel()
	narrow := ansi.StringWidth(wantUsageLine) - 1
	lines := strings.Split(Snapshot(usageRows(), hidden.Set{}, testNow, narrow, false), "\n")
	want := []string{
		usagePrefix + "claude-oauth-primary ok 7d 3% resets 3d",
		strings.Repeat(" ", len(usagePrefix)) + "● codex-oauth limited [plus] 5h 100% resets 2h",
		"",
	}
	if got := lines[1:4]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("snapshot lines 1-3 = %q\nwant %q", got, want)
	}
}

func TestShortViewCapsUsageToKeepTheHeight(t *testing.T) {
	t.Parallel()
	narrow := ansi.StringWidth(wantUsageLine) - 1
	height := chromeLines + minBodyLines + 1 // room for one of the two usage lines
	m := newTestModel(&fakeOpener{}, false, usageRows()...)
	m = send(m, tea.WindowSizeMsg{Width: narrow, Height: height})
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != height {
		t.Errorf("view has %d lines, want %d", len(lines), height)
	}
	if want := usagePrefix + "claude-oauth-primary ok 7d 3% resets 3d"; lines[1] != want {
		t.Errorf("usage line = %q, want %q", lines[1], want)
	}
	if lines[2] != "" {
		t.Errorf("line after the capped usage = %q, want the blank line", lines[2])
	}
	if got := m.bodyHeight(); got != minBodyLines {
		t.Errorf("body height = %d, want %d", got, minBodyLines)
	}
}
