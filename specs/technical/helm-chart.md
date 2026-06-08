---
x-tsid: TS-HelmChart
x-fsid-links:
  - FS-HelmChartTemplatesRender
  - FS-HelmChartValuesOverrides
  - FS-HelmChartHostPortDns
  - FS-HelmChartBootstrapToken
  - FS-HelmChartPersistenceSurvivesPodRestart
  - FS-HelmChartManagementApiService
---

# TS-HelmChart — Helm chart layout and rendered shape

skoed ships a Helm chart at `deploy/helm/skoed/`. One release per cluster;
the DaemonSet places exactly one pod on every schedulable node. Cluster
membership forms automatically: the first scheduled pod bootstraps the Raft
cluster, every subsequent pod uses the chart-provided join token to enroll.

## Directory layout

```
deploy/helm/skoed/
├── Chart.yaml
├── values.yaml
├── values.schema.json            # values validation (optional)
├── README.md
├── templates/
│   ├── _helpers.tpl              # name/labels helpers
│   ├── serviceaccount.yaml
│   ├── secret-bootstrap.yaml     # join token; only rendered when bootstrap.enabled
│   ├── configmap-node.yaml       # base config.yaml template (cluster sections)
│   ├── service.yaml              # ClusterIP for the management API
│   ├── service-dns-nodeport.yaml # optional NodePort for DNS (rare; hostPort is default)
│   └── daemonset.yaml            # the workload itself
└── tests/                        # `helm test` hooks (post-install probes)
    └── connection.yaml
```

## Chart.yaml (immutable identity)

```yaml
apiVersion: v2
name: skoed
description: Self-hosted DNS filtering with multi-node Raft cluster
type: application
version: 0.1.0          # chart version
appVersion: "0.2.0"     # skoed version (M2)
home: https://github.com/skoed/skoed
keywords: [dns, filtering, pi-hole, adguard, cluster, raft]
```

## values.yaml (defaults; every key documented)

```yaml
image:
  repository: skoed
  tag: "0.2.0"
  pullPolicy: IfNotPresent
  pullSecrets: []

# When true, render the bootstrap Secret with a randomly-generated join token.
# Set to false in BYO-secret deployments where the operator manages the
# Secret out-of-band.
bootstrap:
  enabled: true
  token: ""                       # if empty AND bootstrap.enabled, a random token is generated at template time

# DNS server tuning forwarded into the cluster sections of config.yaml.
dns:
  mode: forwarding                # forwarding | recursive
  upstreamResolvers:
    - "9.9.9.9:53"
    - "149.112.112.112:53"
  upstreamTimeoutSeconds: 3
  cache:
    enabled: true
    maxEntries: 10000

filtering:
  blockPolicy: nxdomain           # nxdomain | null | nodata

queryLog:
  maxEntries: 10000
  aggregateRetentionDays: 30

# Per-pod persistence for raft state, bbolt, and the shadow config.yaml.
persistence:
  enabled: true
  storageClassName: ""            # "" = use the cluster default
  size: 1Gi
  accessModes: [ReadWriteOnce]

# Resource requests/limits for the container.
resources:
  requests:
    cpu: 50m
    memory: 64Mi
  limits:
    cpu: 500m
    memory: 256Mi

# Optional NodePort service for the DNS listener. By default DNS is served
# via `hostPort: 53`, which works fine on bare-metal / on-prem clusters but
# is unsupported on some managed K8s flavours.
service:
  api:
    type: ClusterIP
    port: 8080
  dns:
    hostPort: true                # primary path; pod binds 53 on every node
    nodePort:
      enabled: false              # set true and pick a port if hostPort is disallowed
      udpPort: 30053
      tcpPort: 30053

# Node selection / tolerations / affinity (passthrough).
nodeSelector: {}
tolerations: []
affinity: {}

# Pod-level extras (env vars, sidecars, etc.) — empty by default.
extraEnv: []
podAnnotations: {}
```

## Templates

### `templates/_helpers.tpl`

Provides `skoed.fullname`, `skoed.labels`, `skoed.selectorLabels`,
`skoed.bootstrapToken` (idempotent random generator that uses Helm's
`randAlphaNum 64` only when the operator hasn't supplied one).

### `templates/secret-bootstrap.yaml`

```yaml
{{- if .Values.bootstrap.enabled }}
apiVersion: v1
kind: Secret
metadata:
  name: {{ include "skoed.fullname" . }}-bootstrap
  labels: {{- include "skoed.labels" . | nindent 4 }}
type: Opaque
stringData:
  token: {{ include "skoed.bootstrapToken" . | quote }}
  leader-address: "http://{{ include "skoed.fullname" . }}.{{ .Release.Namespace }}.svc.cluster.local:{{ .Values.service.api.port }}"
{{- end }}
```

### `templates/configmap-node.yaml`

Renders the M2 `config.yaml` template with cluster-replicated sections only
(no `node:` section — that section is generated per-pod from env vars
in the DaemonSet command). The configmap is read-only at runtime; each
pod's startup wrapper expands env vars (`SKOED_NODE_ID`, `SKOED_RAFT_ADDRESS`,
`SKOED_API_ADDRESS`) and writes the merged file to `/var/lib/skoed/config.yaml`.

