package main

// Browser-facing routes: the login flow that replaces an OAuth callback, and the
// operator console that replaces the sidecar's web UI.
//
// Both live under /v0/resource/plugins/mirasim/. The host serves that prefix without
// management authentication and only for GET, so every action is a GET carrying its
// arguments in the query string - the same shape upstream's own host-callback example
// uses. The login routes are protected by the unguessable, short-lived state; the
// console is protected by the configured console_token.

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	// Browser-navigable resource routes, served without management authentication.
	routeLogin       = "/login"
	routeLoginCode   = "/login/code"
	routeLoginVerify = "/login/verify"
	routeStatus      = "/status"
	routeStatusData  = "/status/data"
	routeStatusAct   = "/status/action"

	// Management API route, authenticated by the host with the management key. This is
	// what the management panel's quota card reads, so it needs no console token.
	routeQuota = "/" + pluginID + "/quota"
)

type rpcManagementRequest struct {
	pluginapi.ManagementRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// managementRegistration mirrors the host's registration schema. Handlers are attached
// host-side, so only the route descriptors travel over the wire.
type managementRegistration struct {
	Routes    []managementRoute    `json:"routes,omitempty"`
	Resources []managementResource `json:"resources,omitempty"`
}

type managementRoute struct {
	Method string `json:"Method"`
	Path   string `json:"Path"`
}

type managementResource struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu,omitempty"`
	Description string `json:"Description,omitempty"`
}

func managementRegister(_ []byte) ([]byte, error) {
	return okEnvelope(managementRegistration{
		// No Menu on this one: the host turns a GET route that carries a menu label into
		// an unauthenticated resource, which is the opposite of what a quota feed wants.
		Routes: []managementRoute{
			{Method: http.MethodGet, Path: routeQuota},
		},
		Resources: []managementResource{
			{Path: routeLogin},
			{Path: routeLoginCode},
			{Path: routeLoginVerify},
			{Path: routeStatus, Menu: "Mirasim", Description: "Mirasim accounts, quota and rotation."},
			{Path: routeStatusData},
			{Path: routeStatusAct},
		},
	})
}

func managementHandle(request []byte) ([]byte, error) {
	var req rpcManagementRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := loadedConfig()

	// Management API routes keep their /v0/management prefix; resource routes are
	// rewritten under /v0/resource/plugins/<id>. Handle the authenticated one first so a
	// resource path can never be mistaken for it.
	if strings.TrimSuffix(strings.TrimPrefix(req.Path, managementPrefix), "/") == routeQuota {
		return handleQuotaRoute(cfg, req)
	}

	switch resourceSuffix(req.Path) {
	case routeLogin:
		return htmlResponse(http.StatusOK, loginPageHTML(req.Query.Get("state")))
	case routeLoginCode:
		return handleLoginCode(cfg, req)
	case routeLoginVerify:
		return handleLoginVerify(cfg, req)
	case routeStatus:
		// The shell carries no account data, so it is served without a token: the panel
		// embeds this page in an iframe that cannot send one, and answering that with a
		// raw 403 JSON body is what the operator would see. The token gates the data and
		// action routes below, which is where anything worth protecting lives.
		return htmlResponse(http.StatusOK, statusPageHTML(cfg.ConsoleToken != "", req.Query.Get("token")))
	case routeStatusData:
		if resp, ok := requireConsoleToken(cfg, req.Query); !ok {
			return resp, nil
		}
		return handleStatusData(cfg, req)
	case routeStatusAct:
		if resp, ok := requireConsoleToken(cfg, req.Query); !ok {
			return resp, nil
		}
		return handleStatusAction(req)
	default:
		return jsonResponse(http.StatusNotFound, map[string]any{"ok": false, "error": "unknown route"})
	}
}

const managementPrefix = "/v0/management"

// resourceSuffix strips the host-assigned prefix so routing works off stable names.
func resourceSuffix(path string) string {
	prefix := "/v0/resource/plugins/" + pluginID
	if suffix := strings.TrimPrefix(path, prefix); suffix != path {
		return strings.TrimRight(suffix, "/")
	}
	return strings.TrimRight(path, "/")
}

