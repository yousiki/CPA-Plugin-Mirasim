// Package relaysig implements the relay's `mrs-sig-v1` device-signing protocol.
//
// The relay accepts an optional per-device signature. A client proves possession of an
// Ed25519 device key once at POST /v1/device/session, gets back a short-lived ticket,
// and from then on sends the ticket in place of the account access JWT plus a signature
// over every request. The scheme was read out of the desktop client (Mirasim 0.0.209,
// resources/server.cjs) and reproduced here; both relay.mirasim.ai and the retired
// mirasim-relay.mirofish.ai answer /v1/device/session with 401 rather than 404, so the
// endpoint is live.
//
// The string that gets signed is six fields joined with \n:
//
//	mrs-sig-v1
//	POST                                    <- method, upper case
//	/v1/messages                            <- path only, no query string
//	1787300640023                           <- Date.now(), MILLIseconds
//	dLeEwhegSUjRbIiP                        <- 12 random bytes, base64url
//	bfc4014b...                             <- sha256 of the body, lower hex
//
// carried as x-mirasim-sig (base64url Ed25519) alongside x-mirasim-ts, -nonce, -device
// and -client. No request header is covered by the signature, and neither is the query
// string.
//
// Signing is negotiated, never assumed: without a device session the request goes out
// unsigned with the plain access JWT, which is what every relay deployment accepts.
package relaysig

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/hostapi"
)

// Scheme is the literal first line of every signed request.
const Scheme = "mrs-sig-v1"

// SessionPath is where a device key is traded for a ticket.
const SessionPath = "/v1/device/session"

// The signature headers, spelled exactly as the desktop client sends them.
const (
	HeaderDevice = "x-mirasim-device"
	HeaderTS     = "x-mirasim-ts"
	HeaderNonce  = "x-mirasim-nonce"
	HeaderSig    = "x-mirasim-sig"
	HeaderClient = "x-mirasim-client"
)

// nonceBytes is what the desktop client draws per request: 12 bytes, 16 base64url chars.
const nonceBytes = 12

// deviceIDLength is how far the desktop client truncates the hash of its public key.
const deviceIDLength = 22

// identity is a device key plus the two values derived from it.
type identity struct {
	deviceID     string
	publicKeyB64 string
	priv         ed25519.PrivateKey
}

// canonical builds the string that gets signed.
//
// path must be the path the relay will see - no query string, and including any path
// prefix carried by relay_url - and body must be the exact bytes that go on the wire.
func canonical(method, path, ts, nonce string, body []byte) string {
	digest := sha256.Sum256(body)
	return strings.Join([]string{
		Scheme,
		strings.ToUpper(method),
		path,
		ts,
		nonce,
		fmt.Sprintf("%x", digest),
	}, "\n")
}

// headersFor signs one request and returns the headers to add.
func (id *identity) headersFor(method, path string, body []byte, clientVersion string) (map[string]string, error) {
	nonce := make([]byte, nonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	encodedNonce := base64.RawURLEncoding.EncodeToString(nonce)
	signature := ed25519.Sign(id.priv, []byte(canonical(method, path, ts, encodedNonce, body)))

	out := map[string]string{
		HeaderDevice: id.deviceID,
		HeaderTS:     ts,
		HeaderNonce:  encodedNonce,
		HeaderSig:    base64.RawURLEncoding.EncodeToString(signature),
	}
	if clientVersion != "" {
		out[HeaderClient] = clientVersion
	}
	return out, nil
}

// newIdentity derives the device identity from a private key.
//
// deviceId hashes the *base64 text* of the SPKI DER, not the DER bytes, and keeps the
// first 22 base64url characters - matching the desktop client exactly, because the relay
// stores whatever the first /v1/device/session presented.
func newIdentity(priv ed25519.PrivateKey) (*identity, error) {
	der, err := x509.MarshalPKIXPublicKey(priv.Public())
	if err != nil {
		return nil, err
	}
	publicKeyB64 := base64.StdEncoding.EncodeToString(der)
	sum := sha256.Sum256([]byte(publicKeyB64))
	return &identity{
		deviceID:     base64.RawURLEncoding.EncodeToString(sum[:])[:deviceIDLength],
		publicKeyB64: publicKeyB64,
		priv:         priv,
	}, nil
}

// -- device key persistence ---------------------------------------------------

// deviceKeyFile is the on-disk shape. The field name mirrors the desktop client's
// `device.privateKey` so the value is recognisable, but the file is our own: writing into
// the desktop's ~/.mirasim/auth.json would fight with a running desktop client.
type deviceKeyFile struct {
	PrivateKey string `json:"privateKey"`
}

var (
	identityMu   sync.Mutex
	cachedID     *identity
	warnedNoDisk bool
)

// deviceKeyPath is where the key is kept, honouring the config override.
func deviceKeyPath(cfg config.Config) string {
	if path := strings.TrimSpace(cfg.DeviceKeyPath); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".mirasim", "cpa-plugin-device.json")
}

// loadIdentity returns the device identity, generating and persisting a key on first use.
//
// A key that cannot be written to disk is still used for this process: the relay simply
// sees a new device after a restart, which costs one extra mint and nothing else. That
// keeps signing working in a container with no writable home.
func loadIdentity(cfg config.Config) (*identity, error) {
	identityMu.Lock()
	defer identityMu.Unlock()
	if cachedID != nil {
		return cachedID, nil
	}

	path := deviceKeyPath(cfg)
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			var file deviceKeyFile
			if err = json.Unmarshal(raw, &file); err == nil {
				if id, errParse := identityFromPEM(file.PrivateKey); errParse == nil {
					cachedID = id
					return cachedID, nil
				}
			}
		}
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	id, err := newIdentity(priv)
	if err != nil {
		return nil, err
	}
	if path != "" {
		if err = writeDeviceKey(path, priv); err != nil && !warnedNoDisk {
			warnedNoDisk = true
			hostapi.Log("warn", config.PluginID+": device key not persisted ("+err.Error()+
				"); the relay will see a new device after every restart")
		}
	}
	cachedID = id
	return cachedID, nil
}

func identityFromPEM(pemText string) (*identity, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, fmt.Errorf("device key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("device key is %T, want ed25519", parsed)
	}
	return newIdentity(priv)
}

func writeDeviceKey(path string, priv ed25519.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	body, err := json.Marshal(deviceKeyFile{
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	})
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Written through a temp file so a crash mid-write cannot leave a truncated key that
	// would be discarded on the next start.
	temp := path + ".tmp"
	if err = os.WriteFile(temp, append(body, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// -- helpers ------------------------------------------------------------------

// signedPath extracts the path the relay will see from a full request URL. A relay_url
// carrying its own path prefix is covered, because the prefix is part of the path.
func signedPath(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Path == "" {
		return "/"
	}
	return parsed.Path
}
