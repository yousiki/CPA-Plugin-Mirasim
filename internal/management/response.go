package management

import (
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/rpc"
)

// request is what the host hands management.handle: its own request shape plus the
// callback id that ties an outbound HTTP call back to this request.
type request struct {
	pluginapi.ManagementRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func jsonResponse(status int, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return rpc.OK(pluginapi.ManagementResponse{
		StatusCode: status,
		Headers: http.Header{
			"Content-Type":  []string{"application/json; charset=utf-8"},
			"Cache-Control": []string{"no-store"},
		},
		Body: payload,
	})
}

func htmlResponse(status int, html string) ([]byte, error) {
	return rpc.OK(pluginapi.ManagementResponse{
		StatusCode: status,
		Headers: http.Header{
			"Content-Type":  []string{"text/html; charset=utf-8"},
			"Cache-Control": []string{"no-store"},
			// The pages are self-contained; no external origins are needed.
			//
			// script-src allows inline code but no external source, which is what an
			// HTML-rewriting CDN in front of this proxy collides with: Cloudflare Rocket
			// Loader retypes every inline <script> to a non-executable MIME and injects
			// /cdn-cgi/scripts/…/rocket-loader.min.js to run them later — and that
			// injected script is exactly what this policy blocks. The net effect is a
			// page with no JavaScript at all, frozen on its initial markup. The
			// data-cfasync="false" on both inline scripts opts them out of the rewrite,
			// so they keep executing directly and the blocked loader is harmless.
			"Content-Security-Policy": []string{"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'"},
			"Referrer-Policy":         []string{"no-referrer"},
		},
		Body: []byte(html),
	})
}

func errorResponse(status int, message string) ([]byte, error) {
	return jsonResponse(status, map[string]any{"ok": false, "error": message})
}
