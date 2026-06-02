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

// Package serverinfo writes the agent server-info file that the broker
// reads at startup. The file is a JSON document with the protocol, SDK
// language, SDK version, and free-form metadata.
package serverinfo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// DefaultFilePath is the in-pod location the broker reads at startup.
// Keep in sync with kmv1.ServerInfoFilePath in kynoproj/kynomesh.
const DefaultFilePath = "/var/run/kynomesh/server-info"

// sdkModulePath identifies this SDK in build info Deps. Matched by
// suffix so forks and replace directives still resolve.
const sdkModulePath = "/kynomesh-go"

type Language string

const (
	Go Language = "go"
)

type Protocol string

const (
	UDS Protocol = "uds"
	TCP Protocol = "tcp"
)

// ServerInfo is the information about the agent server that the broker
// consumes at startup. Field tags match the broker's definition in
// kynoproj/kynomesh pkg/broker/serverinfo.
type ServerInfo struct {
	Protocol Protocol          `json:"protocol"`
	Language Language          `json:"language"`
	Version  string            `json:"version"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// readBuildInfo is a test seam over debug.ReadBuildInfo.
var readBuildInfo = debug.ReadBuildInfo

// SDKVersion returns the version of this SDK as recorded in the build
// info. Empty when the SDK is the main module (e.g. running tests in
// this repo) or build info is unavailable.
func SDKVersion() string {
	info, ok := readBuildInfo()
	if !ok {
		return ""
	}
	for _, d := range info.Deps {
		if strings.HasSuffix(d.Path, sdkModulePath) {
			return d.Version
		}
	}
	return ""
}

// Default returns a ServerInfo populated with this SDK's language and
// version. Callers set Protocol based on how they started the server.
func Default() ServerInfo {
	return ServerInfo{
		Language: Go,
		Version:  SDKVersion(),
	}
}

// Write serializes info as JSON and writes it atomically to path. The
// parent directory is created if missing. Atomic write avoids the broker
// reading a half-written file.
func Write(path string, info ServerInfo) error {
	if path == "" {
		return fmt.Errorf("serverinfo: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create server-info dir: %w", err)
	}
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("encode server-info: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".server-info-*")
	if err != nil {
		return fmt.Errorf("create server-info tempfile: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write server-info tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close server-info tempfile: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod server-info tempfile: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename server-info: %w", err)
	}
	return nil
}
