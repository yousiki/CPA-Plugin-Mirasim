package main

// The model provider capability: every logged-in credential advertises the same
// verified catalogue.
//
// No alias is set on any entry, on purpose: CLIProxyAPI advertises the alias to clients
// *instead of* the name, so an alias would hide the gateway ids behind a display string
// and clients could no longer request them. Without one, clients ask for exactly what
// the gateway uses and identical ids pool with any other credential advertising them.

import (
	"encoding/json"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func modelStatic(_ []byte) ([]byte, error) {
	// Models exist only for a logged-in account, never statically.
	return okEnvelope(pluginapi.ModelResponse{Provider: pluginID})
}

func modelForAuth(request []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := loadedConfig()
	created := time.Now().Unix()

	models := make([]pluginapi.ModelInfo, 0, len(cfg.models))
	for _, spec := range cfg.models {
		info := pluginapi.ModelInfo{
			ID:          spec.ID,
			Object:      "model",
			Created:     created,
			OwnedBy:     pluginID,
			Type:        "model",
			DisplayName: spec.ID,
			Name:        spec.ID,
		}
		if spec.ContextLength > 0 {
			info.ContextLength = spec.ContextLength
			info.InputTokenLimit = spec.ContextLength
		}
		models = append(models, info)
	}
	return okEnvelope(pluginapi.ModelResponse{Provider: pluginID, Models: models})
}
