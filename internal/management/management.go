// Package management serves the browser-facing routes: the login flow that replaces an
// OAuth callback, and the operator console.
//
// Both live under /v0/resource/plugins/mirasim/. The host serves that prefix without
// management authentication and only for GET, so every action is a GET carrying its
// arguments in the query string - the same shape upstream's own host-callback example
// uses. The login routes are protected by the unguessable, short-lived state; the console
// is protected by the configured console_token.
package management

import (
	"encoding/json"
	"net/http"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/routes"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/rpc"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/ui"
)

// registration mirrors the host's registration schema. Handlers are attached host-side,
// so only the route descriptors travel over the wire.
type registration struct {
	Routes    []route    `json:"routes,omitempty"`
	Resources []resource `json:"resources,omitempty"`
}

type route struct {
	Method string `json:"Method"`
	Path   string `json:"Path"`
}

type resource struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu,omitempty"`
	Description string `json:"Description,omitempty"`
}

func Register(_ []byte) ([]byte, error) {
	return rpc.OK(registration{
		// No Menu on this one: the host turns a GET route that carries a menu label into
		// an unauthenticated resource, which is the opposite of what a quota feed wants.
		Routes: []route{
			{Method: http.MethodGet, Path: routes.Quota},
		},
		Resources: []resource{
			{Path: routes.Login},
			{Path: routes.LoginCode},
			{Path: routes.LoginVerify},
			{Path: routes.Status, Menu: "Mirasim", Description: "Mirasim accounts, quota and rotation."},
			{Path: routes.StatusData},
			{Path: routes.StatusAction},
		},
	})
}

func Handle(raw []byte) ([]byte, error) {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cfg := config.Loaded()

	// Management API routes keep their /v0/management prefix; resource routes are
	// rewritten under /v0/resource/plugins/<id>. Handle the authenticated one first so a
	// resource path can never be mistaken for it.
	if routes.IsManagement(req.Path, routes.Quota) {
		return handleQuotaRoute(cfg, req)
	}

	switch routes.ResourceSuffix(req.Path) {
	case routes.Login:
		return htmlResponse(http.StatusOK, ui.LoginPage(req.Query.Get("state")))
	case routes.LoginCode:
		return handleLoginCode(cfg, req)
	case routes.LoginVerify:
		return handleLoginVerify(cfg, req)
	case routes.Status:
		// The shell carries no account data, so it is served without a token: the panel
		// embeds this page in an iframe that cannot send one, and answering that with a
		// raw 403 JSON body is what the operator would see. The token gates the data and
		// action routes below, which is where anything worth protecting lives.
		return htmlResponse(http.StatusOK, ui.StatusPage(cfg.ConsoleToken != "", req.Query.Get("token")))
	case routes.StatusData:
		if resp, ok := requireConsoleToken(cfg, req.Query); !ok {
			return resp, nil
		}
		return handleStatusData(cfg, req)
	case routes.StatusAction:
		if resp, ok := requireConsoleToken(cfg, req.Query); !ok {
			return resp, nil
		}
		return handleStatusAction(req)
	default:
		return errorResponse(http.StatusNotFound, "unknown route")
	}
}
