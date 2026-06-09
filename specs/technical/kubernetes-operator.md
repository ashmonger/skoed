# Technical Specification: Kubernetes Operator

x-tsid: TS-KubernetesOperator
x-fsid-links:
  - FS-OperatorCrdRegistered
  - FS-ClusterProvisioned
  - FS-ClusterScaleUp
  - FS-ClusterScaleDown
  - FS-PvcSurvivesPodRestart
  - FS-AcmeCertAutoRotate
  - FS-StatusConditions
  - FS-StatusConditionsOnFailure
  - FS-HelmFallbackUnaffected

---

## 1. Overview

M9 introduces a native Kubernetes operator that manages skoed clusters via CRDs,
superseding the M2.5 Helm DaemonSet for production Kubernetes deployments.

| Component         | Kind / Resource                          | API group / version   |
|-------------------|------------------------------------------|-----------------------|
| Cluster resource  | `SkoedCluster` CRD                       | `skoed.io/v1alpha1`   |
| Per-pod status    | `SkoedNode` CRD (read-only, operator-managed) | `skoed.io/v1alpha1` |
| Pod management    | `StatefulSet` (K8s built-in)             | `apps/v1`             |
| Stable DNS        | Headless `Service` (ClusterIP: None)     | `core/v1`             |
| Per-node config   | Shell script in `ConfigMap`              | `core/v1`             |
| Enrollment secret | `Secret` with bootstrap token + admin creds | `core/v1`          |
| Raft + app data   | `PersistentVolumeClaim` per pod (StatefulSet PVC template) | `core/v1` |

---

## 2. Architecture

```
Operator (Deployment, 1 replica)
  └─ watches SkoedCluster CR
       │
       ├── creates headless Service  <cluster>.<ns>.svc.cluster.local
       │     └─ selector: skoed.io/cluster=<name>
       │
       ├── creates Secret  <cluster>-bootstrap
       │     └─ key "token" = 32-byte random hex  (enrollment token)
       │
       ├── creates ConfigMap  <cluster>-scripts
       │     └─ key "init-config.sh"  (shell script — writes /data/node.yaml)
       │
       └── creates StatefulSet  <cluster>
             ├─ pod-0  <cluster>-0  →  bootstrap leader  (no bootstrap: section)
             ├─ pod-1  <cluster>-1  →  follower (bootstrap.leader_address = pod-0)
             └─ pod-N  <cluster>-N  →  follower
                 │
                 ├─ initContainer: alpine:3.20 runs init-config.sh → /data/node.yaml
                 ├─ container:     skoed --config /data/node.yaml
                 └─ PVC "data" at /data  (persists across pod restarts)
```

---

## 3. CRD Schemas

### 3.1 SkoedCluster

**Spec fields:**

| Field                   | Type                          | Default | Notes |
|-------------------------|-------------------------------|---------|-------|
| `replicas`              | int32 (1–7)                   | 1       | Raft voter count |
| `image`                 | string (required)             | —       | e.g. `ghcr.io/skoed/skoed:latest` |
| `storage.size`          | Quantity (required)           | —       | e.g. `1Gi` per pod |
| `storage.storageClass`  | string (optional)             | cluster default | |
| `dns.port`              | int32                         | 53      | |
| `api.port`              | int32                         | 8080    | |
| `tls.secretName`        | string (optional)             | —       | existing K8s TLS Secret for DoH/DoT |
| `tls.acmeDomain`        | string (optional)             | —       | enables ACME cert inside pods |
| `adminSecretRef.name`   | string (optional)             | —       | K8s Secret with keys `username`, `password` |
| `resources`             | ResourceRequirements          | —       | CPU/memory limits for skoed container |

**Status fields:**

| Field             | Type                  | Notes |
|-------------------|-----------------------|-------|
| `conditions`      | `[]metav1.Condition`  | `Ready`, `Quorum` |
| `leader`          | string                | pod name of current Raft leader |
| `voters`          | `[]string`            | pod names of all Raft voters |
| `readyReplicas`   | int32                 | from StatefulSet status |
| `certExpiry`      | `*metav1.Time`        | TLS cert expiry, when TLS configured |

