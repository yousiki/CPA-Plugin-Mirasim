// Package credential is the shape of a stored Mirasim auth file.
//
// Reads go through Payload, a plain map. That is deliberate rather than lazy: the host
// writes its own metadata keys into the same object, and the suspend/resume path is a
// read-modify-write. Decoding into a struct there would silently drop every key this
// plugin does not know about. Writes use Storage, where the full set of fields is ours.
package credential

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
)

// SuspendedKey is the provider-owned suspension flag inside the auth file.
//
// Suspension is tracked here rather than in the host's own `disabled` metadata key: the
// host rewrites that key from the runtime record on every save (sdk/auth/filestore.go),
// and the path that reloads a credential from disk after a plugin write does not read it
// back (internal/pluginhost/auth_callbacks.go buildAuthFromFileData), so a `disabled` we
// write is reverted within milliseconds. A provider-owned field survives the round trip,
// and auth.Parse turns it back into a real Disabled record.
const SuspendedKey = "suspended"

// disabledKey is the host's own flag. It is written alongside SuspendedKey only to keep
// the file self-consistent for anyone reading it; it is not what makes suspension stick.
const disabledKey = "disabled"

// WeightKey and PriorityKey are the routing knobs inside the auth file. The host reads
// a top-level `weight` on its own when it re-synthesizes the file; `priority` it applies
// only for non-plugin credentials, so auth.Parse turns it into the runtime attribute the
// scheduler reads. Both live in Storage as well: Refresh rebuilds the file from that
// struct, and a key it does not carry would be wiped on the next rotation.
const (
	WeightKey   = "weight"
	PriorityKey = "priority"
)

// Storage is the provider-owned credential payload, persisted as the auth file body and
// merged by the host with its own metadata map.
type Storage struct {
	Type         string `json:"type"`
	Email        string `json:"email"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Suspended    bool   `json:"suspended,omitempty"`
	Weight       int64  `json:"weight,omitempty"`
	Priority     int64  `json:"priority,omitempty"`
}

// Payload is a stored auth file as read back from the host, with unknown keys intact.
type Payload map[string]any

func Decode(raw []byte) (Payload, error) {
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (p Payload) Encode() ([]byte, error) {
	return json.Marshal(map[string]any(p))
}

// String reads a trimmed string field, or "" when it is absent or not a string.
func (p Payload) String(key string) string {
	if p == nil {
		return ""
	}
	value, _ := p[key].(string)
	return strings.TrimSpace(value)
}

// Int reads an integer field. JSON numbers decode as float64; a stray string form is
// accepted too because the host's own priority handling does the same.
func (p Payload) Int(key string) int64 {
	if p == nil {
		return 0
	}
	switch value := p[key].(type) {
	case float64:
		return int64(value)
	case int64:
		// What SetInt wrote before the payload has been re-encoded.
		return value
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

// SetInt writes an integer field in place. Zero deletes the key: absent is what an
// untouched credential looks like, and the host itself treats priority 0 as unset.
func (p Payload) SetInt(key string, value int64) {
	if p == nil {
		return
	}
	if value == 0 {
		delete(p, key)
		return
	}
	p[key] = value
}

func (p Payload) Email() string        { return p.String("email") }
func (p Payload) AccessToken() string  { return p.String("access_token") }
func (p Payload) RefreshToken() string { return p.String("refresh_token") }
func (p Payload) LastRefresh() string  { return p.String("last_refresh") }

// IsOurs reports whether this file belongs to this plugin's provider.
func (p Payload) IsOurs() bool {
	return strings.EqualFold(p.String("type"), config.PluginID)
}

func (p Payload) Suspended() bool {
	if p == nil {
		return false
	}
	suspended, _ := p[SuspendedKey].(bool)
	return suspended
}

// SetSuspended toggles suspension in place.
//
// Resuming deletes both keys rather than writing false: an absent key is what a
// never-suspended credential looks like, so this keeps a resumed file identical to a
// freshly logged-in one.
func (p Payload) SetSuspended(suspended bool) {
	if p == nil {
		return
	}
	if suspended {
		p[SuspendedKey] = true
		p[disabledKey] = true
		return
	}
	delete(p, SuspendedKey)
	delete(p, disabledKey)
}
