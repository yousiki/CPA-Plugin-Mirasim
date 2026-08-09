// Package models implements the model provider capability: every logged-in credential
// advertises the same verified catalogue.
//
// No alias is set on any entry, on purpose: CLIProxyAPI advertises the alias to clients
// *instead of* the name, so an alias would hide the gateway ids behind a display string
// and clients could no longer request them. Without one, clients ask for exactly what the
// gateway uses and identical ids pool with any other credential advertising them.
package models

import (
	"encoding/json"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/rpc"
)

func Static(_ []byte) ([]byte, error) {
	// Models exist only for a logged-in account, never statically.
	return rpc.OK(pluginapi.ModelResponse{Provider: config.PluginID})
}

func ForAuth(request []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := config.Loaded()
	created := time.Now().Unix()

	list := make([]pluginapi.ModelInfo, 0, len(cfg.Models))
	for _, spec := range cfg.Models {
		info := pluginapi.ModelInfo{
			ID:          spec.ID,
			Object:      "model",
			Created:     created,
			OwnedBy:     config.PluginID,
			Type:        "model",
			DisplayName: spec.ID,
			Name:        spec.ID,
		}
		if spec.ContextLength > 0 {
			info.ContextLength = spec.ContextLength
			info.InputTokenLimit = spec.ContextLength
		}
		list = append(list, info)
	}
	return rpc.OK(pluginapi.ModelResponse{Provider: config.PluginID, Models: list})
}
