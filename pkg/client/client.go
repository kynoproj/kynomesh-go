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
//	c, err := client.NewForPeer(ctx, "worker-a")  // ready-to-use a2a client
package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// managedHTTPClient is an HTTP client that skips TLS verification. It is
// used for Managed peers: the broker terminates external TLS and in-pod
// hops run over the cluster network, where cert verification adds no
// value and the broker may present a self-signed cert.
var managedHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // in-pod hop, broker terminates external TLS
	},
}

// managedCardResolver resolves AgentCards over a TLS-skipping HTTP client.
var managedCardResolver = &agentcard.Resolver{
	Client:     managedHTTPClient,
	CardParser: agentcard.DefaultCardParser,
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
// Managed peers, TLS verification is skipped on the card fetch.
func ResolveAgentCard(ctx context.Context, name string, opts ...agentcard.ResolveOption) (*a2a.AgentCard, error) {
	p, err := lookupPeer(name)
	if err != nil {
		return nil, err
	}
	if p.URL == "" {
		return nil, fmt.Errorf("kynomesh: peer %q has no URL in topology", name)
	}
	resolver := agentcard.DefaultResolver
	if p.isManaged() {
		resolver = managedCardResolver
	}
	card, err := resolver.Resolve(ctx, p.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("resolve agent card for %q at %s: %w", name, p.URL, err)
	}
	return card, nil
}

// NewForPeer returns an a2aclient.Client wired to the named peer. It
// performs the full peer-discovery flow: look up the peer URL in the
// topology, resolve its AgentCard, and construct a client over one of
// the interfaces the card advertises.
//
// For Managed peers (the default — another AgentDeploy in the same
// AgentSet), gRPC, REST, and JSON-RPC transports are registered up
// front with TLS verification disabled, because the broker terminates
// external TLS and in-pod hops run over the cluster network. The card
// itself is also fetched with TLS verification disabled. Callers can
// override these defaults by passing their own transport options; the
// user-supplied option wins. External peers receive no default
// transport.
func NewForPeer(ctx context.Context, name string, opts ...a2aclient.FactoryOption) (*a2aclient.Client, error) {
	p, err := lookupPeer(name)
	if err != nil {
		return nil, err
	}
	resolver := agentcard.DefaultResolver
	if p.isManaged() {
		resolver = managedCardResolver
	}
	card, err := resolver.Resolve(ctx, p.URL)
	if err != nil {
		return nil, fmt.Errorf("resolve agent card for %q at %s: %w", name, p.URL, err)
	}

	if p.isManaged() {
		// Prepend so user-supplied options override the defaults —
		// FactoryOption.apply writes into a per-protocol map, so the
		// last call for a given protocol wins.
		opts = append([]a2aclient.FactoryOption{
			a2agrpc.WithGRPCTransport(grpc.WithTransportCredentials(insecure.NewCredentials())),
			a2aclient.WithRESTTransport(managedHTTPClient),
			a2aclient.WithJSONRPCTransport(managedHTTPClient),
		}, opts...)
	}

	c, err := a2aclient.NewFromCard(ctx, card, opts...)
	if err != nil {
		return nil, fmt.Errorf("create a2a client for %q: %w", name, err)
	}
	return c, nil
}
