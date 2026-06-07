# Kynomesh Go SDK

[![GoDoc](https://godoc.org/github.com/kynoproj/kynomesh-go?status.svg)](https://godoc.org/github.com/kynoproj/kynomesh-go/pkg)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Go SDK for [Kynomesh](https://github.com/kynoproj/kynomesh) — a
Kubernetes-native platform for orchestrating distributed multi-agent systems
based on the [A2A protocol](https://a2a-protocol.org/).

## What this SDK does

In Kynomesh, your agent code runs alongside an injected broker that handles peer
discovery, transport, and external A2A traffic. This SDK gives you a thin layer
over the upstream [a2aproject/a2a-go](https://github.com/a2aproject/a2a-go) v2
SDK that handles the Kynomesh-specific wiring:

- **`pkg/server`** — start an A2A agent that the broker can reach. Picks the
  right listener automatically and advertises the agent to the broker.
- **`pkg/client`** — call other agents in the same `AgentSet` by name. No URLs,
  no transports, no AgentCard plumbing in your code.

Everything else — `a2a.AgentCard`, `a2a.Message`, executors, request handlers —
comes straight from `a2a-go`. This SDK does not wrap or replace those types.

## Relationship to the A2A Go SDK

| `a2aproject/a2a-go`                                                     | `kynoproj/kynomesh-go`                                  |
| ----------------------------------------------------------------------- | ------------------------------------------------------- |
| `agentcard.DefaultResolver.Resolve(ctx, url)` + `a2aclient.NewFromCard` | `client.NewForPeer(ctx, peerName)`                      |
| Caller hardcodes peer URLs or reads them from config                    | Peers are discovered by name                            |
| Caller picks the listener address                                       | `server.Start` picks the right listener for the runtime |
| Caller advertises the agent to peers                                    | Handled by `server.Start`                               |

You can drop down to the upstream SDK at any time — `pkg/client` accepts
`a2aclient.FactoryOption...` so anything that works with `NewFromCard` works
here.

## Install

```bash
go get github.com/kynoproj/kynomesh-go
```

## Server: write an agent

Implement the upstream `a2asrv.AgentExecutor` interface, build an
`a2a.AgentCard`, and call `server.Start`. The `SupportedInterfaces` URLs on the
card are placeholders — Kynomesh rewrites them to the externally reachable
endpoint at serve time.

```go
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

func (*agentExecutor) Execute(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
    return func(yield func(a2a.Event, error) bool) {
        yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Hello, world!")), nil)
    }
}

func (*agentExecutor) Cancel(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
    return func(yield func(a2a.Event, error) bool) {}
}

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    card := &a2a.AgentCard{
        Name:    "Hello World Agent",
        Version: "0.0.1",
        SupportedInterfaces: []*a2a.AgentInterface{
            a2a.NewAgentInterface("http://127.0.0.1:8088/a2a/jsonrpc", a2a.TransportProtocolJSONRPC),
            a2a.NewAgentInterface("127.0.0.1:8088", a2a.TransportProtocolGRPC),
        },
    }
    if err := server.Start(ctx, &agentExecutor{}, card); err != nil {
        log.Fatalf("agent server: %v", err)
    }
}
```

What `server.Start` does for you:

- Picks the right listener for the runtime — an in-cluster transport when
  running under Kynomesh, or a local TCP address (`127.0.0.1:8088`) for
  development.
- Mounts JSON-RPC, REST, and gRPC transports based on
  `card.SupportedInterfaces`.
- Advertises the agent so peers can discover it.

Full example: [examples/helloworld/server](examples/helloworld/server).

## Health checks

`server.Start` always mounts two health endpoints on the same listener so
Kynomesh's `kynoprobe` can drive readiness and liveness probes regardless of
which A2A transports the card advertises:

- **gRPC** — the standard `grpc.health.v1.Health/Check` service (matches
  `kynoprobe --mode=grpc`, the default the controller uses).
- **HTTP** — `GET /healthz` returns `200 SERVING` or `503 NOT_SERVING` (matches
  `kynoprobe --mode=http --path=/healthz`).

By default the agent reports `SERVING` for its lifetime and flips to
`NOT_SERVING` automatically when `Start` begins shutting down — that's enough
for most agents and needs no extra code.

### Writing a customized health check

Out of the box, the agent always reports SERVING — which is misleading once your
agent depends on something it can't guarantee, like an LLM endpoint, a database
connection, or a model file loaded at startup. In those cases, "ready" is a
property of those dependencies, not of the process itself.

`server.WithHealth` lets you define what readiness actually means. Create a
`*server.Health`, run your own checks against the things the agent needs, and
call `SetServing(true|false)` to publish the result. `kynoprobe` picks up the
change on its next poll.

```go
package main

import (
    "context"
    "log"
    "net/http"
    "os/signal"
    "syscall"
    "time"

    "github.com/a2aproject/a2a-go/v2/a2a"
    "github.com/kynoproj/kynomesh-go/pkg/server"
)

// checkLLM is your agent-specific readiness predicate: it returns nil
// when the upstream the agent depends on is reachable.
func checkLLM(ctx context.Context) error {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.com/v1/models", nil)
    if err != nil {
        return err
    }
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return err
    }
    _ = resp.Body.Close()
    if resp.StatusCode >= 500 {
        return http.ErrServerClosed
    }
    return nil
}

// watchHealth polls the predicate and flips Health whenever the result
// changes. Run it in a goroutine; it returns when ctx is cancelled.
func watchHealth(ctx context.Context, h *server.Health, check func(context.Context) error) {
    tick := time.NewTicker(5 * time.Second)
    defer tick.Stop()
    for {
        probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
        err := check(probeCtx)
        cancel()
        if err != nil {
            log.Printf("health: dependency down: %v", err)
            h.SetServing(false)
        } else {
            h.SetServing(true)
        }
        select {
        case <-ctx.Done():
            return
        case <-tick.C:
        }
    }
}

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    health := server.NewHealth()
    go watchHealth(ctx, health, checkLLM)

    card := &a2a.AgentCard{
        Name:    "llm-backed-agent",
        Version: "0.0.1",
        SupportedInterfaces: []*a2a.AgentInterface{
            a2a.NewAgentInterface("http://127.0.0.1:8088", a2a.TransportProtocolJSONRPC),
        },
    }
    if err := server.Start(ctx, &agentExecutor{}, card,
        server.WithHealth(health),
    ); err != nil {
        log.Fatalf("agent server: %v", err)
    }
}
```

`*server.Health` is safe to share across goroutines, and the same state is
observed by both the gRPC and HTTP surfaces — `kynoprobe` sees the flip on its
next tick regardless of which mode it runs in.

Pick the check to match what "ready" actually means for your agent:

- LLM/API-backed agent → ping the provider.
- Agent that needs a model file on disk → check the loaded flag.
- Agent with a bounded work queue → flip on depth thresholds.

Keep the check cheap and bounded — it runs on every poll, and a slow check just
delays the next status update.

## Client: call a peer agent

Within an `AgentSet`, every agent has a set of peers it is allowed to call,
derived from the AgentSet's routing pattern. `client.NewForPeer` collapses the
whole upstream `agentcard.Resolve` + `a2aclient.NewFromCard` flow into one call:

```go
package main

import (
    "context"
    "log"

    "github.com/a2aproject/a2a-go/v2/a2a"

    "github.com/kynoproj/kynomesh-go/pkg/client"
)

func main() {
    ctx := context.Background()

    // Discover the peer, fetch its AgentCard, and build an a2a
    // client. For Managed peer agents, when the gRPC transport is
    // used, NewForPeer registers a gRPC transport that uses TLS
    // for encryption but skips certificate verification. Pass
    // a2agrpc.WithGRPCTransport(...) to override.
    c, err := client.NewForPeer(ctx, "worker-a")
    if err != nil {
        log.Fatalf("create client: %v", err)
    }

    msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hello, world"))
    resp, err := c.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
    if err != nil {
        log.Fatalf("send message: %v", err)
    }
    log.Printf("response: %+v", resp)
}
```

Lower-level helpers when you don't want the full client:

```go
url, err := client.PeerURL("worker-a")                // just the URL
card, err := client.ResolveAgentCard(ctx, "worker-a") // just the AgentCard
names, err := client.Peers()                          // every reachable peer
```

Error sentinels for `errors.Is` checks:

- `client.ErrPeerNotFound` — the peer is not reachable from this agent.
- `client.ErrTopologyNotAvailable` — peer discovery is not available (e.g.
  running outside a Kynomesh deployment).

Peer information is loaded once per process and cached for its lifetime.

Full example: [examples/helloworld/client](examples/helloworld/client).

## Resources

- [Kynomesh project](https://github.com/kynoproj/kynomesh)
- [Core concepts](https://github.com/kynoproj/kynomesh/blob/main/docs/core-concepts/overview.md)
- [A2A protocol](https://a2a-protocol.org/)
- [a2aproject/a2a-go](https://github.com/a2aproject/a2a-go)

## License

Apache 2.0 — see [LICENSE](LICENSE).
