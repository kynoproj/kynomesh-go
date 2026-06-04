# Research Assistant

A two-agent example showing one Kynomesh agent calling another by name.

The [`helloworld`](../helloworld) example shows a single agent. This is the
next step up: a `coordinator` agent that delegates each incoming question to a
`searcher` worker peer. It exists to answer one question — **what does
calling another agent actually look like in code?**

## What you'll see

The coordinator's `Execute` handler is the whole point of the example:

```go
peer, err := client.NewForPeer(ctx, "searcher")
// ...
resp, err := peer.SendMessage(ctx, &a2a.SendMessageRequest{Message: ...})
```

No peer URL, no transport selection, no AgentCard plumbing. The peer name
`"searcher"` is the same string declared in `manifests/agentset.yaml`.
That's it.

## The agents

Both agents compile into a single binary so one container image serves
every agent in the AgentSet — the `-role` flag picks which one runs.
Each role lives in its own package:

| Role          | Package                                                | What it does                                                           |
| ------------- | ------------------------------------------------------ | ---------------------------------------------------------------------- |
| `coordinator` | [`./coordinator`](coordinator/coordinator.go)          | Entry agent. Receives a question, delegates to `searcher`, replies.    |
| `searcher`    | [`./searcher`](searcher/searcher.go)                   | Worker. Returns canned hits from a tiny in-memory corpus. No peers.    |

[`main.go`](main.go) is just the entry point that reads `-role` and wires
up the right `Executor` and `Card()`. Open `coordinator/coordinator.go`
first — that's where `client.NewForPeer(ctx, "searcher")` happens.

## The AgentSet

The interesting file is [`manifests/agentset.yaml`](manifests/agentset.yaml):

```yaml
apiVersion: kynomesh.kyno.sh/v1alpha1
kind: AgentSet
metadata:
  name: research-assistant
spec:
  pattern: Supervisor    # entry sees all workers; workers see no peers
  entry: coordinator
  agents:
    - name: coordinator
      container:
        image: REPLACE_ME/research-assistant:latest
        args: ["-role", "coordinator"]
    - name: searcher
      container:
        image: REPLACE_ME/research-assistant:latest
        args: ["-role", "searcher"]
```

`pattern: Supervisor` is what makes `client.NewForPeer(ctx, "searcher")`
work from the coordinator. Change the pattern to `Handoff` and every agent
sees every other agent; change it to `Sequential` and each agent sees only
the next one in declaration order. The agent code doesn't change — the
topology does.

## Run it

You need a Kubernetes cluster with [Kynomesh installed][install] and a
container registry you can push to.

[install]: https://github.com/kynoproj/kynomesh#install

```bash
# 1. Build one image for both agents. The included Dockerfile produces a
#    static binary in distroless; the Makefile wraps docker build/push.

# Local build:
make -C examples/research-assistant build

# Or push to a registry:
make -C examples/research-assistant \
    IMAGE_REPO=your-registry.example/research-assistant push

# Multi-arch (linux/amd64,linux/arm64) via buildx:
make -C examples/research-assistant \
    IMAGE_REPO=your-registry.example/research-assistant buildx-push

# 2. Edit manifests/agentset.yaml and replace the two REPLACE_ME image values
#    with the single image you just built.

# 3. Apply.
kubectl apply -f examples/research-assistant/manifests/agentset.yaml

# 4. Wait for the AgentSet to be Ready.
kubectl wait --for=condition=Deployed agentset/research-assistant --timeout=120s

# 5. Send a question to the coordinator. The simplest path is to port-forward
#    the entry agent's broker and use the helloworld client binary:
kubectl port-forward svc/research-assistant-coordinator-headless 8090:8490 &
go run ./examples/helloworld/client -peer coordinator
```

You should see something like:

```
Server responded with: &{Message:0x... Parts:[
  coordinator: handled "Hello, world" via "searcher"
  ---
  searcher: no hits for "hello, world"
]}
```

Try a query the corpus knows:

```bash
go run ./examples/helloworld/client -peer coordinator <<<'tell me about kynomesh'
```

…and the searcher fires back hits the coordinator wraps and returns.

## Clean up

```bash
kubectl delete -f examples/research-assistant/manifests/agentset.yaml
```

## What just happened

When you applied the AgentSet, the Kynomesh controller:

1. Created one `AgentDeploy` per agent (coordinator, searcher).
2. Per pod, scheduled:
   - a **broker** sidecar (Kynomesh-supplied) that terminates A2A on TLS and
     proxies to the agent over a Unix domain socket
   - an **init-runtime** init container that writes a per-agent
     `topology.json` describing which peers this agent may reach, plus the
     `kynoprobe` binary used by the agent container's readiness/liveness
     probes
   - your **agent** container (the binary you built)
3. Created one headless `Service` per agent so peers reach replicas via
   stable cluster DNS.

In the coordinator's pod, `topology.json` lists `searcher` as a Managed peer
with a URL pointing at `research-assistant-searcher-headless.<ns>.svc...`.
`client.NewForPeer(ctx, "searcher")` reads that file, fetches the searcher's
AgentCard from the URL, and builds an A2A client over its preferred
transport. Your code never sees the URL.

In the searcher's pod, `topology.json` lists **no peers** — because
`pattern: Supervisor` says workers don't talk to anyone. If `searcher` tried
`client.NewForPeer(ctx, "coordinator")` it would get `ErrPeerNotFound`.

## What to try next

- Change `pattern` to `Handoff` and have both agents call each other.
- Add a third agent and watch the topology change without touching code.
- Replace the canned corpus in `main.go` with a real retrieval call
  — the `searcher` half of the binary is the only piece you'd actually edit.
