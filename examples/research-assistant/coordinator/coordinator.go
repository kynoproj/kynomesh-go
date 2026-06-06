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

// Package coordinator implements the entry agent in the
// research-assistant example. It receives a research question and
// forwards it to the searcher peer using client.NewForPeer, then
// returns the combined reply.
package coordinator

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/kynoproj/kynomesh-go/pkg/client"
)

const searcherPeer = "searcher"

// Executor is the coordinator's a2asrv.AgentExecutor.
type Executor struct{}

var _ a2asrv.AgentExecutor = (*Executor)(nil)

func (*Executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		question := messageText(ec.Message)

		// Discover the searcher peer by name, fetch its AgentCard, and
		// build an A2A client. The whole upstream
		// agentcard.Resolve + a2aclient.NewFromCard flow collapses into
		// this one call.
		peer, err := client.NewForPeer(ctx, searcherPeer)
		if err != nil {
			yield(nil, fmt.Errorf("dial peer %q: %w", searcherPeer, err))
			return
		}

		req := &a2a.SendMessageRequest{
			Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(question)),
		}
		resp, err := peer.SendMessage(ctx, req)
		if err != nil {
			yield(nil, fmt.Errorf("send to %q: %w", searcherPeer, err))
			return
		}

		yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(combine(question, resp))), nil)
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

// combine wraps the searcher's reply with a coordinator-level summary so
// the reader can tell the two agents apart in the output. The result of
// a non-streaming SendMessage is either *a2a.Message or *a2a.Task; the
// searcher only ever returns a Message, so other cases collapse to a
// generic placeholder.
func combine(question string, result a2a.SendMessageResult) string {
	var inner string
	if msg, ok := result.(*a2a.Message); ok {
		inner = messageText(msg)
	}
	if inner == "" {
		return fmt.Sprintf("coordinator: %q produced no hits", question)
	}
	return fmt.Sprintf("coordinator: handled %q via %q\n---\n%s", question, searcherPeer, inner)
}

// Card returns the coordinator's AgentCard.
func Card() *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "Coordinator Agent",
		Description: "Answers research questions by delegating to the searcher peer.",
		Version:     "0.0.1",
		// Placeholder URLs — Kynomesh's broker rewrites them at serve time.
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://127.0.0.1:8088/a2a/jsonrpc", a2a.TransportProtocolJSONRPC),
			a2a.NewAgentInterface("http://127.0.0.1:8088/a2a/rest", a2a.TransportProtocolHTTPJSON),
			a2a.NewAgentInterface("127.0.0.1:8088", a2a.TransportProtocolGRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		Skills: []a2a.AgentSkill{
			{
				ID:          "research",
				Name:        "Research",
				Description: "Routes a research question to the searcher peer.",
				Tags:        []string{"research"},
				Examples:    []string{"tell me about kynomesh", "what is a2a?"},
			},
		},
	}
}
