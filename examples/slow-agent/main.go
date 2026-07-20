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

// Command slow-agent is a single-agent A2A server whose handler deliberately
// holds each request open for a fixed delay before replying. It exists for the
// Kynomesh autoscaling e2e test: concurrent requests pile up as in-flight
// occupancy, which is the signal the autoscaler scales on. The delay is set by
// -delay (or the SLOW_AGENT_DELAY env var), e.g. "5s".
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kynoproj/kynomesh-go/pkg/server"
)

func main() {
	delay := flag.Duration("delay", defaultDelay(), "per-request processing delay, e.g. 5s")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("slow-agent: starting with per-request delay %s", *delay)
	if err := server.Start(ctx, &Executor{delay: *delay}, Card()); err != nil {
		log.Fatalf("slow-agent: %v", err)
	}
}

// defaultDelay reads SLOW_AGENT_DELAY, falling back to 5s. An unparseable value
// falls back too, so a bad env var can't stop the agent from starting.
func defaultDelay() time.Duration {
	const fallback = 5 * time.Second
	if v := os.Getenv("SLOW_AGENT_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("slow-agent: invalid SLOW_AGENT_DELAY %q, using %s", v, fallback)
	}
	return fallback
}
