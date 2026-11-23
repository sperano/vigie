package source

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Usage row states: how close an account is to its subscription limits.
const (
	StateOK      = "ok"
	StateWarning = "warning"
	StateLimited = "limited"
)

const (
	// claudeTokenKey is the Secret key of a Claude subscription token
	// (`claude setup-token`), as the Kelos chart stores it.
	claudeTokenKey = "CLAUDE_CODE_OAUTH_TOKEN"
	// codexAuthKey is the Secret key of the Codex login, ~/.codex/auth.json.
	codexAuthKey = "CODEX_AUTH_JSON"
	// usageWarnPercent is the share of a window used from which an account
	// shows as warning, when its provider does not say so itself.
	usageWarnPercent = 80
	// activeMark starts the DETAIL of the Claude account Kelos uses now.
	activeMark = "[active] "
	usageSep   = " · "
)

// Where enter opens a usage row: each provider's usage page.
const (
	claudeUsagePage = "https://claude.ai/settings/usage"
	codexUsagePage  = "https://chatgpt.com/codex/settings/usage"
)

// usageWindow is one rate-limit window of an account.
type usageWindow struct {
	Span     time.Duration // the window's length, zero when unknown
	Used     float64       // percent of the window used
	ResetsAt time.Time     // zero when unknown
}

// usageReport is what a provider says of one account.
type usageReport struct {
	State   string // StateOK, StateWarning or StateLimited
	Plan    string // the subscription plan, empty when unknown
	Windows []usageWindow
	Read    time.Time // when the provider was asked
}

// usageAccount is one account to report on.
type usageAccount struct {
	name   string // the Secret it comes from: the row's identity
	codex  bool
	secret string // the Secret value: a Claude token or a Codex auth.json
	active bool   // the Claude account Kelos runs on now
}

// Usage reports the subscription limits (the 5 h and weekly windows) of the
// accounts the Kelos agents run on: the Claude subscription tokens and the
// Codex (ChatGPT) login, read from their Secrets in Namespace. A missing
// Secret has no row (the secondary Claude account is optional). A reading
// is reused for Every, so that a refresh does not probe each account.
type Usage struct {
	Namespace     string
	ClaudeSecrets []string
	// ClaudeActive is the Secret holding the copy of the Claude token Kelos
	// uses now; the account whose token matches it is marked active.
	ClaudeActive string
	CodexSecret  string
	Every        time.Duration
	Kube         KubeClient
	Client       *http.Client
	Now          func() time.Time
	// ClaudeURL and CodexURL replace the providers' endpoints in tests.
	ClaudeURL, CodexURL string

	mu    sync.Mutex
	cache map[string]usageReport // by account name and secret hash
}

// Fetch lists one row per account. A Secret that cannot be read fails the
// source; a provider that cannot be asked fails only that account's row.
func (u *Usage) Fetch(ctx context.Context) ([]Row, error) {
	accounts, err := u.accounts(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, len(accounts))
	var wg sync.WaitGroup
	for i, a := range accounts {
		wg.Go(func() {
			report, err := u.report(ctx, a)
			rows[i] = usageRow(a, report, err, u.Now())
		})
	}
	wg.Wait()
	return rows, nil
}

// accounts reads the configured Secrets, skipping the missing ones.
func (u *Usage) accounts(ctx context.Context) ([]usageAccount, error) {
	active, err := u.optionalSecret(ctx, u.ClaudeActive, claudeTokenKey)
	if err != nil {
		return nil, err
	}
	var accounts []usageAccount
	for _, name := range u.ClaudeSecrets {
		token, err := u.optionalSecret(ctx, name, claudeTokenKey)
		if err != nil {
			return nil, err
		}
		if token != "" {
			accounts = append(accounts, usageAccount{name: name, secret: token, active: token == active})
		}
	}
	auth, err := u.optionalSecret(ctx, u.CodexSecret, codexAuthKey)
	if err != nil {
		return nil, err
	}
	if auth != "" {
		accounts = append(accounts, usageAccount{name: u.CodexSecret, codex: true, secret: auth})
	}
	return accounts, nil
}

