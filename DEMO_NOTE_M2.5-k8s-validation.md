# DEMO NOTE — M2.5 Helm chart K8s validation

## Scope

Validate the M2.5 Helm chart against a real Kubernetes API. Originally
intended to spin up `kind` (Kubernetes-in-Docker) for end-to-end
deployment validation; falls back to comprehensive static validation
because the host blocks the Docker primitives required for in-Docker
K8s clusters.

## Host constraint

This host runs an `ovh` Docker authz plugin that rejects both
`--privileged` and `--userns=host`:

```
docker run --rm --privileged alpine:3.20 echo hi
→ docker: Error response from daemon: plugin ovh failed with error:
  AuthZPlugin.AuthZReq: --privileged not allowed
```

`kind`, `k3d`, and `rancher/k3s`-in-Docker all require both. Installing
k3s natively would need root and would alter the host outside the
repo's scope. Without UoR approval to do so, validation is performed
statically.

## Static validation performed

### 1. helm lint

```
$ helm lint deploy/helm/skoed
==> Linting deploy/helm/skoed
[INFO] Chart.yaml: icon is recommended
1 chart(s) linted, 0 chart(s) failed
```

### 2. Template rendering across 4 value configurations

Each rendered to YAML and validated against the K8s 1.31 OpenAPI schema
via `kubeconform`:

| # | Config | Resources | Schema validation |
|---|--------|-----------|--------------------|
| 1 | Default (bootstrap on, hostPort, 1 replica)        | 6 | ✓ Valid (6/6) |
| 2 | 3 replicas, NodePort service                       | 7 | ✓ Valid (7/7) |
| 3 | Bootstrap disabled (operator-managed Secret)       | 5 | ✓ Valid (5/5) |
| 4 | No persistence (emptyDir)                          | 6 | ✓ Valid (6/6) |

### 3. Topology check

- **DaemonSet** (1 pod per K8s node) — correct shape: every K8s node
  becomes a skoed instance binding hostPort 53/UDP+TCP, so cluster
  DNS works regardless of which node a client resolves through.
- **Service** (ClusterIP, port 8080) — for in-cluster API access.
- **ConfigMap** — carries cluster-replicated config + per-pod
  entrypoint wrapper script that synthesizes a node section from
  `$POD_NAME`/`$POD_IP` on first start.
- **Secret** (`skoed-bootstrap`) — random per-install token + the
  in-cluster Service URL of the leader, picked up by joining pods.
- **ServiceAccount** — placeholder; no RBAC needed (skoed doesn't
  touch the K8s API).
- **Helm test Pod** — curl-based smoke connection probe.

### 4. Port + protocol cross-check

```
- name: dns-udp
  containerPort: 53
  hostPort: 53
  protocol: UDP
- name: dns-tcp
  containerPort: 53
  hostPort: 53
  protocol: TCP
- name: api
  containerPort: 8080
```

DNS bound on the K8s node IP (hostPort), API on the Service ClusterIP
(internal only — operator port-forwards or layers an Ingress for
external access).

## Not validated (requires real cluster)

- Runtime Raft formation between pods.
- PersistentVolumeClaim binding against a real storage class.
- HostPort 53 actually free on the K8s nodes (host-dependent).
- Helm test pod's connection probe against a running Service.
- Upgrade path (`helm upgrade --reuse-values`).

These remain open and should be exercised on a real K8s cluster
(kind on a host without the authz plugin, GKE/AKS/EKS, or k3s on
bare metal) before declaring the chart production-ready.

## Tooling installed for this validation

- `kind` v0.24.0 (could not run — see Host constraint above)
- `kubectl` v1.36.1 (used for `--dry-run=client` — also unable to
  reach an apiserver, see above)
- `helm` v3.16.2 (lint + template — passed)
- `kubeconform` v0.6.7 (schema validation — passed)

All installed to `~/.local/bin/` (no root required, no host changes
outside the user's home).

## What's next

The chart is structurally and schema-correct. Runtime validation
deferred to whichever K8s environment the operator chooses for the
first real deploy. Resuming M3.5 (per-client DoH surfacing) next.
