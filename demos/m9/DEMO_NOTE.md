# Demo Note — M9: Kubernetes Operator

## Implemented scope

### Operator and CRDs
- `SkoedCluster` CRD (`skoed.io/v1alpha1`): manages a skoed cluster on Kubernetes.
  Fields: `spec.replicas`, `spec.image`, `spec.storage`, `spec.dns`, `spec.api`, `spec.tls`, `spec.adminSecretRef`, `spec.resources`.
- `SkoedNode` CRD: read-only per-pod status resource (operator-created). Fields: `role`, `healthy`, `commitIndex`.
- Operator Helm chart at `deploy/helm/skoed-operator/`: Deployment, ServiceAccount, ClusterRole, ClusterRoleBinding, both CRDs.
- Controller built with `sigs.k8s.io/controller-runtime` v0.19.0.

### Lifecycle: provisioning
- Creating a `SkoedCluster` CR provisions a `StatefulSet` + headless `Service` + `<name>-scripts` ConfigMap + `<name>-bootstrap` Secret (auto-generated enrollment token).
- An `alpine:3.20` init container writes `/data/node.yaml` per pod:
  - Pod 0 → bootstrap leader (no `bootstrap:` section).
  - Pods 1+ → followers with `bootstrap.leader_address` pointing to pod 0 and the shared token.
- Each pod gets a dedicated PVC (`volumeClaimTemplates`): Raft state survives pod restarts and node rescheduling.

### Lifecycle: scaling
- **Scale up** — operator increments StatefulSet replicas; new pods write their node.yaml via init container and auto-enroll using the bootstrap token.
- **Scale down** — operator calls `DELETE /api/v1/cluster/nodes/{node_id}` (auto-forwarded to leader) for each pod being removed before patching StatefulSet replicas. If the pod being removed is the current leader, `POST /api/v1/cluster/leadership/transfer` is called first.

### Status conditions (FS-StatusConditions, FS-StatusConditionsOnFailure)
- `SkoedCluster.status.conditions`: `Ready` (readyReplicas == spec.replicas), `Quorum` (leader reachable).
- `SkoedCluster.status.leader`, `.voters`, `.readyReplicas` synced from `GET /api/v1/cluster/status` every 30 s.
- `kubectl get skoedcluster` shows Replicas, Ready, Leader columns without additional flags.

### ACME cert rotation (FS-AcmeCertAutoRotate)
- Operator reads TLS Secret expiry from `spec.tls.secretName`.
- If cert expires within 30 days: annotates StatefulSet pod template with `skoed.io/cert-restart: <timestamp>` to trigger rolling restart. Cert renewal runs inside each pod via the M4 ACME logic on startup.
- `status.certExpiry` tracks the current cert expiry date.

### Helm fallback (FS-HelmFallbackUnaffected)
- Existing `deploy/helm/skoed/` chart is unaffected. The operator selects pods exclusively via the label `skoed.io/cluster=<name>` and does not adopt pods it did not create.

## Quick start

```bash
# 1. Install the operator
helm install skoed-operator deploy/helm/skoed-operator/

# 2. Create admin credentials Secret
kubectl create secret generic my-cluster-admin \
  --from-literal=username=admin \
  --from-literal=password=changeme

# 3. Create a 3-node cluster
kubectl apply -f - <<EOF
apiVersion: skoed.io/v1alpha1
kind: SkoedCluster
metadata:
  name: my-cluster
spec:
  replicas: 3
  image: ghcr.io/skoed/skoed:latest
  storage:
    size: 1Gi
  adminSecretRef:
    name: my-cluster-admin
EOF

# 4. Watch status
kubectl get skoedcluster my-cluster -w

# 5. Scale down to 1
kubectl patch skoedcluster my-cluster --type merge -p '{"spec":{"replicas":1}}'
```

## Not implemented

- `SkoedNode` status population: the CRD is registered and the type exists, but the reconciler does not yet create individual `SkoedNode` objects per pod (per-pod Raft role is readable from `SkoedCluster.status` instead).
- Web UI integration for operator-managed clusters.
- Rolling binary upgrade safety validation: changing `spec.image` triggers a StatefulSet rolling update but Raft safety during the upgrade is not verified by the operator.
- cert-manager integration (ACME is managed directly inside skoed pods; the operator only triggers restarts).

## Limitations

- The bootstrap token is generated once at cluster creation and stored in a K8s Secret. Token rotation requires deleting the Secret (operator regenerates on next reconcile) and restarting pods.
- ACME HTTP-01 challenge requires port 80 to be reachable on the pod IP; this may conflict with in-cluster network policies.
- `DELETE /api/v1/cluster/nodes/{id}` calls during scale-down are best-effort: if a pod is unreachable, the Raft cluster will eventually converge after the pod is deleted.
