package source

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testClaudeToken = "sk-ant-oat01-test"

// claudeHeaders are the unified rate-limit headers of an answer.
func claudeHeaders(status, used5h, used7d string) http.Header {
	h := http.Header{}
	set := func(k, v string) {
		if v != "" {
			h.Set(k, v)
		}
	}
	set("anthropic-ratelimit-unified-status", status)
	set("anthropic-ratelimit-unified-5h-utilization", used5h)
	set("anthropic-ratelimit-unified-7d-utilization", used7d)
	return h
}

func TestParseClaudeLimits(t *testing.T) {
	t.Parallel()
	reset5h := testNow.Add(2 * time.Hour)
	reset7d := testNow.Add(3 * Day)
	withResets := claudeHeaders("allowed", "0.12", "0.34")
	withResets.Set("anthropic-ratelimit-unified-5h-reset", unixString(reset5h))
	withResets.Set("anthropic-ratelimit-unified-7d-reset", unixString(reset7d))
	bad7dReset := claudeHeaders("allowed", "", "0.1")
	bad7dReset.Set("anthropic-ratelimit-unified-7d-reset", "later")
	badReset := claudeHeaders("allowed", "0.1", "")
	badReset.Set("anthropic-ratelimit-unified-5h-reset", "soon")
	tests := []struct {
		name    string
		h       http.Header
		want    usageReport
		wantErr string
	}{
		{"both windows with their resets", withResets, usageReport{State: StateOK, Windows: []usageWindow{
			{Span: 5 * time.Hour, Used: 12, ResetsAt: reset5h},
			{Span: 7 * Day, Used: 34, ResetsAt: reset7d},
		}}, ""},
		{"warning from the status", claudeHeaders("allowed_warning", "0.5", ""), usageReport{State: StateWarning, Windows: []usageWindow{
			{Span: 5 * time.Hour, Used: 50},
		}}, ""},
		{"warning from a window past the threshold", claudeHeaders("allowed", "", "0.8"), usageReport{State: StateWarning, Windows: []usageWindow{
			{Span: 7 * Day, Used: 80},
		}}, ""},
		{"rejected is limited", claudeHeaders("rejected", "1", "0.9"), usageReport{State: StateLimited, Windows: []usageWindow{
			{Span: 5 * time.Hour, Used: 100}, {Span: 7 * Day, Used: 90},
		}}, ""},
		{"status alone", claudeHeaders("allowed", "", ""), usageReport{State: StateOK}, ""},
		{"no headers", http.Header{}, usageReport{}, "the answer has no rate limits"},
		{"bad utilization", claudeHeaders("allowed", "lots", ""), usageReport{}, `5h utilization "lots"`},
		{"bad reset", badReset, usageReport{}, `5h reset "soon"`},
		{"bad 7d reset", bad7dReset, usageReport{}, `7d reset "later"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseClaudeLimits(tt.h)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
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

func unixString(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}

// recorder keeps the requests a fake provider received.
type recorder struct {
	mu     sync.Mutex
	reqs   []*http.Request
	bodies [][]byte
}

func (r *recorder) add(req *http.Request, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs, r.bodies = append(r.reqs, req.Clone(context.Background())), append(r.bodies, body)
}

func (r *recorder) requests() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.reqs)
}

func (r *recorder) rawBodies() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.bodies)
}

// claudeServer answers the probe with status and h, recording the request
// and its body.
func claudeServer(t *testing.T, status int, h http.Header) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the probe: %v", err)
		}
		rec.add(r, body)
		for k, v := range h {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"type":"message"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestClaudeProbe(t *testing.T) {
	t.Parallel()
	srv, rec := claudeServer(t, http.StatusOK, claudeHeaders("allowed", "0.25", "0.5"))
	u := &Usage{Client: srv.Client(), ClaudeURL: srv.URL}
	got, err := u.claude(context.Background(), testClaudeToken, testNow)
	if err != nil {
		t.Fatal(err)
	}
	want := usageReport{State: StateOK, Windows: []usageWindow{{Span: 5 * time.Hour, Used: 25}, {Span: 7 * Day, Used: 50}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	req := rec.requests()[0]
	wantHeaders := map[string]string{
		"Authorization":     "Bearer " + testClaudeToken,
		"Anthropic-Version": "2023-06-01",
		"Anthropic-Beta":    "oauth-2025-04-20",
		"Content-Type":      "application/json",
	}
	for k, v := range wantHeaders {
		if got := req.Header.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	if req.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.Method)
	}
	// The body as the API reads it: its JSON keys, not the Go struct.
	var gotBody map[string]any
	if err := json.Unmarshal(rec.rawBodies()[0], &gotBody); err != nil {
		t.Fatalf("decoding the probe: %v", err)
	}
	wantBody := map[string]any{
		"model": "claude-haiku-5-5", "max_tokens": float64(1), "system": claudeProbeSystem,
		"messages": []any{map[string]any{"role": "user", "content": "ok"}},
	}
	if !reflect.DeepEqual(gotBody, wantBody) {
		t.Errorf("body = %v, want %v", gotBody, wantBody)
	}
}

func TestClaudeProbeAnswers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		status    int
		h         http.Header
		wantState string
		wantErr   string
	}{
		{"rejected answer still reports", http.StatusTooManyRequests, claudeHeaders("rejected", "1", ""), StateLimited, ""},
		{"unauthorized", http.StatusUnauthorized, http.Header{}, "", "claude: HTTP 401 Unauthorized"},
		{"server error with headers", http.StatusInternalServerError, claudeHeaders("allowed", "0.1", ""), "", "claude: HTTP 500 Internal Server Error"},
		{"answer without limits", http.StatusOK, http.Header{}, "", "claude: the answer has no rate limits"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := claudeServer(t, tt.status, tt.h)
			u := &Usage{Client: srv.Client(), ClaudeURL: srv.URL}
			got, err := u.claude(context.Background(), testClaudeToken, testNow)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), testClaudeToken) {
					t.Errorf("error %q carries the token", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tt.wantState {
				t.Errorf("state = %q, want %q", got.State, tt.wantState)
			}
		})
	}
}

func TestClaudeProbeTransportError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	u := &Usage{Client: srv.Client(), ClaudeURL: srv.URL}
	_, err := u.claude(context.Background(), testClaudeToken, testNow)
	var urlErr interface{ Timeout() bool }
	if err == nil || !strings.HasPrefix(err.Error(), "claude: ") || !errors.As(err, &urlErr) {
		t.Fatalf("err = %v, want a wrapped transport error", err)
	}
}
