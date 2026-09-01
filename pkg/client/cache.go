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
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
)

// clientCacheEntry holds the lazily-built client for one peer name, plus
// the sync.Once guarding its construction so concurrent first-callers
// block on a single newForPeer instead of racing.
type clientCacheEntry struct {
	once   sync.Once
	client *a2aclient.Client
	err    error
}

// peerClients caches one clientCacheEntry per peer name for the life of
// the process. Entries are created lazily under mu; construction itself
// happens outside mu via the entry's sync.Once.
var (
	peerClientsMu sync.Mutex
	peerClients   = map[string]*clientCacheEntry{}
)

// PeerClient returns the cached a2aclient.Client for the named peer,
// building it via newForPeer on first use and reusing it on every
// subsequent call for the same peer name. Construction is safe under
// concurrent first-use of the same peer: only one caller builds the
// client, and the rest block until it is ready.
//
// opts are only applied the first time the peer's client is built; they
// are ignored on cache hits.
func PeerClient(ctx context.Context, name string, opts ...a2aclient.FactoryOption) (*a2aclient.Client, error) {
	peerClientsMu.Lock()
	entry, ok := peerClients[name]
	if !ok {
		entry = &clientCacheEntry{}
		peerClients[name] = entry
	}
	peerClientsMu.Unlock()

	entry.once.Do(func() {
		var card *a2a.AgentCard
		entry.client, card, entry.err = newForPeer(ctx, name, opts...)
		if entry.err == nil {
			// Best-effort: a failure to record the hash must not fail
			// the build the caller actually asked for.
			_ = recordPeerHash(name, card)
		}
	})
	if entry.err != nil {
		// Don't let a failed build (e.g. transient card-resolve error)
		// permanently poison the cache for this peer name — evict so
		// the next call gets a fresh attempt instead of the same error
		// forever.
		ForgetPeer(name)
	}
	return entry.client, entry.err
}

// ForgetPeer drops the cached client for the named peer, if any. The
// next PeerClient call for that name resolves the AgentCard and builds
// a fresh client. Safe to call for a peer with no cached entry.
func ForgetPeer(name string) {
	peerClientsMu.Lock()
	delete(peerClients, name)
	peerClientsMu.Unlock()
}

// resetPeerClientCache drops every cached peer client. Test-only; not
// exposed as a public API.
func resetPeerClientCache() {
	peerClientsMu.Lock()
	peerClients = map[string]*clientCacheEntry{}
	peerClientsMu.Unlock()
}
