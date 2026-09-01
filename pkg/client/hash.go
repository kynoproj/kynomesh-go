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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// envPodName mirrors pkg/server's in-pod signal: set by the Kynomesh
// pod spec, absent in local dev.
const envPodName = "POD_NAME"

// inCluster is a test seam wrapping os.Getenv(envPodName).
var inCluster = func() bool { return os.Getenv(envPodName) != "" }

// defaultPeerHashesPath is the in-pod location the broker reads to
// compare against a peer's live state (e.g. its AgentCard) for drift
// detection. Keep in sync with kmv1.PeerHashesFilePath in
// kynoproj/kynomesh.
const defaultPeerHashesPath = "/var/run/kynomesh/peer-hashes.json"

// peerHashesPath is a test seam; production uses defaultPeerHashesPath.
var peerHashesPath = defaultPeerHashesPath

// peerHashesInit guards clearing the peer-hashes file exactly once per
// process, the first time any peer client is built — before that
// peer's hash (the first entry ever written) is recorded. A stale file
// from a previous process incarnation must never be read as current by
// the broker.
var peerHashesInit sync.Once

// peerHashesMu serializes read-modify-write updates to the peer-hashes
// file; PeerClient callers for different peers can race to record
// their hash concurrently.
var peerHashesMu sync.Mutex

// hashAgentCard returns the hex-encoded SHA-256 digest of card's JSON
// encoding. a2a.AgentCard is a struct (not a map), so encoding/json
// serializes its fields in a fixed, declaration order — the same card
// content always hashes the same way.
func hashAgentCard(card *a2a.AgentCard) (string, error) {
	data, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("encode agent card: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// recordPeerHash hashes card and records it for name in the
// peer-hashes file, creating or updating only that peer's entry. The
// first call per process clears any pre-existing file first, so a
// stale entry from a prior process incarnation never lingers.
//
// A no-op outside a Kynomesh pod (e.g. local dev, tests hand-rolling a
// topology file): there is no broker there to read the file, and
// writing it would just leave a stray file with no consumer.
func recordPeerHash(name string, card *a2a.AgentCard) error {
	if !inCluster() {
		return nil
	}

	hash, err := hashAgentCard(card)
	if err != nil {
		return err
	}

	peerHashesInit.Do(func() {
		_ = os.Remove(peerHashesPath)
	})

	peerHashesMu.Lock()
	defer peerHashesMu.Unlock()

	hashes, err := readPeerHashes(peerHashesPath)
	if err != nil {
		return err
	}
	hashes[name] = hash
	return writePeerHashes(peerHashesPath, hashes)
}

// readPeerHashes returns the current peer name -> hash map, or an
// empty map if the file does not exist yet.
func readPeerHashes(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read peer hashes %q: %w", path, err)
	}
	hashes := map[string]string{}
	if err := json.Unmarshal(raw, &hashes); err != nil {
		return nil, fmt.Errorf("decode peer hashes %q: %w", path, err)
	}
	return hashes, nil
}

// writePeerHashes serializes hashes as JSON and writes it atomically to
// path, so a concurrent reader (the broker) never observes a
// half-written file.
func writePeerHashes(path string, hashes map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create peer hashes dir: %w", err)
	}
	data, err := json.Marshal(hashes)
	if err != nil {
		return fmt.Errorf("encode peer hashes: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".peer-hashes-*")
	if err != nil {
		return fmt.Errorf("create peer hashes tempfile: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write peer hashes tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close peer hashes tempfile: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod peer hashes tempfile: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename peer hashes: %w", err)
	}
	return nil
}

// resetPeerHashesState resets the clear-once guard and removes any file
// at peerHashesPath. Test-only; not exposed as a public API.
func resetPeerHashesState() {
	peerHashesInit = sync.Once{}
	peerHashesMu.Lock()
	_ = os.Remove(peerHashesPath)
	peerHashesMu.Unlock()
}
