/*
Copyright 2026 The Kynoproj Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package client

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
)

var (
	// headerName is the subset of RFC 9110 tokens that gRPC metadata also accepts
	// (it lower-cases keys and allows only letters, digits, '-', '_' and '.').
	headerName = regexp.MustCompile(`^[0-9A-Za-z_.-]+$`)
	// headerValue is visible ASCII, space and tab: no control characters, and
	// nothing gRPC metadata would reject.
	headerValue = regexp.MustCompile(`^[\t\x20-\x7e]*$`)
)

// validateHeaders returns an independent copy of headers, or an error for a
// malformed entry.
//
// It fails fast so a bad header never reaches the wire. Errors name the header
// but never echo its value, which may be a secret.
func validateHeaders(headers map[string]string) (map[string]string, error) {
	checked := make(map[string]string, len(headers))
	seen := make(map[string]struct{}, len(headers))
	for name, value := range headers {
		if !headerName.MatchString(name) || strings.HasPrefix(strings.ToLower(name), "grpc-") {
			return nil, fmt.Errorf("kynomesh: header names may contain only letters, digits, '-', '_' and '.', and must not start with \"grpc-\"")
		}
		if !headerValue.MatchString(value) {
			return nil, fmt.Errorf("kynomesh: value of header %q must be printable ASCII", name)
		}
		lower := strings.ToLower(name)
		if _, dup := seen[lower]; dup {
			return nil, fmt.Errorf("kynomesh: header %q is given more than once", name)
		}
		seen[lower] = struct{}{}
		checked[name] = value
	}
	return checked, nil
}

// headerInterceptor adds fixed headers to every call, unless the call already
// sets them, so a per-call header (via a2aclient.AttachServiceParams) wins.
type headerInterceptor struct {
	a2aclient.PassthroughInterceptor
	headers map[string]string
}

func newHeaderInterceptor(headers map[string]string) *headerInterceptor {
	return &headerInterceptor{headers: headers}
}

// Before implements a2aclient.CallInterceptor.
func (h *headerInterceptor) Before(ctx context.Context, req *a2aclient.Request) (context.Context, any, error) {
	if req.ServiceParams == nil {
		req.ServiceParams = a2aclient.ServiceParams{}
	}
	for name, value := range h.headers {
		if !hasParam(req.ServiceParams, name) {
			req.ServiceParams.Append(name, value)
		}
	}
	return ctx, nil, nil
}

// hasParam reports whether params already holds name, ignoring case. It scans the
// keys because ServiceParams.Get only matches lower-case keys, and the a2a SDK
// itself writes some mixed-case ones (for example "Authorization").
func hasParam(params a2aclient.ServiceParams, name string) bool {
	for key, values := range params {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return true
		}
	}
	return false
}

// hostOf returns the lower-cased host of rawURL, which may be host:port
// without a scheme.
func hostOf(rawURL string) string {
	if !strings.Contains(rawURL, "://") {
		rawURL = "//" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// ensureCardStaysOnPeerHost returns an error if card advertises an interface on
// a different host than peerURL.
//
// The card is peer-controlled content. Without this check a misconfigured or
// tampered card could redirect calls, and the headers with them, to a host the
// caller never configured.
func ensureCardStaysOnPeerHost(card *a2a.AgentCard, peerURL, peerName string) error {
	expected := hostOf(peerURL)
	for _, iface := range card.SupportedInterfaces {
		if host := hostOf(iface.URL); host != expected {
			return fmt.Errorf("kynomesh: the card of peer %q advertises an interface on host %q, not %q; refusing to send headers to it",
				peerName, host, expected)
		}
	}
	return nil
}
