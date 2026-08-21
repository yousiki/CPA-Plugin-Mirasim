// Package executor implements the executor capability: forward Anthropic Messages
// traffic to the Mirasim relay.
//
// The relay is an Anthropic-compatible gateway (LiteLLM in front of Bedrock), so the
// plugin declares `claude` as both its input and output format and the host translates
// every other client protocol into and out of it - exactly what it does for the built-in
// Claude executor.
package executor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/credential"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/hostapi"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/relaysig"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/rpc"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/textutil"
)

type rpcRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcStreamResponse struct {
	Headers http.Header                     `json:"headers,omitempty"`
	Chunks  []pluginapi.ExecutorStreamChunk `json:"chunks,omitempty"`
}

// maxStreamErrorBody bounds how much of a failed streaming response is read back for the
// error message.
const maxStreamErrorBody = 64 * 1024

func decodeRequest(request []byte) (rpcRequest, error) {
	var req rpcRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return rpcRequest{}, err
	}
	return req, nil
}

// accessToken pulls the current access JWT out of the credential the host selected.
func accessToken(storageJSON []byte) (string, error) {
	stored, err := credential.Decode(storageJSON)
	if err != nil {
		return "", fmt.Errorf("decode %s credential: %w", config.PluginID, err)
	}
	token := stored.AccessToken()
	if token == "" {
		return "", fmt.Errorf("%s credential has no access_token", config.PluginID)
	}
	return token, nil
}

// upstreamHeaders builds the request headers for the relay.
//
// Only the two Anthropic negotiation headers are forwarded from the client; everything
// else is constructed. The configured beta is appended rather than replacing the
// caller's, so a client asking for prompt caching keeps it while still getting the 1M
// context window opt-in.
func upstreamHeaders(cfg config.Config, req pluginapi.ExecutorRequest, token string, stream bool) http.Header {
	header := http.Header{
		"Content-Type":  []string{"application/json"},
		"Authorization": []string{"Bearer " + token},
	}
	version := strings.TrimSpace(req.Headers.Get("Anthropic-Version"))
	if version == "" {
		version = "2023-06-01"
	}
	header.Set("Anthropic-Version", version)

	betas := make([]string, 0, 4)
	seen := make(map[string]bool)
	appendBeta := func(value string) {
		for _, beta := range strings.Split(value, ",") {
			if beta = strings.TrimSpace(beta); beta != "" && !seen[beta] {
				seen[beta] = true
				betas = append(betas, beta)
			}
		}
	}
	for _, value := range req.Headers.Values("Anthropic-Beta") {
		appendBeta(value)
	}
	appendBeta(cfg.ContextBeta)
	if len(betas) > 0 {
		header.Set("Anthropic-Beta", strings.Join(betas, ","))
	}

	if stream {
		header.Set("Accept", "text/event-stream")
	} else {
		header.Set("Accept", "application/json")
	}
	return header
}

func messagesURL(cfg config.Config, suffix string) string {
	return cfg.RelayURL + "/v1/messages" + suffix
}

// relayHeaders builds the upstream headers and, when a device session is live, upgrades
// them to a signed request carrying the relay ticket instead of the account JWT.
//
// The second return value says whether the request went out signed, which is what makes a
// 401 actionable: a rejected ticket has to be dropped and re-minted, while a 401 on an
// unsigned request is just a bad credential.
func relayHeaders(cfg config.Config, req pluginapi.ExecutorRequest, token, method, url string, body []byte, stream bool) (http.Header, bool) {
	header := upstreamHeaders(cfg, req, token, stream)
	signed := relaysig.Sign(cfg, header, relaysig.Request{
		Token:  token,
		Method: method,
		URL:    url,
		Body:   body,
	})
	return header, signed
}

// noteRelayStatus feeds an upstream status back into the device-session state machine.
func noteRelayStatus(cfg config.Config, token string, signed bool, status int) {
	if signed && status == http.StatusUnauthorized {
		relaysig.Refused(cfg, token)
	}
}

// -- executor.execute ---------------------------------------------------------

func Execute(request []byte) ([]byte, error) {
	req, err := decodeRequest(request)
	if err != nil {
		return nil, err
	}
	cfg := config.Loaded()
	token, err := accessToken(req.StorageJSON)
	if err != nil {
		return rpc.ErrorStatus("invalid_credential", err.Error(), http.StatusUnauthorized), nil
	}

	target := messagesURL(cfg, "")
	header, signed := relayHeaders(cfg, req.ExecutorRequest, token, http.MethodPost, target, req.Payload, false)
	resp, err := hostapi.HTTPDo(req.HostCallbackID, http.MethodPost, target, header, req.Payload)
	if err != nil {
		return nil, err
	}
	noteRelayStatus(cfg, token, signed, resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return upstreamErrorEnvelope(resp.StatusCode, resp.Body), nil
	}
	return rpc.OK(pluginapi.ExecutorResponse{Payload: resp.Body, Headers: resp.Headers})
}

// -- executor.count_tokens ----------------------------------------------------

