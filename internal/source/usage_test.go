package source

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

const (
	usageNamespace  = "kelos-agents"
	primarySecret   = "claude-oauth-primary"
	secondarySecret = "claude-oauth-secondary"
	activeSecret    = "claude-oauth"
	codexSecret     = "codex-oauth"
	secondaryToken  = "sk-ant-oat01-second"
	usageEvery      = 10 * time.Minute
)

func usageSecret(name, key, value string) kubeObject {
	return kubeObj(secretsResource, usageNamespace, obj{"metadata": obj{"name": name}, "data": obj{key: b64(value)}})
}

// usageFixture is a Usage reading fake Secrets and fake providers.
type usageFixture struct {
	usage       *Usage
	claudeCalls *recorder
	codexCalls  *recorder
	now         *time.Time
}

func newUsageFixture(t *testing.T, claudeStatus int, claude http.Header, secrets ...kubeObject) usageFixture {
	t.Helper()
	claudeSrv, claudeCalls := claudeServer(t, claudeStatus, claude)
	codexSrv, codexCalls := codexServer(t, http.StatusOK, codexAnswerJSON)
	now := testNow
	u := &Usage{
		Namespace: usageNamespace, ClaudeSecrets: []string{primarySecret, secondarySecret},
		ClaudeActive: activeSecret, CodexSecret: codexSecret, Every: usageEvery,
		Kube: fakeKube(t, secrets...).client(t), Client: claudeSrv.Client(),
		Now: func() time.Time { return now }, ClaudeURL: claudeSrv.URL, CodexURL: codexSrv.URL,
	}
	return usageFixture{usage: u, claudeCalls: claudeCalls, codexCalls: codexCalls, now: &now}
}

func allSecrets() []kubeObject {
	return []kubeObject{
		usageSecret(primarySecret, claudeTokenKey, testClaudeToken),
		usageSecret(secondarySecret, claudeTokenKey, secondaryToken),
		usageSecret(activeSecret, claudeTokenKey, secondaryToken+"\n"),
		usageSecret(codexSecret, codexAuthKey, testCodexAuth),
	}
}

func TestUsageFetch(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t, http.StatusOK, claudeHeaders("allowed", "0.25", "0.5"), allSecrets()...)
	rows, err := f.usage.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	claudeDetail := "5h 25% · 7d 50%"
	want := []Row{
		{Source: SourceUsage, Origin: OriginKelos, Name: primarySecret, State: StateOK, Since: testNow,
			Detail: claudeDetail, Target: Target{URL: claudeUsagePage}},
		{Source: SourceUsage, Origin: OriginKelos, Name: secondarySecret, State: StateOK, Since: testNow,
			Detail: "[active] " + claudeDetail, Target: Target{URL: claudeUsagePage}},
		{Source: SourceUsage, Origin: OriginKelos, Name: codexSecret, State: StateOK, Since: testNow,
			Detail: "[pro] 5h 7% resets 10m · 7d 31%", Target: Target{URL: codexUsagePage}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows:\n got %+v\nwant %+v", rows, want)
	}
	tokens := map[string]bool{}
	for _, r := range f.claudeCalls.requests() {
		tokens[r.Header.Get("Authorization")] = true
	}
	if want := map[string]bool{"Bearer " + testClaudeToken: true, "Bearer " + secondaryToken: true}; !reflect.DeepEqual(tokens, want) {
		t.Errorf("claude probed with %v, want %v", tokens, want)
	}
}

func TestUsageReusesReadings(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t, http.StatusOK, claudeHeaders("allowed", "0.25", ""), allSecrets()...)
	fetch := func() []Row {
		t.Helper()
		rows, err := f.usage.Fetch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	fetch()
	*f.now = testNow.Add(usageEvery - time.Second)
	rows := fetch()
	if got := len(f.claudeCalls.requests()) + len(f.codexCalls.requests()); got != len(rows) {
		t.Errorf("providers asked %d times within the interval, want %d", got, len(rows))
	}
	if !rows[0].Since.Equal(testNow) {
		t.Errorf("reused reading dated %v, want %v", rows[0].Since, testNow)
	}
	*f.now = testNow.Add(usageEvery)
	rows = fetch()
	if got := len(f.claudeCalls.requests()) + len(f.codexCalls.requests()); got != 2*len(rows) {
		t.Errorf("providers asked %d times after the interval, want %d", got, 2*len(rows))
	}
	if !rows[0].Since.Equal(*f.now) {
		t.Errorf("new reading dated %v, want %v", rows[0].Since, *f.now)
	}
}

