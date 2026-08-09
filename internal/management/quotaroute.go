package management

import (
	"net/http"
	"strings"
	"time"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/credential"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/hostapi"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/mirofish"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/quota"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/textutil"
)

// handleQuotaRoute feeds the management panel's quota card.
//
// Unlike the console this is behind the host's management middleware, so it carries no
// console token; the caller has already proven it holds the management key.
func handleQuotaRoute(cfg config.Config, req request) ([]byte, error) {
	authIndex := strings.TrimSpace(req.Query.Get("auth_index"))
	if authIndex == "" {
		return errorResponse(http.StatusBadRequest, "auth_index is required")
	}
	stored, err := hostapi.AuthGet(authIndex)
	if err != nil {
		return errorResponse(http.StatusNotFound, err.Error())
	}
	payload, err := credential.Decode(stored.JSON)
	if err != nil {
		return errorResponse(http.StatusBadGateway, err.Error())
	}
	if !payload.IsOurs() {
		return errorResponse(http.StatusBadRequest, "not a "+config.PluginID+" credential")
	}

	access := payload.AccessToken()
	email := textutil.FirstNonEmpty(payload.Email(), mirofish.JWTEmail(access))
	// The panel's refresh button is an explicit user action, so honour it; the per-account
	// minimum interval inside quota.ProbeCached still keeps a rapid reload from spending a
	// request slot each time.
	snapshot, ok := quota.ProbeCached(cfg, req.HostCallbackID, email, access, req.Query.Get("force") != "0")
	if !ok {
		return jsonResponse(http.StatusOK, map[string]any{
			"ok": true, "email": email, "quota": nil, "quota_enabled": cfg.QuotaProbe,
		})
	}
	return jsonResponse(http.StatusOK, map[string]any{
		"ok":            true,
		"email":         email,
		"quota":         snapshot,
		"quota_enabled": cfg.QuotaProbe,
		"now":           time.Now().Unix(),
	})
}
