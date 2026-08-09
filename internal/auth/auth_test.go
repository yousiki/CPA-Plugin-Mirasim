package auth

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
)

func TestFileName(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  string
	}{
		{name: "ordinary address", email: "someone@example.com", want: "mirasim-someone@example.com.json"},
		{name: "empty", email: "  ", want: "mirasim-unknown.json"},
		// The host joins this onto auth-dir without sanitising, so nothing may escape it.
		{name: "path separators", email: "a/b\\c@example.com", want: "mirasim-a_b_c@example.com.json"},
		{name: "parent traversal", email: "../../etc/passwd", want: "mirasim-____etc_passwd.json"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := FileName(test.email)
			if got != test.want {
				t.Errorf("FileName(%q) = %q, want %q", test.email, got, test.want)
			}
		})
	}
}

// The host joins the returned name onto auth-dir with no further sanitising, so whatever an
// operator types as an email has to come out as a plain filename inside that directory.
func TestFileNameNeverEscapesTheAuthDir(t *testing.T) {
	const dir = "/cpa/auths"

	for _, email := range []string{
		"../../etc/passwd", "..\\..\\win", "a..b@example.com", "..", ".", "./.",
		"/absolute@example.com", "sub/dir/x@example.com", "nul\x00byte@example.com",
	} {
		got := FileName(email)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("FileName(%q) = %q, contains a path separator", email, got)
		}
		if resolved := filepath.Join(dir, got); filepath.Dir(resolved) != dir {
			t.Errorf("FileName(%q) = %q, resolves to %q outside %q", email, got, resolved, dir)
		}
		if !strings.HasPrefix(got, config.PluginID+"-") || !strings.HasSuffix(got, ".json") {
			t.Errorf("FileName(%q) = %q, want the %s- prefix and .json suffix", email, got, config.PluginID)
		}
	}
}

func TestNextRefreshAfterNeverParksPastExpiry(t *testing.T) {
	cfg := config.Default()

	// No expiry to read: the configured interval is the whole answer.
	floor := NextRefreshAfter(cfg, "not-a-jwt")
	if got := time.Until(floor).Round(time.Second); got != time.Duration(cfg.RefreshIntervalSeconds)*time.Second {
		t.Errorf("without an expiry: next refresh in %v, want %ds", got, cfg.RefreshIntervalSeconds)
	}

	// A token expiring sooner than the interval must pull the refresh forward, or it dies
	// before the host ever looks at it again.
	soon := time.Now().Add(3 * time.Minute)
	next := NextRefreshAfter(cfg, jwtExpiring(t, soon))
	if !next.Before(soon) {
		t.Errorf("next refresh %v is not before the token expiry %v", next, soon)
	}
}

// jwtExpiring builds an unsigned JWT whose payload carries only an exp claim, which is all
// NextRefreshAfter reads.
func jwtExpiring(t *testing.T, at time.Time) string {
	t.Helper()
	payload := fmt.Sprintf(`{"exp":%d}`, at.Unix())
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
}
