package relaysig

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/hostapi"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/mirofish"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/textutil"
)

// Timings copied from the desktop client, which is what the relay's own rate limiting was
// tuned against.
const (
	// renewLead re-mints this long before the ticket expires.
	renewLead = 120 * time.Second
	// mintCooldown is the floor between two mint attempts for one account.
	mintCooldown = 30 * time.Second
	// refusedFloor is the floor between two 401-triggered re-mints.
	refusedFloor = 30 * time.Second
	// unsupportedBackoff is how long the relay is left alone after it says it does not
	// speak the protocol.
	unsupportedBackoff = 15 * time.Minute
	// defaultTicketTTL is used when the relay returns a ticket with no expiry.
	defaultTicketTTL = 10 * time.Minute
	// firstTicketWait is how long a request waits for the very first ticket, and
	// firstTicketPoll how often it looks. Bounded well under the HTTP timeout so a slow
	// handshake delays a request rather than failing it.
	firstTicketWait = 5 * time.Second
	firstTicketPoll = 25 * time.Millisecond
)

// refusalsBeforeStandDown is a deliberate divergence from the desktop client.
//
// Desktop re-mints on every 401 forever. If the relay accepted a mint but then rejected
// our per-request signature - a clock skew, a path we canonicalise differently - that
// loop fails every single request. Standing down after a few refusals turns a total
// outage into three lost requests per backoff window.
//
// Note the fallback is only a real fallback on a relay that still accepts unsigned
// requests; relay.mirasim.ai answers those 401 client_outdated. It is kept because a
// signature the relay refuses is not better than a token it refuses, and because the
// backoff is what stops a refused signature from becoming a mint loop.
const refusalsBeforeStandDown = 3

// session is one account's device-session state.
type session struct {
	mu               sync.Mutex
	ticket           string
	expiresAt        time.Time
	mintedFor        string
	nextAttempt      time.Time
	refusedNotBefore time.Time
	standDownUntil   time.Time
	refusals         int
	minting          bool
}

var (
	sessionsMu sync.Mutex
	sessions   = make(map[string]*session)
)

