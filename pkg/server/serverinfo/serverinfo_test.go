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

package serverinfo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

func TestWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server-info")
	want := ServerInfo{
		Protocol: UDS,
		Language: Go,
		Version:  "v1.2.3",
		Metadata: map[string]string{"sdk": "kynomesh-go"},
	}
	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got ServerInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Protocol != want.Protocol || got.Language != want.Language || got.Version != want.Version {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
	if got.Metadata["sdk"] != "kynomesh-go" {
		t.Errorf("metadata not preserved: %+v", got.Metadata)
	}
}

func TestWriteCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "server-info")
	if err := Write(path, ServerInfo{Protocol: UDS, Language: Go}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestWriteReplacesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server-info")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := Write(path, ServerInfo{Protocol: UDS, Language: Go, Version: "fresh"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got ServerInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal (file content %q): %v", string(data), err)
	}
	if got.Version != "fresh" {
		t.Errorf("Version = %q, want fresh", got.Version)
	}
}

func TestWriteOmitsEmptyMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server-info")
	if err := Write(path, ServerInfo{Protocol: UDS, Language: Go, Version: "v1"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != `{"protocol":"uds","language":"go","version":"v1"}` {
		t.Errorf("payload = %q", string(data))
	}
}

func TestWriteRejectsEmptyPath(t *testing.T) {
	if err := Write("", ServerInfo{}); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestSDKVersionFromBuildInfo(t *testing.T) {
	prev := readBuildInfo
	t.Cleanup(func() { readBuildInfo = prev })

	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Deps: []*debug.Module{
				{Path: "example.com/unrelated", Version: "v9.9.9"},
				{Path: "github.com/kynoproj/kynomesh-go", Version: "v0.4.2"},
			},
		}, true
	}
	if got := SDKVersion(); got != "v0.4.2" {
		t.Errorf("SDKVersion = %q, want v0.4.2", got)
	}
}

func TestSDKVersionMissingDepReturnsEmpty(t *testing.T) {
	prev := readBuildInfo
	t.Cleanup(func() { readBuildInfo = prev })

	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Deps: []*debug.Module{{Path: "example.com/other", Version: "v1"}}}, true
	}
	if got := SDKVersion(); got != "" {
		t.Errorf("SDKVersion = %q, want empty", got)
	}
}

func TestSDKVersionNoBuildInfoReturnsEmpty(t *testing.T) {
	prev := readBuildInfo
	t.Cleanup(func() { readBuildInfo = prev })

	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	if got := SDKVersion(); got != "" {
		t.Errorf("SDKVersion = %q, want empty", got)
	}
}

func TestDefaultPopulatesGoLanguage(t *testing.T) {
	got := Default()
	if got.Language != Go {
		t.Errorf("Language = %q, want %q", got.Language, Go)
	}
}
