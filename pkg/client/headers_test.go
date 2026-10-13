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
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

const testToken = "Bearer secret"

var testAuth = map[string]string{"Authorization": testToken}

// protectedAgent serves an AgentCard and a JSON-RPC endpoint, and records the
// Authorization header of every request. The card is refused (401) unless the
// header equals want, like a peer that protects its card.
type protectedAgent struct {
	srv *httptest.Server

	mu        sync.Mutex
	cardAuths []string
	callAuths []string
}

func newProtectedAgent(t *testing.T, want string, ifaceURLs ...string) *protectedAgent {
	t.Helper()
	a := &protectedAgent{}
	mux := http.NewServeMux()
	mux.HandleFunc(a2asrv.WellKnownAgentCardPath, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		a.mu.Lock()
		a.cardAuths = append(a.cardAuths, auth)
		a.mu.Unlock()
		if want != "" && auth != want {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		urls := ifaceURLs
		if len(urls) == 0 {
			urls = []string{a.srv.URL}
		}
		ifaces := make([]*a2a.AgentInterface, 0, len(urls))
		for _, u := range urls {
			ifaces = append(ifaces, a2a.NewAgentInterface(u, a2a.TransportProtocolJSONRPC))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&a2a.AgentCard{Name: "calc", Version: "0.0.1", SupportedInterfaces: ifaces})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.callAuths = append(a.callAuths, r.Header.Get("Authorization"))
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{}}`))
	})
	a.srv = httptest.NewServer(mux)
	t.Cleanup(a.srv.Close)
	return a
}

func (a *protectedAgent) cards() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.cardAuths...)
}

func (a *protectedAgent) calls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.callAuths...)
}

func sendPing(t *testing.T, c *a2aclient.Client, ctx context.Context) {
	t.Helper()
	// The reply content is irrelevant: these tests look at what the server received.
	_, _ = c.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	})
}

func TestValidateHeadersReturnsAnIndependentCopy(t *testing.T) {
	source := map[string]string{"X-Api-Key": "k"}

	got, err := validateHeaders(source)
	if err != nil {
		t.Fatalf("validateHeaders: %v", err)
	}
	source["X-Api-Key"] = "changed"

	if got["X-Api-Key"] != "k" {
		t.Errorf("copy was mutated through the source: %v", got)
	}
}

func TestValidateHeadersRejectsMalformedEntries(t *testing.T) {
	cases := map[string]map[string]string{
		"empty name":           {"": "v"},
		"blank name":           {"  ": "v"},
		"space in name":        {"X A": "v"},
		"colon in name":        {"X-A:": "v"},
		"token char gRPC bans": {"X+Key": "v"},
		"grpc reserved prefix": {"grpc-timeout": "1S"},
		"grpc prefix any case": {"Grpc-Encoding": "gzip"},
		"injection in name":    {"X-A: b\r\nX-Evil": "1"},
		"line break in value":  {"Authorization": "a\r\nX-Injected: 1"},
		"nul in value":         {"Authorization": "nul\x00byte"},
		"non-ascii value":      {"Authorization": "café"},
		"duplicate, any case":  {"Authorization": "a", "authorization": "b"},
		"secret in bad header": {"Authorization": "Bearer secret\n"},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := validateHeaders(headers)
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("error echoes a header value: %v", err)
			}
		})
	}
}

func TestNewForPeerHeadersAuthenticatesTheCardFetchAndEveryCall(t *testing.T) {
	agent := newProtectedAgent(t, testToken)
	writeTopologyWithKind(t, "calc", "External", agent.srv.URL)

	c, _, err := newForPeerCardHeaders(context.Background(), "calc", testAuth)
	if err != nil {
		t.Fatalf("newForPeerCardHeaders: %v", err)
	}
	sendPing(t, c, context.Background())

	if got := agent.cards(); len(got) != 1 || got[0] != testToken {
		t.Errorf("card auth = %v, want [%s]", got, testToken)
	}
	got := agent.calls()
	if len(got) == 0 {
		t.Fatal("the call never reached the peer")
	}
	for _, auth := range got {
		if auth != "" {
			t.Errorf("call auth = %q, want none: card headers must not reach calls", auth)
		}
	}
}

func TestNewForPeerWithoutHeadersCannotReadAProtectedCard(t *testing.T) {
	agent := newProtectedAgent(t, testToken)
	writeTopologyWithKind(t, "calc", "External", agent.srv.URL)

	if _, _, err := newForPeer(context.Background(), "calc"); err == nil {
		t.Fatal("expected the protected card to be refused")
	}
}

func TestPerCallHeadersAuthenticateCalls(t *testing.T) {
	agent := newProtectedAgent(t, testToken)
	writeTopologyWithKind(t, "calc", "External", agent.srv.URL)
	c, _, err := newForPeerCardHeaders(context.Background(), "calc", testAuth)
	if err != nil {
		t.Fatalf("newForPeerCardHeaders: %v", err)
	}

	ctx := a2aclient.AttachServiceParams(context.Background(), a2aclient.ServiceParams{"Authorization": {"Bearer fresh"}})
	sendPing(t, c, ctx)

	if got := agent.calls(); len(got) == 0 || got[0] != "Bearer fresh" {
		t.Errorf("call auth = %v, want Bearer fresh", got)
	}
}

func TestMalformedHeadersFailBeforeAnyRequest(t *testing.T) {
	agent := newProtectedAgent(t, "")
	writeTopologyWithKind(t, "calc", "External", agent.srv.URL)

	_, _, err := newForPeerCardHeaders(context.Background(), "calc", map[string]string{"Authorization": "a\nb"})

	if err == nil {
		t.Fatal("expected an error")
	}
	if n := len(agent.cards()); n != 0 {
		t.Errorf("%d card requests were made, want none", n)
	}
}

func TestWarnsWhenHeadersGoToAPeerWithoutTLSVerification(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv(InsecureTLSPeersEnv, "calc")
	agent := newProtectedAgent(t, "")
	writeTopologyWithKind(t, "calc", "External", agent.srv.URL)

	if _, _, err := newForPeerCardHeaders(context.Background(), "calc", testAuth); err != nil {
		t.Fatalf("newForPeerCardHeaders: %v", err)
	}

	if !strings.Contains(buf.String(), "without TLS verification") || !strings.Contains(buf.String(), "calc") {
		t.Errorf("expected a warning naming the peer, got: %s", buf.String())
	}
	if strings.Contains(buf.String(), "secret") {
		t.Errorf("warning leaks a header value: %s", buf.String())
	}
}

func TestNoTLSWarningWhenThePeerIsVerified(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	agent := newProtectedAgent(t, "")
	writeTopologyWithKind(t, "calc", "External", agent.srv.URL)

	if _, _, err := newForPeerCardHeaders(context.Background(), "calc", testAuth); err != nil {
		t.Fatalf("newForPeerCardHeaders: %v", err)
	}

	if strings.Contains(buf.String(), "without TLS verification") {
		t.Errorf("unexpected warning: %s", buf.String())
	}
}

func TestPeerClientWithCardHeadersAppliesOnFirstBuildOnly(t *testing.T) {
	resetCacheBetweenTests(t)
	agent := newProtectedAgent(t, testToken)
	writeTopologyWithKind(t, "calc", "External", agent.srv.URL)

	first, err := PeerClientWithCardHeaders(context.Background(), "calc", testAuth)
	if err != nil {
		t.Fatalf("first PeerClientWithCardHeaders: %v", err)
	}
	second, err := PeerClientWithCardHeaders(context.Background(), "calc", map[string]string{"Authorization": "Bearer other"})
	if err != nil {
		t.Fatalf("second PeerClientWithCardHeaders: %v", err)
	}

	if first != second {
		t.Error("second call did not reuse the cached client")
	}
	if n := len(agent.cards()); n != 1 {
		t.Errorf("%d card requests, want 1", n)
	}

	sendPing(t, second, context.Background())
	if got := agent.cards(); got[0] != testToken {
		t.Errorf("card auth = %v, want the first build's %s", got, testToken)
	}
}

func TestForgetPeerLetsNewHeadersTakeEffect(t *testing.T) {
	resetCacheBetweenTests(t)
	agent := newProtectedAgent(t, "")
	writeTopologyWithKind(t, "calc", "External", agent.srv.URL)
	if _, err := PeerClientWithCardHeaders(context.Background(), "calc", testAuth); err != nil {
		t.Fatalf("PeerClientWithCardHeaders: %v", err)
	}

	ForgetPeer("calc")
	if _, err := PeerClientWithCardHeaders(context.Background(), "calc", map[string]string{"Authorization": "Bearer other"}); err != nil {
		t.Fatalf("PeerClientWithCardHeaders after forget: %v", err)
	}

	got := agent.cards()
	if len(got) != 2 || got[1] != "Bearer other" {
		t.Errorf("card auths = %v, want the second to be Bearer other", got)
	}
}

func TestFailedBuildWithHeadersIsNotCached(t *testing.T) {
	resetCacheBetweenTests(t)
	agent := newProtectedAgent(t, testToken)
	writeTopologyWithKind(t, "calc", "External", agent.srv.URL)

	if _, err := PeerClientWithCardHeaders(context.Background(), "calc", map[string]string{"Authorization": "Bearer wrong"}); err == nil {
		t.Fatal("expected the wrong token to be refused")
	}
	if _, err := PeerClientWithCardHeaders(context.Background(), "calc", testAuth); err != nil {
		t.Fatalf("retry with the right token: %v", err)
	}
}

func TestResolveAgentCardAcceptsRequestHeaders(t *testing.T) {
	agent := newProtectedAgent(t, testToken)
	writeTopologyWithKind(t, "calc", "External", agent.srv.URL)

	card, err := ResolveAgentCard(context.Background(), "calc", agentcard.WithRequestHeader("Authorization", testToken))
	if err != nil {
		t.Fatalf("ResolveAgentCard: %v", err)
	}
	if card.Name != "calc" {
		t.Errorf("card.Name = %q, want calc", card.Name)
	}
}

// redirectTarget records the Authorization header of every request it receives.
func redirectTarget(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), auths...)
	}
}

func TestHeadersAreNotSentToARedirectOnTheCardFetch(t *testing.T) {
	target, received := redirectTarget(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(peer.Close)
	writeTopologyWithKind(t, "calc", "External", peer.URL)

	c, _, err := newForPeerCardHeaders(context.Background(), "calc", testAuth)

	if err == nil || c != nil {
		t.Fatalf("expected the redirected card fetch to fail, got client %v, err %v", c, err)
	}
	if got := received(); len(got) != 0 {
		t.Errorf("the redirect target received %v, want no request", got)
	}
}

func TestForgetEntryOnlyDropsTheEntryItWasGiven(t *testing.T) {
	resetCacheBetweenTests(t)
	stale, current := &clientCacheEntry{}, &clientCacheEntry{}
	peerClientsMu.Lock()
	peerClients["calc"] = current
	peerClientsMu.Unlock()

	forgetEntry("calc", stale)

	peerClientsMu.Lock()
	defer peerClientsMu.Unlock()
	if peerClients["calc"] != current {
		t.Error("a stale entry's cleanup evicted the newer entry")
	}
}

func TestForgetEntryDropsTheEntryItWasGiven(t *testing.T) {
	resetCacheBetweenTests(t)
	entry := &clientCacheEntry{}
	peerClientsMu.Lock()
	peerClients["calc"] = entry
	peerClientsMu.Unlock()

	forgetEntry("calc", entry)

	peerClientsMu.Lock()
	defer peerClientsMu.Unlock()
	if _, ok := peerClients["calc"]; ok {
		t.Error("the entry was not dropped")
	}
}