// handleQuotaRoute feeds the management panel's quota card.
//
// Unlike the console this is behind the host's management middleware, so it carries no
// console token; the caller has already proven it holds the management key.
func handleQuotaRoute(cfg pluginConfig, req rpcManagementRequest) ([]byte, error) {
	authIndex := strings.TrimSpace(req.Query.Get("auth_index"))
	if authIndex == "" {
		return jsonResponse(http.StatusBadRequest, map[string]any{"ok": false, "error": "auth_index is required"})
	}
	stored, err := hostAuthGet(authIndex)
	if err != nil {
		return jsonResponse(http.StatusNotFound, map[string]any{"ok": false, "error": err.Error()})
	}
	var payload map[string]any
	if err = json.Unmarshal(stored.JSON, &payload); err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
	}
	if !strings.EqualFold(stringField(payload, "type"), pluginID) {
		return jsonResponse(http.StatusBadRequest, map[string]any{"ok": false, "error": "not a " + pluginID + " credential"})
	}

	access := stringField(payload, "access_token")
	email := firstNonEmpty(stringField(payload, "email"), jwtEmail(access))
	// The panel's refresh button is an explicit user action, so honour it; the per-account
	// minimum interval inside probeQuotaCached still keeps a rapid reload from spending
	// a request slot each time.
	snapshot, ok := probeQuotaCached(cfg, req.HostCallbackID, email, access, req.Query.Get("force") != "0")
	if !ok {
		return jsonResponse(http.StatusOK, map[string]any{
			"ok": true, "email": email, "quota": nil, "quota_enabled": cfg.QuotaProbe,
		})
	}
	return jsonResponse(http.StatusOK, map[string]any{
		"ok":            true,
		"email":         email,
		"quota":         snapshot,
		"quota_enabled": cfg.QuotaProbe,
		"now":           time.Now().Unix(),
	})
}

// -- login --------------------------------------------------------------------

// loginPageURL is what the panel shows and opens after the user clicks Login.
//
// The BaseURL the host passes in points at 127.0.0.1, which is useless to a remote
// browser, so a configured public base URL wins. Falling back to the host value still
// works for a loopback deployment.
func loginPageURL(cfg pluginConfig, hostBaseURL, state string) string {
	base := cfg.PublicBaseURL
	if base == "" {
		base = originOf(hostBaseURL)
	}
	return fmt.Sprintf("%s/v0/resource/plugins/%s%s?state=%s", base, pluginID, routeLogin, url.QueryEscape(state))
}

func originOf(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func handleLoginCode(cfg pluginConfig, req rpcManagementRequest) ([]byte, error) {
	session := lookupLoginSession(req.Query.Get("state"))
	if session == nil {
		return jsonResponse(http.StatusNotFound, map[string]any{"ok": false, "error": "login session expired; start again from the panel"})
	}
	email := strings.ToLower(strings.TrimSpace(req.Query.Get("email")))
	if email == "" || !strings.Contains(email, "@") {
		return jsonResponse(http.StatusBadRequest, map[string]any{"ok": false, "error": "a valid email is required"})
	}

	devCode, err := requestLoginCode(req.HostCallbackID, cfg, email)
	if err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
	}

	loginMu.Lock()
	session.email = email
	session.codeSent = true
	session.attempts = 0
	loginMu.Unlock()

	out := map[string]any{"ok": true, "email": email}
	if devCode != "" {
		// Staging builds echo the code back; surfacing it keeps staging testable.
		out["dev_code"] = devCode
	}
	return jsonResponse(http.StatusOK, out)
}

