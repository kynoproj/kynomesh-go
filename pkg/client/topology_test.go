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
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const samplePayload = `{
  "pattern": "Supervisor",
  "isEntry": true,
  "peers": [
    {"name": "worker-a", "kind": "Managed", "url": "https://workers-worker-a-headless.ns.svc.cluster.local:8490"},
    {"name": "worker-b", "url": "https://workers-worker-b-headless.ns.svc.cluster.local:8490"},
    {"name": "no-url", "kind": "External"}
  ]
}`

// redirectTopology points topologyPath at a temp file populated with payload
// and resets the cache so each test gets a fresh read. Pass empty payload to
// leave the file absent (simulates non-pod environment).
func redirectTopology(t *testing.T, payload string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "topology.json")
	if payload != "" {
		if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
			t.Fatalf("seed topology: %v", err)
		}
	}
	prev := topologyPath
	topologyPath = path
	resetTopologyCache()
	t.Cleanup(func() {
		topologyPath = prev
		resetTopologyCache()
	})
}

func TestPeerURLReturnsAddressFromTopology(t *testing.T) {
	redirectTopology(t, samplePayload)
	got, err := PeerURL("worker-a")
	if err != nil {
		t.Fatalf("PeerURL: %v", err)
	}
	want := "https://workers-worker-a-headless.ns.svc.cluster.local:8490"
	if got != want {
		t.Errorf("PeerURL = %q, want %q", got, want)
	}
}

func TestPeerURLUnknownPeerReturnsErrPeerNotFound(t *testing.T) {
	redirectTopology(t, samplePayload)
	_, err := PeerURL("does-not-exist")
	if !errors.Is(err, ErrPeerNotFound) {
		t.Errorf("err = %v, want ErrPeerNotFound", err)
	}
}

func TestPeerURLEmptyURLReturnsError(t *testing.T) {
	redirectTopology(t, samplePayload)
	_, err := PeerURL("no-url")
	if err == nil {
		t.Fatal("expected error for peer with no URL")
	}
	if errors.Is(err, ErrPeerNotFound) {
		t.Errorf("err = %v, should distinguish missing-URL from missing-peer", err)
	}
}

func TestPeerURLMissingTopologyReturnsErrTopologyNotAvailable(t *testing.T) {
	redirectTopology(t, "") // no file written
	_, err := PeerURL("worker-a")
	if !errors.Is(err, ErrTopologyNotAvailable) {
		t.Errorf("err = %v, want ErrTopologyNotAvailable", err)
	}
}

func TestPeersListsAllNames(t *testing.T) {
	redirectTopology(t, samplePayload)
	got, err := Peers()
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("Peers = %v (len %d), want 3 entries", got, len(got))
	}
}

func TestPeersMissingTopologyReturnsErrTopologyNotAvailable(t *testing.T) {
	redirectTopology(t, "")
	_, err := Peers()
	if !errors.Is(err, ErrTopologyNotAvailable) {
		t.Errorf("err = %v, want ErrTopologyNotAvailable", err)
	}
}

func TestLoadInvalidJSONReturnsError(t *testing.T) {
	redirectTopology(t, "not json")
	_, err := Peers()
	if err == nil || errors.Is(err, ErrTopologyNotAvailable) {
		t.Errorf("err = %v, want a decode error", err)
	}
}

// The topology file is written once by the init container and never
// changes; subsequent calls should serve from the in-memory cache
// rather than re-reading the file.
func TestTopologyCachedAcrossCalls(t *testing.T) {
	redirectTopology(t, samplePayload)

	// Prime the cache.
	if _, err := PeerURL("worker-a"); err != nil {
		t.Fatalf("PeerURL: %v", err)
	}

	// Overwrite the file with completely different contents; the cache
	// should still report the original peers.
	if err := os.WriteFile(topologyPath, []byte(`{"peers":[{"name":"different","url":"http://x"}]}`), 0o644); err != nil {
		t.Fatalf("rewrite topology: %v", err)
	}
	names, err := Peers()
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	for _, n := range names {
		if n == "different" {
			t.Errorf("cache was bypassed; got peer %q from rewritten file", n)
		}
	}
}

// A missing file at startup is also cached: once we've seen
// ErrTopologyNotAvailable, even if the file later appears, Peers()
// should keep returning that error. This matches the load-once contract.
func TestErrTopologyNotAvailableCached(t *testing.T) {
	redirectTopology(t, "") // no file
	if _, err := Peers(); !errors.Is(err, ErrTopologyNotAvailable) {
		t.Fatalf("priming err = %v, want ErrTopologyNotAvailable", err)
	}

	// Create the file after the cache has memoized the error.
	if err := os.WriteFile(topologyPath, []byte(samplePayload), 0o644); err != nil {
		t.Fatalf("write topology: %v", err)
	}
	if _, err := Peers(); !errors.Is(err, ErrTopologyNotAvailable) {
		t.Errorf("err = %v, want cached ErrTopologyNotAvailable", err)
	}
}
