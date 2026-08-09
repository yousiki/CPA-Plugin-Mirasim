package management

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/routes"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/rpc"
)

// call drives Handle the way the host does: a JSON request in, an envelope out.
func call(t *testing.T, path string, query url.Values) pluginapi.ManagementResponse {
	t.Helper()
	raw, err := json.Marshal(request{
		ManagementRequest: pluginapi.ManagementRequest{
			Method: http.MethodGet,
			Path:   path,
			Query:  query,
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	out, err := Handle(raw)
	if err != nil {
		t.Fatalf("Handle(%s): %v", path, err)
	}
	var env rpc.Envelope
	if err = json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("envelope not ok: %+v", env.Error)
	}
	var resp pluginapi.ManagementResponse
	if err = json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("decode management response: %v", err)
	}
	return resp
}

func withConfig(t *testing.T, cfg config.Config) {
	t.Helper()
	previous := config.Loaded()
	config.Store(cfg)
	t.Cleanup(func() { config.Store(previous) })
}

func TestHandleUnknownRoute(t *testing.T) {
	withConfig(t, config.Default())

	resp := call(t, routes.ResourcePrefix+"/nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// The shell holds no account data and the panel's iframe cannot supply a token, so it must
// render even when the caller has none - otherwise the operator sees a raw 403 body.
func TestStatusShellNeedsNoToken(t *testing.T) {
	cfg := config.Default()
	cfg.ConsoleToken = "s3cret"
	withConfig(t, cfg)

	resp := call(t, routes.ResourcePrefix+routes.Status, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Headers.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want HTML", got)
	}
	if strings.Contains(string(resp.Body), "s3cret") {
		t.Error("the shell leaked the console token into the page")
	}
}

func TestConsoleDataRejectsAWrongToken(t *testing.T) {
	cfg := config.Default()
	cfg.ConsoleToken = "s3cret"
	withConfig(t, cfg)

	for _, name := range []string{routes.StatusData, routes.StatusAction} {
		resp := call(t, routes.ResourcePrefix+name, url.Values{"token": {"wrong"}})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, resp.StatusCode)
		}
		if !strings.Contains(string(resp.Body), "invalid console token") {
			t.Errorf("%s: body = %s", name, resp.Body)
		}
	}
}

func TestConsoleDataRejectsAMissingToken(t *testing.T) {
	cfg := config.Default()
	cfg.ConsoleToken = "s3cret"
	withConfig(t, cfg)

	resp := call(t, routes.ResourcePrefix+routes.StatusData, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// With no token configured the console stays shut rather than exposing credential state on
// a route the host serves without authentication.
func TestConsoleDataClosedWithoutAConfiguredToken(t *testing.T) {
	cfg := config.Default()
	cfg.ConsoleToken = ""
	withConfig(t, cfg)

	resp := call(t, routes.ResourcePrefix+routes.StatusData, url.Values{"token": {"anything"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "console is disabled") {
		t.Errorf("body = %s, want the disabled hint", resp.Body)
	}
}

func TestStatusActionRejectsAnUnknownOp(t *testing.T) {
	cfg := config.Default()
	cfg.ConsoleToken = "s3cret"
	withConfig(t, cfg)

	// A valid token gets past the guard; the op is what has to be rejected, and it is
	// rejected before any host callback is attempted.
	resp := call(t, routes.ResourcePrefix+routes.StatusAction, url.Values{
		"token": {"s3cret"},
		"op":    {"delete"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := string(resp.Body)
	for _, op := range []string{"suspend", "resume", "suspend_all", "resume_all"} {
		if !strings.Contains(body, op) {
			t.Errorf("body = %s, want it to name %q", body, op)
		}
	}
}

// The bulk ops take no auth_index; the single-account ops must still require one.
func TestStatusActionRequiresAnAuthIndexForSingleOps(t *testing.T) {
	cfg := config.Default()
	cfg.ConsoleToken = "s3cret"
	withConfig(t, cfg)

	for _, op := range []string{"suspend", "resume"} {
		resp := call(t, routes.ResourcePrefix+routes.StatusAction, url.Values{
			"token": {"s3cret"},
			"op":    {op},
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("op=%s: status = %d, want 400", op, resp.StatusCode)
		}
		if !strings.Contains(string(resp.Body), "auth_index is required") {
			t.Errorf("op=%s: body = %s", op, resp.Body)
		}
	}
}

func TestQuotaRouteRequiresAnAuthIndex(t *testing.T) {
	withConfig(t, config.Default())

	// This route is behind the host's management middleware, so it carries no console
	// token - reaching it at all proves the caller holds the management key.
	resp := call(t, routes.ManagementPrefix+routes.Quota, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "auth_index is required") {
		t.Errorf("body = %s", resp.Body)
	}
}

func TestRegisterAdvertisesEveryRoute(t *testing.T) {
	out, err := Register(nil)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	var env rpc.Envelope
	if err = json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var reg registration
	if err = json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatalf("decode registration: %v", err)
	}

	// The quota feed must be a Route, not a Resource: the host turns a GET resource
	// carrying a menu label into an unauthenticated page.
	if len(reg.Routes) != 1 || reg.Routes[0].Path != routes.Quota {
		t.Errorf("Routes = %+v, want just %s", reg.Routes, routes.Quota)
	}
	advertised := make(map[string]bool, len(reg.Resources))
	for _, res := range reg.Resources {
		advertised[res.Path] = true
	}
	for _, want := range []string{
		routes.Login, routes.LoginCode, routes.LoginVerify,
		routes.Status, routes.StatusData, routes.StatusAction,
	} {
		if !advertised[want] {
			// An unadvertised route is simply not served, which fails as a 404 at runtime.
			t.Errorf("resource %s is not advertised", want)
		}
	}
}
