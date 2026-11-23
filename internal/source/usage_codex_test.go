package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	testCodexAccess  = "codex-access-token"
	testCodexAccount = "acct-123"
	testCodexAuth    = `{"OPENAI_API_KEY":null,"tokens":{"id_token":"i","access_token":"` + testCodexAccess +
		`","refresh_token":"r","account_id":"` + testCodexAccount + `"},"last_refresh":"2026-10-05T00:00:00Z"}`
	codexShortSeconds = 18000
	codexWeekSeconds  = 604800
)

func TestCodexReport(t *testing.T) {
	t.Parallel()
	resetAt := testNow.Add(3 * Day)
	tests := []struct {
		name    string
		answer  codexUsage
		want    usageReport
		wantErr string
	}{
		{"both windows", codexAnswer(false,
			&codexWindow{UsedPercent: 12, LimitWindowSeconds: codexShortSeconds, ResetAfterSeconds: 3600},
			&codexWindow{UsedPercent: 40, LimitWindowSeconds: codexWeekSeconds, ResetAt: resetAt.Unix()}),
			usageReport{State: StateOK, Plan: "plus", Windows: []usageWindow{
				{Span: 5 * time.Hour, Used: 12, ResetsAt: testNow.Add(time.Hour)},
				{Span: 7 * Day, Used: 40, ResetsAt: resetAt},
			}}, ""},
		{"reset_at wins over reset_after", codexAnswer(false,
			&codexWindow{UsedPercent: 1, ResetAt: resetAt.Unix(), ResetAfterSeconds: 1}, nil),
			usageReport{State: StateOK, Plan: "plus", Windows: []usageWindow{{Used: 1, ResetsAt: resetAt}}}, ""},
		{"no reset known", codexAnswer(false, nil, &codexWindow{UsedPercent: 2, LimitWindowSeconds: codexWeekSeconds}),
			usageReport{State: StateOK, Plan: "plus", Windows: []usageWindow{{Span: 7 * Day, Used: 2}}}, ""},
		{"past the threshold warns", codexAnswer(false, &codexWindow{UsedPercent: 85}, nil),
			usageReport{State: StateWarning, Plan: "plus", Windows: []usageWindow{{Used: 85}}}, ""},
		{"limit reached", codexAnswer(true, &codexWindow{UsedPercent: 100}, nil),
			usageReport{State: StateLimited, Plan: "plus", Windows: []usageWindow{{Used: 100}}}, ""},
		{"no rate limit", codexUsage{PlanType: "plus"}, usageReport{}, "the answer has no rate limits"},
		{"no windows", codexAnswer(false, nil, nil), usageReport{}, "the answer has no rate limits"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := codexReport(tt.answer, testNow)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func codexAnswer(reached bool, primary, secondary *codexWindow) codexUsage {
	return codexUsage{PlanType: "plus", RateLimit: &codexRateLimit{
		LimitReached: reached, PrimaryWindow: primary, SecondaryWindow: secondary,
	}}
}

// codexServer answers every request with status and body, recording it.
func codexServer(t *testing.T, status int, body string) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r, nil)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

const codexAnswerJSON = `{"plan_type":"pro","rate_limit":{"allowed":true,"limit_reached":false,` +
	`"primary_window":{"used_percent":7,"limit_window_seconds":18000,"reset_after_seconds":600,"reset_at":0},` +
	`"secondary_window":{"used_percent":31,"limit_window_seconds":604800,"reset_after_seconds":0,"reset_at":0}},"credits":null}`

func TestCodexFetch(t *testing.T) {
	t.Parallel()
	srv, rec := codexServer(t, http.StatusOK, codexAnswerJSON)
	u := &Usage{Client: srv.Client(), CodexURL: srv.URL}
	got, err := u.codex(context.Background(), testCodexAuth, testNow)
	if err != nil {
		t.Fatal(err)
	}
	want := usageReport{State: StateOK, Plan: "pro", Windows: []usageWindow{
		{Span: 5 * time.Hour, Used: 7, ResetsAt: testNow.Add(10 * time.Minute)},
		{Span: 7 * Day, Used: 31},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	req := rec.requests()[0]
	for k, v := range map[string]string{
		"Authorization":      "Bearer " + testCodexAccess,
		"Chatgpt-Account-Id": testCodexAccount,
		"User-Agent":         "vigie",
	} {
		if got := req.Header.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	if req.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", req.Method)
	}
}

func TestCodexFetchWithoutAccountID(t *testing.T) {
	t.Parallel()
	srv, rec := codexServer(t, http.StatusOK, codexAnswerJSON)
	u := &Usage{Client: srv.Client(), CodexURL: srv.URL}
	if _, err := u.codex(context.Background(), `{"tokens":{"access_token":"a"}}`, testNow); err != nil {
		t.Fatal(err)
	}
	if got, found := rec.requests()[0].Header["Chatgpt-Account-Id"]; found {
		t.Errorf("account header sent as %q, want none", got)
	}
}

func TestCodexFetchErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		auth    string
		status  int
		body    string
		wantErr string
	}{
		{"auth not JSON", "nope", http.StatusOK, codexAnswerJSON, "codex: decoding the auth.json: invalid character 'o' in literal null (expecting 'u')"},
		{"API key login", `{"OPENAI_API_KEY":"sk-x","tokens":null}`, http.StatusOK, codexAnswerJSON, "codex: the auth.json has no ChatGPT access token"},
		{"expired token", testCodexAuth, http.StatusUnauthorized, `{"detail":"expired"}`, "codex: HTTP 401 Unauthorized"},
		{"answer not JSON", testCodexAuth, http.StatusOK, "<html>", "codex: decoding the answer: invalid character '<' looking for beginning of value"},
		{"answer without limits", testCodexAuth, http.StatusOK, `{"plan_type":"plus"}`, "codex: the answer has no rate limits"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := codexServer(t, tt.status, tt.body)
			u := &Usage{Client: srv.Client(), CodexURL: srv.URL}
			_, err := u.codex(context.Background(), tt.auth, testNow)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), testCodexAccess) {
				t.Errorf("error %q carries the token", err)
			}
		})
	}
}
