package hostapi

import (
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func AuthList() ([]pluginapi.HostAuthFileEntry, error) {
	raw, err := Call(pluginabi.MethodHostAuthList, struct{}{})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Files []pluginapi.HostAuthFileEntry `json:"files"`
	}
	if err = json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode host auth list: %w", err)
	}
	return resp.Files, nil
}

func AuthGet(authIndex string) (pluginapi.HostAuthGetResponse, error) {
	raw, err := Call(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return pluginapi.HostAuthGetResponse{}, err
	}
	var resp pluginapi.HostAuthGetResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return pluginapi.HostAuthGetResponse{}, fmt.Errorf("decode host auth get: %w", err)
	}
	return resp, nil
}

func AuthGetRuntime(authIndex string) (pluginapi.HostAuthFileEntry, error) {
	raw, err := Call(pluginabi.MethodHostAuthGetRuntime, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return pluginapi.HostAuthFileEntry{}, err
	}
	var resp pluginapi.HostAuthGetRuntimeResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return pluginapi.HostAuthFileEntry{}, fmt.Errorf("decode host auth runtime: %w", err)
	}
	return resp.Auth, nil
}

// AuthSave writes credential JSON to the auth directory and upserts the runtime record.
func AuthSave(name string, payload json.RawMessage) error {
	_, err := Call(pluginabi.MethodHostAuthSave, pluginapi.HostAuthSaveRequest{Name: name, JSON: payload})
	return err
}
