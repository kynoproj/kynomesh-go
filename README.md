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
            a2a.NewAgentInterface("http://127.0.0.1:8088", a2a.TransportProtocolJSONRPC),
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

## Client: call a peer

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
    // client. For Managed peers (the default — peers in the same
    // AgentSet), NewForPeer registers a gRPC transport with
    // insecure credentials by default; pass
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
