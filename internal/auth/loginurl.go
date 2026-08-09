package auth

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/routes"
)

// LoginPageURL is what the panel shows and opens after the user clicks Login.
//
// The BaseURL the host passes in points at 127.0.0.1, which is useless to a remote
// browser, so a configured public base URL wins. Falling back to the host value still
// works for a loopback deployment.
func LoginPageURL(cfg config.Config, hostBaseURL, state string) string {
	base := cfg.PublicBaseURL
	if base == "" {
		base = originOf(hostBaseURL)
	}
	return fmt.Sprintf("%s%s%s?state=%s", base, routes.ResourcePrefix, routes.Login, url.QueryEscape(state))
}

func originOf(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}
