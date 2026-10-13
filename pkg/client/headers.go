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
	"fmt"
	"regexp"
	"strings"
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
