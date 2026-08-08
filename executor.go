package main

// The executor capability: forward Anthropic Messages traffic to the Mirasim relay.
//
// The relay is an Anthropic-compatible gateway (LiteLLM in front of Bedrock), so the
// plugin declares `claude` as both its input and output format and the host translates
// every other client protocol into and out of it - exactly what it does for the
// built-in Claude executor.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcExecutorStreamResponse struct {
	Headers http.Header                     `json:"headers,omitempty"`
	Chunks  []pluginapi.ExecutorStreamChunk `json:"chunks,omitempty"`
}

// maxStreamErrorBody bounds how much of a failed streaming response is read back for
// the error message.
const maxStreamErrorBody = 64 * 1024

func decodeExecutorRequest(request []byte) (rpcExecutorRequest, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return rpcExecutorRequest{}, err
	}
	return req, nil
}

// accessToken pulls the current access JWT out of the credential the host selected.
func accessToken(storageJSON []byte) (string, error) {
	var stored map[string]any
	if err := json.Unmarshal(storageJSON, &stored); err != nil {
		return "", fmt.Errorf("decode %s credential: %w", pluginID, err)
	}
	token := stringField(stored, "access_token")
	if token == "" {
		return "", fmt.Errorf("%s credential has no access_token", pluginID)
	}
	return token, nil
}

// upstreamHeaders builds the request headers for the relay.
//
// Only the two Anthropic negotiation headers are forwarded from the client; everything
// else is constructed. The configured beta is appended rather than replacing the
// caller's, so a client asking for prompt caching keeps it while still getting the 1M
// context window opt-in.
func upstreamHeaders(cfg pluginConfig, req pluginapi.ExecutorRequest, token string, stream bool) http.Header {
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

func messagesURL(cfg pluginConfig, suffix string) string {
	return cfg.RelayURL + "/v1/messages" + suffix
}

// -- executor.execute ---------------------------------------------------------

func executorExecute(request []byte) ([]byte, error) {
	req, err := decodeExecutorRequest(request)
	if err != nil {
		return nil, err
	}
	cfg := loadedConfig()
	token, err := accessToken(req.StorageJSON)
	if err != nil {
		return errorEnvelopeStatus("invalid_credential", err.Error(), http.StatusUnauthorized), nil
	}

	resp, err := hostHTTPDo(req.HostCallbackID, http.MethodPost, messagesURL(cfg, ""),
		upstreamHeaders(cfg, req.ExecutorRequest, token, false), req.Payload)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return upstreamErrorEnvelope(resp.StatusCode, resp.Body), nil
	}
	return okEnvelope(pluginapi.ExecutorResponse{Payload: resp.Body, Headers: resp.Headers})
}

// -- executor.count_tokens ----------------------------------------------------

func executorCountTokens(request []byte) ([]byte, error) {
	req, err := decodeExecutorRequest(request)
	if err != nil {
		return nil, err
	}
	cfg := loadedConfig()
	token, err := accessToken(req.StorageJSON)
	if err != nil {
		return errorEnvelopeStatus("invalid_credential", err.Error(), http.StatusUnauthorized), nil
	}

	resp, err := hostHTTPDo(req.HostCallbackID, http.MethodPost, messagesURL(cfg, "/count_tokens"),
		upstreamHeaders(cfg, req.ExecutorRequest, token, false), req.Payload)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		return okEnvelope(pluginapi.ExecutorResponse{Payload: resp.Body, Headers: resp.Headers})
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
		// The gateway does not expose count_tokens. Answering with an estimate keeps
		// clients that budget context (claude-code does) working instead of failing the
		// whole turn on a metadata call.
		estimate := len(req.Payload) / 4
		payload := fmt.Appendf(nil, `{"input_tokens":%d}`, estimate)
		return okEnvelope(pluginapi.ExecutorResponse{
			Payload: payload,
			Headers: http.Header{"Content-Type": []string{"application/json"}},
		})
	default:
		return upstreamErrorEnvelope(resp.StatusCode, resp.Body), nil
	}
}

// -- executor.execute_stream --------------------------------------------------