// accountKey is a stable per-account key. The JWT subject survives the hourly token
// rotation, so a refreshed credential keeps its device session instead of leaking a map
// entry per token.
func accountKey(token string) string {
	if claims := mirofish.DecodeJWT(token); claims != nil {
		if sub := strings.TrimSpace(claims.Sub); sub != "" {
			return sub
		}
	}
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

func sessionFor(token string) *session {
	key := accountKey(token)
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	found, ok := sessions[key]
	if !ok {
		found = &session{}
		sessions[key] = found
	}
	return found
}

// Request is one outbound relay call to be signed.
type Request struct {
	// Token is the account access JWT the caller would otherwise send.
	Token string
	// Method and URL are the request as it will be sent; only URL's path is signed.
	Method string
	URL    string
	// Body is the exact bytes on the wire. Empty for a GET.
	Body []byte
}

// Sign upgrades a prepared relay request to a signed one, in place.
//
// When a device session is live the Authorization header is rewritten to carry the ticket
// instead of the account JWT and the signature headers are added; the return value says
// whether that happened. When it is not - signing disabled, or the relay said it does not
// speak the protocol - header is left alone and the plain-token request goes out as before.
//
// An ageing ticket is renewed in the background, but the *first* one is waited for: the
// relay answers an unsigned request with 401 client_outdated ("this request must be
// signed"), so sending one while the initial mint is still in flight is a guaranteed
// failure rather than a graceful fallback. The wait is bounded, so a relay that hangs on
// the handshake still gets the plain token instead of a stalled request.
func Sign(cfg config.Config, header http.Header, req Request) bool {
	if !cfg.DeviceSigning || strings.TrimSpace(req.Token) == "" {
		return false
	}
	current := sessionFor(req.Token)
	ticket := current.credential(cfg, req.Token)
	if ticket == "" {
		ticket = current.awaitTicket(cfg, req.Token)
	}
	if ticket == "" {
		return false
	}
	id, err := loadIdentity(cfg)
	if err != nil {
		return false
	}
	signature, err := id.headersFor(req.Method, signedPath(req.URL), req.Body, cfg.ClientVersion)
	if err != nil {
		return false
	}
	// The ticket replaces the account JWT; the relay authenticates the device session
	// from here on.
	header.Set("Authorization", "Bearer "+ticket)
	for name, value := range signature {
		header.Set(name, value)
	}
	return true
}

// Refused is called when the relay answers 401 to a request that carried a ticket. The
// ticket is dropped and a re-mint scheduled; after too many in a row the account stands
// down to the plain token for a backoff window.
func Refused(cfg config.Config, token string) {
	if !cfg.DeviceSigning || strings.TrimSpace(token) == "" {
		return
	}
	current := sessionFor(token)
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.ticket == "" {
		return
	}
	now := time.Now()
	if now.Before(current.refusedNotBefore) {
		return
	}
	current.refusedNotBefore = now.Add(refusedFloor)
	current.ticket = ""
	current.mintedFor = ""
	current.nextAttempt = time.Time{}
	current.refusals++
	if current.refusals >= refusalsBeforeStandDown {
		current.refusals = 0
		current.standDownUntil = now.Add(unsupportedBackoff)
		hostapi.Log("warn", config.PluginID+": relay kept rejecting signed requests; "+
			"falling back to the plain access token for "+unsupportedBackoff.String())
		return
	}
	hostapi.Log("info", config.PluginID+": relay refused the signed request; re-minting the device session")
	current.kick(cfg, token)
}

// credential returns the ticket to use now, kicking a background mint whenever one is
// due. An empty return means "send the plain token, unsigned".
func (s *session) credential(cfg config.Config, token string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Before(s.standDownUntil) {
		return ""
	}
	if s.ticket == "" {
		s.kick(cfg, token)
		return ""
	}
	if !now.Before(s.expiresAt) {
		s.ticket = ""
		s.mintedFor = ""
		s.kick(cfg, token)
		return ""
	}
	// Still valid: renew ahead of expiry, and after a token rotation, but keep using the
	// ticket in hand until the replacement lands.
	if !now.Before(s.expiresAt.Add(-renewLead)) || s.mintedFor != token {
		s.kick(cfg, token)
	}
	return s.ticket
}

// awaitTicket blocks up to firstTicketWait for the first ticket of a session.
//
// It returns "" immediately when the relay has been marked as not speaking the protocol,
// which is the one case where an unsigned request is still the right thing to send.
//
// ponytail: polls credential rather than waiting on a condition variable; the mint is a
// single ~200ms request, so a 25ms tick is cheaper than the plumbing it would replace.
func (s *session) awaitTicket(cfg config.Config, token string) string {
	deadline := time.Now().Add(firstTicketWait)
	for {
		s.mu.Lock()
		now := time.Now()
		// A mint that has finished without leaving a ticket has failed. Waiting out the
		// rest of the deadline would only delay the plain-token attempt.
		done := !s.minting && s.ticket == "" && now.Before(s.nextAttempt)
		giveUp := done || now.Before(s.standDownUntil) || now.After(deadline)
		s.mu.Unlock()
		if giveUp {
			return ""
		}
		time.Sleep(firstTicketPoll)
		if ticket := s.credential(cfg, token); ticket != "" {
			return ticket
		}
	}
}

// kick starts a mint if one is due. Callers must hold s.mu.
func (s *session) kick(cfg config.Config, token string) {
	now := time.Now()
	if s.minting || now.Before(s.nextAttempt) || now.Before(s.standDownUntil) {
		return
	}
	if s.ticket != "" && s.mintedFor == token && now.Before(s.expiresAt.Add(-renewLead)) {
		return
	}
	s.minting = true
	s.nextAttempt = now.Add(mintCooldown)
	go s.mint(cfg, token)
}

// mintResponse is the /v1/device/session reply. expiresIn is seconds; expiresAt is a Unix
// timestamp in seconds. The desktop client accepts either and falls back to 10 minutes.
type mintResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn *int64 `json:"expiresIn"`
	ExpiresAt *int64 `json:"expiresAt"`
}

