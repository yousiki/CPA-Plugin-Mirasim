package main

// Typed wrappers over the host callback bridge. The host performs every real HTTP
// request so proxy handling, transport policy, auth context and request logging stay
// under its control; plugins must not dial out themselves.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// hostHTTPRequest is the wire shape of host.http.do and host.http.do_stream.
type hostHTTPRequest struct {
	HostCallbackID string              `json:"host_callback_id,omitempty"`
	Method         string              `json:"method,omitempty"`
	URL            string              `json:"url,omitempty"`
	Headers        map[string][]string `json:"headers,omitempty"`
	Body           []byte              `json:"body,omitempty"`
}

// hostHTTPStreamResponse is the host.http.do_stream result. A non-empty StreamID means
// the response is still open and must be drained with hostHTTPStreamRead.
type hostHTTPStreamResponse struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers,omitempty"`
	StreamID   string              `json:"stream_id,omitempty"`
	Chunks     []struct {
		Payload []byte `json:"Payload,omitempty"`
	} `json:"chunks,omitempty"`
}

type hostHTTPStreamReadResponse struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

type hostStreamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

func headerToMap(header http.Header) map[string][]string {
	if len(header) == 0 {
		return nil
	}
	out := make(map[string][]string, len(header))
	for key, values := range header {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func mapToHeader(values map[string][]string) http.Header {
	if len(values) == 0 {
		return nil
	}
	out := make(http.Header, len(values))
	for key, value := range values {
		out[http.CanonicalHeaderKey(key)] = append([]string(nil), value...)
	}
	return out
}

// hostHTTPDo performs a non-streaming upstream request through the host.
func hostHTTPDo(callbackID, method, url string, header http.Header, body []byte) (pluginapi.HTTPResponse, error) {
	raw, err := callHost(pluginabi.MethodHostHTTPDo, hostHTTPRequest{
		HostCallbackID: callbackID,
		Method:         method,
		URL:            url,
		Headers:        headerToMap(header),
		Body:           body,
	})
	if err != nil {
		return pluginapi.HTTPResponse{}, err
	}
	// pluginapi.HTTPResponse carries no json tags, so the host emits Go field names.
	var resp pluginapi.HTTPResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("decode host http response: %w", err)
	}
	return resp, nil
}

// hostHTTPDoStream opens a streaming upstream request through the host.
func hostHTTPDoStream(callbackID, method, url string, header http.Header, body []byte) (hostHTTPStreamResponse, error) {
	raw, err := callHost(pluginabi.MethodHostHTTPDoStream, hostHTTPRequest{
		HostCallbackID: callbackID,
		Method:         method,
		URL:            url,
		Headers:        headerToMap(header),
		Body:           body,
	})
	if err != nil {
		return hostHTTPStreamResponse{}, err
	}
	var resp hostHTTPStreamResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return hostHTTPStreamResponse{}, fmt.Errorf("decode host http stream response: %w", err)
	}
	return resp, nil
}

func hostHTTPStreamRead(streamID string) (hostHTTPStreamReadResponse, error) {
	raw, err := callHost(pluginabi.MethodHostHTTPStreamRead, map[string]string{"stream_id": streamID})
	if err != nil {
		return hostHTTPStreamReadResponse{}, err
	}
	var resp hostHTTPStreamReadResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return hostHTTPStreamReadResponse{}, fmt.Errorf("decode host http stream read: %w", err)
	}
	return resp, nil
}

func hostHTTPStreamClose(streamID string) {
	if streamID == "" {
		return
	}
	_, _ = callHost(pluginabi.MethodHostHTTPStreamClose, map[string]string{"stream_id": streamID})
}

// hostStreamEmit pushes one chunk to the executor stream the host opened for us.
func hostStreamEmit(streamID string, payload []byte) error {
	_, err := callHost(pluginabi.MethodHostStreamEmit, hostStreamEmitRequest{StreamID: streamID, Payload: payload})
	return err
}

// hostStreamClose ends the executor stream. A non-empty message surfaces as a stream error.
func hostStreamClose(streamID, errorMessage string) {
	if streamID == "" {
		return
	}
	_, _ = callHost(pluginabi.MethodHostStreamClose, hostStreamEmitRequest{StreamID: streamID, Error: errorMessage})
}

func hostLog(level, message string) {
	_, _ = callHost(pluginabi.MethodHostLog, map[string]any{"level": level, "message": message})
}

func hostAuthList() ([]pluginapi.HostAuthFileEntry, error) {
	raw, err := callHost(pluginabi.MethodHostAuthList, struct{}{})
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

func hostAuthGet(authIndex string) (pluginapi.HostAuthGetResponse, error) {
	raw, err := callHost(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return pluginapi.HostAuthGetResponse{}, err
	}
	var resp pluginapi.HostAuthGetResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return pluginapi.HostAuthGetResponse{}, fmt.Errorf("decode host auth get: %w", err)
	}
	return resp, nil
}

func hostAuthGetRuntime(authIndex string) (pluginapi.HostAuthFileEntry, error) {
	raw, err := callHost(pluginabi.MethodHostAuthGetRuntime, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return pluginapi.HostAuthFileEntry{}, err
	}
	var resp pluginapi.HostAuthGetRuntimeResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return pluginapi.HostAuthFileEntry{}, fmt.Errorf("decode host auth runtime: %w", err)
	}
	return resp.Auth, nil
}

// hostAuthSave writes credential JSON to the auth directory and upserts the runtime record.
func hostAuthSave(name string, payload json.RawMessage) error {
	_, err := callHost(pluginabi.MethodHostAuthSave, pluginapi.HostAuthSaveRequest{Name: name, JSON: payload})
	return err
}
