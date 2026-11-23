package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// The Claude probe: the smallest Messages request a subscription token may
// make. A `claude setup-token` token is scoped to inference, so it cannot
// read the account's usage page; every answer to a request, a rejected one
// included, carries the unified rate-limit headers instead.
const (
	claudeMessagesURL = "https://api.anthropic.com/v1/messages"
	claudeAPIVersion  = "2023-06-01"
	claudeOAuthBeta   = "oauth-2025-04-20"
	claudeProbeModel  = "claude-haiku-5-5"
	// claudeProbeSystem is the system prompt a subscription token is
	// accepted with.
	claudeProbeSystem = "You are Claude Code, Anthropic's official CLI for Claude."
	claudeProbeText   = "ok"
	claudeProbeTokens = 1
)

// The unified rate-limit headers: an overall status, then per window a
// utilization (a fraction of 1) and a reset time (Unix seconds).
const (
	claudeLimitPrefix   = "Anthropic-Ratelimit-Unified-"
	claudeStatusHeader  = claudeLimitPrefix + "Status"
	claudeUtilization   = "-Utilization"
	claudeReset         = "-Reset"
	claudeStatusWarning = "allowed_warning"
	claudeStatusLimited = "rejected"
	percent             = 100
)

// claudeWindows are the windows the headers name, shortest first.
var claudeWindows = []struct {
	key  string
	span time.Duration
}{
	{"5h", 5 * time.Hour},
	{"7d", 7 * Day},
}

type claudeProbe struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system"`
	Messages  []claudeMessage `json:"messages"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// claude probes one Claude subscription token.
func (u *Usage) claude(ctx context.Context, token string, now time.Time) (usageReport, error) {
	body, err := json.Marshal(claudeProbe{
		Model: claudeProbeModel, MaxTokens: claudeProbeTokens, System: claudeProbeSystem,
		Messages: []claudeMessage{{Role: "user", Content: claudeProbeText}},
	})
	if err != nil {
		return usageReport{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, HTTPTimeout)
	defer cancel()
	endpoint := u.ClaudeURL
	if endpoint == "" {
		endpoint = claudeMessagesURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return usageReport{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Anthropic-Version", claudeAPIVersion)
	req.Header.Set("Anthropic-Beta", claudeOAuthBeta)
	req.Header.Set("Content-Type", "application/json")
	resp, err := u.Client.Do(req)
	if err != nil {
		return usageReport{}, fmt.Errorf("claude: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusTooManyRequests {
		return usageReport{}, fmt.Errorf("claude: HTTP %s", resp.Status)
	}
	report, err := parseClaudeLimits(resp.Header)
	if err != nil {
		return usageReport{}, fmt.Errorf("claude: %w", err)
	}
	return report, nil
}

// parseClaudeLimits reads the unified rate-limit headers. A window whose
// utilization is missing is left out; one that does not parse is an error.
func parseClaudeLimits(h http.Header) (usageReport, error) {
	var windows []usageWindow
	for _, w := range claudeWindows {
		raw := h.Get(claudeLimitPrefix + w.key + claudeUtilization)
		if raw == "" {
			continue
		}
		used, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return usageReport{}, fmt.Errorf("%s utilization %q: %w", w.key, raw, err)
		}
		window := usageWindow{Span: w.span, Used: used * percent}
		if reset := h.Get(claudeLimitPrefix + w.key + claudeReset); reset != "" {
			at, err := strconv.ParseInt(reset, 10, 64)
			if err != nil {
				return usageReport{}, fmt.Errorf("%s reset %q: %w", w.key, reset, err)
			}
			window.ResetsAt = time.Unix(at, 0).UTC()
		}
		windows = append(windows, window)
	}
	status := h.Get(claudeStatusHeader)
	if status == "" && len(windows) == 0 {
		return usageReport{}, errNoLimits
	}
	state := stateOf(status == claudeStatusLimited, status == claudeStatusWarning, windows)
	return usageReport{State: state, Windows: windows}, nil
}
