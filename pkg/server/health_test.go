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
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// kynoprobe's grpc mode calls Health.Check with the empty service name.
// SDK must answer SERVING regardless of which A2A transports the card
// advertises.
func TestGRPCHealthCheckServingAcrossTransports(t *testing.T) {
	tests := []struct {
		name       string
		transports []a2a.TransportProtocol
	}{
		{"jsonrpc only", []a2a.TransportProtocol{a2a.TransportProtocolJSONRPC}},
		{"rest only", []a2a.TransportProtocol{a2a.TransportProtocolHTTPJSON}},
		{"grpc only", []a2a.TransportProtocol{a2a.TransportProtocolGRPC}},
		{"none", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sock, cancel, done := startWithHealth(t, newCard(tt.transports...), nil)
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Errorf("Start returned: %v", err)
				}
			}()

			status := dialAndCheck(t, sock, "")
			if status != healthpb.HealthCheckResponse_SERVING {
				t.Errorf("grpc health status = %s, want SERVING", status)
			}
		})
	}
}

// kynoprobe's http mode hits /healthz over the UDS and expects 2xx.
func TestHTTPHealthzServing(t *testing.T) {
	sock, cancel, done := startWithHealth(t, newCard(a2a.TransportProtocolJSONRPC), nil)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Start returned: %v", err)
		}
	}()

	resp := httpGetUDS(t, sock, HealthPath)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// SetServing(false) must flip both surfaces before shutdown completes.
func TestHealthSetServingFlipsBothSurfaces(t *testing.T) {
	h := NewHealth()
	sock, cancel, done := startWithHealth(t, newCard(a2a.TransportProtocolJSONRPC), h)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Start returned: %v", err)
		}
	}()

	h.SetServing(false)

	if got := dialAndCheck(t, sock, ""); got != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Errorf("grpc status = %s, want NOT_SERVING", got)
	}
	resp := httpGetUDS(t, sock, HealthPath)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("http status = %d, want 503", resp.StatusCode)
	}
}

func startWithHealth(t *testing.T, card *a2a.AgentCard, h *Health) (sock string, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	sock = shortSock(t)
	redirectServerInfo(t)
	ctx, c := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	opts := []Option{WithAddress(sock), WithShutdownTimeout(2 * time.Second)}
	if h != nil {
		opts = append(opts, WithHealth(h))
	}
	go func() {
		errCh <- Start(ctx, noopExecutor{}, card, opts...)
	}()
	waitForSocket(t, sock, 2*time.Second)
	return sock, c, errCh
}

func dialAndCheck(t *testing.T, sock, service string) healthpb.HealthCheckResponse_ServingStatus {
	t.Helper()
	conn, err := grpc.NewClient(
		"unix://"+sock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: service})
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	return resp.Status
}

func httpGetUDS(t *testing.T, sock, path string) *http.Response {
	t.Helper()
	cl := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	resp, err := cl.Get("http://unix" + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}