// mintBody is the handshake payload. The field order is the byte order that gets hashed,
// so it must not be reshuffled without re-signing.
type mintBody struct {
	PublicKey string `json:"publicKey"`
	DeviceID  string `json:"deviceId"`
}

// mint trades the device key for a ticket. It runs detached from any request, so every
// exit path has to clear the minting flag and nothing here may panic out.
func (s *session) mint(cfg config.Config, token string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			hostapi.Log("warn", config.PluginID+": device session mint panicked")
		}
		s.mu.Lock()
		s.minting = false
		s.mu.Unlock()
	}()

	id, err := loadIdentity(cfg)
	if err != nil {
		hostapi.Log("warn", config.PluginID+": no device key available ("+err.Error()+
			"); using the plain access token")
		return
	}
	body, err := json.Marshal(mintBody{PublicKey: id.publicKeyB64, DeviceID: id.deviceID})
	if err != nil {
		return
	}
	target := cfg.RelayURL + SessionPath
	header := http.Header{
		"Content-Type":  []string{"application/json"},
		"Authorization": []string{"Bearer " + token},
	}
	// The handshake signs itself: the device key is what is being introduced, and the
	// account JWT is what authorises the introduction.
	signature, err := id.headersFor(http.MethodPost, signedPath(target), body, cfg.ClientVersion)
	if err != nil {
		return
	}
	for name, value := range signature {
		header.Set(name, value)
	}

	// No host callback id: the mint is not part of any one request's lifecycle.
	resp, err := hostapi.HTTPDo("", http.MethodPost, target, header, body)
	if err != nil {
		hostapi.Log("warn", config.PluginID+": device session mint failed ("+err.Error()+
			"); using the plain access token")
		return
	}

	now := time.Now()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented:
		// This relay does not speak mrs-sig-v1. Stop asking for a while.
		s.mu.Lock()
		s.standDownUntil = now.Add(unsupportedBackoff)
		s.mu.Unlock()
		hostapi.Log("info", config.PluginID+": relay does not support device signing; using the plain access token")
		return
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		hostapi.Log("warn", config.PluginID+": device session mint rejected (HTTP "+
			strconv.Itoa(resp.StatusCode)+": "+textutil.Truncate(strings.TrimSpace(string(resp.Body)), 200)+
			"); using the plain access token")
		return
	}

	var parsed mintResponse
	if err = json.Unmarshal(resp.Body, &parsed); err != nil || strings.TrimSpace(parsed.Ticket) == "" {
		hostapi.Log("warn", config.PluginID+": device session mint returned no ticket; using the plain access token")
		return
	}

	expiresAt := now.Add(defaultTicketTTL)
	switch {
	case parsed.ExpiresIn != nil && *parsed.ExpiresIn > 0:
		expiresAt = now.Add(time.Duration(*parsed.ExpiresIn) * time.Second)
	case parsed.ExpiresAt != nil && *parsed.ExpiresAt > 0:
		expiresAt = time.Unix(*parsed.ExpiresAt, 0)
	}

	s.mu.Lock()
	s.ticket = parsed.Ticket
	s.expiresAt = expiresAt
	s.mintedFor = token
	s.refusals = 0
	s.standDownUntil = time.Time{}
	s.mu.Unlock()
	hostapi.Log("info", config.PluginID+": device session established (device "+id.deviceID+")")
}

// Forget drops an account's device session, for a credential that is going away.
func Forget(token string) {
	if strings.TrimSpace(token) == "" {
		return
	}
	key := accountKey(token)
	sessionsMu.Lock()
	delete(sessions, key)
	sessionsMu.Unlock()
}
