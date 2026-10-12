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

// Package client provides peer-discovery helpers for Kynomesh agents.
//
// In-pod, the broker init container writes a topology file describing
// which peers this agent is allowed to call and how to reach them.
// This package wraps that file so user code does not need to know its
// path or format:
//
//	url, err := client.PeerURL("worker-a")        // just the URL
//	card, err := client.ResolveAgentCard(ctx, "worker-a")
//	c, err := client.PeerClient(ctx, "worker-a")  // ready-to-use a2a client, cached per-process per peer name
//
// A peer that requires auth headers, on the AgentCard fetch as well as on calls, uses
// PeerClientWithHeaders instead.
package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// managedTLSConfig skips certificate verification.
var managedTLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // in-pod hop, broker terminates external TLS

// managedHTTPClient is an HTTP client that skips TLS verification (but
// keeps TLS encryption), used for Managed peers.
var managedHTTPClient = &http.Client{
	// TODO: timeout should be configurable
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: managedTLSConfig,
	},
}

// refuseRedirects stops an HTTP client from following redirects.
func refuseRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// noRedirectClient returns an HTTP client that never follows redirects, with
// TLS verification skipped when skipVerify is set.
func noRedirectClient(skipVerify bool) *http.Client {
	transport := http.DefaultTransport
	if skipVerify {
		transport = managedHTTPClient.Transport
	}
	return &http.Client{Timeout: managedHTTPClient.Timeout, Transport: transport, CheckRedirect: refuseRedirects}
}

// managedCardResolver resolves AgentCards over a TLS-skipping HTTP client.
var managedCardResolver = &agentcard.Resolver{
	Client:     managedHTTPClient,
	CardParser: agentcard.DefaultCardParser,
}

// InsecureTLSPeersEnv names the environment variable holding a
// comma-separated list of External peers whose TLS certificate is not
// verified.
//
// An External peer is normally expected to present a CA-verifiable
// certificate. The exception is an agent in another AgentSet, referenced
// via externalAgents: its broker presents a self-signed certificate,
// which can never verify. Naming such a peer here gives it the same
// treatment Managed peers get (encrypted, but the peer is not
// authenticated). Managed peers need no entry.
const InsecureTLSPeersEnv = "KYNOMESH_INSECURE_TLS_PEERS"

// insecureTLSPeers parses InsecureTLSPeersEnv. Read on every call so it
// can change at runtime.
func insecureTLSPeers() map[string]struct{} {
	names := map[string]struct{}{}
	for _, n := range strings.Split(os.Getenv(InsecureTLSPeersEnv), ",") {
		if n = strings.TrimSpace(n); n != "" {
			names[n] = struct{}{}
		}
	}
	return names
}

// skipsTLSVerify reports whether TLS verification is skipped for the
// peer: always for Managed peers, for External peers only when named in
// InsecureTLSPeersEnv.
func skipsTLSVerify(p peer) bool {
	if p.isManaged() {
		return true
	}
	_, ok := insecureTLSPeers()[p.Name]
	return ok
}

// ErrPeerNotFound is returned when the named peer is not in this
// agent's topology — either because it does not exist in the AgentSet
// or because the routing pattern forbids this agent from reaching it.
var ErrPeerNotFound = errors.New("kynomesh: peer not in topology")

// ErrTopologyNotAvailable is returned when the topology file is absent
// (e.g. running outside a Kynomesh pod). Callers can fall back to
// hard-coded URLs in local-dev paths.
var ErrTopologyNotAvailable = errors.New("kynomesh: topology not available")

// PeerURL returns the broker URL of the named peer. Returns
// ErrPeerNotFound if the peer is not in this agent's topology, or
// ErrTopologyNotAvailable when running outside a Kynomesh pod.
func PeerURL(name string) (string, error) {
	return peerURL(name)
}

// Peers returns the names of every peer this agent is allowed to
// discover. Returns ErrTopologyNotAvailable when running outside a
// Kynomesh pod.
func Peers() ([]string, error) {
	return peerNames()
}

