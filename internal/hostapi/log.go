package hostapi

import "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"

// Log writes to the host's logger. Levels follow the host's own vocabulary
// ("debug", "info", "warn", "error").
func Log(level, message string) {
	_, _ = Call(pluginabi.MethodHostLog, map[string]any{"level": level, "message": message})
}
