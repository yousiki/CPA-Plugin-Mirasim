// Package mirofish is the Mirofish auth backend client.
//
// Verified behaviour (measured against the live services):
//
//	POST {login}/auth/code    {email}         -> 200, emails a 6-digit code
//	POST {login}/auth/verify  {email, code}   -> {access_token, refresh_token, token_type}
//	POST {login}/auth/refresh {refresh_token} -> {access_token, refresh_token, token_type}
//	  * does NOT require a valid access token (proven with an expired one)
//	  * rotates the refresh token, but the previous one stays valid, so the loop is
//	    idempotent and crash-safe: a lost write cannot lock an account out
//
// Access TTL is ~3600s; refresh TTL is 30 days and is re-minted on every refresh, so
// refreshing at least once per 30 days means never logging in again.
package mirofish

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/hostapi"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/textutil"
)

type TokenPair struct {
	Access  string
	Refresh string
}

type authResponse struct {
	AccessToken  string `json:"access_token"`
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	DevCode      string `json:"dev_code"`
}

// BackendError is a rejection from the Mirofish auth backend, carrying its status.
//
// The refresh path keys its one destructive signal - telling the host the credential
// needs re-enrolment - on this type. Matching on message text would also catch a 401
// from an unrelated host call and wipe a working credential over a transient error.
type BackendError struct {
	Status  int
	Message string
}

func (e *BackendError) Error() string { return e.Message }

// Unauthorized reports whether the backend rejected the credential itself rather than
// failing transiently. 429 is excluded on purpose: it is a retryable rate limit.
func (e *BackendError) Unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden ||
		e.Status == http.StatusBadRequest || e.Status == http.StatusNotFound
}

func postJSON(callbackID string, cfg config.Config, path string, body any) (*authResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	header := http.Header{"Content-Type": []string{"application/json"}}
	resp, err := hostapi.HTTPDo(callbackID, http.MethodPost, cfg.LoginURL+path, header, payload)
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &BackendError{
			Status:  resp.StatusCode,
			Message: fmt.Sprintf("%s failed (HTTP %d): %s", path, resp.StatusCode, textutil.Truncate(string(resp.Body), 200)),
		}
	}
	parsed := &authResponse{}
	if len(resp.Body) > 0 {
		// A non-JSON 2xx body is not fatal; the caller checks the fields it needs.
		_ = json.Unmarshal(resp.Body, parsed)
	}
	return parsed, nil
}

// RequestCode asks the backend to email a 6-digit code. Dev builds may echo the code
// back; it is surfaced so staging stays testable.
func RequestCode(callbackID string, cfg config.Config, email string) (string, error) {
	resp, err := postJSON(callbackID, cfg, "/auth/code", map[string]string{"email": email})
	if err != nil {
		return "", err
	}
	return resp.DevCode, nil
}

func VerifyCode(callbackID string, cfg config.Config, email, code string) (TokenPair, error) {
	resp, err := postJSON(callbackID, cfg, "/auth/verify", map[string]string{"email": email, "code": code})
	if err != nil {
		return TokenPair{}, err
	}
	access := textutil.FirstNonEmpty(resp.AccessToken, resp.Token)
	if access == "" || resp.RefreshToken == "" {
		return TokenPair{}, fmt.Errorf("/auth/verify returned no token pair")
	}
	return TokenPair{Access: access, Refresh: resp.RefreshToken}, nil
}

func Refresh(callbackID string, cfg config.Config, refreshToken string) (TokenPair, error) {
	resp, err := postJSON(callbackID, cfg, "/auth/refresh", map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return TokenPair{}, err
	}
	access := textutil.FirstNonEmpty(resp.AccessToken, resp.Token)
	if access == "" {
		return TokenPair{}, fmt.Errorf("/auth/refresh returned no access_token")
	}
	// Keep the previous refresh token when the backend does not rotate.
	return TokenPair{Access: access, Refresh: textutil.FirstNonEmpty(resp.RefreshToken, refreshToken)}, nil
}