func handleLoginVerify(cfg pluginConfig, req rpcManagementRequest) ([]byte, error) {
	session := lookupLoginSession(req.Query.Get("state"))
	if session == nil {
		return jsonResponse(http.StatusNotFound, map[string]any{"ok": false, "error": "login session expired; start again from the panel"})
	}
	code := strings.TrimSpace(req.Query.Get("code"))
	if code == "" {
		return jsonResponse(http.StatusBadRequest, map[string]any{"ok": false, "error": "verification code is required"})
	}

	loginMu.Lock()
	email := session.email
	sent := session.codeSent
	session.attempts++
	attempts := session.attempts
	loginMu.Unlock()

	if !sent || email == "" {
		return jsonResponse(http.StatusBadRequest, map[string]any{"ok": false, "error": "request a code first"})
	}
	if attempts > maxVerifyAttempts {
		loginMu.Lock()
		session.failure = "too many verification attempts"
		loginMu.Unlock()
		return jsonResponse(http.StatusTooManyRequests, map[string]any{"ok": false, "error": "too many verification attempts; start again"})
	}

	pair, err := verifyLoginCode(req.HostCallbackID, cfg, email, code)
	if err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
	}

	loginMu.Lock()
	session.tokens = &pair
	loginMu.Unlock()

	// The panel's poll turns this into a saved credential within a few seconds.
	return jsonResponse(http.StatusOK, map[string]any{"ok": true, "email": firstNonEmpty(jwtEmail(pair.Access), email)})
}

// -- console ------------------------------------------------------------------

// requireConsoleToken gates every console route. The resource prefix is unauthenticated
// by design, so without a configured token the console stays closed rather than
// exposing credential state and suspend/resume to anyone who can reach the port.
func requireConsoleToken(cfg pluginConfig, query url.Values) ([]byte, bool) {
	if cfg.ConsoleToken == "" {
		body, _ := jsonResponse(http.StatusForbidden, map[string]any{
			"ok":    false,
			"error": "console is disabled: set plugins.configs." + pluginID + ".console_token",
		})
		return body, false
	}
	supplied := strings.TrimSpace(query.Get("token"))
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(cfg.ConsoleToken)) != 1 {
		body, _ := jsonResponse(http.StatusForbidden, map[string]any{"ok": false, "error": "invalid console token"})
		return body, false
	}
	return nil, true
}

type accountView struct {
	AuthIndex   string         `json:"auth_index"`
	Name        string         `json:"name"`
	Label       string         `json:"label"`
	Email       string         `json:"email"`
	Status      string         `json:"status"`
	Disabled    bool           `json:"disabled"`
	Unavailable bool           `json:"unavailable"`
	Expired     string         `json:"expired,omitempty"`
	SecondsLeft int64          `json:"seconds_left"`
	Quota       *quotaSnapshot `json:"quota,omitempty"`
	Error       string         `json:"error,omitempty"`
}

func handleStatusData(cfg pluginConfig, req rpcManagementRequest) ([]byte, error) {
	entries, err := hostAuthList()
	if err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
	}
	// Cached readings are always shown; `quota=1` additionally forces a live probe,
	// which costs one request slot per account.
	force := cfg.QuotaProbe && req.Query.Get("quota") == "1"

	accounts := make([]accountView, 0, len(entries))
	for _, entry := range entries {
		if !strings.EqualFold(entry.Provider, pluginID) && !strings.EqualFold(entry.Type, pluginID) {
			continue
		}
		view := accountView{
			AuthIndex:   entry.AuthIndex,
			Name:        entry.Name,
			Label:       entry.Label,
			Status:      entry.Status,
			Disabled:    entry.Disabled,
			Unavailable: entry.Unavailable,
		}
		stored, errGet := hostAuthGet(entry.AuthIndex)
		if errGet != nil {
			view.Error = errGet.Error()
			accounts = append(accounts, view)
			continue
		}
		var payload map[string]any
		if errDecode := json.Unmarshal(stored.JSON, &payload); errDecode != nil {
			view.Error = errDecode.Error()
			accounts = append(accounts, view)
			continue
		}
		access := stringField(payload, "access_token")
		view.Email = firstNonEmpty(stringField(payload, "email"), jwtEmail(access), entry.Label)
		if suspended, _ := payload[suspendedKey].(bool); suspended {
			// The stored flag is authoritative: the runtime record briefly reports
			// active again right after a save, before the reparse lands.
			view.Disabled = true
		}
		if expiry := jwtExpiry(access); !expiry.IsZero() {
			view.Expired = expiry.Format(time.RFC3339)
			view.SecondsLeft = int64(time.Until(expiry).Seconds())
		}
		if snapshot, ok := probeQuotaCached(cfg, req.HostCallbackID, view.Email, access, force); ok {
			view.Quota = &snapshot
		}
		accounts = append(accounts, view)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Email < accounts[j].Email })

	return jsonResponse(http.StatusOK, map[string]any{
		"ok":            true,
		"accounts":      accounts,
		"relay_url":     cfg.RelayURL,
		"login_url":     cfg.LoginURL,
		"models":        modelIDs(cfg),
		"quota_enabled": cfg.QuotaProbe,
		"now":           time.Now().Unix(),
	})
}

