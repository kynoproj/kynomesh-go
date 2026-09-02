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
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

// useTempPeerHashesPath points peerHashesPath at a fresh file under
// t.TempDir() and restores it (and the clear-once guard) on cleanup,
// so tests don't observe each other's recorded hashes.
func useTempPeerHashesPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peer-hashes.json")
	prev := peerHashesPath
	peerHashesPath = path
	resetPeerHashesState()
	t.Cleanup(func() {
		peerHashesPath = prev
		resetPeerHashesState()
	})
	return path
}

// fakeInCluster makes inCluster() report true for the duration of the
// test, so recordPeerHash doesn't no-op as it would in real local dev
// (where these tests actually run).
func fakeInCluster(t *testing.T) {
	t.Helper()
	prev := inCluster
	inCluster = func() bool { return true }
	t.Cleanup(func() { inCluster = prev })
}

func readRecordedHashes(t *testing.T, path string) map[string]string {
	t.Helper()
	hashes, err := readPeerHashes(path)
	if err != nil {
		t.Fatalf("readPeerHashes: %v", err)
	}
	return hashes
}

func TestPeerClientRecordsHashOnFirstBuild(t *testing.T) {
	resetCacheBetweenTests(t)
	fakeInCluster(t)
	path := useTempPeerHashesPath(t)

	var hits int64
	srv := countingAgent(t, "worker-a", &hits)
	writeTopologyWithURL(t, "worker-a", srv.URL)

	if _, err := PeerClient(context.Background(), "worker-a"); err != nil {
		t.Fatalf("PeerClient: %v", err)
	}

	hashes := readRecordedHashes(t, path)
	if _, ok := hashes["worker-a"]; !ok {
		t.Errorf("hashes = %v, want an entry for worker-a", hashes)
	}
}

func TestPeerClientDoesNotRecordHashForUncalledPeer(t *testing.T) {
	resetCacheBetweenTests(t)
	fakeInCluster(t)
	path := useTempPeerHashesPath(t)

	var hits int64
	srv := countingAgent(t, "worker-a", &hits)
	writeTopologyWithURL(t, "worker-a", srv.URL)
	// worker-a is in the topology but PeerClient is never called for it.

	hashes := readRecordedHashes(t, path)
	if len(hashes) != 0 {
		t.Errorf("hashes = %v, want empty (peer never resolved)", hashes)
	}
}

func TestPeerClientHashFileClearedOnFirstUseOfProcess(t *testing.T) {
	resetCacheBetweenTests(t)
	fakeInCluster(t)
	path := useTempPeerHashesPath(t)

	// Simulate a stale file left by a previous process incarnation.
	if err := writePeerHashes(path, map[string]string{"stale-peer": "deadbeef"}); err != nil {
		t.Fatalf("seed stale hashes: %v", err)
	}

	var hits int64
	srv := countingAgent(t, "worker-a", &hits)
	writeTopologyWithURL(t, "worker-a", srv.URL)

	if _, err := PeerClient(context.Background(), "worker-a"); err != nil {
		t.Fatalf("PeerClient: %v", err)
	}

	hashes := readRecordedHashes(t, path)
	if _, ok := hashes["stale-peer"]; ok {
		t.Errorf("hashes = %v, want stale-peer entry cleared on first peer-client build this process", hashes)
	}
	if _, ok := hashes["worker-a"]; !ok {
		t.Errorf("hashes = %v, want an entry for worker-a", hashes)
	}
}

