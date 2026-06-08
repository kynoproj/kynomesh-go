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

// Command research-assistant is the single binary for the example. The
// -role flag selects which agent to run, so one container image works
// for every agent in the AgentSet. The role implementations live in
// sibling packages (./coordinator, ./searcher).
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/kynoproj/kynomesh-go/examples/research-assistant/coordinator"
	"github.com/kynoproj/kynomesh-go/examples/research-assistant/searcher"
	"github.com/kynoproj/kynomesh-go/pkg/server"
)

func main() {
	role := flag.String("role", "", "agent role to run: coordinator or searcher")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var (
		exec a2asrv.AgentExecutor
		card *a2a.AgentCard
	)
	switch *role {
	case "coordinator":
		exec, card = &coordinator.Executor{}, coordinator.Card()
	case "searcher":
		exec, card = &searcher.Executor{}, searcher.Card()
	default:
		log.Fatalf(`research-assistant: -role must be "coordinator" or "searcher" (got %q)`, *role)
	}

	if err := server.Start(ctx, exec, card); err != nil {
		log.Fatalf("%s: %v", *role, err)
	}
}