### `templates/service.yaml`

ClusterIP Service routing port `service.api.port` → pod port 8080.
Selector targets the DaemonSet's pod labels. A second Service is rendered
when `service.dns.nodePort.enabled` is true.

### `templates/daemonset.yaml`

The workload:

- `kind: DaemonSet`, namespaces inherited
- One container per pod, image `{{ .Values.image.repository }}:{{ .Values.image.tag }}`
- Ports: `containerPort: 53/UDP`, `containerPort: 53/TCP` (with `hostPort: 53` when enabled), `containerPort: 8080`
- Env: `SKOED_NODE_ID = $(POD_NAME)`, `SKOED_RAFT_ADDRESS = $(POD_IP):7000`, `SKOED_API_ADDRESS = $(POD_IP):8080`, plus `SKOED_BOOTSTRAP_TOKEN` and `SKOED_BOOTSTRAP_LEADER` from the bootstrap Secret
- VolumeMounts: `data` (PVC), `config` (configmap-rendered base config.yaml, read-only)
- VolumeClaimTemplates: one PVC per pod using `persistence.size` / `storageClassName`
- Probes: `livenessProbe` GET `/api/v1/health` on 8080; `readinessProbe` GET `/api/v1/health` plus an init-container that waits for the Raft transport to be reachable on `POD_IP:7000`

### `templates/tests/connection.yaml`

Post-install `helm test` job:

- Wait for the DaemonSet's pod readiness
- `curl` the API Service for `/api/v1/cluster/health`
- Assert `status == "ok"` and `members >= 1` (single-node clusters are valid)

## Binary side: bootstrap-from-env support

The skoed binary itself does not currently read `SKOED_BOOTSTRAP_TOKEN`
or `SKOED_BOOTSTRAP_LEADER`. M2.5 adds an init-container model OR a small
shell wrapper that materialises the merged `config.yaml` from:

- The ConfigMap-mounted cluster sections (read-only)
- The pod-specific node section synthesised from env (POD_NAME, POD_IP)
- The bootstrap section from the Secret-mounted env vars

The resulting `/var/lib/skoed/config.yaml` is written ONCE on first start
into the persistent volume. On every subsequent start, the wrapper notices
the file already exists and starts the binary unchanged.

Implementation: a `command:` and `args:` in the DaemonSet that runs a small
inline `sh -c` script before the binary. No code changes to the skoed
binary are strictly required, but a stretch goal for M2.5 is to teach the
binary to honour `SKOED_BOOTSTRAP_TOKEN_FILE` / `SKOED_BOOTSTRAP_LEADER`
directly, avoiding the wrapper entirely.

## How the cluster forms

1. First pod to schedule has no Raft state on its PVC → reads the bootstrap
   section from env → finds no existing leader (because no other pod yet
   exists) → in this single-pod case, the wrapper REMOVES the bootstrap
   section from the rendered config.yaml so the pod self-bootstraps as a
   single-node Raft cluster. This is the canonical "leader" pod.
2. Every later pod reads the bootstrap section → calls
   `POST /api/v1/cluster/join` on the leader-address from the Secret → leader
   issues `raft.AddVoter` → joining pod replicates.
3. On pod restart, the PVC already contains `cluster.bbolt` and `raft/`, so
   the wrapper sees `hasRaft=true` and skips the bootstrap dance entirely.

## CI smoke test (Phase 3)

`tests/acceptance/helm_test.go` validates the rendered manifest shape by
spawning `helm template` (skip if not installed) and parsing the YAML output
with `gopkg.in/yaml.v3`. Asserted invariants:

- DaemonSet exists, name matches the release
- DaemonSet container ports: 53/UDP+TCP, 8080/TCP
- Service exists, ClusterIP, selector matches DaemonSet labels
- Secret exists when `bootstrap.enabled=true`
- VolumeClaimTemplate present with the requested size

A live `kind`/`k3s`-driven test is a future enhancement; not required to
land M2.5.

## Non-goals (explicit)

- ACME / cert-manager — wired up in M4 alongside the DoH/DoT server
- Operator pattern (CRDs) — manual `helm upgrade` is enough at this scale
- Ingress with TLS termination — port-forward or NodePort cover the demo
- Multi-cluster federation — every Helm release is a single skoed cluster
