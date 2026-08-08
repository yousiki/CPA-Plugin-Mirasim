package main

// Mirofish auth backend client.
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

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type tokenPair struct {
	Access  string
	Refresh string
}

type jwtClaims struct {
	Sub    string `json:"sub"`
	Email  string `json:"email"`
	Tenant string `json:"tenant"`
	Plan   string `json:"plan"`
	Exp    int64  `json:"exp"`
}

type authResponse struct {
	AccessToken  string `json:"access_token"`
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	DevCode      string `json:"dev_code"`
}

// authBackendError is a rejection from the Mirofish auth backend, carrying its status.
//
// The refresh path keys its one destructive signal - telling the host the credential
// needs re-enrolment - on this type. Matching on message text would also catch a 401
// from an unrelated host call and wipe a working credential over a transient error.
type authBackendError struct {
	Status  int
	Message string
}

func (e *authBackendError) Error() string { return e.Message }

// unauthorized reports whether the backend rejected the credential itself rather than
// failing transiently. 429 is excluded on purpose: it is a retryable rate limit.
func (e *authBackendError) unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden ||
		e.Status == http.StatusBadRequest || e.Status == http.StatusNotFound
}

func postAuthJSON(callbackID string, cfg pluginConfig, path string, body any) (*authResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	header := http.Header{"Content-Type": []string{"application/json"}}
	resp, err := hostHTTPDo(callbackID, http.MethodPost, cfg.LoginURL+path, header, payload)
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &authBackendError{
			Status:  resp.StatusCode,
			Message: fmt.Sprintf("%s failed (HTTP %d): %s", path, resp.StatusCode, truncate(string(resp.Body), 200)),
		}
	}
	parsed := &authResponse{}
	if len(resp.Body) > 0 {
		// A non-JSON 2xx body is not fatal; the caller checks the fields it needs.
		_ = json.Unmarshal(resp.Body, parsed)
	}
	return parsed, nil
}

// requestLoginCode asks the backend to email a 6-digit code. Dev builds may echo the
// code back; it is surfaced so staging stays testable.
func requestLoginCode(callbackID string, cfg pluginConfig, email string) (string, error) {
	resp, err := postAuthJSON(callbackID, cfg, "/auth/code", map[string]string{"email": email})
	if err != nil {
		return "", err
	}
	return resp.DevCode, nil
}

func verifyLoginCode(callbackID string, cfg pluginConfig, email, code string) (tokenPair, error) {
	resp, err := postAuthJSON(callbackID, cfg, "/auth/verify", map[string]string{"email": email, "code": code})
	if err != nil {
		return tokenPair{}, err
	}
	access := firstNonEmpty(resp.AccessToken, resp.Token)
	if access == "" || resp.RefreshToken == "" {
		return tokenPair{}, fmt.Errorf("/auth/verify returned no token pair")
	}
	return tokenPair{Access: access, Refresh: resp.RefreshToken}, nil
}

func refreshTokens(callbackID string, cfg pluginConfig, refreshToken string) (tokenPair, error) {
	resp, err := postAuthJSON(callbackID, cfg, "/auth/refresh", map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return tokenPair{}, err
	}
	access := firstNonEmpty(resp.AccessToken, resp.Token)
	if access == "" {
		return tokenPair{}, fmt.Errorf("/auth/refresh returned no access_token")
	}
	// Keep the previous refresh token when the backend does not rotate.
	return tokenPair{Access: access, Refresh: firstNonEmpty(resp.RefreshToken, refreshToken)}, nil
}

// decodeJWT reads a JWT payload without verifying the signature. Never trusted for
// authorization - only to learn the account email and expiry the backend already issued.
func decodeJWT(token string) *jwtClaims {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 || parts[1] == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	claims := &jwtClaims{}
	if err = json.Unmarshal(raw, claims); err != nil {
		return nil
	}
	return claims
}

func jwtEmail(token string) string {
	claims := decodeJWT(token)
	if claims == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(claims.Email))
}

func jwtExpiry(token string) time.Time {
	claims := decodeJWT(token)
	if claims == nil || claims.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0).UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
