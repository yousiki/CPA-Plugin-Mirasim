package relaysig

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
)

func testIdentity(t *testing.T) *identity {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := newIdentity(priv)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// The canonical string is the whole protocol; a stray field or separator would make every
// signed request fail server-side with nothing local to see.
func TestCanonical(t *testing.T) {
	got := canonical("post", "/v1/messages", "1787300640023", "dLeEwhegSUjRbIiP", nil)
	want := strings.Join([]string{
		"mrs-sig-v1",
		"POST",
		"/v1/messages",
		"1787300640023",
		"dLeEwhegSUjRbIiP",
		// sha256 of the empty string, which is what a bodyless request digests to.
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}, "\n")
	if got != want {
		t.Fatalf("canonical mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestDeviceIDStableAndSized(t *testing.T) {
	id := testIdentity(t)
	if len(id.deviceID) != deviceIDLength {
		t.Fatalf("deviceID is %d chars, want %d", len(id.deviceID), deviceIDLength)
	}
	again, err := newIdentity(id.priv)
	if err != nil {
		t.Fatal(err)
	}
	if again.deviceID != id.deviceID {
		t.Fatalf("deviceID not derived deterministically: %q vs %q", again.deviceID, id.deviceID)
	}
	if testIdentity(t).deviceID == id.deviceID {
		t.Fatal("two keys produced the same deviceID")
	}
}

func TestSignatureVerifiesAndTamperFails(t *testing.T) {
	id := testIdentity(t)
	body := []byte(`{"model":"claude-opus-5"}`)
	headers, err := id.headersFor(http.MethodPost, "/v1/messages", body, "0.0.209")
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(headers[HeaderSig])
	if err != nil {
		t.Fatal(err)
	}
	pub := id.priv.Public().(ed25519.PublicKey)

	signed := canonical(http.MethodPost, "/v1/messages", headers[HeaderTS], headers[HeaderNonce], body)
	if !ed25519.Verify(pub, []byte(signed), signature) {
		t.Fatal("signature does not verify over the canonical string")
	}
	for name, tampered := range map[string]string{
		"method": canonical(http.MethodGet, "/v1/messages", headers[HeaderTS], headers[HeaderNonce], body),
		"path":   canonical(http.MethodPost, "/v1/messages/count_tokens", headers[HeaderTS], headers[HeaderNonce], body),
		"body":   canonical(http.MethodPost, "/v1/messages", headers[HeaderTS], headers[HeaderNonce], append(body, ' ')),
	} {
		if ed25519.Verify(pub, []byte(tampered), signature) {
			t.Fatalf("signature still verified after tampering with the %s", name)
		}
	}
	if got := len(headers[HeaderNonce]); got != base64.RawURLEncoding.EncodedLen(nonceBytes) {
		t.Fatalf("nonce is %d chars, want %d", got, base64.RawURLEncoding.EncodedLen(nonceBytes))
	}
}

// The signed path is the pathname only: a query string is excluded, a relay_url prefix is not.
func TestSignedPath(t *testing.T) {
	cases := map[string]string{
		"https://relay.mirasim.ai/v1/messages":               "/v1/messages",
		"https://relay.mirasim.ai/api/v1/messages?beta=true": "/api/v1/messages",
		"https://relay.mirasim.ai":                           "/",
		"https://relay.mirasim.ai/v1/messages/count_tokens":  "/v1/messages/count_tokens",
	}
	for raw, want := range cases {
		if got := signedPath(raw); got != want {
			t.Fatalf("signedPath(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Signing is opt-out and fail-open: neither a disabled config nor an account with no
// ticket may touch the plain-token request the caller already built.
func TestSignLeavesUnsignedRequestsAlone(t *testing.T) {
	cfg := config.Default()
	for _, name := range []string{"disabled", "no ticket"} {
		if name == "disabled" {
			cfg.DeviceSigning = false
		} else {
			cfg.DeviceSigning = true
		}
		header := http.Header{"Authorization": []string{"Bearer account-jwt"}}
		start := time.Now()
		if Sign(cfg, header, Request{Token: "account-jwt", Method: http.MethodPost, URL: "https://relay.mirasim.ai/v1/messages"}) {
			t.Fatalf("%s: Sign reported a signed request", name)
		}
		// The first ticket is waited for, but a handshake that cannot even be attempted
		// must not hold the request for the whole window.
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("%s: Sign blocked for %s with no reachable relay", name, elapsed)
		}
		if header.Get("Authorization") != "Bearer account-jwt" || header.Get(HeaderSig) != "" {
			t.Fatalf("%s: Sign modified the headers: %v", name, header)
		}
	}
	Forget("account-jwt")
}
