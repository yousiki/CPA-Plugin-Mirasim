// Command mirasim is a CLIProxyAPI standard dynamic library plugin that turns the
// Mirasim (Mirofish) relay into a first-class subscription provider: it owns the
// email + verification-code login flow, keeps the 1-hour access JWT rotated through
// the host's own auto-refresh loop, and executes requests against the relay.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

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
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, err := handleMethod(C.GoString(method), requestBytes)
	if err != nil {
		writeResponse(response, errorEnvelope("plugin_error", err.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	shutdownLoginSessions()
}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if err := configure(request); err != nil {
			return nil, err
		}
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodPluginShutdown:
		shutdownLoginSessions()
		return okEnvelope(struct{}{})

	case pluginabi.MethodAuthIdentifier:
		return okEnvelope(map[string]string{"identifier": pluginID})
	case pluginabi.MethodAuthParse:
		return authParse(request)
	case pluginabi.MethodAuthLoginStart:
		return authLoginStart(request)
	case pluginabi.MethodAuthLoginPoll:
		return authLoginPoll(request)
	case pluginabi.MethodAuthRefresh:
		return authRefresh(request)

	case pluginabi.MethodModelStatic:
		return modelStatic(request)
	case pluginabi.MethodModelForAuth:
		return modelForAuth(request)

	case pluginabi.MethodExecutorIdentifier:
		return okEnvelope(map[string]string{"identifier": pluginID})
	case pluginabi.MethodExecutorExecute:
		return executorExecute(request)
	case pluginabi.MethodExecutorExecuteStream:
		return executorExecuteStream(request)
	case pluginabi.MethodExecutorCountTokens:
		return executorCountTokens(request)
	case pluginabi.MethodExecutorHTTPRequest:
		return executorHTTPRequest(request)

	case pluginabi.MethodManagementRegister:
		return managementRegister(request)
	case pluginabi.MethodManagementHandle:
		return managementHandle(request)

	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
	}
	cfg, err := decodeConfig(req.ConfigYAML)
	if err != nil {
		return err
	}
	currentConfig.Store(cfg)
	return nil
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "Mirasim",
			Version:          pluginVersion,
			Author:           "yousiki",
			GitHubRepository: "https://github.com/yousiki/CPA-mirasim-plugin",
			Logo:             pluginLogo,
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
			// The relay speaks the Anthropic Messages API; the host translates every
			// other client protocol into and out of it, exactly as it does for the
			// built-in Claude executor.
			ExecutorInputFormats:  []string{"claude"},
			ExecutorOutputFormats: []string{"claude"},
			ManagementAPI:         true,
		},
	}
}

// callHost invokes a host callback and unwraps its envelope.
//
// It lives in this file because the `static` helpers it uses are declared in this
// file's cgo preamble; every other file in the plugin is plain Go.
func callHost(method string, payload any) (json.RawMessage, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal host callback %s: %w", method, err)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		cPayload := C.CBytes(rawPayload)
		if cPayload == nil {
			return nil, fmt.Errorf("allocate host callback %s", method)
		}
		defer C.free(cPayload)
		requestPtr = (*C.uint8_t)(cPayload)
	}
	callCode := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawPayload)), &response)
	var rawResponse []byte
	if response.ptr != nil && response.len > 0 {
		rawResponse = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(rawResponse) == 0 {
		return nil, fmt.Errorf("host callback %s returned no response, code=%d", method, int(callCode))
	}

	var env envelope
	if err = json.Unmarshal(rawResponse, &env); err != nil {
		return nil, fmt.Errorf("decode host envelope %s: %w", method, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	if callCode != 0 {
		return nil, fmt.Errorf("host callback %s returned code=%d", method, int(callCode))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}

func okEnvelope(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func errorEnvelopeStatus(code, message string, status int) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message, HTTPStatus: status}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
