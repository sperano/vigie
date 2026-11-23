package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// codexUsageURL is the ChatGPT backend Codex reads its rate limits from. It
// takes the login's access token, which the Kelos controller's refresher
// keeps fresh in the Secret; vigie never refreshes it, since that would
// rotate the refresh token from under the agents.
const codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

const (
	codexAccountHeader = "ChatGPT-Account-Id"
	codexUserAgent     = "vigie"
)

// errNoCodexLogin is an auth.json without a ChatGPT login (an API key).
var errNoCodexLogin = errors.New("codex: the auth.json has no ChatGPT access token")

// codexAuth is the part of ~/.codex/auth.json vigie reads.
type codexAuth struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

// codexUsage is the part of the usage answer vigie reads.
type codexUsage struct {
	PlanType  string          `json:"plan_type"`
	RateLimit *codexRateLimit `json:"rate_limit"`
}

// codexRateLimit is the short (primary) and weekly (secondary) window.
type codexRateLimit struct {
	LimitReached    bool         `json:"limit_reached"`
	PrimaryWindow   *codexWindow `json:"primary_window"`
	SecondaryWindow *codexWindow `json:"secondary_window"`
}

type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

// codex asks for the rate limits of the login in authJSON.
func (u *Usage) codex(ctx context.Context, authJSON string, now time.Time) (usageReport, error) {
	var auth codexAuth
	if err := json.Unmarshal([]byte(authJSON), &auth); err != nil {
		return usageReport{}, fmt.Errorf("codex: decoding the auth.json: %w", err)
	}
	if auth.Tokens.AccessToken == "" {
		return usageReport{}, errNoCodexLogin
	}
	ctx, cancel := context.WithTimeout(ctx, HTTPTimeout)
	defer cancel()
	endpoint := u.CodexURL
	if endpoint == "" {
		endpoint = codexUsageURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return usageReport{}, err
	}
	req.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
	if auth.Tokens.AccountID != "" {
		req.Header.Set(codexAccountHeader, auth.Tokens.AccountID)
	}
	req.Header.Set("User-Agent", codexUserAgent)
	resp, err := u.Client.Do(req)
	if err != nil {
		return usageReport{}, fmt.Errorf("codex: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return usageReport{}, fmt.Errorf("codex: HTTP %s", resp.Status)
	}
	var answer codexUsage
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return usageReport{}, fmt.Errorf("codex: decoding the answer: %w", err)
	}
	report, err := codexReport(answer, now)
	if err != nil {
		return usageReport{}, fmt.Errorf("codex: %w", err)
	}
	return report, nil
}

// codexReport reads the primary (short) then secondary (weekly) windows.
func codexReport(answer codexUsage, now time.Time) (usageReport, error) {
	limits := answer.RateLimit
	if limits == nil || (limits.PrimaryWindow == nil && limits.SecondaryWindow == nil) {
		return usageReport{}, errNoLimits
	}
	var windows []usageWindow
	for _, w := range []*codexWindow{limits.PrimaryWindow, limits.SecondaryWindow} {
		if w != nil {
			windows = append(windows, w.window(now))
		}
	}
	return usageReport{
		State:   stateOf(limits.LimitReached, false, windows),
		Plan:    answer.PlanType,
		Windows: windows,
	}, nil
}

// window is w in vigie's terms: its reset is reset_at, else now plus
// reset_after_seconds, else unknown.
func (w codexWindow) window(now time.Time) usageWindow {
	out := usageWindow{Span: time.Duration(w.LimitWindowSeconds) * time.Second, Used: w.UsedPercent}
	switch {
	case w.ResetAt > 0:
		out.ResetsAt = time.Unix(w.ResetAt, 0).UTC()
	case w.ResetAfterSeconds > 0:
		out.ResetsAt = now.Add(time.Duration(w.ResetAfterSeconds) * time.Second)
	}
	return out
}
