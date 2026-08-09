// Package quota tracks usage for Mirasim accounts.
//
// The relay reports usage through Anthropic's unified rate-limit headers. There is no
// usage endpoint to query, but an invalid request still carries the headers, so the
// sidecar's trick carries over: send `max_tokens: 0`, get a 400 with
// `x-litellm-response-cost: 0`, and read the headers off it. The probe costs nothing but
// does spend one slot of the ~8000-request 5h window, which is why it is rate-limited
// here rather than run per page load.
//
// Probes ride the host's own refresh cadence (one per account per refresh, ~25 min by
// default), the same shape the sidecar's 30-minute scheduler had. The console reads the
// cache and can force a fresh probe on demand.
package quota

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/hostapi"
)

// minInterval keeps forced probes from being spammed by a console reload.
const minInterval = 60 * time.Second

// Snapshot is one reading of an account's rate-limit state.
//
// Utilization is normalised to percent; -1 means the header was absent.
//
// Measured against the live relay: it answers a probe with
//
//	anthropic-ratelimit-unified-7d-utilization: 0.22689583333333332
//	anthropic-ratelimit-unified-7d-reset: 1786673322
//
// so utilization is a 0..1 fraction. The 5h headers the sidecar also looked for were
// *not* present in any observed response — the fields are kept because reading them
// costs nothing if the gateway starts sending them, but nothing should assume they
// arrive.
//
// `x-litellm-key-spend` is deliberately NOT read. The relay validates the Mirofish JWT
// itself and does the per-account accounting that the unified headers above report, then
// forwards to LiteLLM under one shared virtual key — so that header is the *gateway's*
// lifetime spend and comes back byte-identical for every account (observed:
// 40679.36706610024 for all of them, while 7d utilization differed). It was previously
// rendered per account as "Spend", which reads as that account's cost and is not.
// A genuinely per-account figure would have to be accumulated here from
// `x-litellm-response-cost` on the executor's own responses, and would then cover only
// traffic that went through this proxy.
type Snapshot struct {
	Status        int     `json:"status"`
	Utilization5h float64 `json:"utilization_5h"`
	Utilization7d float64 `json:"utilization_7d"`
	Reset5h       int64   `json:"reset_5h,omitempty"`
	Reset7d       int64   `json:"reset_7d,omitempty"`
	At            int64   `json:"at"`
	Error         string  `json:"error,omitempty"`
}

var (
	mu    sync.RWMutex
	cache = make(map[string]Snapshot)
)

func key(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func Lookup(email string) (Snapshot, bool) {
	mu.RLock()
	defer mu.RUnlock()
	snapshot, ok := cache[key(email)]
	return snapshot, ok
}

func Store(email string, snapshot Snapshot) {
	cacheKey := key(email)
	if cacheKey == "" {
		return
	}
	mu.Lock()
	cache[cacheKey] = snapshot
	mu.Unlock()
}

func Forget(email string) {
	mu.Lock()
	delete(cache, key(email))
	mu.Unlock()
}

// ProbeCached returns the cached reading unless a fresh one is asked for and the minimum
// interval has elapsed.
func ProbeCached(cfg config.Config, callbackID, email, accessToken string, force bool) (Snapshot, bool) {
	cached, ok := Lookup(email)
	if !cfg.QuotaProbe {
		return cached, ok
	}
	if ok && !force {
		return cached, true
	}
	if ok && force && time.Since(time.Unix(cached.At, 0)) < minInterval {
		return cached, true
	}
	if strings.TrimSpace(accessToken) == "" {
		return cached, ok
	}
	snapshot := Probe(cfg, callbackID, accessToken)
	Store(email, snapshot)
	return snapshot, true
}

// ProbeAsync refreshes one account's reading in the background.
//
// Called from auth.refresh, which the host drives on its own schedule, so this is the
// automatic "once per account per cycle" probe. It is best-effort: a failed probe must
// never turn into a failed refresh.
func ProbeAsync(cfg config.Config, email, accessToken string) {
	if !cfg.QuotaProbe || strings.TrimSpace(accessToken) == "" || key(email) == "" {
		return
	}
	go func() {
		defer func() {
			// A panic in a detached goroutine would take the whole proxy down; the host's
			// panic fuse only covers calls it made itself.
			if recovered := recover(); recovered != nil {
				hostapi.Log("warn", config.PluginID+": quota probe panicked")
			}
		}()
		Store(email, Probe(cfg, "", accessToken))
	}()
}

// Probe reads the relay's rate-limit headers for one access token.
func Probe(cfg config.Config, callbackID, accessToken string) Snapshot {
	snapshot := Snapshot{
		At:            time.Now().Unix(),
		Utilization5h: -1,
		Utilization7d: -1,
	}
	header := http.Header{
		"Content-Type":      []string{"application/json"},
		"Anthropic-Version": []string{"2023-06-01"},
		"Authorization":     []string{"Bearer " + accessToken},
	}
	body := []byte(`{"model":"claude-haiku-4-5","max_tokens":0,"messages":[{"role":"user","content":"x"}]}`)

	resp, err := hostapi.HTTPDo(callbackID, http.MethodPost, cfg.RelayURL+"/v1/messages", header, body)
	if err != nil {
		snapshot.Error = err.Error()
		return snapshot
	}
	snapshot.Status = resp.StatusCode
	snapshot.Utilization5h = utilizationPercent(resp.Headers.Get("anthropic-ratelimit-unified-5h-utilization"))
	snapshot.Utilization7d = utilizationPercent(resp.Headers.Get("anthropic-ratelimit-unified-7d-utilization"))
	snapshot.Reset5h = headerInt(resp.Headers.Get("anthropic-ratelimit-unified-5h-reset"))
	snapshot.Reset7d = headerInt(resp.Headers.Get("anthropic-ratelimit-unified-7d-reset"))

	// 401/403 means the token is the problem, not the quota; say so rather than showing
	// an empty meter that looks like "no usage".
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		snapshot.Error = "credential rejected by the relay (HTTP " + strconv.Itoa(resp.StatusCode) + ")"
	} else if snapshot.Utilization5h < 0 && snapshot.Utilization7d < 0 {
		snapshot.Error = "relay returned no rate-limit headers (HTTP " + strconv.Itoa(resp.StatusCode) + ")"
	}
	return snapshot
}

// utilizationPercent converts the gateway's 0..1 fraction to a percentage. A value above
// 1 is passed through: it can only mean the gateway switched to reporting percentages,
// and scaling it again would report 100% for a barely-used account.
func utilizationPercent(raw string) float64 {
	value, ok := parseFloat(raw)
	if !ok {
		return -1
	}
	if value <= 1 {
		return value * 100
	}
	return value
}

func headerInt(raw string) int64 {
	value, ok := parseFloat(raw)
	if !ok {
		return 0
	}
	return int64(value)
}

func parseFloat(raw string) (float64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