func CountTokens(request []byte) ([]byte, error) {
	req, err := decodeRequest(request)
	if err != nil {
		return nil, err
	}
	cfg := config.Loaded()
	token, err := accessToken(req.StorageJSON)
	if err != nil {
		return rpc.ErrorStatus("invalid_credential", err.Error(), http.StatusUnauthorized), nil
	}

	target := messagesURL(cfg, "/count_tokens")
	header, signed := relayHeaders(cfg, req.ExecutorRequest, token, http.MethodPost, target, req.Payload, false)
	resp, err := hostapi.HTTPDo(req.HostCallbackID, http.MethodPost, target, header, req.Payload)
	if err != nil {
		return nil, err
	}
	noteRelayStatus(cfg, token, signed, resp.StatusCode)
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		return rpc.OK(pluginapi.ExecutorResponse{Payload: resp.Body, Headers: resp.Headers})
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
		// The gateway does not expose count_tokens. Answering with an estimate keeps
		// clients that budget context (claude-code does) working instead of failing the
		// whole turn on a metadata call.
		estimate := len(req.Payload) / 4
		payload := fmt.Appendf(nil, `{"input_tokens":%d}`, estimate)
		return rpc.OK(pluginapi.ExecutorResponse{
			Payload: payload,
			Headers: http.Header{"Content-Type": []string{"application/json"}},
		})
	default:
		return upstreamErrorEnvelope(resp.StatusCode, resp.Body), nil
	}
}

// -- executor.execute_stream --------------------------------------------------

func ExecuteStream(request []byte) ([]byte, error) {
	req, err := decodeRequest(request)
	if err != nil {
		return nil, err
	}
	cfg := config.Loaded()
	token, err := accessToken(req.StorageJSON)
	if err != nil {
		return rpc.ErrorStatus("invalid_credential", err.Error(), http.StatusUnauthorized), nil
	}

	target := messagesURL(cfg, "")
	header, signed := relayHeaders(cfg, req.ExecutorRequest, token, http.MethodPost, target, req.Payload, true)
	stream, err := hostapi.HTTPDoStream(req.HostCallbackID, http.MethodPost, target, header, req.Payload)
	if err != nil {
		return nil, err
	}
	noteRelayStatus(cfg, token, signed, stream.StatusCode)
	responseHeaders := hostapi.MapToHeader(stream.Headers)

	if stream.StatusCode < 200 || stream.StatusCode > 299 {
		body := drainStream(stream, maxStreamErrorBody)
		return upstreamErrorEnvelope(stream.StatusCode, body), nil
	}

	// Either the upstream response is already complete, or the host opened no executor
	// stream for us to emit into. Both mean there is nothing to pump asynchronously, so
	// buffer the body and return the events inline.
	if stream.StreamID == "" || req.StreamID == "" {
		events := splitSSEEvents(drainStream(stream, 0), true)
		chunks := make([]pluginapi.ExecutorStreamChunk, 0, len(events))
		for _, event := range events {
			chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: event})
		}
		return rpc.OK(rpcStreamResponse{Headers: responseHeaders, Chunks: chunks})
	}

	go relayStream(stream.StreamID, req.StreamID)
	return rpc.OK(rpcStreamResponse{Headers: responseHeaders})
}

// relayStream pumps the upstream response into the executor stream the host opened.
//
// Chunks are re-framed into complete SSE events because that is what the host's
// translation layer expects from a Claude-format executor; the HTTP bridge hands back
// arbitrary byte blocks.
func relayStream(upstreamID, executorID string) {
	defer hostapi.HTTPStreamClose(upstreamID)

	var buffer []byte
	failure := ""
	for {
		chunk, err := hostapi.HTTPStreamRead(upstreamID)
		if err != nil {
			failure = err.Error()
			break
		}
		if chunk.Error != "" {
			failure = chunk.Error
			break
		}
		if len(chunk.Payload) > 0 {
			buffer = append(buffer, chunk.Payload...)
			var events [][]byte
			events, buffer = takeSSEEvents(buffer)
			for _, event := range events {
				if errEmit := hostapi.StreamEmit(executorID, event); errEmit != nil {
					// The consumer is gone (client disconnect); stop pulling upstream.
					hostapi.StreamClose(executorID, "")
					return
				}
			}
		}
		if chunk.Done {
			break
		}
	}
	if failure == "" {
		for _, event := range splitSSEEvents(buffer, true) {
			if errEmit := hostapi.StreamEmit(executorID, event); errEmit != nil {
				break
			}
		}
	}
	hostapi.StreamClose(executorID, failure)
}

// drainStream reads the rest of an upstream response into memory. A limit of 0 reads
// everything; a positive limit stops early, which is enough for an error body.
func drainStream(stream hostapi.StreamResponse, limit int) []byte {
	body := make([]byte, 0, 4096)
	for _, chunk := range stream.Chunks {
		body = append(body, chunk.Payload...)
	}
	if stream.StreamID == "" {
		return body
	}
	defer hostapi.HTTPStreamClose(stream.StreamID)
	for {
		if limit > 0 && len(body) >= limit {
			return body
		}
		chunk, err := hostapi.HTTPStreamRead(stream.StreamID)
		if err != nil || chunk.Error != "" {
			return body
		}
		body = append(body, chunk.Payload...)
		if chunk.Done {
			return body
		}
	}
}

// -- executor.http_request ----------------------------------------------------

func HTTPRequest(_ []byte) ([]byte, error) {
	// Provider-specific passthrough endpoints are not part of the relay contract; the
	// Messages API and its count_tokens sibling are handled above.
	return rpc.ErrorStatus("not_supported",
		config.PluginID+" executor does not serve passthrough HTTP requests", http.StatusNotImplemented), nil
}

func upstreamErrorEnvelope(status int, body []byte) []byte {
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(status)
	}
	return rpc.ErrorStatus("upstream_error",
		fmt.Sprintf("%s relay responded %d: %s", config.PluginID, status, textutil.Truncate(message, 2000)), status)
}
