package hostapi

import "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"

type streamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

// StreamEmit pushes one chunk to the executor stream the host opened for us.
func StreamEmit(streamID string, payload []byte) error {
	_, err := Call(pluginabi.MethodHostStreamEmit, streamEmitRequest{StreamID: streamID, Payload: payload})
	return err
}

// StreamClose ends the executor stream. A non-empty message surfaces as a stream error.
func StreamClose(streamID, errorMessage string) {
	if streamID == "" {
		return
	}
	_, _ = Call(pluginabi.MethodHostStreamClose, streamEmitRequest{StreamID: streamID, Error: errorMessage})
}
