package management

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/credential"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/hostapi"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/mirofish"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/quota"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/textutil"
)

// requireConsoleToken gates every console route. The resource prefix is unauthenticated
// by design, so without a configured token the console stays closed rather than exposing
// credential state and suspend/resume to anyone who can reach the port.
func requireConsoleToken(cfg config.Config, query url.Values) ([]byte, bool) {
	if cfg.ConsoleToken == "" {
		body, _ := errorResponse(http.StatusForbidden,
			"console is disabled: set plugins.configs."+config.PluginID+".console_token")
		return body, false
	}
	supplied := strings.TrimSpace(query.Get("token"))
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(cfg.ConsoleToken)) != 1 {
		body, _ := errorResponse(http.StatusForbidden, "invalid console token")
		return body, false
	}
	return nil, true
}

// isOurs reports whether a host auth entry belongs to this plugin's provider.
func isOurs(entry pluginapi.HostAuthFileEntry) bool {
	return strings.EqualFold(entry.Provider, config.PluginID) ||
		strings.EqualFold(entry.Type, config.PluginID)
}

type accountView struct {
	AuthIndex   string          `json:"auth_index"`
	Name        string          `json:"name"`
	Label       string          `json:"label"`
	Email       string          `json:"email"`
	Status      string          `json:"status"`
	Disabled    bool            `json:"disabled"`
	Unavailable bool            `json:"unavailable"`
	Expired     string          `json:"expired,omitempty"`
	SecondsLeft int64           `json:"seconds_left"`
	Quota       *quota.Snapshot `json:"quota,omitempty"`
	Error       string          `json:"error,omitempty"`
}

func handleStatusData(cfg config.Config, req request) ([]byte, error) {
	entries, err := hostapi.AuthList()
	if err != nil {
		return errorResponse(http.StatusBadGateway, err.Error())
	}
	// Cached readings are always shown; `quota=1` additionally forces a live probe, which
	// costs one request slot per account.
	force := cfg.QuotaProbe && req.Query.Get("quota") == "1"

	accounts := make([]accountView, 0, len(entries))
	for _, entry := range entries {
		if !isOurs(entry) {
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
		stored, errGet := hostapi.AuthGet(entry.AuthIndex)
		if errGet != nil {
			view.Error = errGet.Error()
			accounts = append(accounts, view)
			continue
		}
		payload, errDecode := credential.Decode(stored.JSON)
		if errDecode != nil {
			view.Error = errDecode.Error()
			accounts = append(accounts, view)
			continue
		}
		access := payload.AccessToken()
		view.Email = textutil.FirstNonEmpty(payload.Email(), mirofish.JWTEmail(access), entry.Label)
		if payload.Suspended() {
			// The stored flag is authoritative: the runtime record briefly reports active
			// again right after a save, before the reparse lands.
			view.Disabled = true
		}
		if expiry := mirofish.JWTExpiry(access); !expiry.IsZero() {
			view.Expired = expiry.Format(time.RFC3339)
			view.SecondsLeft = int64(time.Until(expiry).Seconds())
		}
		if snapshot, ok := quota.ProbeCached(cfg, req.HostCallbackID, view.Email, access, force); ok {
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
		"models":        cfg.ModelIDList(),
		"quota_enabled": cfg.QuotaProbe,
		"now":           time.Now().Unix(),
	})
}

// -- suspend / resume ---------------------------------------------------------

func handleStatusAction(req request) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(req.Query.Get("op"))) {
	case "suspend":
		return toggleOne(req, true)
	case "resume":
		return toggleOne(req, false)
	case "suspend_all":
		return toggleAll(true)
	case "resume_all":
		return toggleAll(false)
	default:
		return errorResponse(http.StatusBadRequest, "op must be suspend, resume, suspend_all or resume_all")
	}
}

func toggleOne(req request, disabled bool) ([]byte, error) {
	authIndex := strings.TrimSpace(req.Query.Get("auth_index"))
	if authIndex == "" {
		return errorResponse(http.StatusBadRequest, "auth_index is required")
	}
	result, err := setSuspended(authIndex, disabled)
	if err != nil {
		return errorResponse(http.StatusBadGateway, err.Error())
	}
	return jsonResponse(http.StatusOK, map[string]any{
		"ok":       true,
		"disabled": disabled,
		"name":     result.name,
		"changed":  result.changed,
	})
}

// toggleAll takes every Mirasim credential out of rotation, or puts it back.
//
// The credentials are walked sequentially on purpose: each save makes the host reload the
// credential from disk, so concurrent writes would race its file store. A per-account
// failure is reported rather than aborting the sweep — leaving half the accounts in the
// old state with no word about which half is the worse outcome.
func toggleAll(disabled bool) ([]byte, error) {
	entries, err := hostapi.AuthList()
	if err != nil {
		return errorResponse(http.StatusBadGateway, err.Error())
	}

	changed, skipped := 0, 0
	failed := make([]map[string]any, 0)
	for _, entry := range entries {
		if !isOurs(entry) {
			continue
		}
		result, errToggle := setSuspended(entry.AuthIndex, disabled)
		switch {
		case errToggle != nil:
			failed = append(failed, map[string]any{
				"auth_index": entry.AuthIndex,
				"email":      textutil.FirstNonEmpty(result.email, entry.Label, entry.Name),
				"error":      errToggle.Error(),
			})
		case result.changed:
			changed++
		default:
			skipped++
		}
	}
	return jsonResponse(http.StatusOK, map[string]any{
		"ok":       true,
		"disabled": disabled,
		"changed":  changed,
		"skipped":  skipped,
		"failed":   failed,
	})
}

// toggle is the outcome of one credential's suspend/resume.
type toggle struct {
	email   string
	name    string
	changed bool
}

// setSuspended flips one credential's suspension in place.
//
// Toggling in place keeps the refresh token, so a resumed account never needs to log in
// again; the sidecar had to delete the credential instead.
//
// An account already in the target state is left untouched: a redundant save would make
// the host reload the credential for nothing, which matters most on the bulk path where
// that would happen once per account.
func setSuspended(authIndex string, disabled bool) (toggle, error) {
	stored, err := hostapi.AuthGet(authIndex)
	if err != nil {
		return toggle{}, err
	}
	payload, err := credential.Decode(stored.JSON)
	if err != nil {
		return toggle{}, err
	}
	if !payload.IsOurs() {
		return toggle{}, fmt.Errorf("not a %s credential", config.PluginID)
	}

	email := textutil.FirstNonEmpty(payload.Email(), mirofish.JWTEmail(payload.AccessToken()))
	name := textutil.FirstNonEmpty(stored.Name, authIndex)
	if !strings.HasSuffix(name, ".json") {
		name += ".json"
	}
	if payload.Suspended() == disabled {
		return toggle{email: email, name: name}, nil
	}

	payload.SetSuspended(disabled)
	updated, err := payload.Encode()
	if err != nil {
		return toggle{email: email, name: name}, err
	}
	if err = hostapi.AuthSave(name, updated); err != nil {
		return toggle{email: email, name: name}, err
	}
	if disabled {
		// A suspended account stops being probed, so its reading would go stale silently.
		quota.Forget(email)
	}
	return toggle{email: email, name: name, changed: true}, nil
}
