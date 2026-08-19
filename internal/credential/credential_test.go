package credential

import (
	"encoding/json"
	"testing"
)

// The read-modify-write in suspend/resume is why Payload is a map: the host merges its own
// metadata into the same object, and anything this plugin does not know about still has to
// come back out of Encode untouched.
func TestSetSuspendedPreservesUnknownKeys(t *testing.T) {
	raw := []byte(`{
		"type": "mirasim",
		"email": "someone@example.com",
		"access_token": "a",
		"refresh_token": "r",
		"last_refresh": "2026-08-09T00:00:00Z",
		"some_future_host_key": {"nested": [1, 2]}
	}`)

	payload, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	payload.SetSuspended(true)
	encoded, err := payload.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var round map[string]any
	if err = json.Unmarshal(encoded, &round); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	if round["some_future_host_key"] == nil {
		t.Error("an unknown host key was dropped by the round trip")
	}
	if round["last_refresh"] != "2026-08-09T00:00:00Z" {
		t.Errorf("last_refresh = %v, want it carried over", round["last_refresh"])
	}
	if round[SuspendedKey] != true || round["disabled"] != true {
		t.Errorf("suspended=%v disabled=%v, want both true", round[SuspendedKey], round["disabled"])
	}
}

// Resuming has to leave the file looking like one that was never suspended, so both flags
// are deleted rather than written as false.
func TestSetSuspendedFalseDeletesBothFlags(t *testing.T) {
	payload, err := Decode([]byte(`{"type":"mirasim","suspended":true,"disabled":true,"refresh_token":"r"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	payload.SetSuspended(false)

	if _, present := payload[SuspendedKey]; present {
		t.Error("suspended is still present after resume")
	}
	if _, present := payload["disabled"]; present {
		t.Error("disabled is still present after resume")
	}
	if payload.Suspended() {
		t.Error("Suspended() is still true after resume")
	}
	if payload.RefreshToken() != "r" {
		t.Error("resume dropped the refresh token, which would force a re-login")
	}
}

func TestAccessors(t *testing.T) {
	payload, err := Decode([]byte(`{
		"type": "MIRASIM",
		"email": "  someone@example.com  ",
		"access_token": "access",
		"refresh_token": "refresh",
		"suspended": false
	}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := payload.Email(); got != "someone@example.com" {
		t.Errorf("Email() = %q, want it trimmed", got)
	}
	if payload.AccessToken() != "access" || payload.RefreshToken() != "refresh" {
		t.Errorf("token accessors returned %q / %q", payload.AccessToken(), payload.RefreshToken())
	}
	// The host does not promise a particular case for the type field.
	if !payload.IsOurs() {
		t.Error("IsOurs() = false for an upper-case type")
	}
	if payload.Suspended() {
		t.Error("Suspended() = true for suspended:false")
	}
}

func TestAccessorsTolerateWrongTypes(t *testing.T) {
	// A hand-edited or host-rewritten file must not panic the console.
	payload, err := Decode([]byte(`{"type":123,"email":null,"suspended":"yes"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if payload.Email() != "" || payload.String("type") != "" {
		t.Error("a non-string field should read as empty")
	}
	if payload.Suspended() {
		t.Error("a non-bool suspended should read as false")
	}
	if payload.IsOurs() {
		t.Error("IsOurs() = true for a non-string type")
	}
}

func TestNilPayloadIsSafe(t *testing.T) {
	var payload Payload
	if payload.Email() != "" || payload.Suspended() || payload.IsOurs() {
		t.Error("a nil payload should read as empty")
	}
	payload.SetSuspended(true) // must not panic
}

func TestIntAndSetInt(t *testing.T) {
	payload, err := Decode([]byte(`{"weight":5,"priority":"-2","note":"keep me"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if payload.Int(WeightKey) != 5 {
		t.Errorf("Int(weight) = %d, want 5", payload.Int(WeightKey))
	}
	// The host's own priority handling accepts a string form, so Int does too.
	if payload.Int(PriorityKey) != -2 {
		t.Errorf("Int(priority) = %d, want -2", payload.Int(PriorityKey))
	}
	if payload.Int("note") != 0 || payload.Int("missing") != 0 {
		t.Error("a non-numeric or absent field should read as 0")
	}

	payload.SetInt(WeightKey, 7)
	if payload.Int(WeightKey) != 7 {
		t.Errorf("Int after SetInt = %d, want 7", payload.Int(WeightKey))
	}
	// Zero clears: absent is what an untouched credential looks like.
	payload.SetInt(WeightKey, 0)
	payload.SetInt(PriorityKey, 0)
	if _, ok := payload[WeightKey]; ok {
		t.Error("SetInt(0) should delete the weight key")
	}
	if _, ok := payload[PriorityKey]; ok {
		t.Error("SetInt(0) should delete the priority key")
	}
	if payload["note"] != "keep me" {
		t.Error("SetInt touched an unrelated key")
	}

	var nilPayload Payload
	nilPayload.SetInt(WeightKey, 1) // must not panic
	if nilPayload.Int(WeightKey) != 0 {
		t.Error("a nil payload should read as 0")
	}
}
