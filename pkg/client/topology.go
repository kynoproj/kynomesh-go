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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// defaultTopologyPath is the in-pod location the broker init container
// writes to. Keep in sync with kmv1.TopologyFilePath in kynoproj/kynomesh.
const defaultTopologyPath = "/var/run/kynomesh/topology.json"

// peer mirrors kmv1.Peer's JSON shape.
type peer struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
	URL  string `json:"url,omitempty"`
}

// topology mirrors kmv1.Topology's JSON shape.
type topology struct {
	Pattern string `json:"pattern,omitempty"`
	IsEntry bool   `json:"isEntry,omitempty"`
	Peers   []peer `json:"peers,omitempty"`
}

// topologyPath is a test seam; production uses defaultTopologyPath.
var topologyPath = defaultTopologyPath

// The topology file is written once by the init container before the
// agent container starts, and never changes during the pod's lifetime.
// Cache the parsed value (and any load error) for the process lifetime
// so that Peers() + a loop of ResolveAgentCard() doesn't re-read the
// file on every call.
var (
	topologyOnce   sync.Once
	cachedTopology *topology
	cachedErr      error
)

// loadTopology returns the parsed topology, reading the file at most
// once per process. Subsequent calls return the cached result.
func loadTopology() (*topology, error) {
	topologyOnce.Do(func() {
		cachedTopology, cachedErr = readTopology(topologyPath)
	})
	return cachedTopology, cachedErr
}

// readTopology parses the topology file at path. ErrTopologyNotAvailable
// is returned when the file is absent (typical outside a Kynomesh pod).
func readTopology(path string) (*topology, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrTopologyNotAvailable
		}
		return nil, fmt.Errorf("read topology %q: %w", path, err)
	}
	var t topology
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("decode topology %q: %w", path, err)
	}
	return &t, nil
}

// resetTopologyCache drops the cached topology so the next call reloads
// from topologyPath. Test-only; not exposed as a public API.
func resetTopologyCache() {
	topologyOnce = sync.Once{}
	cachedTopology = nil
	cachedErr = nil
}

// isManaged reports whether a peer entry should be treated as a Managed
// peer. The CRD's default for an empty Kind is Managed; "External" is
// the only other kmv1.PeerKind value.
func (p peer) isManaged() bool {
	return p.Kind == "" || p.Kind == "Managed"
}

// lookupPeer returns the peer entry with the given name. Returns
// ErrPeerNotFound when no such peer exists in this agent's topology.
func lookupPeer(name string) (peer, error) {
	t, err := loadTopology()
	if err != nil {
		return peer{}, err
	}
	for _, p := range t.Peers {
		if p.Name == name {
			return p, nil
		}
	}
	return peer{}, fmt.Errorf("%w: %q", ErrPeerNotFound, name)
}

func peerURL(name string) (string, error) {
	p, err := lookupPeer(name)
	if err != nil {
		return "", err
	}
	if p.URL == "" {
		return "", fmt.Errorf("kynomesh: peer %q has no URL in topology", name)
	}
	return p.URL, nil
}

func peerNames() ([]string, error) {
	t, err := loadTopology()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(t.Peers))
	for _, p := range t.Peers {
		names = append(names, p.Name)
	}
	return names, nil
}
