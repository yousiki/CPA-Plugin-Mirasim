package mirofish

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type Claims struct {
	Sub    string `json:"sub"`
	Email  string `json:"email"`
	Tenant string `json:"tenant"`
	Plan   string `json:"plan"`
	Exp    int64  `json:"exp"`
}

// DecodeJWT reads a JWT payload without verifying the signature. Never trusted for
// authorization - only to learn the account email and expiry the backend already issued.
func DecodeJWT(token string) *Claims {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 || parts[1] == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	claims := &Claims{}
	if err = json.Unmarshal(raw, claims); err != nil {
		return nil
	}
	return claims
}

func JWTEmail(token string) string {
	claims := DecodeJWT(token)
	if claims == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(claims.Email))
}

func JWTExpiry(token string) time.Time {
	claims := DecodeJWT(token)
	if claims == nil || claims.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0).UTC()
}
