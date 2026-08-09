// Package auth implements the auth provider capability: parse stored credentials, drive
// the email + verification-code login, and refresh access tokens on the host's schedule.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/credential"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/mirofish"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/quota"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/rpc"
)

// -- auth.parse ---------------------------------------------------------------

func Parse(request []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(req.Provider), config.PluginID) {
		return rpc.OK(pluginapi.AuthParseResponse{Handled: false})
	}

	stored, err := credential.Decode(req.RawJSON)
	if err != nil {
		return nil, fmt.Errorf("decode %s auth file: %w", config.PluginID, err)
	}
	access := stored.AccessToken()
	refresh := stored.RefreshToken()
	if refresh == "" {
		return nil, fmt.Errorf("%s auth file has no refresh_token", config.PluginID)
	}
	email := stored.Email()
	if email == "" {
		email = mirofish.JWTEmail(access)
	}

	data := buildAuthData(record{
		email:     email,
		pair:      mirofish.TokenPair{Access: access, Refresh: refresh},
		fileName:  req.FileName,
		suspended: stored.Suspended(),
		// Parsing is not refreshing. Restamping last_refresh here would reset the host's
		// rotation clock on every file event and let an access token drift toward expiry
		// unnoticed.
		lastRefresh: stored.LastRefresh(),
	})
	// Leave ID empty: the host derives it from the file path, and matching that keeps the
	// parsed record and the login-time record pointing at one credential.
	data.ID = ""
	return rpc.OK(pluginapi.AuthParseResponse{Handled: true, Auth: data})
}

// -- auth.login.start / auth.login.poll ---------------------------------------

func LoginStart(request []byte) ([]byte, error) {
	var req pluginapi.AuthLoginStartRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := config.Loaded()
	session, err := newSession()
	if err != nil {
		return nil, fmt.Errorf("generate login state: %w", err)
	}

	return rpc.OK(pluginapi.AuthLoginStartResponse{
		Provider:  config.PluginID,
		URL:       LoginPageURL(cfg, req.BaseURL, session.state),
		State:     session.state,
		ExpiresAt: session.expiresAt.UTC(),
	})
}

func LoginPoll(request []byte) ([]byte, error) {
	var req pluginapi.AuthLoginPollRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	session := LookupSession(req.State)
	if session == nil {
		return rpc.OK(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "login session expired; start again",
		})
	}

	result := session.poll()
	switch {
	case result.failure != "":
		return rpc.OK(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: result.failure,
		})
	case result.pending:
		return rpc.OK(pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending})
	}

	email := result.email
	if claimed := mirofish.JWTEmail(result.tokens.Access); claimed != "" {
		// The JWT is authoritative: it is what the relay will bill and rate-limit.
		email = claimed
	}
	if email == "" {
		return rpc.OK(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "login succeeded but no account email was returned",
		})
	}
	data := buildAuthData(record{email: email, pair: result.tokens})
	// Seed the quota cache now so the console has a reading before the first refresh.
	quota.ProbeAsync(config.Loaded(), email, result.tokens.Access)

	return rpc.OK(pluginapi.AuthLoginPollResponse{
		Status:  pluginapi.AuthLoginStatusSuccess,
		Message: "signed in as " + email,
		Auth:    data,
	})
}

// -- auth.refresh -------------------------------------------------------------

