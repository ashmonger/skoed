Feature: Helm Chart Deployment
  As a Kubernetes operator
  I want to install dblock onto my cluster with a single `helm install`
  So that I get a working multi-node dblock cluster without crafting raw manifests.

  Background:
    Given a Kubernetes cluster (≥ 2 nodes) with `kubectl` access
    And the dblock Helm chart at `deploy/helm/dblock/`

  @fsid:FS-HelmChartTemplatesRender
  Scenario: `helm template` produces valid Kubernetes manifests
    When the operator runs `helm template my-dblock deploy/helm/dblock`
    Then every emitted document is valid YAML
    And the output contains exactly one DaemonSet named `my-dblock`
    And the output contains exactly one ClusterIP Service named `my-dblock`
    And the output contains exactly one Secret named `my-dblock-bootstrap`
    And the output contains a PersistentVolumeClaim template scoped to the DaemonSet

  @fsid:FS-HelmChartValuesOverrides
  Scenario: Operator overrides image, replicas-irrelevant fields, and resources via values.yaml
    When the operator installs with `--set image.tag=v0.9.0 --set resources.limits.memory=256Mi --set persistence.size=2Gi`
    Then the rendered DaemonSet pulls image `dblock:v0.9.0`
    And the rendered container has `resources.limits.memory: 256Mi`
    And the rendered PersistentVolumeClaim requests `2Gi` of storage

  @fsid:FS-HelmChartHostPortDns
  Scenario: DNS listener is reachable on every Kubernetes node via hostPort
    Given the chart is installed with defaults
    When pods are scheduled (one per node)
    Then each pod's container exposes port 53/UDP and 53/TCP via `hostPort`
    And `dig @<node-ip> example.com` from any in-cluster pod reaches a dblock instance

  @fsid:FS-HelmChartBootstrapToken
  Scenario: Replica pods enroll using a shared bootstrap token
    When the chart is installed with `--set bootstrap.enabled=true`
    Then a Kubernetes Secret named `<release>-bootstrap` is created with key `token`
    And every pod's config.yaml references that Secret via env var or volume mount
    And the first pod bootstraps as the Raft leader
    And subsequent pods enrol as Raft followers using the shared token

  @fsid:FS-HelmChartPersistenceSurvivesPodRestart
  Scenario: Pod data persists across restarts
    Given the chart is installed and one pod has committed cluster state
    When the pod is deleted (kubectl delete pod)
    Then the replacement pod attaches the same PVC
    And the cluster's commit_index is preserved
    And the node rejoins as the same Raft member without re-enrollment

  @fsid:FS-HelmChartManagementApiService
  Scenario: Management API is reachable via a ClusterIP Service
    When the chart is installed
    Then a ClusterIP Service routes port 8080 to every pod
    And `kubectl port-forward svc/<release> 8080:8080` exposes the API to the operator's workstation

  Non-goals:
    - cert-manager / ACME integration (M4 + DoH server)
    - Operator pattern / CRDs (manual `helm upgrade` is sufficient at M2.5 scale)
    - Ingress with TLS termination (out of scope; admins use port-forward or NodePort)
