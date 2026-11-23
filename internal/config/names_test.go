package config

import (
	"reflect"
	"testing"
	"time"
)

const (
	envUsageInterval  = 3 * time.Minute
	flagUsageInterval = 30 * time.Second
)

func wantDefaultClaudeSecrets() NameList {
	return NameList{"claude-oauth-primary", "claude-oauth-secondary"}
}

func TestNameListSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want NameList
	}{
		{"one", "a", NameList{"a"}},
		{"several, trimmed", " a , b ", NameList{"a", "b"}},
		{"blank names dropped", "a,,  ,b,", NameList{"a", "b"}},
		{"repeated names dropped", "a,b, a,b", NameList{"a", "b"}},
		{"empty clears", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := NameList{"old"}
			if err := l.Set(tt.in); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(l, tt.want) {
				t.Errorf("Set(%q) = %#v, want %#v", tt.in, l, tt.want)
			}
		})
	}
}

func TestNameListString(t *testing.T) {
	t.Parallel()
	l := NameList{"a", "b"}
	if got, want := l.String(), "a,b"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

type usageSettings struct {
	claude   NameList
	active   string
	codex    string
	interval time.Duration
}

func TestParseUsage(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"VIGIE_CLAUDE_SECRETS":       "one,two",
		"VIGIE_CLAUDE_ACTIVE_SECRET": "env-active",
		"VIGIE_CODEX_SECRET":         "env-codex",
		"VIGIE_USAGE_INTERVAL":       "3m",
	}
	empty := map[string]string{
		"VIGIE_CLAUDE_SECRETS": "", "VIGIE_CODEX_SECRET": "",
	}
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want usageSettings
	}{
		{"defaults", nil, nil, usageSettings{wantDefaultClaudeSecrets(), DefaultClaudeActive, DefaultCodexSecret, DefaultUsageInterval}},
		{"env overrides default", nil, env, usageSettings{NameList{"one", "two"}, "env-active", "env-codex", envUsageInterval}},
		{"flag beats env", []string{"-claude-secrets", "three", "-codex-secret", "flag-codex", "-usage-interval", "30s"}, env,
			usageSettings{NameList{"three"}, "env-active", "flag-codex", flagUsageInterval}},
		{"active flag beats env", []string{"-claude-active-secret", "flag-active"}, env,
			usageSettings{NameList{"one", "two"}, "flag-active", "env-codex", envUsageInterval}},
		{"empty active flag marks none", []string{"-claude-active-secret="}, env,
			usageSettings{NameList{"one", "two"}, "", "env-codex", envUsageInterval}},
		{"empty flags turn the accounts off", []string{"-claude-secrets=", "-codex-secret="}, env,
			usageSettings{nil, "env-active", "", envUsageInterval}},
		{"empty env keeps the defaults", nil, empty, usageSettings{wantDefaultClaudeSecrets(), DefaultClaudeActive, DefaultCodexSecret, DefaultUsageInterval}},
		{"no legacy interval", nil, map[string]string{"ATOP_USAGE_INTERVAL": "3m"}, usageSettings{wantDefaultClaudeSecrets(), DefaultClaudeActive, DefaultCodexSecret, DefaultUsageInterval}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := Parse(testName, tt.args, envFrom(tt.env))
			if err != nil {
				t.Fatal(err)
			}
			got := usageSettings{c.ClaudeSecrets, c.ClaudeActive, c.CodexSecret, c.UsageInterval}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
