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

package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kynoproj/kynomesh-go/pkg/server/serverinfo"
)

// macOS sun_path is capped at ~104 chars; t.TempDir paths exceed it.
func shortSockPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(os.TempDir(), fmt.Sprintf("km-ln-%d.sock", time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

func TestResolveListener(t *testing.T) {
	tests := []struct {
		name     string
		inPod    bool
		opts     options
		wantNet  string
		wantAddr string
	}{
		{
			name:     "in-pod defaults to UDS at broker socket path",
			inPod:    true,
			wantNet:  "unix",
			wantAddr: brokerSocketPath,
		},
		{
			name:     "outside pod defaults to local TCP",
			inPod:    false,
			wantNet:  "tcp",
			wantAddr: defaultLocalAddr,
		},
		{
			name:     "explicit absolute path wins as UDS",
			inPod:    false,
			opts:     options{address: "/tmp/custom.sock"},
			wantNet:  "unix",
			wantAddr: "/tmp/custom.sock",
		},
		{
			name:     "explicit host:port wins as TCP even inside pod",
			inPod:    true,
			opts:     options{address: "0.0.0.0:9999"},
			wantNet:  "tcp",
			wantAddr: "0.0.0.0:9999",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := listenMode
			listenMode = func() bool { return tt.inPod }
			defer func() { listenMode = prev }()

			got := resolveListener(tt.opts)
			if got.network != tt.wantNet || got.address != tt.wantAddr {
				t.Errorf("resolveListener = (%s, %s), want (%s, %s)",
					got.network, got.address, tt.wantNet, tt.wantAddr)
			}
		})
	}
}

func TestNewListenerUDSRemovesStaleSocket(t *testing.T) {
	sock := shortSockPath(t)
	if err := os.WriteFile(sock, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed stale socket: %v", err)
	}

	ln, err := newListener(listenerConfig{network: "unix", address: sock})
	if err != nil {
		t.Fatalf("newListener: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("expected socket file, got mode %v", info.Mode())
	}
}

func TestWriteServerInfoUDSEmitsProtocol(t *testing.T) {
	dir := t.TempDir()
	prev := serverInfoPath
	serverInfoPath = filepath.Join(dir, "server-info")
	t.Cleanup(func() { serverInfoPath = prev })

	if err := writeServerInfo(listenerConfig{network: "unix", address: "/tmp/x.sock"}); err != nil {
		t.Fatalf("writeServerInfo: %v", err)
	}
	data, err := os.ReadFile(serverInfoPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got serverinfo.ServerInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Protocol != serverinfo.UDS {
		t.Errorf("Protocol = %q, want uds", got.Protocol)
	}
	if got.Language != serverinfo.Go {
		t.Errorf("Language = %q, want go", got.Language)
	}
}

func TestNewListenerTCP(t *testing.T) {
	ln, err := newListener(listenerConfig{network: "tcp", address: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("newListener: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if ln.Addr().Network() != "tcp" {
		t.Errorf("network = %q, want tcp", ln.Addr().Network())
	}
}