func TestUsageNewTokenIsReadAgain(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t, http.StatusOK, claudeHeaders("allowed", "0.25", ""),
		usageSecret(primarySecret, claudeTokenKey, testClaudeToken))
	f.usage.CodexSecret = ""
	if _, err := f.usage.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.usage.Kube = fakeKube(t, usageSecret(primarySecret, claudeTokenKey, secondaryToken)).client(t)
	if _, err := f.usage.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.claudeCalls.requests()); got != 2 {
		t.Errorf("claude probed %d times, want 2 (the token changed)", got)
	}
	want := cacheKey(usageAccount{name: primarySecret, secret: secondaryToken})
	if keys := slices.Collect(maps.Keys(f.usage.cache)); !reflect.DeepEqual(keys, []string{want}) {
		t.Errorf("cache holds %d readings, want only the new token's", len(keys))
	}
}

func TestUsageLimitedNeedsYou(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		secrets []kubeObject
		active  string
		want    map[string]bool
	}{
		{"only the active claude account", allSecrets(), activeSecret,
			map[string]bool{primarySecret: false, secondarySecret: true}},
		{"every claude account without an active secret", allSecrets(), "",
			map[string]bool{primarySecret: true, secondarySecret: true}},
		{"every claude account when the active copy is missing", allSecrets()[:2], activeSecret,
			map[string]bool{primarySecret: true, secondarySecret: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newUsageFixture(t, http.StatusTooManyRequests, claudeHeaders("rejected", "1", ""), tt.secrets...)
			f.usage.CodexSecret, f.usage.ClaudeActive = "", tt.active
			rows, err := f.usage.Fetch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, r := range rows {
				got[r.Name] = r.NeedsYou
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("needs you %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUsageFailedReadingIsRetried(t *testing.T) {
	t.Parallel()
	f := newUsageFixture(t, http.StatusUnauthorized, http.Header{}, usageSecret(primarySecret, claudeTokenKey, testClaudeToken))
	f.usage.CodexSecret = ""
	for range 2 {
		rows, err := f.usage.Fetch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if rows[0].State != StateError {
			t.Fatalf("state = %q, want %q", rows[0].State, StateError)
		}
	}
	if got := len(f.claudeCalls.requests()); got != 2 {
		t.Errorf("claude probed %d times, want 2 (an error is not kept)", got)
	}
}

func TestUsageAccounts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		secrets []kubeObject
		codex   string
		want    []string
	}{
		{"missing secondary and active have no row", []kubeObject{usageSecret(primarySecret, claudeTokenKey, testClaudeToken)}, "", []string{primarySecret}},
		{"empty token has no row", []kubeObject{usageSecret(primarySecret, claudeTokenKey, " \n"), usageSecret(codexSecret, codexAuthKey, testCodexAuth)}, codexSecret, []string{codexSecret}},
		{"codex off", allSecrets(), "", []string{primarySecret, secondarySecret}},
		{"nothing there", nil, codexSecret, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newUsageFixture(t, http.StatusOK, claudeHeaders("allowed", "0.1", ""), tt.secrets...)
			f.usage.CodexSecret = tt.codex
			rows, err := f.usage.Fetch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, r := range rows {
				names = append(names, r.Name)
			}
			if !reflect.DeepEqual(names, tt.want) {
				t.Errorf("rows %v, want %v", names, tt.want)
			}
		})
	}
}

func TestUsageSecretErrors(t *testing.T) {
	t.Parallel()
	t.Run("unreadable secrets fail the source", func(t *testing.T) {
		t.Parallel()
		api := fakeKube(t, allSecrets()...)
		api.failList("secrets")
		u := &Usage{Namespace: usageNamespace, ClaudeSecrets: []string{primarySecret}, Kube: api.client(t), Now: fixedNow}
		_, err := u.Fetch(context.Background())
		if err == nil || err.Error() != "reading secret kelos-agents/claude-oauth-primary: boom" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a secret without the key fails the source", func(t *testing.T) {
		t.Parallel()
		api := fakeKube(t, usageSecret(codexSecret, "other", "x"))
		u := &Usage{Namespace: usageNamespace, CodexSecret: codexSecret, Kube: api.client(t), Now: fixedNow}
		_, err := u.Fetch(context.Background())
		if err == nil || err.Error() != `secret kelos-agents/codex-oauth has no key "CODEX_AUTH_JSON"` {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no cluster fails the source", func(t *testing.T) {
		t.Parallel()
		noCluster := errors.New("kubernetes config: none")
		u := &Usage{Namespace: usageNamespace, CodexSecret: codexSecret, Now: fixedNow,
			Kube: func() (rest.Interface, error) { return nil, noCluster }}
		if _, err := u.Fetch(context.Background()); !errors.Is(err, noCluster) {
			t.Fatalf("err = %v, want %v", err, noCluster)
		}
	})
}

func TestUsageRow(t *testing.T) {
	t.Parallel()
	limited := usageReport{State: StateLimited, Read: testNow, Windows: []usageWindow{{Span: 5 * time.Hour, Used: 100, ResetsAt: testNow.Add(90 * time.Minute)}}}
	ok := usageReport{State: StateOK, Read: testNow, Windows: []usageWindow{{Span: 7 * Day, Used: 3}}}
	spare := usageAccount{name: primarySecret, spare: true}
	unknown := usageAccount{name: primarySecret}
	active := usageAccount{name: primarySecret, active: true}
	codex := usageAccount{name: codexSecret, codex: true}
	tests := []struct {
		name       string
		account    usageAccount
		report     usageReport
		err        error
		wantState  string
		wantDetail string
		wantNeeds  bool
	}{
		{"limited active claude needs you", active, limited, nil, StateLimited, "[active] 5h 100% resets 1h", true},
		{"limited spare claude does not", spare, limited, nil, StateLimited, "5h 100% resets 1h", false},
		{"limited claude with no known active one needs you", unknown, limited, nil, StateLimited, "5h 100% resets 1h", true},
		{"limited codex needs you", codex, limited, nil, StateLimited, "5h 100% resets 1h", true},
		{"ok active claude", active, ok, nil, StateOK, "[active] 7d 3%", false},
		{"error on spare claude needs you", spare, usageReport{}, errors.New("claude: HTTP 401 Unauthorized"), StateError, "claude: HTTP 401 Unauthorized", true},
		{"error needs you", active, usageReport{}, errors.New("claude: HTTP 401 Unauthorized"), StateError, "[active] claude: HTTP 401 Unauthorized", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := usageRow(tt.account, tt.report, tt.err, testNow)
			if got.State != tt.wantState || got.Detail != tt.wantDetail || got.NeedsYou != tt.wantNeeds {
				t.Errorf("got state %q detail %q needs you %v, want %q %q %v",
					got.State, got.Detail, got.NeedsYou, tt.wantState, tt.wantDetail, tt.wantNeeds)
			}
			if got.IsSourceError() {
				t.Error("an account row must not be the source's error row")
			}
		})
	}
}

func TestUsageDetail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		report usageReport
		want   string
	}{
		{"plan and windows", usageReport{Plan: "plus", Windows: []usageWindow{
			{Span: 5 * time.Hour, Used: 12.4, ResetsAt: testNow.Add(2*time.Hour + time.Minute)},
			{Span: 7 * Day, Used: 33.6, ResetsAt: testNow.Add(3*Day + time.Hour)},
		}}, "[plus] 5h 12% resets 2h · 7d 34% resets 3d"},
		{"unknown span", usageReport{Windows: []usageWindow{{Used: 5}}}, "5%"},
		{"reset in the past", usageReport{Windows: []usageWindow{{Span: time.Hour, Used: 1, ResetsAt: testNow.Add(-time.Minute)}}}, "1h 1% resets 0s"},
		{"no windows", usageReport{Plan: "pro"}, "[pro] "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := usageDetail(tt.report, testNow); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLargestUnit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		d    time.Duration
		want string
	}{
		{18000 * time.Second, "5h"},
		{604800 * time.Second, "7d"},
		{90 * time.Second, "1m"},
		{59 * time.Second, "59s"},
		{time.Millisecond, "0s"},
		{-time.Hour, "0s"},
	}
	for _, tt := range tests {
		if got := largestUnit(tt.d); got != tt.want {
			t.Errorf("largestUnit(%s) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
