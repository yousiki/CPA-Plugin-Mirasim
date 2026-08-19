// Package router implements the model_router capability: requests carrying Anthropic
// server-executed tools are diverted to the built-in claude provider before auth
// selection, because the Mirasim relay (LiteLLM in front of Bedrock) cannot execute
// them — Bedrock rejects the tool type with a ValidationException.
//
// Everything else is left unhandled, so normal mixed selection keeps pooling Mirasim
// credentials with any other credential advertising the same model ids.
package router

import (
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/rpc"
)

// serverToolTypePrefixes are the typed tool declarations the Anthropic API executes on
// its own infrastructure. Client-executed typed tools (bash_, text_editor_, computer_,
// memory_) are deliberately absent: Bedrock accepts those, so the relay can serve them.
var serverToolTypePrefixes = []string{
	"code_execution_",
	"tool_search_tool_",
	"web_fetch_",
	"web_search_",
}

type rpcRouteRequest struct {
	pluginapi.ModelRouteRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// Route answers model.route. It only claims claude-format requests that declare a
// server tool, target a model in this plugin's catalogue, and can actually be served
// by a logged-in claude credential; anything else stays on the normal selection path
// (where a Mirasim credential without claude siblings still handles the request and
// surfaces the relay's own rejection).
func Route(request []byte) ([]byte, error) {
	var req rpcRouteRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	if req.SourceFormat != "claude" ||
		!hasServerTool(req.Body) ||
		!catalogueHas(req.RequestedModel) ||
		!providerAvailable(req.AvailableProviders, "claude") {
		return rpc.OK(pluginapi.ModelRouteResponse{})
	}
	return rpc.OK(pluginapi.ModelRouteResponse{
		Handled:    true,
		TargetKind: pluginapi.ModelRouteTargetProvider,
		Target:     "claude",
		Reason:     "anthropic server tools are executed by the first-party API; the " + config.PluginID + " relay (Bedrock) rejects them",
	})
}

func hasServerTool(body []byte) bool {
	var payload struct {
		Tools []struct {
			Type string `json:"type"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	for _, tool := range payload.Tools {
		toolType := strings.ToLower(strings.TrimSpace(tool.Type))
		for _, prefix := range serverToolTypePrefixes {
			if strings.HasPrefix(toolType, prefix) {
				return true
			}
		}
	}
	return false
}

// catalogueHas reports whether the requested model, minus any "(thinking)" style
// suffix, is one this plugin advertises. Foreign models must not be hijacked: forcing
// provider "claude" for a model claude does not serve would strand the request.
func catalogueHas(model string) bool {
	model = strings.TrimSpace(model)
	if index := strings.IndexByte(model, '('); index > 0 {
		model = strings.TrimSpace(model[:index])
	}
	for _, spec := range config.Loaded().Models {
		if spec.ID == model {
			return true
		}
	}
	return false
}

func providerAvailable(providers []string, want string) bool {
	for _, provider := range providers {
		if strings.EqualFold(strings.TrimSpace(provider), want) {
			return true
		}
	}
	return false
}