func executorExecuteStream(request []byte) ([]byte, error) {
	req, err := decodeExecutorRequest(request)
	if err != nil {
		return nil, err
	}
	cfg := loadedConfig()
	token, err := accessToken(req.StorageJSON)
	if err != nil {
		return errorEnvelopeStatus("invalid_credential", err.Error(), http.StatusUnauthorized), nil
	}

	stream, err := hostHTTPDoStream(req.HostCallbackID, http.MethodPost, messagesURL(cfg, ""),
		upstreamHeaders(cfg, req.ExecutorRequest, token, true), req.Payload)
	if err != nil {
		return nil, err
	}
	responseHeaders := mapToHeader(stream.Headers)

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
		return okEnvelope(rpcExecutorStreamResponse{Headers: responseHeaders, Chunks: chunks})
	}

	go relayStream(stream.StreamID, req.StreamID)
	return okEnvelope(rpcExecutorStreamResponse{Headers: responseHeaders})
}

// relayStream pumps the upstream response into the executor stream the host opened.
//
// Chunks are re-framed into complete SSE events because that is what the host's
// translation layer expects from a Claude-format executor; the HTTP bridge hands back
// arbitrary byte blocks.
func relayStream(upstreamID, executorID string) {
	defer hostHTTPStreamClose(upstreamID)

	var buffer []byte
	failure := ""
	for {
		chunk, err := hostHTTPStreamRead(upstreamID)
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
				if errEmit := hostStreamEmit(executorID, event); errEmit != nil {
					// The consumer is gone (client disconnect); stop pulling upstream.
					hostStreamClose(executorID, "")
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
			if errEmit := hostStreamEmit(executorID, event); errEmit != nil {
				break
			}
		}
	}
	hostStreamClose(executorID, failure)
}

// drainStream reads the rest of an upstream response into memory. A limit of 0 reads
// everything; a positive limit stops early, which is enough for an error body.
func drainStream(stream hostHTTPStreamResponse, limit int) []byte {
	body := make([]byte, 0, 4096)
	for _, chunk := range stream.Chunks {
		body = append(body, chunk.Payload...)
	}
	if stream.StreamID == "" {
		return body
	}
	defer hostHTTPStreamClose(stream.StreamID)
	for {
		if limit > 0 && len(body) >= limit {
			return body
		}
		chunk, err := hostHTTPStreamRead(stream.StreamID)
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

func executorHTTPRequest(request []byte) ([]byte, error) {
	// Provider-specific passthrough endpoints are not part of the relay contract; the
	// Messages API and its count_tokens sibling are handled above.
	return errorEnvelopeStatus("not_supported",
		pluginID+" executor does not serve passthrough HTTP requests", http.StatusNotImplemented), nil
}

// -- SSE framing --------------------------------------------------------------

// takeSSEEvents splits off every complete event, returning the unconsumed tail.
//
// An event ends at a blank line. Lines are normalised to \n and each emitted event
// keeps its terminating blank line, matching what the native Claude executor produces.
func takeSSEEvents(buffer []byte) ([][]byte, []byte) {
	events := make([][]byte, 0, 4)
	for {
		end := indexAfterBlankLine(buffer)
		if end < 0 {
			return events, buffer
		}
		events = append(events, normalizeSSEEvent(buffer[:end]))
		buffer = bytes.Clone(buffer[end:])
	}
}

// splitSSEEvents frames a complete body. When flush is set, a trailing partial event is
// emitted as well rather than dropped.
func splitSSEEvents(body []byte, flush bool) [][]byte {
	events, tail := takeSSEEvents(body)
	if flush && len(bytes.TrimSpace(tail)) > 0 {
		events = append(events, normalizeSSEEvent(tail))
	}
	return events
}

// indexAfterBlankLine finds the first event terminator, returning the offset just past
// it, or -1 when the buffer holds no complete event yet.
func indexAfterBlankLine(buffer []byte) int {
	for offset := 0; offset < len(buffer); offset++ {
		if buffer[offset] != '\n' {
			continue
		}
		rest := buffer[offset+1:]
		switch {
		case len(rest) >= 2 && rest[0] == '\r' && rest[1] == '\n':
			return offset + 3
		case len(rest) >= 1 && rest[0] == '\n':
			return offset + 2
		}
	}
	return -1
}

func normalizeSSEEvent(event []byte) []byte {
	normalized := bytes.ReplaceAll(event, []byte("\r\n"), []byte("\n"))
	if !bytes.HasSuffix(normalized, []byte("\n\n")) {
		if !bytes.HasSuffix(normalized, []byte("\n")) {
			normalized = append(normalized, '\n')
		}
		normalized = append(normalized, '\n')
	}
	return normalized
}

func upstreamErrorEnvelope(status int, body []byte) []byte {
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(status)
	}
	return errorEnvelopeStatus("upstream_error",
		fmt.Sprintf("%s relay responded %d: %s", pluginID, status, truncate(message, 2000)), status)
}
