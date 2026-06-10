# Kubernetes

Deploy skoed on Kubernetes using the official Helm chart published to the OCI registry.

---

## Prerequisites

- Helm 3.8 or later (OCI registry support is enabled by default from 3.8 onward)
- A Kubernetes cluster with a storage class that supports `ReadWriteOnce` PVCs
- Port 53 accessible from cluster nodes (consider a `hostNetwork: true` or `LoadBalancer` service depending on your CNI)

Verify your Helm version:

```bash
helm version
# version.BuildInfo{Version:"v3.x.x", ...}
```

---

## Quick Install

```bash
helm install skoed oci://ghcr.io/ashmonger/charts/skoed \
  --namespace skoed \
  --create-namespace
```

This installs skoed with default values: a single-replica `Deployment`, a `ClusterIP` service on port 8080, and a 1 Gi persistent volume.

---

## Key Values

| Value | Default | Description |
|-------|---------|-------------|
| `replicaCount` | `1` | Number of replicas (see cluster note below) |
| `image.tag` | `latest` | Container image tag |
| `service.type` | `ClusterIP` | Kubernetes service type (`ClusterIP`, `NodePort`, `LoadBalancer`) |
| `persistence.enabled` | `true` | Mount a PVC at `/var/lib/skoed` |
| `persistence.size` | `1Gi` | PVC size |
| `config` | `{}` | Inline skoed `config.yaml` as a YAML value |
| `kind` | `Deployment` | Top-level workload kind (`Deployment` or `DaemonSet`) |

---

## Custom Values Example

The following `values.yaml` deploys skoed as a `DaemonSet` (one pod per node) with a custom DNS listener and upstream resolvers:

```yaml
# values.yaml
kind: DaemonSet

image:
  tag: "1.4.2"

service:
  type: NodePort

persistence:
  enabled: true
  size: 2Gi

config:
  node:
    id: ""          # auto-generated from pod name when empty
  dns:
    listen: "0.0.0.0:53"
  api:
    listen: "0.0.0.0:8080"
  upstream:
    - "1.1.1.1:53"
    - "9.9.9.9:53"
  cache:
    ttl_override: 300
```

Apply with:

```bash
helm install skoed oci://ghcr.io/ashmonger/charts/skoed \
  --namespace skoed \
  --create-namespace \
  -f values.yaml
```

---

## Cluster Mode

skoed uses Raft for state replication, which requires each cluster member to have a stable identity and its own persistent volume. Setting `replicaCount > 1` on a single `Deployment` is **not** a supported cluster topology — replicas would share no coordination.

For a proper cluster, install one Helm release per node with unique `config.node.id` values and cross-node `cluster.peers` entries:

```bash
helm install skoed-node-1 oci://ghcr.io/ashmonger/charts/skoed \
  --namespace skoed --create-namespace \
  --set config.node.id=node-1 \
  --set "config.cluster.peers={skoed-node-2-svc:9000,skoed-node-3-svc:9000}"

helm install skoed-node-2 oci://ghcr.io/ashmonger/charts/skoed \
  --namespace skoed \
  --set config.node.id=node-2 \
  --set "config.cluster.peers={skoed-node-1-svc:9000,skoed-node-3-svc:9000}"

helm install skoed-node-3 oci://ghcr.io/ashmonger/charts/skoed \
  --namespace skoed \
  --set config.node.id=node-3 \
  --set "config.cluster.peers={skoed-node-1-svc:9000,skoed-node-2-svc:9000}"
```

See [Cluster bootstrap](../cluster/bootstrap.md) for the full multi-node setup guide.

---

## Upgrade

```bash
helm upgrade skoed oci://ghcr.io/ashmonger/charts/skoed \
  --namespace skoed \
  --version <new-version>
```

Pass `-f values.yaml` if you maintain a local values file. Helm performs a rolling update; skoed rejoins the Raft cluster automatically after each pod restart.

---

## Uninstall

```bash
helm uninstall skoed --namespace skoed
```

This removes the Helm release and its Kubernetes resources. The PVC is **retained** by default (Kubernetes reclaim policy). Delete it explicitly if you want to remove all data:

```bash
kubectl delete pvc -l app.kubernetes.io/instance=skoed -n skoed
```

---

## Next steps

- [First-run authentication setup](../first-run/auth-setup.md)
- [Cluster bootstrap](../cluster/bootstrap.md)