// optionalSecret reads key of Secret name, "" when name is empty or the
// Secret does not exist.
func (u *Usage) optionalSecret(ctx context.Context, name, key string) (string, error) {
	if name == "" {
		return "", nil
	}
	value, err := secretValue(ctx, u.Kube, TokenSecret{Namespace: u.Namespace, Name: name, Key: key})
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	return value, err
}

// report asks the account's provider, or reuses a reading younger than
// Every of the same Secret value.
func (u *Usage) report(ctx context.Context, a usageAccount) (usageReport, error) {
	key := fmt.Sprintf("%s\x00%x", a.name, sha256.Sum256([]byte(a.secret)))
	now := u.Now()
	u.mu.Lock()
	cached, found := u.cache[key]
	u.mu.Unlock()
	if found && now.Sub(cached.Read) < u.Every {
		return cached, nil
	}
	var report usageReport
	var err error
	if a.codex {
		report, err = u.codex(ctx, a.secret, now)
	} else {
		report, err = u.claude(ctx, a.secret, now)
	}
	if err != nil {
		return usageReport{}, err
	}
	report.Read = now
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cache == nil {
		u.cache = map[string]usageReport{}
	}
	u.cache[key] = report
	return report, nil
}

// errNoLimits is a provider answer that carries no rate-limit window.
var errNoLimits = errors.New("the answer has no rate limits")

// usageRow is the row of one account. A limited account needs you, unless
// it is a Claude account Kelos does not use now.
func usageRow(a usageAccount, r usageReport, err error, now time.Time) Row {
	row := Row{Source: SourceUsage, Origin: OriginKelos, Name: a.name, Target: Target{URL: claudeUsagePage}}
	if a.codex {
		row.Target.URL = codexUsagePage
	}
	mark := ""
	if a.active {
		mark = activeMark
	}
	if err != nil {
		row.State, row.Detail, row.NeedsYou = StateError, mark+err.Error(), true
		return row
	}
	row.State, row.Since = r.State, r.Read
	row.Detail = mark + usageDetail(r, now)
	row.NeedsYou = r.State == StateLimited && (a.codex || a.active)
	return row
}

// usageDetail renders the plan and each window: "[plus] 5h 12% resets 2h".
func usageDetail(r usageReport, now time.Time) string {
	parts := make([]string, 0, len(r.Windows))
	for _, w := range r.Windows {
		part := fmt.Sprintf("%.0f%%", w.Used)
		if w.Span > 0 {
			part = largestUnit(w.Span) + " " + part
		}
		if !w.ResetsAt.IsZero() {
			part += " resets " + largestUnit(w.ResetsAt.Sub(now))
		}
		parts = append(parts, part)
	}
	detail := strings.Join(parts, usageSep)
	if r.Plan != "" {
		detail = "[" + r.Plan + "] " + detail
	}
	return detail
}

// stateOf is StateLimited when limited, else StateWarning when warned or
// when a window is used past usageWarnPercent, else StateOK.
func stateOf(limited, warned bool, windows []usageWindow) string {
	if limited {
		return StateLimited
	}
	if warned {
		return StateWarning
	}
	for _, w := range windows {
		if w.Used >= usageWarnPercent {
			return StateWarning
		}
	}
	return StateOK
}

var spanUnits = []struct {
	suffix string
	size   time.Duration
}{
	{"d", Day},
	{"h", time.Hour},
	{"m", time.Minute},
	{"s", time.Second},
}

// largestUnit renders d in its largest whole unit, a negative d as zero.
func largestUnit(d time.Duration) string {
	d = max(d, 0)
	for _, u := range spanUnits {
		if d >= u.size {
			return fmt.Sprintf("%d%s", d/u.size, u.suffix)
		}
	}
	return "0" + spanUnits[len(spanUnits)-1].suffix
}