**Status conditions:**

| Condition | True meaning                                    | False meaning                         |
|-----------|-------------------------------------------------|---------------------------------------|
| `Ready`   | `readyReplicas == spec.replicas`               | One or more pods not yet ready        |
| `Quorum`  | Leader is reachable via cluster status API      | No leader elected or API unreachable  |

**PrinterColumns:** Replicas, ReadyReplicas, Leader, Age.

### 3.2 SkoedNode (operator-managed, read-only)

**Status fields:**

| Field          | Type            | Values |
|----------------|-----------------|--------|
| `podName`      | string          | — |
| `role`         | string          | `leader` \| `follower` \| `candidate` \| `unknown` |
| `healthy`      | bool            | — |
| `commitIndex`  | int64           | — |
| `lastContact`  | `*metav1.Time`  | — |

**PrinterColumns:** Pod, Role, Healthy, Age.

---

## 4. Reconciler Flow

The `SkoedClusterReconciler.Reconcile` method runs on every SkoedCluster change and on a 30 s requeue:

1. **Fetch** the SkoedCluster CR; return if `NotFound`.
2. **Bootstrap Secret** — if absent, generate a 32-byte random hex token and create `<name>-bootstrap` Secret.
3. **Headless Service** — if absent, create a ClusterIP-None Service with DNS, API, and Raft ports; label selector `skoed.io/cluster=<name>`.
4. **Scripts ConfigMap** — if absent, create `<name>-scripts` with `init-config.sh` (see §5).
5. **Pre-deregistration** (scale-down only) — if `desired replicas < current StatefulSet replicas`, call `DELETE /api/v1/cluster/nodes/{podName}` for each pod being removed before updating the StatefulSet (see §6).
6. **StatefulSet** — if absent, create. If replicas differ, patch replicas only (no pod template change unless image or resources changed).
7. **Cert rotation check** — if TLS Secret is configured and expires within 30 days, annotate StatefulSet pod template to trigger rolling restart (see §7).
8. **Status sync** — query `GET /api/v1/cluster/status` on pod-0; update `status.leader`, `status.voters`, `status.readyReplicas`, and conditions `Ready` + `Quorum`.
9. **Requeue** after 30 s.

All created resources carry a controller owner reference to the SkoedCluster CR so they are garbage-collected when the CR is deleted.

---

## 5. Init Config Script (`init-config.sh`)

Stored in ConfigMap `<cluster>-scripts`, executed by the `alpine:3.20` init container.
Env vars injected into the init container:

| Env var           | Source |
|-------------------|--------|
| `POD_NAME`        | Downward API `metadata.name` |
| `POD_NAMESPACE`   | Downward API `metadata.namespace` |
| `API_PORT`        | `spec.api.port` from CR |
| `DNS_PORT`        | `spec.dns.port` from CR |
| `RAFT_PORT`       | Hardcoded `9300` |
| `BOOTSTRAP_TOKEN` | SecretKeyRef `<cluster>-bootstrap` / `token` |

Script logic (pseudocode):

```sh
INDEX = pod ordinal suffix  (e.g. "2" from "mycluster-2")
CLUSTER = pod name prefix   (e.g. "mycluster")

write /data/node.yaml with:
  node.id = POD_NAME
  node.raft_address = "0.0.0.0:RAFT_PORT"
  node.api_address  = "0.0.0.0:API_PORT"
  node.dns.listen.port = DNS_PORT
  node.data_dir = /data

if INDEX != "0":
  append bootstrap section:
    bootstrap.leader_address = CLUSTER-0.CLUSTER.POD_NAMESPACE.svc.cluster.local:API_PORT
    bootstrap.token = BOOTSTRAP_TOKEN
```

