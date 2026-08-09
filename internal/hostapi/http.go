package hostapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// httpRequest is the wire shape of host.http.do and host.http.do_stream.
type httpRequest struct {
	HostCallbackID string              `json:"host_callback_id,omitempty"`
	Method         string              `json:"method,omitempty"`
	URL            string              `json:"url,omitempty"`
	Headers        map[string][]string `json:"headers,omitempty"`
	Body           []byte              `json:"body,omitempty"`
}

// StreamResponse is the host.http.do_stream result. A non-empty StreamID means the
// response is still open and must be drained with StreamRead.
type StreamResponse struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers,omitempty"`
	StreamID   string              `json:"stream_id,omitempty"`
	Chunks     []struct {
		Payload []byte `json:"Payload,omitempty"`
	} `json:"chunks,omitempty"`
}

type StreamReadResponse struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

func HeaderToMap(header http.Header) map[string][]string {
	if len(header) == 0 {
		return nil
	}
	out := make(map[string][]string, len(header))
	for key, values := range header {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func MapToHeader(values map[string][]string) http.Header {
	if len(values) == 0 {
		return nil
	}
	out := make(http.Header, len(values))
	for key, value := range values {
		out[http.CanonicalHeaderKey(key)] = append([]string(nil), value...)
	}
	return out
}

// HTTPDo performs a non-streaming upstream request through the host.
func HTTPDo(callbackID, method, url string, header http.Header, body []byte) (pluginapi.HTTPResponse, error) {
	raw, err := Call(pluginabi.MethodHostHTTPDo, httpRequest{
		HostCallbackID: callbackID,
		Method:         method,
		URL:            url,
		Headers:        HeaderToMap(header),
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

// HTTPDoStream opens a streaming upstream request through the host.
func HTTPDoStream(callbackID, method, url string, header http.Header, body []byte) (StreamResponse, error) {
	raw, err := Call(pluginabi.MethodHostHTTPDoStream, httpRequest{
		HostCallbackID: callbackID,
		Method:         method,
		URL:            url,
		Headers:        HeaderToMap(header),
		Body:           body,
	})
	if err != nil {
		return StreamResponse{}, err
	}
	var resp StreamResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return StreamResponse{}, fmt.Errorf("decode host http stream response: %w", err)
	}
	return resp, nil
}

func HTTPStreamRead(streamID string) (StreamReadResponse, error) {
	raw, err := Call(pluginabi.MethodHostHTTPStreamRead, map[string]string{"stream_id": streamID})
	if err != nil {
		return StreamReadResponse{}, err
	}
	var resp StreamReadResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return StreamReadResponse{}, fmt.Errorf("decode host http stream read: %w", err)
	}
	return resp, nil
}

func HTTPStreamClose(streamID string) {
	if streamID == "" {
		return
	}
	_, _ = Call(pluginabi.MethodHostHTTPStreamClose, map[string]string{"stream_id": streamID})
}
