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

// Package searcher implements the worker agent in the research-assistant
// example. It returns canned "search hits" for any incoming query — the
// point is to show what an A2A worker agent looks like, not to do real
// retrieval.
package searcher

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// corpus is the fake search index. Real agents would call a vector DB,
// a search API, or an LLM — keeping it static keeps the example focused
// on the mesh wiring, not the retrieval logic.
var corpus = map[string][]string{
	"kynomesh": {
		"Kynomesh is a Kubernetes-native platform for multi-agent A2A systems.",
		"It runs an injected broker sidecar that handles peer discovery and transport.",
	},
	"a2a": {
		"A2A is an open protocol for agent-to-agent communication.",
		"Transports include JSON-RPC, REST, and gRPC over a single TLS endpoint.",
	},
	"go": {
		"The Kynomesh Go SDK lives at github.com/kynoproj/kynomesh-go.",
		"`server.Start` and `client.NewForPeer` are the two main entry points.",
	},
}

// Executor is the searcher's a2asrv.AgentExecutor.
type Executor struct{}

var _ a2asrv.AgentExecutor = (*Executor)(nil)

func (*Executor) Execute(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		query := strings.ToLower(strings.TrimSpace(messageText(ec.Message)))
		hits := lookup(query)
		yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(formatHits(query, hits))), nil)
	}
}

func (*Executor) Cancel(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {}
}

func messageText(m *a2a.Message) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range m.Parts {
		b.WriteString(p.Text())
	}
	return b.String()
}

// lookup returns the union of corpus entries whose key appears in query.
// Tiny and intentional: a real agent would do retrieval; we just want a
// deterministic response the README can show.
func lookup(query string) []string {
	var out []string
	for key, entries := range corpus {
		if strings.Contains(query, key) {
			out = append(out, entries...)
		}
	}
	return out
}

func formatHits(query string, hits []string) string {
	if len(hits) == 0 {
		return fmt.Sprintf("searcher: no hits for %q", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "searcher: %d hit(s) for %q\n", len(hits), query)
	for i, h := range hits {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, h)
	}
	return b.String()
}

// Card returns the searcher's AgentCard.
func Card() *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "Searcher Agent",
		Description: "Returns canned search hits for a query.",
		Version:     "0.0.1",
		// Placeholder URLs — Kynomesh's broker rewrites them at serve time.
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://127.0.0.1:8089/a2a/jsonrpc", a2a.TransportProtocolJSONRPC),
			a2a.NewAgentInterface("127.0.0.1:8089", a2a.TransportProtocolGRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		Skills: []a2a.AgentSkill{
			{
				ID:          "search",
				Name:        "Search",
				Description: "Looks up a query in the (canned) corpus.",
				Tags:        []string{"search"},
				Examples:    []string{"tell me about kynomesh", "what is a2a?"},
			},
		},
	}
}