func Refresh(request []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := config.Loaded()

	stored, err := credential.Decode(req.StorageJSON)
	if err != nil {
		return nil, fmt.Errorf("decode %s credential: %w", config.PluginID, err)
	}
	refresh := stored.RefreshToken()
	if refresh == "" {
		return nil, fmt.Errorf("%s credential has no refresh_token; re-login required", config.PluginID)
	}
	email := stored.Email()
	if email == "" {
		email = mirofish.JWTEmail(stored.AccessToken())
	}

	pair, err := mirofish.Refresh("", cfg, refresh)
	if err != nil {
		var backendErr *mirofish.BackendError
		if errors.As(err, &backendErr) && backendErr.Unauthorized() {
			// The refresh token is gone (>30 days idle, or revoked). Surface it as an auth
			// failure so the panel shows the credential as needing re-login instead of
			// retrying forever.
			return rpc.ErrorStatus("unauthorized", "needs re-login: "+backendErr.Message, 401), nil
		}
		return nil, err
	}
	if claimed := mirofish.JWTEmail(pair.Access); claimed != "" {
		email = claimed
	}

	// Ride the host's refresh cadence for the quota reading, which is what gave the
	// sidecar's 30-minute scheduler its "one probe per account per cycle" behaviour.
	quota.ProbeAsync(cfg, email, pair.Access)

	// Leave ID and FileName to the host: its refresh adapter carries them over from the
	// credential being refreshed, which is authoritative even if the email changed.
	data := buildAuthData(record{email: email, pair: pair, suspended: stored.Suspended()})
	data.ID = req.AuthID
	data.FileName = ""
	return rpc.OK(pluginapi.AuthRefreshResponse{
		Auth:             data,
		NextRefreshAfter: NextRefreshAfter(cfg, pair.Access),
	})
}

// -- shared -------------------------------------------------------------------

// record describes one credential to hand back to the host.
type record struct {
	email     string
	pair      mirofish.TokenPair
	fileName  string
	suspended bool
	// lastRefresh preserves an existing rotation timestamp; empty means "now".
	lastRefresh string
}

// buildAuthData assembles the record the host persists and schedules.
//
// The two metadata keys are load-bearing: without `expired` and
// `refresh_interval_seconds` the host's auto-refresh loop has no expiry and no lead
// registered for this provider, so it never claims the credential and the access token
// silently dies after an hour.
func buildAuthData(rec record) pluginapi.AuthData {
	cfg := config.Loaded()
	email := strings.ToLower(strings.TrimSpace(rec.email))
	fileName := strings.TrimSpace(rec.fileName)
	if fileName == "" || !strings.HasSuffix(fileName, ".json") {
		fileName = FileName(email)
	}

	storage, _ := json.Marshal(credential.Storage{
		Type:         config.PluginID,
		Email:        email,
		AccessToken:  rec.pair.Access,
		RefreshToken: rec.pair.Refresh,
		Suspended:    rec.suspended,
	})

	lastRefresh := strings.TrimSpace(rec.lastRefresh)
	if lastRefresh == "" {
		lastRefresh = time.Now().UTC().Format(time.RFC3339)
	}
	metadata := map[string]any{
		"email":                    email,
		"last_refresh":             lastRefresh,
		"refresh_interval_seconds": cfg.RefreshIntervalSeconds,
	}
	if expiry := mirofish.JWTExpiry(rec.pair.Access); !expiry.IsZero() {
		metadata["expired"] = expiry.Format(time.RFC3339)
	}
	if rec.suspended {
		metadata["disabled"] = true
	}

	return pluginapi.AuthData{
		Provider:         config.PluginID,
		ID:               fileName,
		FileName:         fileName,
		Label:            email,
		Disabled:         rec.suspended,
		StorageJSON:      storage,
		Metadata:         metadata,
		NextRefreshAfter: NextRefreshAfter(cfg, rec.pair.Access),
	}
}

// NextRefreshAfter is the earliest the host should try again. It is a floor, not a
// schedule: shouldRefresh still fires early when the token is close to expiry.
func NextRefreshAfter(cfg config.Config, accessToken string) time.Time {
	interval := time.Duration(cfg.RefreshIntervalSeconds) * time.Second
	floor := time.Now().Add(interval)
	expiry := mirofish.JWTExpiry(accessToken)
	if expiry.IsZero() {
		return floor
	}
	// Never park past the token's own lifetime.
	if latest := expiry.Add(-time.Minute); latest.Before(floor) {
		return latest
	}
	return floor
}

// FileName keeps the credential one file per account and keeps the name inside the auth
// directory: the host joins it onto auth-dir without further sanitising.
func FileName(email string) string {
	safe := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			return '_'
		}
		return r
	}, strings.TrimSpace(email))
	safe = strings.ReplaceAll(safe, "..", "_")
	if safe == "" {
		safe = "unknown"
	}
	return config.PluginID + "-" + safe + ".json"
}