func modelIDs(cfg pluginConfig) []string {
	out := make([]string, 0, len(cfg.models))
	for _, spec := range cfg.models {
		out = append(out, spec.ID)
	}
	return out
}

func handleStatusAction(req rpcManagementRequest) ([]byte, error) {
	op := strings.ToLower(strings.TrimSpace(req.Query.Get("op")))
	authIndex := strings.TrimSpace(req.Query.Get("auth_index"))
	if authIndex == "" {
		return jsonResponse(http.StatusBadRequest, map[string]any{"ok": false, "error": "auth_index is required"})
	}

	var disabled bool
	switch op {
	case "suspend":
		disabled = true
	case "resume":
		disabled = false
	default:
		return jsonResponse(http.StatusBadRequest, map[string]any{"ok": false, "error": "op must be suspend or resume"})
	}

	stored, err := hostAuthGet(authIndex)
	if err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
	}
	var payload map[string]any
	if err = json.Unmarshal(stored.JSON, &payload); err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
	}
	if !strings.EqualFold(stringField(payload, "type"), pluginID) {
		return jsonResponse(http.StatusBadRequest, map[string]any{"ok": false, "error": "not a " + pluginID + " credential"})
	}

	// Toggling in place keeps the refresh token, so a resumed account never needs to log
	// in again; the sidecar had to delete the credential instead.
	//
	// The provider-owned `suspended` field is what actually sticks. The host's own
	// `disabled` is rewritten from the runtime record on the very next save, so it is
	// set alongside only to keep the file self-consistent for anyone reading it.
	if disabled {
		payload["suspended"] = true
		payload["disabled"] = true
	} else {
		delete(payload, "suspended")
		delete(payload, "disabled")
	}
	updated, err := json.Marshal(payload)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
	}
	name := firstNonEmpty(stored.Name, authIndex)
	if !strings.HasSuffix(name, ".json") {
		name += ".json"
	}
	if err = hostAuthSave(name, updated); err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
	}
	if disabled {
		// A suspended account stops being probed, so its reading would go stale silently.
		forgetQuota(stringField(payload, "email"))
	}
	return jsonResponse(http.StatusOK, map[string]any{"ok": true, "disabled": disabled, "name": name})
}

// -- responses ----------------------------------------------------------------

func jsonResponse(status int, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return okEnvelope(pluginapi.ManagementResponse{
		StatusCode: status,
		Headers: http.Header{
			"Content-Type":  []string{"application/json; charset=utf-8"},
			"Cache-Control": []string{"no-store"},
		},
		Body: payload,
	})
}

func htmlResponse(status int, html string) ([]byte, error) {
	return okEnvelope(pluginapi.ManagementResponse{
		StatusCode: status,
		Headers: http.Header{
			"Content-Type":  []string{"text/html; charset=utf-8"},
			"Cache-Control": []string{"no-store"},
			// The pages are self-contained; no external origins are needed.
			"Content-Security-Policy": []string{"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'"},
			"Referrer-Policy":         []string{"no-referrer"},
		},
		Body: []byte(html),
	})
}
