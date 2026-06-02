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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

type noopExecutor struct{}

func (noopExecutor) Execute(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {}
}

func (noopExecutor) Cancel(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {}
}

func newCard(transports ...a2a.TransportProtocol) *a2a.AgentCard {
	ifaces := make([]*a2a.AgentInterface, 0, len(transports))
	for _, tp := range transports {
		ifaces = append(ifaces, a2a.NewAgentInterface("http://placeholder", tp))
	}
	return &a2a.AgentCard{
		Name:                "test-agent",
		Version:             "0.0.1",
		SupportedInterfaces: ifaces,
	}
}

func startTestServer(t *testing.T, card *a2a.AgentCard) (client *http.Client, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	sock := shortSock(t)
	redirectServerInfo(t)
	ctx, c := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Start(ctx, noopExecutor{}, card, WithAddress(sock), WithShutdownTimeout(2*time.Second))
	}()
	waitForSocket(t, sock, 2*time.Second)
	cl := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	return cl, c, errCh
}

// macOS sun_path is capped at ~104 chars; t.TempDir paths exceed it.
func shortSock(t *testing.T) string {
	t.Helper()
	p := filepath.Join(os.TempDir(), fmt.Sprintf("km-%d.sock", time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

// redirectServerInfo points serverInfoPath at a temp file so tests don't
// touch /var/run/kynomesh on dev machines.
func redirectServerInfo(t *testing.T) {
	t.Helper()
	prev := serverInfoPath
	serverInfoPath = filepath.Join(t.TempDir(), "server-info")
	t.Cleanup(func() { serverInfoPath = prev })
}

func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", path); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s never became reachable", path)
}

func TestStartRejectsMissingDeps(t *testing.T) {
	if err := Start(context.Background(), nil, newCard()); err == nil {
		t.Error("nil executor should be rejected")
	}
	if err := Start(context.Background(), noopExecutor{}, nil); err == nil {
		t.Error("nil card should be rejected")
	}
}

func TestStartServesAgentCard(t *testing.T) {
	card := newCard(a2a.TransportProtocolJSONRPC)
	cl, cancel, done := startTestServer(t, card)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	}()

	resp, err := cl.Get("http://unix" + a2asrv.WellKnownAgentCardPath)
	if err != nil {
		t.Fatalf("GET agent card: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var got a2a.AgentCard
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "test-agent" {
		t.Errorf("card name = %q, want test-agent", got.Name)
	}
}

func TestStartMountsTransportsListedInCard(t *testing.T) {
	// An empty POST to a mounted JSON-RPC handler returns 400; an
	// unmounted path returns 404.
	tests := []struct {
		name         string
		transports   []a2a.TransportProtocol
		jsonRPCFound bool
	}{
		{"jsonrpc only", []a2a.TransportProtocol{a2a.TransportProtocolJSONRPC}, true},
		{"rest only", []a2a.TransportProtocol{a2a.TransportProtocolHTTPJSON}, false},
		{"both http transports", []a2a.TransportProtocol{a2a.TransportProtocolJSONRPC, a2a.TransportProtocolHTTPJSON}, true},
		{"none", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, cancel, done := startTestServer(t, newCard(tt.transports...))
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Errorf("Start returned error: %v", err)
				}
			}()

			resp, err := cl.Post("http://unix"+jsonrpcPath, "application/json", http.NoBody)
			if err != nil {
				t.Fatalf("POST jsonrpc: %v", err)
			}
			_ = resp.Body.Close()
			gotFound := resp.StatusCode != http.StatusNotFound
			if gotFound != tt.jsonRPCFound {
				t.Errorf("JSON-RPC mounted = %v (status %d), want %v", gotFound, resp.StatusCode, tt.jsonRPCFound)
			}
		})
	}
}

func TestStartShutsDownOnContextCancel(t *testing.T) {
	cl, cancel, done := startTestServer(t, newCard(a2a.TransportProtocolJSONRPC))

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within shutdown timeout")
	}

	if _, err := cl.Get("http://unix" + a2asrv.WellKnownAgentCardPath); err == nil {
		t.Error("expected request to fail after shutdown")
	}
}