Pod 0 (INDEX == "0") receives no `bootstrap:` section — it is the Raft bootstrap leader.
Pods 1+ include `bootstrap:` and enroll using the pre-shared token.

---

## 6. Scale-Down Pre-Deregistration Sequence

When `spec.replicas` decreases (e.g., 3 → 1):

1. Reconciler reads current StatefulSet replica count.
2. For each pod index `i` from `current-1` down to `desired` (removing highest ordinals first):
   a. Query `GET /api/v1/cluster/status` on any pod to identify the current leader.
   b. If pod `i` **is** the leader: `POST /api/v1/cluster/leadership/transfer` to elect a different leader.
   c. Call `DELETE /api/v1/cluster/nodes/{podName}` — the endpoint auto-forwards to the leader.
   d. Log errors but continue (best-effort; Raft will eventually converge if a deregistration is missed).
3. After all deregistrations attempted, patch StatefulSet replicas to `desired`.

HTTP calls use Basic Auth from `spec.adminSecretRef` (if configured).
Target URL pattern: `http://<pod>.<cluster>.<namespace>.svc.cluster.local:<apiPort>/api/v1/...`
Request timeout: 5 s per call.

---

## 7. ACME Cert Rotation

When `spec.tls.secretName` is non-empty:

1. Reconciler reads the TLS Secret named `spec.tls.secretName`.
2. Parses the `tls.crt` field to extract the certificate expiry date.
3. If `expiry - now < 30 days`:
   a. Annotates the StatefulSet pod template with `skoed.io/cert-restart: <RFC3339 timestamp>`.
   b. This triggers a rolling pod restart, allowing each pod's ACME renewal logic (M4) to run on startup.
4. Updates `status.certExpiry` with the current expiry date on every reconcile.

The operator does **not** directly call ACME endpoints — cert renewal is handled by skoed internally (M4 feature). The operator only detects expiry and triggers the restart.

---

## 8. Helm Chart Structure

```
deploy/helm/skoed-operator/
  Chart.yaml
  values.yaml
  templates/
    serviceaccount.yaml
    rbac.yaml              (ClusterRole + ClusterRoleBinding)
    deployment.yaml        (operator controller, 1 replica)
    crds/
      skoedcluster.yaml    (CRD apiextensions.k8s.io/v1)
      skoednode.yaml
```

The operator requires these RBAC permissions:
- `skoed.io/*`: full access (CRDs, status subresources)
- `apps/statefulsets`: full access
- `core/services,configmaps,secrets,persistentvolumeclaims`: full access
- `core/pods`: get, list, watch
- `coordination.k8s.io/leases`: full access (leader election)

---

## 9. External Dependencies

| Library                             | Version  | Purpose |
|-------------------------------------|----------|---------|
| `sigs.k8s.io/controller-runtime`    | v0.19.0  | Operator framework, reconciler, leader election |
| `k8s.io/api`                        | v0.31.0  | K8s resource types |
| `k8s.io/apimachinery`               | v0.31.0  | Meta types, scheme |
| `k8s.io/client-go`                  | v0.31.0  | K8s client |

---

## 10. Acceptance Test Approach

Tests in `tests/acceptance/kubernetes_operator_test.go`:
- Require `helm` CLI (skip otherwise).
- Run `helm template` against the operator chart and plain skoed chart.
- Parse rendered YAML to verify CRD presence, printer columns, and RBAC rules.
- Full behavioral tests (scale-up/down, PVC persistence) require a live cluster with `kind`; those tests are `t.Skip`'d with clear messages explaining the requirement.

---

## 11. Delivery Sequence

1. Go API types: `api/v1alpha1/` (SkoedCluster, SkoedNode, groupversion).
2. Reconciler: `internal/controllers/skoedcluster_controller.go`.
3. Main: `cmd/operator/main.go`.
4. Helm chart: `deploy/helm/skoed-operator/`.
5. Acceptance tests: `tests/acceptance/kubernetes_operator_test.go`.
6. Demo note: `demos/m9/DEMO_NOTE.md`.
