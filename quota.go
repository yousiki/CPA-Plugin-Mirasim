package main

// Quota tracking for Mirasim accounts.
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

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// quotaMinInterval keeps forced probes from being spammed by a console reload.
const quotaMinInterval = 60 * time.Second

// quotaSnapshot is one reading of an account's rate-limit state.
//
// Utilization is normalised to percent; -1 means the header was absent.
//
// Measured against the live relay: it answers a probe with
//
//	anthropic-ratelimit-unified-7d-utilization: 0.22689583333333332
//	anthropic-ratelimit-unified-7d-reset: 1786673322
//	x-litellm-key-spend: 40679.36706610024
//
// so utilization is a 0..1 fraction. The 5h headers the sidecar also looked for were
// *not* present in any observed response — the fields are kept because reading them
// costs nothing if the gateway starts sending them, but nothing should assume they
// arrive.
type quotaSnapshot struct {
	Status        int     `json:"status"`
	Utilization5h float64 `json:"utilization_5h"`
	Utilization7d float64 `json:"utilization_7d"`
	Reset5h       int64   `json:"reset_5h,omitempty"`
	Reset7d       int64   `json:"reset_7d,omitempty"`
	KeySpend      float64 `json:"key_spend"`
	At            int64   `json:"at"`
	Error         string  `json:"error,omitempty"`
}

var (
	quotaMu    sync.RWMutex
	quotaCache = make(map[string]quotaSnapshot)
)

func quotaKey(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func lookupQuota(email string) (quotaSnapshot, bool) {
	quotaMu.RLock()
	defer quotaMu.RUnlock()
	snapshot, ok := quotaCache[quotaKey(email)]
	return snapshot, ok
}

func storeQuota(email string, snapshot quotaSnapshot) {
	key := quotaKey(email)
	if key == "" {
		return
	}
	quotaMu.Lock()
	quotaCache[key] = snapshot
	quotaMu.Unlock()
}

func forgetQuota(email string) {
	quotaMu.Lock()
	delete(quotaCache, quotaKey(email))
	quotaMu.Unlock()
}

// probeQuotaCached returns the cached reading unless a fresh one is asked for and the
// minimum interval has elapsed.
func probeQuotaCached(cfg pluginConfig, callbackID, email, accessToken string, force bool) (quotaSnapshot, bool) {
	cached, ok := lookupQuota(email)
	if !cfg.QuotaProbe {
		return cached, ok
	}
	if ok && !force {
		return cached, true
	}
	if ok && force && time.Since(time.Unix(cached.At, 0)) < quotaMinInterval {
		return cached, true
	}
	if strings.TrimSpace(accessToken) == "" {
		return cached, ok
	}
	snapshot := probeQuota(cfg, callbackID, accessToken)
	storeQuota(email, snapshot)
	return snapshot, true
}

// probeQuotaAsync refreshes one account's reading in the background.
//
// Called from auth.refresh, which the host drives on its own schedule, so this is the
// automatic "once per account per cycle" probe. It is best-effort: a failed probe must
// never turn into a failed refresh.
func probeQuotaAsync(cfg pluginConfig, email, accessToken string) {
	if !cfg.QuotaProbe || strings.TrimSpace(accessToken) == "" || quotaKey(email) == "" {
		return
	}
	go func() {
		defer func() {
			// A panic in a detached goroutine would take the whole proxy down; the host's
			// panic fuse only covers calls it made itself.
			if recovered := recover(); recovered != nil {
				hostLog("warn", pluginID+": quota probe panicked")
			}
		}()
		storeQuota(email, probeQuota(cfg, "", accessToken))
	}()
}

// probeQuota reads the relay's rate-limit headers for one access token.
func probeQuota(cfg pluginConfig, callbackID, accessToken string) quotaSnapshot {
	snapshot := quotaSnapshot{
		At:            time.Now().Unix(),
		Utilization5h: -1,
		Utilization7d: -1,
		KeySpend:      -1,
	}
	header := http.Header{
		"Content-Type":      []string{"application/json"},
		"Anthropic-Version": []string{"2023-06-01"},
		"Authorization":     []string{"Bearer " + accessToken},
	}
	body := []byte(`{"model":"claude-haiku-4-5","max_tokens":0,"messages":[{"role":"user","content":"x"}]}`)

	resp, err := hostHTTPDo(callbackID, http.MethodPost, cfg.RelayURL+"/v1/messages", header, body)
	if err != nil {
		snapshot.Error = err.Error()
		return snapshot
	}
	snapshot.Status = resp.StatusCode
	snapshot.Utilization5h = utilizationPercent(resp.Headers.Get("anthropic-ratelimit-unified-5h-utilization"))
	snapshot.Utilization7d = utilizationPercent(resp.Headers.Get("anthropic-ratelimit-unified-7d-utilization"))
	snapshot.Reset5h = headerInt(resp.Headers.Get("anthropic-ratelimit-unified-5h-reset"))
	snapshot.Reset7d = headerInt(resp.Headers.Get("anthropic-ratelimit-unified-7d-reset"))
	snapshot.KeySpend = headerFloat(resp.Headers.Get("x-litellm-key-spend"))

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

func headerFloat(raw string) float64 {
	value, ok := parseFloat(raw)
	if !ok {
		return -1
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
