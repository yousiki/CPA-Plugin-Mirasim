// Package routes holds the plugin's HTTP paths.
//
// They live in their own leaf package because two packages need them and neither may
// import the other: management routes requests by them, and auth builds the login page
// URL out of them.
package routes

import (
	"strings"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
)

// Browser-navigable resource routes, served by the host without management
// authentication and only for GET.
const (
	Login        = "/login"
	LoginCode    = "/login/code"
	LoginVerify  = "/login/verify"
	Status       = "/status"
	StatusData   = "/status/data"
	StatusAction = "/status/action"
)

// Quota is a management API route, authenticated by the host with the management key.
// This is what the management panel's quota card reads, so it needs no console token.
const Quota = "/" + config.PluginID + "/quota"

// ManagementPrefix is the host prefix on authenticated management routes.
const ManagementPrefix = "/v0/management"

// ResourcePrefix is the host prefix on unauthenticated resource routes.
const ResourcePrefix = "/v0/resource/plugins/" + config.PluginID

// ResourceSuffix strips the host-assigned prefix so routing works off stable names.
func ResourceSuffix(path string) string {
	if suffix := strings.TrimPrefix(path, ResourcePrefix); suffix != path {
		return strings.TrimRight(suffix, "/")
	}
	return strings.TrimRight(path, "/")
}

// IsManagement reports whether path is the given authenticated management route.
func IsManagement(path, route string) bool {
	return strings.TrimSuffix(strings.TrimPrefix(path, ManagementPrefix), "/") == route
}
