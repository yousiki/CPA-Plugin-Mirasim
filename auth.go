package main

// The auth provider capability: parse stored credentials, drive the email +
// verification-code login, and refresh access tokens on the host's schedule.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// loginSessionTTL bounds how long a started login stays resumable. The panel polls
// every 3s, so this only needs to cover a human reading an email.
const loginSessionTTL = 10 * time.Minute

// maxVerifyAttempts caps guesses against one state. The code is 6 digits and the
// login routes are unauthenticated, so the state alone must not be a free oracle.
const maxVerifyAttempts = 6

// authStorage is the provider-owned credential payload. It is persisted as the auth
// file body, merged with the host-managed metadata map.
//
// Suspension is tracked here rather than in the host's own `disabled` metadata key: the
// host rewrites that key from the runtime record on every save
// (sdk/auth/filestore.go), and the path that reloads a credential from disk after a
// plugin write does not read it back (internal/pluginhost/auth_callbacks.go
// buildAuthFromFileData), so a `disabled` we write is reverted within milliseconds. A
// provider-owned field survives the round trip, and auth.parse turns it back into a
// real Disabled record.
type authStorage struct {
	Type         string `json:"type"`
	Email        string `json:"email"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Suspended    bool   `json:"suspended,omitempty"`
}

// suspendedKey is the provider-owned suspension flag inside the auth file.
const suspendedKey = "suspended"

type loginSession struct {
	state     string
	expiresAt time.Time
	email     string
	codeSent  bool
	attempts  int
	tokens    *tokenPair
	failure   string
}

var (
	loginMu       sync.Mutex
	loginSessions = make(map[string]*loginSession)
)

func shutdownLoginSessions() {
	loginMu.Lock()
	loginSessions = make(map[string]*loginSession)
	loginMu.Unlock()
}

// pruneLoginSessionsLocked drops expired sessions. Callers must hold loginMu.
func pruneLoginSessionsLocked(now time.Time) {
	for state, session := range loginSessions {
		if now.After(session.expiresAt) {
			delete(loginSessions, state)
		}
	}
}

func lookupLoginSession(state string) *loginSession {
	loginMu.Lock()
	defer loginMu.Unlock()
	pruneLoginSessionsLocked(time.Now())
	return loginSessions[strings.TrimSpace(state)]
}

// newLoginState returns an opaque state token.
//
// The charset is constrained by the host: ValidateOAuthState rejects anything outside
// [A-Za-z0-9._-], which raw base64url satisfies.
func newLoginState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// -- auth.parse ---------------------------------------------------------------

func authParse(request []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(req.Provider), pluginID) {
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}

	var stored map[string]any
	if err := json.Unmarshal(req.RawJSON, &stored); err != nil {
		return nil, fmt.Errorf("decode %s auth file: %w", pluginID, err)
	}
	access := stringField(stored, "access_token")
	refresh := stringField(stored, "refresh_token")
	if refresh == "" {
		return nil, fmt.Errorf("%s auth file has no refresh_token", pluginID)
	}
	email := stringField(stored, "email")
	if email == "" {
		email = jwtEmail(access)
	}
	suspended, _ := stored[suspendedKey].(bool)

	data := buildAuthData(authRecord{
		email:     email,
		pair:      tokenPair{Access: access, Refresh: refresh},
		fileName:  req.FileName,
		suspended: suspended,
		// Parsing is not refreshing. Restamping last_refresh here would reset the
		// host's rotation clock on every file event and let an access token drift
		// toward expiry unnoticed.
		lastRefresh: stringField(stored, "last_refresh"),
	})
	// Leave ID empty: the host derives it from the file path, and matching that keeps
	// the parsed record and the login-time record pointing at one credential.
	data.ID = ""
	return okEnvelope(pluginapi.AuthParseResponse{Handled: true, Auth: data})
}

// -- auth.login.start / auth.login.poll ---------------------------------------

func authLoginStart(request []byte) ([]byte, error) {
	var req pluginapi.AuthLoginStartRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := loadedConfig()
	state, err := newLoginState()
	if err != nil {
		return nil, fmt.Errorf("generate login state: %w", err)
	}
	expiresAt := time.Now().Add(loginSessionTTL)

	loginMu.Lock()
	pruneLoginSessionsLocked(time.Now())
	loginSessions[state] = &loginSession{state: state, expiresAt: expiresAt}
	loginMu.Unlock()

	return okEnvelope(pluginapi.AuthLoginStartResponse{
		Provider:  pluginID,
		URL:       loginPageURL(cfg, req.BaseURL, state),
		State:     state,
		ExpiresAt: expiresAt.UTC(),
	})
}

func authLoginPoll(request []byte) ([]byte, error) {
	var req pluginapi.AuthLoginPollRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	session := lookupLoginSession(req.State)
	if session == nil {
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "login session expired; start again",
		})
	}

	loginMu.Lock()
	defer loginMu.Unlock()
	switch {
	case session.failure != "":
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: session.failure,
		})
	case session.tokens == nil:
		return okEnvelope(pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending})
	}

	email := session.email
	if claimed := jwtEmail(session.tokens.Access); claimed != "" {
		// The JWT is authoritative: it is what the relay will bill and rate-limit.
		email = claimed
	}
	if email == "" {
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "login succeeded but no account email was returned",
		})
	}
	data := buildAuthData(authRecord{email: email, pair: *session.tokens})
	// Seed the quota cache now so the console has a reading before the first refresh.
	probeQuotaAsync(loadedConfig(), email, session.tokens.Access)
	delete(loginSessions, session.state)

	return okEnvelope(pluginapi.AuthLoginPollResponse{
		Status:  pluginapi.AuthLoginStatusSuccess,
		Message: "signed in as " + email,
		Auth:    data,
	})
}

// -- auth.refresh -------------------------------------------------------------

func authRefresh(request []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := loadedConfig()

	var stored map[string]any
	if err := json.Unmarshal(req.StorageJSON, &stored); err != nil {
		return nil, fmt.Errorf("decode %s credential: %w", pluginID, err)
	}
	refresh := stringField(stored, "refresh_token")
	if refresh == "" {
		return nil, fmt.Errorf("%s credential has no refresh_token; re-login required", pluginID)
	}
	email := stringField(stored, "email")
	if email == "" {
		email = jwtEmail(stringField(stored, "access_token"))
	}

	pair, err := refreshTokens("", cfg, refresh)
	if err != nil {
		var backendErr *authBackendError
		if errors.As(err, &backendErr) && backendErr.unauthorized() {
			// The refresh token is gone (>30 days idle, or revoked). Surface it as an
			// auth failure so the panel shows the credential as needing re-login
			// instead of retrying forever.
			return errorEnvelopeStatus("unauthorized", "needs re-login: "+backendErr.Message, 401), nil
		}
		return nil, err
	}
	if claimed := jwtEmail(pair.Access); claimed != "" {
		email = claimed
	}

	// Ride the host's refresh cadence for the quota reading, which is what gave the
	// sidecar's 30-minute scheduler its "one probe per account per cycle" behaviour.
	probeQuotaAsync(cfg, email, pair.Access)

	// Leave ID and FileName to the host: its refresh adapter carries them over from the
	// credential being refreshed, which is authoritative even if the email changed.
	suspended, _ := stored[suspendedKey].(bool)
	data := buildAuthData(authRecord{email: email, pair: pair, suspended: suspended})
	data.ID = req.AuthID
	data.FileName = ""
	return okEnvelope(pluginapi.AuthRefreshResponse{
		Auth:             data,
		NextRefreshAfter: nextRefreshAfter(cfg, pair.Access),
	})
}

// -- shared -------------------------------------------------------------------

// authRecord describes one credential to hand back to the host.
type authRecord struct {
	email     string
	pair      tokenPair
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
func buildAuthData(record authRecord) pluginapi.AuthData {
	cfg := loadedConfig()
	email := strings.ToLower(strings.TrimSpace(record.email))
	fileName := strings.TrimSpace(record.fileName)
	if fileName == "" || !strings.HasSuffix(fileName, ".json") {
		fileName = authFileName(email)
	}

	storage, _ := json.Marshal(authStorage{
		Type:         pluginID,
		Email:        email,
		AccessToken:  record.pair.Access,
		RefreshToken: record.pair.Refresh,
		Suspended:    record.suspended,
	})

	lastRefresh := strings.TrimSpace(record.lastRefresh)
	if lastRefresh == "" {
		lastRefresh = time.Now().UTC().Format(time.RFC3339)
	}
	metadata := map[string]any{
		"email":                    email,
		"last_refresh":             lastRefresh,
		"refresh_interval_seconds": cfg.RefreshIntervalSeconds,
	}
	if expiry := jwtExpiry(record.pair.Access); !expiry.IsZero() {
		metadata["expired"] = expiry.Format(time.RFC3339)
	}
	if record.suspended {
		metadata["disabled"] = true
	}

	return pluginapi.AuthData{
		Provider:         pluginID,
		ID:               fileName,
		FileName:         fileName,
		Label:            email,
		Disabled:         record.suspended,
		StorageJSON:      storage,
		Metadata:         metadata,
		NextRefreshAfter: nextRefreshAfter(cfg, record.pair.Access),
	}
}

// nextRefreshAfter is the earliest the host should try again. It is a floor, not a
// schedule: shouldRefresh still fires early when the token is close to expiry.
func nextRefreshAfter(cfg pluginConfig, accessToken string) time.Time {
	interval := time.Duration(cfg.RefreshIntervalSeconds) * time.Second
	floor := time.Now().Add(interval)
	expiry := jwtExpiry(accessToken)
	if expiry.IsZero() {
		return floor
	}
	// Never park past the token's own lifetime.
	if latest := expiry.Add(-time.Minute); latest.Before(floor) {
		return latest
	}
	return floor
}

// authFileName keeps the credential one file per account and keeps the name inside the
// auth directory: the host joins it onto auth-dir without further sanitising.
func authFileName(email string) string {
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
	return pluginID + "-" + safe + ".json"
}

func stringField(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