// ResolveAgentCard fetches the AgentCard of the named peer from its
// well-known location. The peer URL is looked up in the topology. For
// Managed peers, and External peers named in InsecureTLSPeersEnv, TLS
// verification is skipped on the card fetch.
func ResolveAgentCard(ctx context.Context, name string, opts ...agentcard.ResolveOption) (*a2a.AgentCard, error) {
	p, err := lookupPeer(name)
	if err != nil {
		return nil, err
	}
	if p.URL == "" {
		return nil, fmt.Errorf("kynomesh: peer %q has no URL in topology", name)
	}
	resolver := agentcard.DefaultResolver
	if skipsTLSVerify(p) {
		resolver = managedCardResolver
	}
	card, err := resolver.Resolve(ctx, p.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("resolve agent card for %q at %s: %w", name, p.URL, err)
	}
	return card, nil
}

// newForPeer returns an a2aclient.Client wired to the named peer, along
// with the AgentCard it was built from. It performs the full
// peer-discovery flow: look up the peer URL in the topology, resolve
// its AgentCard, and construct a client over one of the interfaces the
// card advertises.
//
// Unexported: this always builds a fresh client (a full AgentCard
// resolve + construction). For Managed peers, and External peers named
// in InsecureTLSPeersEnv, TLS verification is skipped on the card fetch
// and the client transports, unless opts override them.
func newForPeer(ctx context.Context, name string, opts ...a2aclient.FactoryOption) (*a2aclient.Client, *a2a.AgentCard, error) {
	return newForPeerHeaders(ctx, name, nil, opts...)
}

// newForPeerHeaders is newForPeer for a peer that requires headers (such as
// an Authorization header). The headers are sent on the AgentCard fetch and
// attached to every call made through the returned client; a header set on an
// individual call wins. They are refused if the card advertises an interface
// on a different host than the peer's address.
func newForPeerHeaders(ctx context.Context, name string, headers map[string]string, opts ...a2aclient.FactoryOption) (*a2aclient.Client, *a2a.AgentCard, error) {
	headers, err := validateHeaders(headers)
	if err != nil {
		return nil, nil, err
	}
	p, err := lookupPeer(name)
	if err != nil {
		return nil, nil, err
	}
	resolver := agentcard.DefaultResolver
	if skipsTLSVerify(p) {
		resolver = managedCardResolver
	}
	if len(headers) > 0 {
		// Never follow a redirect with the headers: Go re-sends custom headers to
		// the redirect target, which could be another host.
		httpClient := noRedirectClient(skipsTLSVerify(p))
		resolver = &agentcard.Resolver{Client: httpClient, CardParser: agentcard.DefaultCardParser}
		// Prepended so a caller's own transport options still win.
		opts = append([]a2aclient.FactoryOption{
			a2aclient.WithRESTTransport(httpClient),
			a2aclient.WithJSONRPCTransport(httpClient),
		}, opts...)
	}
	if len(headers) > 0 && skipsTLSVerify(p) {
		slog.Warn("kynomesh: sending headers to peer without TLS verification; they can be read by anyone on the network path",
			"peer", name)
	}
	resolveOpts := make([]agentcard.ResolveOption, 0, len(headers))
	for header, value := range headers {
		resolveOpts = append(resolveOpts, agentcard.WithRequestHeader(header, value))
	}
	card, err := resolver.Resolve(ctx, p.URL, resolveOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve agent card for %q at %s: %w", name, p.URL, err)
	}
	if len(headers) > 0 {
		if err := ensureCardStaysOnPeerHost(card, p.URL, name); err != nil {
			return nil, nil, err
		}
		opts = append(opts[:len(opts):len(opts)], a2aclient.WithCallInterceptors(newHeaderInterceptor(headers)))
	}

	if skipsTLSVerify(p) {
		if !p.isManaged() {
			slog.Warn("kynomesh: TLS verification disabled for external peer",
				"peer", name, "env", InsecureTLSPeersEnv)
		}
		// Prepend so user-supplied options override the defaults —
		// FactoryOption.apply writes into a per-protocol map, so the
		// last call for a given protocol wins.
		opts = append([]a2aclient.FactoryOption{
			a2agrpc.WithGRPCTransport(grpc.WithTransportCredentials(credentials.NewTLS(managedTLSConfig))),
			a2aclient.WithRESTTransport(managedHTTPClient),
			a2aclient.WithJSONRPCTransport(managedHTTPClient),
		}, opts...)
	}

	c, err := a2aclient.NewFromCard(ctx, card, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create a2a client for %q: %w", name, err)
	}
	return c, card, nil
}
