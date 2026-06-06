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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// fakeAgentTLS is like fakeAgent but serves over HTTPS with a self-signed
// cert, so a TLS-verifying client will reject the connection while a
// TLS-skipping client will accept it.
func fakeAgentTLS(t *testing.T, name string, transports ...a2a.TransportProtocol) *httptest.Server {
	t.Helper()
	if len(transports) == 0 {
		transports = []a2a.TransportProtocol{a2a.TransportProtocolJSONRPC}
	}
	ifaces := make([]*a2a.AgentInterface, 0, len(transports))
	for _, tp := range transports {
		ifaces = append(ifaces, a2a.NewAgentInterface("https://placeholder", tp))
	}
	card := &a2a.AgentCard{
		Name:                name,
		Version:             "0.0.1",
		SupportedInterfaces: ifaces,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(a2asrv.WellKnownAgentCardPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fakeAgent serves a well-known AgentCard so the resolver succeeds.
// Transports default to JSON-RPC if none are provided.
func fakeAgent(t *testing.T, name string, transports ...a2a.TransportProtocol) *httptest.Server {
	t.Helper()
	if len(transports) == 0 {
		transports = []a2a.TransportProtocol{a2a.TransportProtocolJSONRPC}
	}
	ifaces := make([]*a2a.AgentInterface, 0, len(transports))
	for _, tp := range transports {
		ifaces = append(ifaces, a2a.NewAgentInterface("http://placeholder", tp))
	}
	card := &a2a.AgentCard{
		Name:                name,
		Version:             "0.0.1",
		SupportedInterfaces: ifaces,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(a2asrv.WellKnownAgentCardPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeTopologyWithURL(t *testing.T, peerName, url string) {
	t.Helper()
	writeTopologyWithKind(t, peerName, "Managed", url)
}

func writeTopologyWithKind(t *testing.T, peerName, kind, url string) {
	t.Helper()
	payload := fmt.Sprintf(`{"peers":[{"name":%q,"kind":%q,"url":%q}]}`, peerName, kind, url)
	path := filepath.Join(t.TempDir(), "topology.json")
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("seed topology: %v", err)
	}
	prev := topologyPath
	topologyPath = path
	resetTopologyCache()
	t.Cleanup(func() {
		topologyPath = prev
		resetTopologyCache()
	})
}

func TestResolveAgentCardFetchesCardForPeer(t *testing.T) {
	srv := fakeAgent(t, "worker-a")
	writeTopologyWithURL(t, "worker-a", srv.URL)

	card, err := ResolveAgentCard(context.Background(), "worker-a")
	if err != nil {
		t.Fatalf("ResolveAgentCard: %v", err)
	}
	if card.Name != "worker-a" {
		t.Errorf("card.Name = %q, want worker-a", card.Name)
	}
}

func TestResolveAgentCardUnknownPeer(t *testing.T) {
	writeTopologyWithURL(t, "worker-a", "http://example.invalid")
	_, err := ResolveAgentCard(context.Background(), "worker-b")
	if !errors.Is(err, ErrPeerNotFound) {
		t.Errorf("err = %v, want ErrPeerNotFound", err)
	}
}

func TestNewForPeerReturnsUsableClient(t *testing.T) {
	srv := fakeAgent(t, "worker-a")
	writeTopologyWithURL(t, "worker-a", srv.URL)

	c, err := NewForPeer(context.Background(), "worker-a")
	if err != nil {
		t.Fatalf("NewForPeer: %v", err)
	}
	if c == nil {
		t.Fatal("client is nil")
	}
}

func TestNewForPeerPropagatesPeerNotFound(t *testing.T) {
	writeTopologyWithURL(t, "worker-a", "http://example.invalid")
	_, err := NewForPeer(context.Background(), "worker-b")
	if !errors.Is(err, ErrPeerNotFound) {
		t.Errorf("err = %v, want ErrPeerNotFound", err)
	}
}

// Managed peers get an insecure gRPC transport by default, so building
// a client against a card that only advertises gRPC succeeds without
// the caller passing any transport options.
func TestNewForPeerManagedDefaultsInsecureGRPC(t *testing.T) {
	srv := fakeAgent(t, "worker-a", a2a.TransportProtocolGRPC)
	writeTopologyWithKind(t, "worker-a", "Managed", srv.URL)

	c, err := NewForPeer(context.Background(), "worker-a")
	if err != nil {
		t.Fatalf("NewForPeer: %v", err)
	}
	if c == nil {
		t.Fatal("client is nil")
	}
}

// External peers do not get a default transport: if the user does not
// supply one, building a client against a gRPC-only card must fail.
// This guards against silently sending unauthenticated traffic outside
// the cluster.
func TestNewForPeerExternalNoDefaultTransport(t *testing.T) {
	srv := fakeAgent(t, "third-party", a2a.TransportProtocolGRPC)
	writeTopologyWithKind(t, "third-party", "External", srv.URL)

	if _, err := NewForPeer(context.Background(), "third-party"); err == nil {
		t.Error("expected error for External peer with no transport options supplied")
	}
}

// Managed peers fetch their AgentCard with TLS verification disabled,
// so a self-signed broker cert is accepted.
func TestResolveAgentCardManagedSkipsTLSVerify(t *testing.T) {
	srv := fakeAgentTLS(t, "worker-a")
	writeTopologyWithKind(t, "worker-a", "Managed", srv.URL)

	card, err := ResolveAgentCard(context.Background(), "worker-a")
	if err != nil {
		t.Fatalf("ResolveAgentCard: %v", err)
	}
	if card.Name != "worker-a" {
		t.Errorf("card.Name = %q, want worker-a", card.Name)
	}
}

// External peers do not get the TLS-skipping resolver, so a self-signed
// broker cert is rejected. This guards against silently trusting any
// cert when reaching outside the cluster.
func TestResolveAgentCardExternalVerifiesTLS(t *testing.T) {
	srv := fakeAgentTLS(t, "third-party")
	writeTopologyWithKind(t, "third-party", "External", srv.URL)

	if _, err := ResolveAgentCard(context.Background(), "third-party"); err == nil {
		t.Error("expected TLS verification error for External peer with self-signed cert")
	}
}

// Managed peers build a working client even when the card is served
// over HTTPS with a self-signed cert.
func TestNewForPeerManagedSkipsTLSVerify(t *testing.T) {
	srv := fakeAgentTLS(t, "worker-a")
	writeTopologyWithKind(t, "worker-a", "Managed", srv.URL)

	c, err := NewForPeer(context.Background(), "worker-a")
	if err != nil {
		t.Fatalf("NewForPeer: %v", err)
	}
	if c == nil {
		t.Fatal("client is nil")
	}
}
