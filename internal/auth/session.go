package auth

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"sync"
	"time"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/mirofish"
)

// sessionTTL bounds how long a started login stays resumable. The panel polls every 3s,
// so this only needs to cover a human reading an email.
const sessionTTL = 10 * time.Minute

// MaxVerifyAttempts caps guesses against one state. The code is 6 digits and the login
// routes are unauthenticated, so the state alone must not be a free oracle.
const MaxVerifyAttempts = 6

// Session is one in-flight login. Every field is guarded by sessionMu; the accessors
// below are the only way to touch them.
type Session struct {
	state     string
	expiresAt time.Time
	email     string
	codeSent  bool
	attempts  int
	tokens    *mirofish.TokenPair
	failure   string
}

var (
	sessionMu sync.Mutex
	sessions  = make(map[string]*Session)
)

// Shutdown drops every in-flight login. Called on plugin.shutdown so a reload does not
// leave resumable states behind.
func Shutdown() {
	sessionMu.Lock()
	sessions = make(map[string]*Session)
	sessionMu.Unlock()
}

// pruneLocked drops expired sessions. Callers must hold sessionMu.
func pruneLocked(now time.Time) {
	for state, session := range sessions {
		if now.After(session.expiresAt) {
			delete(sessions, state)
		}
	}
}

// LookupSession returns the live session for a state, or nil when it is unknown or
// expired.
func LookupSession(state string) *Session {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	pruneLocked(time.Now())
	return sessions[strings.TrimSpace(state)]
}

func newSession() (*Session, error) {
	state, err := newState()
	if err != nil {
		return nil, err
	}
	session := &Session{state: state, expiresAt: time.Now().Add(sessionTTL)}

	sessionMu.Lock()
	pruneLocked(time.Now())
	sessions[state] = session
	sessionMu.Unlock()
	return session, nil
}

// newState returns an opaque state token.
//
// The charset is constrained by the host: ValidateOAuthState rejects anything outside
// [A-Za-z0-9._-], which raw base64url satisfies.
func newState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// MarkCodeSent records that a code went out to email, resetting the attempt counter so a
// resend gives the operator a fresh budget.
func (s *Session) MarkCodeSent(email string) {
	sessionMu.Lock()
	s.email = email
	s.codeSent = true
	s.attempts = 0
	sessionMu.Unlock()
}

// NextAttempt books one verification attempt and reports the state needed to judge it.
func (s *Session) NextAttempt() (email string, codeSent bool, attempts int) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	s.attempts++
	return s.email, s.codeSent, s.attempts
}

// Fail marks the login as unrecoverable, which the panel's poll surfaces as an error.
func (s *Session) Fail(reason string) {
	sessionMu.Lock()
	s.failure = reason
	sessionMu.Unlock()
}

// Complete hands the verified token pair to the poll that is waiting for it.
func (s *Session) Complete(pair mirofish.TokenPair) {
	sessionMu.Lock()
	s.tokens = &pair
	sessionMu.Unlock()
}

// pollResult is what one poll of a session sees.
type pollResult struct {
	failure string
	pending bool
	email   string
	tokens  mirofish.TokenPair
}

// poll reads the session's terminal state and, on success, consumes it: the credential is
// handed back exactly once.
func (s *Session) poll() pollResult {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	switch {
	case s.failure != "":
		return pollResult{failure: s.failure}
	case s.tokens == nil:
		return pollResult{pending: true}
	}
	result := pollResult{email: s.email, tokens: *s.tokens}
	delete(sessions, s.state)
	return result
}
