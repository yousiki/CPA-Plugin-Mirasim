// Package plugin routes host method calls to the capability that implements them.
//
// It is the whole plugin minus the C ABI: cmd/mirasim decodes the call off the bridge and
// hands it straight here, which keeps everything reachable from a plain `go test`.
package plugin

import (
	"encoding/json"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/auth"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/executor"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/management"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/models"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/router"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/rpc"
)

// lifecycleRequest is the plugin.register / plugin.reconfigure payload.
type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	ModelProvider         bool     `json:"model_provider"`
	AuthProvider          bool     `json:"auth_provider"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string `json:"executor_output_formats,omitempty"`
	ManagementAPI         bool     `json:"management_api"`
	ModelRouter           bool     `json:"model_router"`
}

// Handle dispatches one host call.
func Handle(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if err := Configure(request); err != nil {
			return nil, err
		}
		return rpc.OK(Registration())
	case pluginabi.MethodPluginShutdown:
		auth.Shutdown()
		return rpc.OK(struct{}{})

	case pluginabi.MethodAuthIdentifier:
		return rpc.OK(map[string]string{"identifier": config.PluginID})
	case pluginabi.MethodAuthParse:
		return auth.Parse(request)
	case pluginabi.MethodAuthLoginStart:
		return auth.LoginStart(request)
	case pluginabi.MethodAuthLoginPoll:
		return auth.LoginPoll(request)
	case pluginabi.MethodAuthRefresh:
		return auth.Refresh(request)

	case pluginabi.MethodModelStatic:
		return models.Static(request)
	case pluginabi.MethodModelForAuth:
		return models.ForAuth(request)
	case pluginabi.MethodModelRoute:
		return router.Route(request)

	case pluginabi.MethodExecutorIdentifier:
		return rpc.OK(map[string]string{"identifier": config.PluginID})
	case pluginabi.MethodExecutorExecute:
		return executor.Execute(request)
	case pluginabi.MethodExecutorExecuteStream:
		return executor.ExecuteStream(request)
	case pluginabi.MethodExecutorCountTokens:
		return executor.CountTokens(request)
	case pluginabi.MethodExecutorHTTPRequest:
		return executor.HTTPRequest(request)

	case pluginabi.MethodManagementRegister:
		return management.Register(request)
	case pluginabi.MethodManagementHandle:
		return management.Handle(request)

	default:
		return rpc.Error("unknown_method", "unknown method: "+method), nil
	}
}

// Shutdown releases everything the plugin holds across a reload.
func Shutdown() {
	auth.Shutdown()
}

// Configure decodes and publishes the plugin's config block.
func Configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
	}
	cfg, err := config.Decode(req.ConfigYAML)
	if err != nil {
		return err
	}
	config.Store(cfg)
	return nil
}

// Registration is what the host learns about this plugin at load time.
func Registration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "Mirasim",
			Version:          config.Version,
			Author:           "yousiki",
			GitHubRepository: "https://github.com/yousiki/CPA-Plugin-Mirasim",
			Logo:             config.Logo,
			ConfigFields: []pluginapi.ConfigField{
				{Name: "login_url", Type: pluginapi.ConfigFieldTypeString, Description: "Mirofish auth backend base URL. Default https://admin.test.mirofish.ai (the staging host compiled into the Mirasim app)."},
				{Name: "relay_url", Type: pluginapi.ConfigFieldTypeString, Description: "Mirasim relay base URL serving the Anthropic Messages API. Default https://mirasim-relay.mirofish.ai."},
				{Name: "public_base_url", Type: pluginapi.ConfigFieldTypeString, Description: "Externally reachable base URL of this CPA instance, e.g. https://api.example.com. Required for the login page link to be openable from a remote browser."},
				{Name: "model_ids", Type: pluginapi.ConfigFieldTypeString, Description: "Override the advertised catalogue: \"id[:contextLength],...\". Empty uses the built-in verified list."},
				{Name: "console_token", Type: pluginapi.ConfigFieldTypeString, Description: "Shared secret guarding the plugin status console. The console route is unauthenticated by design, so it returns 403 until this is set."},
				{Name: "quota_probe", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Allow the console to read the relay's rate-limit headers. The probe is free but spends one slot of the ~8000-request 5h window."},
				{Name: "refresh_interval_seconds", Type: pluginapi.ConfigFieldTypeInteger, Description: "How often the host refreshes each credential. Access tokens live ~3600s; default 1500."},
				{Name: "context_beta", Type: pluginapi.ConfigFieldTypeString, Description: "anthropic-beta header opting into the 1M context window. Default context-1m-2025-08-07; empty disables it."},
				{Name: "http_timeout_seconds", Type: pluginapi.ConfigFieldTypeInteger, Description: "Timeout for non-streaming upstream calls. Default 120."},
			},
		},
		Capabilities: registrationCapability{
			ModelProvider: true,
			AuthProvider:  true,
			Executor:      true,
			// Models are bound to a logged-in credential, never static.
			ExecutorModelScope: string(pluginapi.ExecutorModelScopeOAuth),
			// The relay speaks the Anthropic Messages API; the host translates every other
			// client protocol into and out of it, exactly as it does for the built-in
			// Claude executor.
			ExecutorInputFormats:  []string{"claude"},
			ExecutorOutputFormats: []string{"claude"},
			ManagementAPI:         true,
			// Diverts requests carrying Anthropic server tools (WebSearch etc.) to the
			// built-in claude provider: the relay's Bedrock backend rejects them.
			ModelRouter: true,
		},
	}
}
