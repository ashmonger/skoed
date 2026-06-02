# dblock Helm chart

Helm chart for deploying [dblock](https://github.com/dblock/dblock) — a
self-hosted DNS filter with native multi-node Raft clustering — onto a
Kubernetes cluster.

## Quick start

```sh
helm install dblock ./deploy/helm/dblock
```

This bootstraps a DaemonSet (one pod per K8s node) with:

- DNS listener on `hostPort: 53` (UDP+TCP) → every node serves DNS.
- Management API on a ClusterIP Service at port 8080.
- A `*-bootstrap` Secret containing a randomly-generated join token + the
  in-cluster Service URL of the cluster's leader.
- A per-pod PersistentVolumeClaim for `cluster.bbolt` + raft log + the
  shadow `config.yaml`.

The first pod scheduled becomes the Raft leader; every subsequent pod
enrols using the shared token in the Secret.

## Verification

```sh
kubectl port-forward svc/dblock 8080:8080
curl http://localhost:8080/api/v1/cluster/health
```

## Common overrides

```sh
# Pin the image tag
helm upgrade --install dblock ./deploy/helm/dblock --set image.tag=v0.2.0

# Use NodePort for DNS (managed K8s often disallows hostPort)
helm upgrade --install dblock ./deploy/helm/dblock \
  --set service.dns.hostPort=false \
  --set service.dns.nodePort.enabled=true

# Larger PVC + tighter resources
helm upgrade --install dblock ./deploy/helm/dblock \
  --set persistence.size=5Gi \
  --set resources.limits.memory=512Mi

# BYO bootstrap token (e.g. from external-secrets)
helm upgrade --install dblock ./deploy/helm/dblock \
  --set bootstrap.enabled=false
# (then create your own Secret named <release>-bootstrap with keys `token`
#  and `leader-address`)
```

## Uninstalling

```sh
helm uninstall dblock
```

PVCs are intentionally **not** deleted by `helm uninstall` so that an
accidental teardown doesn't wipe cluster state. Delete them manually:

```sh
kubectl delete pvc -l app.kubernetes.io/instance=dblock
```

## Limitations (M2.5)

- TLS termination for the management API requires an Ingress or sidecar.
- DoH/DoT server endpoints are out of scope here; landing in M4.
- The chart assumes a writable shared storage class for PVCs. On clusters
  without one, set `persistence.enabled=false` to use `emptyDir` — pods
  rebuild Raft state from scratch on every restart, which is fine for
  evaluation but loses history.
