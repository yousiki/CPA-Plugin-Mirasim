package main

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// pluginID is the dynamic library basename, the provider key, and the
// `plugins.configs.<id>` key. All three must agree: CPA selects the executor by
// auth.Provider and looks the auth provider up by the same string.
const pluginID = "mirasim"

const pluginVersion = "0.1.0"

// modelSpec is one advertised model id and its context window.
type modelSpec struct {
	ID            string
	ContextLength int64
}

// defaultModels is the catalogue every Mirasim credential advertises.
//
// Each id was verified end to end through CLIProxyAPI with a claude-code-shaped request
// (streaming, cached system blocks, tool schemas) and answered with a `msg_bdrk_*` id,
// proving the relay's Bedrock backend served it. The relay advertises 40 ids in its own
// /v1/models; the rest are deliberately excluded because they do not work for this tenant:
//
//   - the whole `anthropic/<model>` family answers 503 "model service is temporarily
//     unavailable" (the lone exception, `anthropic/claude-opus-4-8`, is a redundant
//     spelling of an id already listed)
//   - `gpt-5.6`, `gpt-5.6-terra`, `gpt-5.6-sol`, `gpt-5.6-luna` answer 403
//   - `gpt-4o-mini` answers 400 and the wildcard `*` answers 503
//   - the undated `claude-haiku-4-5` passes small probes but fails claude-code's real
//     payload with 400, so the dated id is used instead
//
// kimi-k3 and gpt-4o-mini-openrouter declare no context length on the relay, so none is set.
var defaultModels = []modelSpec{
	{ID: "claude-opus-5", ContextLength: 1_000_000},
	{ID: "claude-opus-4-8", ContextLength: 1_000_000},
	{ID: "claude-opus-4-7", ContextLength: 1_000_000},
	{ID: "claude-sonnet-5", ContextLength: 1_000_000},
	{ID: "claude-fable-5", ContextLength: 1_000_000},
	{ID: "claude-haiku-4-5-20251001", ContextLength: 200_000},
	{ID: "claude-opus-5-20260724", ContextLength: 200_000},
	{ID: "kimi-k3"},
	{ID: "gpt-4o-mini-openrouter"},
}

// pluginConfig mirrors `plugins.configs.mirasim` in the CPA config.
type pluginConfig struct {
	LoginURL               string `yaml:"login_url"`
	RelayURL               string `yaml:"relay_url"`
	PublicBaseURL          string `yaml:"public_base_url"`
	ModelIDs               string `yaml:"model_ids"`
	ConsoleToken           string `yaml:"console_token"`
	QuotaProbe             bool   `yaml:"quota_probe"`
	RefreshIntervalSeconds int    `yaml:"refresh_interval_seconds"`
	ContextBeta            string `yaml:"context_beta"`
	HTTPTimeoutSeconds     int    `yaml:"http_timeout_seconds"`

	// models is the parsed form of ModelIDs, or defaultModels when it is empty.
	models []modelSpec
}

var currentConfig atomic.Value

func defaultPluginConfig() pluginConfig {
	return pluginConfig{
		// Compiled into the Mirasim app; note it is the staging host.
		LoginURL: "https://admin.test.mirofish.ai",
		RelayURL: "https://mirasim-relay.mirofish.ai",
		// Access tokens live ~1h. The host refresh loop needs an interval to claim the
		// credential at all, so refresh well before expiry rather than at the edge.
		RefreshIntervalSeconds: 1500,
		QuotaProbe:             true,
		// 1M context is opted into with a header, never with a model-name suffix.
		ContextBeta:        "context-1m-2025-08-07",
		HTTPTimeoutSeconds: 120,
		models:             defaultModels,
	}
}

func decodeConfig(raw []byte) (pluginConfig, error) {
	cfg := defaultPluginConfig()
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return pluginConfig{}, err
		}
	}
	cfg.LoginURL = trimTrailingSlash(cfg.LoginURL)
	cfg.RelayURL = trimTrailingSlash(cfg.RelayURL)
	cfg.PublicBaseURL = trimTrailingSlash(cfg.PublicBaseURL)
	cfg.ConsoleToken = strings.TrimSpace(cfg.ConsoleToken)
	cfg.ContextBeta = strings.TrimSpace(cfg.ContextBeta)
	if cfg.RefreshIntervalSeconds <= 0 {
		cfg.RefreshIntervalSeconds = defaultPluginConfig().RefreshIntervalSeconds
	}
	if cfg.HTTPTimeoutSeconds <= 0 {
		cfg.HTTPTimeoutSeconds = defaultPluginConfig().HTTPTimeoutSeconds
	}
	cfg.models = parseModelIDs(cfg.ModelIDs)
	return cfg, nil
}

func loadedConfig() pluginConfig {
	if cfg, ok := currentConfig.Load().(pluginConfig); ok {
		return cfg
	}
	return defaultPluginConfig()
}

func (c pluginConfig) httpTimeout() time.Duration {
	return time.Duration(c.HTTPTimeoutSeconds) * time.Second
}

// parseModelIDs reads `id[:contextLength],...`, falling back to the built-in catalogue.
func parseModelIDs(raw string) []modelSpec {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultModels
	}
	out := make([]modelSpec, 0, 8)
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		spec := modelSpec{ID: token}
		if separator := strings.LastIndex(token, ":"); separator > 0 {
			if context, err := strconv.ParseInt(strings.TrimSpace(token[separator+1:]), 10, 64); err == nil && context > 0 {
				spec = modelSpec{ID: strings.TrimSpace(token[:separator]), ContextLength: context}
			}
		}
		if spec.ID != "" {
			out = append(out, spec)
		}
	}
	if len(out) == 0 {
		return defaultModels
	}
	return out
}

func trimTrailingSlash(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}
