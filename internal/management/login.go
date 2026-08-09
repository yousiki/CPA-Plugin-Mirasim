package management

import (
	"net/http"
	"strings"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/auth"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/mirofish"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/textutil"
)

func handleLoginCode(cfg config.Config, req request) ([]byte, error) {
	session := auth.LookupSession(req.Query.Get("state"))
	if session == nil {
		return errorResponse(http.StatusNotFound, "login session expired; start again from the panel")
	}
	email := strings.ToLower(strings.TrimSpace(req.Query.Get("email")))
	if email == "" || !strings.Contains(email, "@") {
		return errorResponse(http.StatusBadRequest, "a valid email is required")
	}

	devCode, err := mirofish.RequestCode(req.HostCallbackID, cfg, email)
	if err != nil {
		return errorResponse(http.StatusBadGateway, err.Error())
	}
	session.MarkCodeSent(email)

	out := map[string]any{"ok": true, "email": email}
	if devCode != "" {
		// Staging builds echo the code back; surfacing it keeps staging testable.
		out["dev_code"] = devCode
	}
	return jsonResponse(http.StatusOK, out)
}

func handleLoginVerify(cfg config.Config, req request) ([]byte, error) {
	session := auth.LookupSession(req.Query.Get("state"))
	if session == nil {
		return errorResponse(http.StatusNotFound, "login session expired; start again from the panel")
	}
	code := strings.TrimSpace(req.Query.Get("code"))
	if code == "" {
		return errorResponse(http.StatusBadRequest, "verification code is required")
	}

	email, sent, attempts := session.NextAttempt()
	if !sent || email == "" {
		return errorResponse(http.StatusBadRequest, "request a code first")
	}
	if attempts > auth.MaxVerifyAttempts {
		session.Fail("too many verification attempts")
		return errorResponse(http.StatusTooManyRequests, "too many verification attempts; start again")
	}

	pair, err := mirofish.VerifyCode(req.HostCallbackID, cfg, email, code)
	if err != nil {
		return errorResponse(http.StatusBadGateway, err.Error())
	}
	session.Complete(pair)

	// The panel's poll turns this into a saved credential within a few seconds.
	return jsonResponse(http.StatusOK, map[string]any{
		"ok":    true,
		"email": textutil.FirstNonEmpty(mirofish.JWTEmail(pair.Access), email),
	})
}
