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

package main

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// Executor holds each request open for delay before replying, so concurrent
// requests accumulate as in-flight occupancy.
type Executor struct {
	delay time.Duration
}

var _ a2asrv.AgentExecutor = (*Executor)(nil)

func (e *Executor) Execute(ctx context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		select {
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return
		case <-time.After(e.delay):
		}
		msg := fmt.Sprintf("slow-agent: handled after %s", e.delay)
		yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(msg)), nil)
	}
}

func (*Executor) Cancel(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {}
}

// Card returns the slow-agent's AgentCard. URLs are placeholders — Kynomesh's
// broker rewrites them at serve time.
func Card() *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "Slow Agent",
		Description: "Holds each request open for a fixed delay; used to drive autoscaling load.",
		Version:     "0.0.1",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://127.0.0.1:8089/a2a/jsonrpc", a2a.TransportProtocolJSONRPC),
			a2a.NewAgentInterface("127.0.0.1:8089", a2a.TransportProtocolGRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		Skills: []a2a.AgentSkill{
			{
				ID:          "slow",
				Name:        "Slow",
				Description: "Replies after a fixed delay.",
				Tags:        []string{"slow", "load"},
				Examples:    []string{"hello"},
			},
		},
	}
}
