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
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

func resetCacheBetweenTests(t *testing.T) {
	t.Helper()
	resetPeerClientCache()
	t.Cleanup(resetPeerClientCache)
}

// countingAgent is like fakeAgent but counts AgentCard resolve requests,
// so tests can assert the card is fetched at most once per peer.
func countingAgent(t *testing.T, name string, hits *int64) *httptest.Server {
	t.Helper()
	card := &a2a.AgentCard{
		Name:                name,
		Version:             "0.0.1",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://placeholder", a2a.TransportProtocolJSONRPC)},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(a2asrv.WellKnownAgentCardPath, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPeerClientCachesAcrossCalls(t *testing.T) {
	resetCacheBetweenTests(t)

	var hits int64
	srv := countingAgent(t, "worker-a", &hits)
	writeTopologyWithURL(t, "worker-a", srv.URL)

	c1, err := PeerClient(context.Background(), "worker-a")
	if err != nil {
		t.Fatalf("PeerClient: %v", err)
	}
	c2, err := PeerClient(context.Background(), "worker-a")
	if err != nil {
		t.Fatalf("PeerClient: %v", err)
	}
	if c1 != c2 {
		t.Errorf("PeerClient returned different client pointers across calls, want same cached instance")
	}
	if hits != 1 {
		t.Errorf("card resolve hits = %d, want 1 (card should be fetched once, cached after)", hits)
	}
}

func TestPeerClientLazyDoesNotBuildUncalledPeer(t *testing.T) {
	resetCacheBetweenTests(t)

	var hits int64
	srv := countingAgent(t, "worker-a", &hits)
	writeTopologyWithURL(t, "worker-a", srv.URL)

	if hits != 0 {
		t.Fatalf("hits = %d before any PeerClient call, want 0", hits)
	}
	// No PeerClient call for "worker-a" here — merely being present in
	// the topology must not trigger a card fetch or client build.
}

func TestPeerClientConcurrentFirstUseBuildsOnce(t *testing.T) {
	resetCacheBetweenTests(t)

	var hits int64
	srv := countingAgent(t, "worker-a", &hits)
	writeTopologyWithURL(t, "worker-a", srv.URL)

	const n = 20
	var wg sync.WaitGroup
	clients := make([]*a2aclient.Client, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clients[i], errs[i] = PeerClient(context.Background(), "worker-a")
		}(i)
	}
	wg.Wait()

	for i := range clients {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: PeerClient: %v", i, errs[i])
		}
		if clients[i] != clients[0] {
			t.Errorf("goroutine %d got a different client pointer than goroutine 0", i)
		}
	}
	if hits != 1 {
		t.Errorf("card resolve hits = %d, want 1 under concurrent first-use", hits)
	}
}

func TestForgetPeerForcesRebuild(t *testing.T) {
	resetCacheBetweenTests(t)

	var hits int64
	srv := countingAgent(t, "worker-a", &hits)
	writeTopologyWithURL(t, "worker-a", srv.URL)

	if _, err := PeerClient(context.Background(), "worker-a"); err != nil {
		t.Fatalf("PeerClient: %v", err)
	}
	ForgetPeer("worker-a")
	if _, err := PeerClient(context.Background(), "worker-a"); err != nil {
		t.Fatalf("PeerClient after ForgetPeer: %v", err)
	}
	if hits != 2 {
		t.Errorf("card resolve hits = %d, want 2 (rebuild after ForgetPeer)", hits)
	}
}

func TestPeerClientRetriesAfterFailure(t *testing.T) {
	resetCacheBetweenTests(t)

	// No topology entry for "worker-a" at all: first call fails with
	// ErrPeerNotFound. Seed the topology afterward and confirm a later
	// call succeeds instead of replaying the cached error forever.
	writeTopologyWithURL(t, "other-peer", "http://example.invalid")

	if _, err := PeerClient(context.Background(), "worker-a"); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("first PeerClient err = %v, want ErrPeerNotFound", err)
	}

	var hits int64
	srv := countingAgent(t, "worker-a", &hits)
	writeTopologyWithURL(t, "worker-a", srv.URL)

	if _, err := PeerClient(context.Background(), "worker-a"); err != nil {
		t.Fatalf("second PeerClient: %v", err)
	}
}
