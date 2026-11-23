package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sperano/vigie/internal/config"
	"github.com/sperano/vigie/internal/source"
)

func TestHiddenStoreUsesDefaultLegacyFallback(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("HOME", t.TempDir())
	store, err := hiddenStore("")
	if err != nil {
		t.Fatal(err)
	}
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(userConfigDir, "vigie", "hidden.json"); store.Path != want {
		t.Errorf("Path = %q, want %q", store.Path, want)
	}
	if want := filepath.Join(userConfigDir, "atop", "hidden.json"); store.LegacyPath != want {
		t.Errorf("LegacyPath = %q, want %q", store.LegacyPath, want)
	}
}

func TestHiddenStoreCustomPathHasNoLegacyFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.json")
	store, err := hiddenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Path != path || store.LegacyPath != "" {
		t.Errorf("hiddenStore = %+v, want Path %q and no LegacyPath", store, path)
	}
}

func TestBuildSources(t *testing.T) {
	agentAndWork := []string{source.SourceKelosSession, source.SourceKelosTask, source.SourcePR, source.SourceVikunja}
	withUsage := append(slices.Clone(agentAndWork), source.SourceUsage)
	tests := []struct {
		name string
		cfg  config.Config
		want []string
	}{
		{"tui reads local sessions", config.Config{}, append([]string{source.SourceLocal}, agentAndWork...)},
		{"serve leaves local sessions out", config.Config{Serve: true}, agentAndWork},
		{"claude accounts add usage", config.Config{Serve: true, ClaudeSecrets: config.NameList{"c"}}, withUsage},
		{"codex account adds usage", config.Config{Serve: true, CodexSecret: "x"}, withUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources, err := buildSources(tt.cfg)
			if err != nil {
				t.Fatalf("buildSources: %v", err)
			}
			if got := sourceNames(sources); !slices.Equal(got, tt.want) {
				t.Errorf("sources = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildSourcesRefusesAHalfSetGitHubApp(t *testing.T) {
	t.Setenv(source.GitHubAppIDEnv, "12345")
	t.Setenv(source.GitHubAppInstallationIDEnv, "")
	t.Setenv(source.GitHubAppPrivateKeyEnv, "")
	_, err := buildSources(config.Config{Serve: true})
	want := "github app: " + source.GitHubAppInstallationIDEnv + ", " + source.GitHubAppPrivateKeyEnv +
		" not set, but the other GitHub App settings are"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}