func TestPeerClientHashFileAccumulatesAcrossPeers(t *testing.T) {
	resetCacheBetweenTests(t)
	fakeInCluster(t)
	path := useTempPeerHashesPath(t)

	var hitsA, hitsB int64
	srvA := countingAgent(t, "worker-a", &hitsA)
	srvB := countingAgent(t, "worker-b", &hitsB)
	writeTwoPeerTopology(t, "worker-a", srvA.URL, "worker-b", srvB.URL)

	if _, err := PeerClient(context.Background(), "worker-a"); err != nil {
		t.Fatalf("PeerClient worker-a: %v", err)
	}
	if _, err := PeerClient(context.Background(), "worker-b"); err != nil {
		t.Fatalf("PeerClient worker-b: %v", err)
	}

	hashes := readRecordedHashes(t, path)
	if _, ok := hashes["worker-a"]; !ok {
		t.Errorf("hashes = %v, want an entry for worker-a", hashes)
	}
	if _, ok := hashes["worker-b"]; !ok {
		t.Errorf("hashes = %v, want an entry for worker-b (must not be overwritten by worker-a's write)", hashes)
	}
}

func TestPeerClientDoesNotWriteHashFileOutsidePod(t *testing.T) {
	resetCacheBetweenTests(t)
	// Deliberately no fakeInCluster(t): local dev / non-pod is the
	// default in these tests, same as it is for a real local run.
	path := useTempPeerHashesPath(t)

	var hits int64
	srv := countingAgent(t, "worker-a", &hits)
	writeTopologyWithURL(t, "worker-a", srv.URL)

	if _, err := PeerClient(context.Background(), "worker-a"); err != nil {
		t.Fatalf("PeerClient: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("peer-hashes file exists outside a pod, want no file (err=%v)", err)
	}
}

func TestHashAgentCardDeterministic(t *testing.T) {
	card := &a2a.AgentCard{Name: "worker-a", Version: "0.0.1"}
	h1, err := hashAgentCard(card)
	if err != nil {
		t.Fatalf("hashAgentCard: %v", err)
	}
	h2, err := hashAgentCard(card)
	if err != nil {
		t.Fatalf("hashAgentCard: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash1 = %q, hash2 = %q, want equal for identical cards", h1, h2)
	}

	other := &a2a.AgentCard{Name: "worker-a", Version: "0.0.2"}
	h3, err := hashAgentCard(other)
	if err != nil {
		t.Fatalf("hashAgentCard: %v", err)
	}
	if h1 == h3 {
		t.Errorf("hash unchanged despite different card content")
	}
}

// TestCanonicalizeKeyOrderIndependent pins the property JCS
// canonicalization exists to provide: two JSON encodings of the same
// logical document that differ only in key order (e.g. as another
// SDK's marshaler might produce for the same AgentCard) canonicalize
// to identical bytes, and therefore hash identically. Go's own
// json.Marshal on a struct always emits fields in declaration order,
// so this can't be demonstrated through hashAgentCard/a2a.AgentCard
// directly — it exercises the canonicalizer on hand-written JSON with
// deliberately different key orders instead.
func TestCanonicalizeKeyOrderIndependent(t *testing.T) {
	inOrder := []byte(`{"name":"worker-a","version":"0.0.1"}`)
	reordered := []byte(`{"version":"0.0.1","name":"worker-a"}`)

	c1, err := jsoncanonicalizer.Transform(inOrder)
	if err != nil {
		t.Fatalf("Transform inOrder: %v", err)
	}
	c2, err := jsoncanonicalizer.Transform(reordered)
	if err != nil {
		t.Fatalf("Transform reordered: %v", err)
	}
	if string(c1) != string(c2) {
		t.Errorf("canonical1 = %q, canonical2 = %q, want equal for same content in different key order", c1, c2)
	}

	sum1 := sha256.Sum256(c1)
	sum2 := sha256.Sum256(c2)
	if sum1 != sum2 {
		t.Errorf("hash differs despite canonical bytes matching")
	}
}

func writeTwoPeerTopology(t *testing.T, name1, url1, name2, url2 string) {
	t.Helper()
	payload := `{"peers":[` +
		`{"name":"` + name1 + `","kind":"Managed","url":"` + url1 + `"},` +
		`{"name":"` + name2 + `","kind":"Managed","url":"` + url2 + `"}` +
		`]}`
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
