# Research Assistant

A two-agent example showing one Kynomesh agent calling another by name.

A `coordinator` agent delegates each incoming question to a `searcher` worker
peer. It exists to answer one question — **what does calling another agent
actually look like in code?**

## What you'll see

The coordinator's `Execute` handler is the whole point of the example:

```go
peer, err := client.NewForPeer(ctx, "searcher")
// ...
resp, err := peer.SendMessage(ctx, &a2a.SendMessageRequest{Message: ...})
```

No peer URL, no transport selection, no AgentCard plumbing. The peer name
`"searcher"` is the same string declared in `manifests/agentset.yaml`. That's
it.

## The agents

Both agents compile into a single binary so one container image serves every
agent in the AgentSet — the `-role` flag picks which one runs. Each role lives
in its own package:

| Role          | Package                                       | What it does                                                        |
| ------------- | --------------------------------------------- | ------------------------------------------------------------------- |
| `coordinator` | [`./coordinator`](coordinator/coordinator.go) | Entry agent. Receives a question, delegates to `searcher`, replies. |
| `searcher`    | [`./searcher`](searcher/searcher.go)          | Worker. Returns canned hits from a tiny in-memory corpus. No peers. |

[`main.go`](main.go) is just the entry point that reads `-role` and wires up the
right `Executor` and `Card()`. Open `coordinator/coordinator.go` first — that's
where `client.NewForPeer(ctx, "searcher")` happens.

## The AgentSet

The interesting file is [`manifests/agentset.yaml`](manifests/agentset.yaml):

```yaml
apiVersion: kynomesh.kyno.sh/v1alpha1
kind: AgentSet
metadata:
  name: research-assistant
spec:
  pattern: Supervisor # entry sees all workers; workers see no peers
  entry: coordinator
  agents:
    - name: coordinator
      container:
        image: quay.io/kynoio/examples/research-assistant-go:latest
        args: ["-role", "coordinator"]
    - name: searcher
      container:
        image: quay.io/kynoio/examples/research-assistant-go:latest
        args: ["-role", "searcher"]
```

`pattern: Supervisor` is what makes `client.NewForPeer(ctx, "searcher")` work
from the coordinator. Change the pattern to `Handoff` and every agent sees every
other agent; change it to `Sequential` and each agent sees only the next one in
declaration order. The agent code doesn't change — the topology does.

## Run it

You need a Kubernetes cluster with `Kynomesh`
[installed](https://github.com/kynoproj/kynomesh/blob/main/docs/operations/installation.md).

```bash

# 1. Install a2acli client:

curl -fsSL https://raw.githubusercontent.com/kynoproj/a2acli/main/install.sh | bash

# 2. Apply the AgentSet manifest.

kubectl apply -f examples/research-assistant/manifests/agentset.yaml

# 3. Wait for the AgentSet to be Ready.

kubectl wait --for=condition=Deployed agentset/research-assistant --timeout=120s

# 4. Port-forward the research assistant's ingress endpoint.

kubectl port-forward svc/research-assistant-ingress 8490

#5. In a different terminal, send a question to the ingress.

a2acli -k -u https://localhost:8490 --override-host=localhost:8490 \
 send 'Hello, what can you do?'

```

The flags: `-k` skips TLS verification (the broker serves a self-signed cert),
and `--override-host` keeps the SNI/Host header aligned with the in-cluster
service name the broker expects.

You should see something like:

```json
{
  "messageId": "019ea01f-96ea-7590-963d-7fc6b05e569e",
  "parts": [
    {
      "text": "coordinator: handled \"Hello, what can you do?\" via \"searcher\"\n---\nsearcher: no hits for \"hello, what can you do?\""
    }
  ],
  "role": "ROLE_AGENT"
}
```

Try a query the corpus knows:

```bash
a2acli -k -u https://localhost:8490 --override-host=localhost:8490 \
    send 'tell me about kynomesh'
```

…and the searcher fires back hits the coordinator wraps and returns.

## Clean up

```bash
kubectl delete -f examples/research-assistant/manifests/agentset.yaml
```
