package router

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
)

func routeOnce(t *testing.T, req pluginapi.ModelRouteRequest) pluginapi.ModelRouteResponse {
	t.Helper()
	raw, err := json.Marshal(rpcRouteRequest{ModelRouteRequest: req})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Route(raw)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatalf("Route returned an error envelope: %s", out)
	}
	var resp pluginapi.ModelRouteResponse
	if err := json.Unmarshal(envelope.Result, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestRouteDivertsServerToolsToClaude(t *testing.T) {
	model := config.DefaultModels[0].ID
	req := pluginapi.ModelRouteRequest{
		SourceFormat:       "claude",
		RequestedModel:     model,
		Body:               []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"},{"name":"Bash"}]}`),
		AvailableProviders: []string{"claude"},
	}
	resp := routeOnce(t, req)
	if !resp.Handled || resp.TargetKind != pluginapi.ModelRouteTargetProvider || resp.Target != "claude" {
		t.Fatalf("expected handled provider route to claude, got %#v", resp)
	}
	if resp.TargetModel != "" {
		t.Fatalf("the client model must be kept, got %q", resp.TargetModel)
	}
}

func TestRouteDivertsThinkingSuffixedModel(t *testing.T) {
	req := pluginapi.ModelRouteRequest{
		SourceFormat:       "claude",
		RequestedModel:     config.DefaultModels[0].ID + "(8192)",
		Body:               []byte(`{"tools":[{"type":"web_fetch_20250910","name":"web_fetch"}]}`),
		AvailableProviders: []string{"claude"},
	}
	if resp := routeOnce(t, req); !resp.Handled {
		t.Fatalf("suffixed catalogue model should be diverted, got %#v", resp)
	}
}

func TestRouteLeavesEverythingElseAlone(t *testing.T) {
	model := config.DefaultModels[0].ID
	serverTools := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`)
	cases := map[string]pluginapi.ModelRouteRequest{
		"no server tools": {
			SourceFormat:       "claude",
			RequestedModel:     model,
			Body:               []byte(`{"tools":[{"name":"Bash"},{"type":"custom","name":"x"},{"type":"bash_20250124","name":"bash"}]}`),
			AvailableProviders: []string{"claude"},
		},
		"foreign format": {
			SourceFormat:       "openai",
			RequestedModel:     model,
			Body:               serverTools,
			AvailableProviders: []string{"claude"},
		},
		"foreign model": {
			SourceFormat:       "claude",
			RequestedModel:     "gemini-3-pro",
			Body:               serverTools,
			AvailableProviders: []string{"claude"},
		},
		"no claude credential": {
			SourceFormat:       "claude",
			RequestedModel:     model,
			Body:               serverTools,
			AvailableProviders: []string{"codex"},
		},
		"unparseable body": {
			SourceFormat:       "claude",
			RequestedModel:     model,
			Body:               []byte(`not json`),
			AvailableProviders: []string{"claude"},
		},
	}
	for name, req := range cases {
		if resp := routeOnce(t, req); resp.Handled {
			t.Errorf("%s: expected unhandled, got %#v", name, resp)
		}
	}
}
