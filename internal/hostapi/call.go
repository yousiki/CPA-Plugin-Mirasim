// Package hostapi holds typed wrappers over the host callback bridge. The host performs
// every real HTTP request so proxy handling, transport policy, auth context and request
// logging stay under its control; plugins must not dial out themselves.
//
// The bridge itself lives in cmd/mirasim, because it calls the C statics declared in that
// file's cgo preamble. It is injected here with SetCall, which keeps every package in
// internal/ free of cgo and therefore testable with plain `go test`.
package hostapi

import (
	"encoding/json"
	"errors"
	"sync/atomic"
)

// Caller invokes a host callback and returns its unwrapped result.
type Caller func(method string, payload any) (json.RawMessage, error)

var caller atomic.Pointer[Caller]

// ErrNoCaller means the ABI bridge was never wired. It can only happen in a test binary
// or if cmd/mirasim stops calling SetCall.
var ErrNoCaller = errors.New("hostapi: no host callback bridge installed")

// SetCall installs the bridge. cmd/mirasim does this from an init function, so it is in
// place before the host makes its first call.
func SetCall(fn Caller) {
	if fn == nil {
		caller.Store(nil)
		return
	}
	caller.Store(&fn)
}

// Call invokes a host callback by method name.
func Call(method string, payload any) (json.RawMessage, error) {
	fn := caller.Load()
	if fn == nil {
		return nil, ErrNoCaller
	}
	return (*fn)(method, payload)
}
