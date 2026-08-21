// Package config holds the plugin's identity and its `plugins.configs.mirasim` block.
//
// It is a leaf: it imports nothing else from this module, so every other package can
// depend on it without introducing a cycle.
package config

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// PluginID is the dynamic library basename, the provider key, and the
// `plugins.configs.<id>` key. All three must agree: CPA selects the executor by
// auth.Provider and looks the auth provider up by the same string.
const PluginID = "mirasim"

// Version is the single source of truth for the plugin version; the Makefile scrapes it
// out of this file, so keep the `const Version = "..."` form on one line.
const Version = "0.3.1"

// ModelSpec is one advertised model id and its context window.
type ModelSpec struct {
	ID            string
	ContextLength int64
}

// DefaultModels is the catalogue every Mirasim credential advertises.
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
var DefaultModels = []ModelSpec{
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

// Config mirrors `plugins.configs.mirasim` in the CPA config.
type Config struct {
	LoginURL               string `yaml:"login_url"`
	RelayURL               string `yaml:"relay_url"`
	PublicBaseURL          string `yaml:"public_base_url"`
	ModelIDs               string `yaml:"model_ids"`
	ConsoleToken           string `yaml:"console_token"`
	QuotaProbe             bool   `yaml:"quota_probe"`
	RefreshIntervalSeconds int    `yaml:"refresh_interval_seconds"`
	ContextBeta            string `yaml:"context_beta"`
	HTTPTimeoutSeconds     int    `yaml:"http_timeout_seconds"`

	// Models is the parsed form of ModelIDs, or DefaultModels when it is empty. It is
	// never read from YAML - `yaml:"-"` keeps a stray `models:` key in the config from
	// overwriting the parsed catalogue with something the parser never validated.
	Models []ModelSpec `yaml:"-"`
}

var current atomic.Value

func Default() Config {
	return Config{
		// The previous hosts (admin.test.mirofish.ai, admin.mirofish.ai) were retired
		// 2026-08-21 and answer 403 to every /auth/* login call; this is the address
		// their retirement notice names. Same protocol, spec at /openapi.json.
		LoginURL: "https://auth.mirasim.ai",
		RelayURL: "https://mirasim-relay.mirofish.ai",
		// Access tokens live ~1h. The host refresh loop needs an interval to claim the
		// credential at all, so refresh well before expiry rather than at the edge.
		RefreshIntervalSeconds: 1500,
		QuotaProbe:             true,
		// 1M context is opted into with a header, never with a model-name suffix.
		ContextBeta:        "context-1m-2025-08-07",
		HTTPTimeoutSeconds: 120,
		Models:             DefaultModels,
	}
}

func Decode(raw []byte) (Config, error) {
	cfg := Default()
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return Config{}, err
		}
	}
	cfg.LoginURL = trimTrailingSlash(cfg.LoginURL)
	cfg.RelayURL = trimTrailingSlash(cfg.RelayURL)
	cfg.PublicBaseURL = trimTrailingSlash(cfg.PublicBaseURL)
	cfg.ConsoleToken = strings.TrimSpace(cfg.ConsoleToken)
	cfg.ContextBeta = strings.TrimSpace(cfg.ContextBeta)
	if cfg.RefreshIntervalSeconds <= 0 {
		cfg.RefreshIntervalSeconds = Default().RefreshIntervalSeconds
	}
	if cfg.HTTPTimeoutSeconds <= 0 {
		cfg.HTTPTimeoutSeconds = Default().HTTPTimeoutSeconds
	}
	cfg.Models = ParseModelIDs(cfg.ModelIDs)
	return cfg, nil
}

// Store publishes a decoded config for Loaded to hand out.
func Store(cfg Config) {
	current.Store(cfg)
}

// Loaded returns the active config, or the defaults before plugin.register has run.
func Loaded() Config {
	if cfg, ok := current.Load().(Config); ok {
		return cfg
	}
	return Default()
}

func (c Config) HTTPTimeout() time.Duration {
	return time.Duration(c.HTTPTimeoutSeconds) * time.Second
}

// ModelIDList is the advertised catalogue as bare ids, for the console footer.
func (c Config) ModelIDList() []string {
	out := make([]string, 0, len(c.Models))
	for _, spec := range c.Models {
		out = append(out, spec.ID)
	}
	return out
}

// ParseModelIDs reads `id[:contextLength],...`, falling back to the built-in catalogue.
func ParseModelIDs(raw string) []ModelSpec {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultModels
	}
	out := make([]ModelSpec, 0, 8)
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		spec := ModelSpec{ID: token}
		if separator := strings.LastIndex(token, ":"); separator > 0 {
			if context, err := strconv.ParseInt(strings.TrimSpace(token[separator+1:]), 10, 64); err == nil && context > 0 {
				spec = ModelSpec{ID: strings.TrimSpace(token[:separator]), ContextLength: context}
			}
		}
		if spec.ID != "" {
			out = append(out, spec)
		}
	}
	if len(out) == 0 {
		return DefaultModels
	}
	return out
}

func trimTrailingSlash(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}
