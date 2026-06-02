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

// Kynomesh port of the upstream a2a-go helloworld server example.
// Original: https://github.com/a2aproject/a2a-go/tree/main/examples/helloworld/server
package main

import (
	"context"
	"iter"
	"log"
	"os/signal"
	"syscall"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/kynoproj/kynomesh-go/pkg/server"
)

type agentExecutor struct{}

var _ a2asrv.AgentExecutor = (*agentExecutor)(nil)

func (*agentExecutor) Execute(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		response := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Hello, world!"))
		yield(response, nil)
	}
}

func (*agentExecutor) Cancel(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {}
}

// SupportedInterfaces URLs are placeholders; the kynomesh broker
// rewrites them to its externally reachable endpoint.
func helloWorldCard() *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "Hello World Agent",
		Description: "Just a hello world agent",
		Version:     "0.0.1",
		SupportedInterfaces: []*a2a.AgentInterface{
			// The URLs below will be ignored when running in a K8s cluster,
			// supplying 127.0.0.1:8088 is helpful when doing local dev testing.
			a2a.NewAgentInterface("http://127.0.0.1:8088", a2a.TransportProtocolJSONRPC),
			a2a.NewAgentInterface("http://127.0.0.1:8088", a2a.TransportProtocolHTTPJSON),
			a2a.NewAgentInterface("127.0.0.1:8088", a2a.TransportProtocolGRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		Skills: []a2a.AgentSkill{
			{
				ID:          "hello_world",
				Name:        "Hello, world!",
				Description: "Returns a 'Hello, world!'",
				Tags:        []string{"hello world"},
				Examples:    []string{"hi", "hello"},
			},
		},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.Start(ctx, &agentExecutor{}, helloWorldCard()); err != nil {
		log.Fatalf("Agent server: %v", err)
	}
}
