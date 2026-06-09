Feature: Kubernetes Operator
  As a platform engineer running skoed on Kubernetes at production scale
  I want a native operator that manages skoed clusters via CRDs
  So that I get automated lifecycle management (scaling, certs, PVCs) without manual Helm upgrades.

  Note: CRDs are named `SkoedCluster` / `SkoedNode` (consistent with the app rename from dblock to skoed).
  The ROADMAP listed `DblockCluster` / `DblockCluster` prior to the rename.
  The Helm chart (M2.5) remains available as a fallback for non-operator deployments.

  Background:
    Given a Kubernetes cluster (≥ 1.27) with `kubectl` access
    And the skoed operator installed (CRDs registered, controller running)

  @fsid:FS-OperatorCrdRegistered
  Scenario: Operator installation registers the CRDs
    When the admin installs the operator via `helm install skoed-operator deploy/helm/skoed-operator/`
    Then `kubectl get crd skoedclusters.skoed.io` exits 0
    And `kubectl get crd skoednodes.skoed.io` exits 0
    And the controller pod reaches the Running state within 60 seconds

  @fsid:FS-ClusterProvisioned
  Scenario: Creating a SkoedCluster CR provisions a running cluster
    When the admin applies a SkoedCluster manifest with `spec.replicas: 3`
    Then the operator creates 3 pods and 3 PersistentVolumeClaims
    And each pod mounts its dedicated PVC at the skoed data directory
    And the cluster reaches Raft quorum (≥ 2 of 3 nodes healthy)
    And `GET /api/v1/cluster/status` on any node lists exactly 3 members
    And the SkoedCluster status condition `Ready` becomes `True`

  @fsid:FS-ClusterScaleUp
  Scenario: Increasing spec.replicas adds nodes and joins them to Raft
    Given a SkoedCluster with `spec.replicas: 1` in a Ready state
    When the admin patches the CR to `spec.replicas: 3`
    Then the operator provisions 2 new pods with dedicated PVCs
    And each new node enrolls in the Raft cluster as a follower
    And `GET /api/v1/cluster/status` on the leader lists 3 members
    And the SkoedCluster status condition `Ready` returns to `True`

  @fsid:FS-ClusterScaleDown
  Scenario: Decreasing spec.replicas gracefully removes a follower
    Given a SkoedCluster with `spec.replicas: 3` in a Ready state
    When the admin patches the CR to `spec.replicas: 1`
    Then the operator removes 2 follower nodes (never the leader)
    And each removed node is deregistered from Raft before its pod is deleted
    And the remaining node stays healthy with no quorum loss during removal
    And the SkoedCluster status condition `Ready` returns to `True`

  @fsid:FS-PvcSurvivesPodRestart
  Scenario: Raft state persists across pod restarts via PVC
    Given a SkoedCluster with `spec.replicas: 3` and a non-zero Raft commit_index
    When a follower pod is deleted (kubectl delete pod)
    Then the replacement pod is scheduled and attaches the same PVC
    And the restarted node reports the same commit_index as before deletion
    And the node rejoins the Raft cluster as the same member (same node ID)
    And no re-enrollment or manual intervention is required

  @fsid:FS-AcmeCertAutoRotate
  Scenario: Operator rotates ACME/TLS certs before expiry without downtime
    Given a SkoedCluster with DoH/DoT enabled and a TLS cert expiring within 30 days
    When the operator's cert-rotation reconciler runs
    Then the operator triggers ACME renewal and updates the Kubernetes Secret
    And each pod's cert volume is refreshed (via Secret mount or rolling restart)
    And DNS queries in flight during rotation are not dropped
    And the SkoedCluster status reflects the new cert expiry date

  @fsid:FS-StatusConditions
  Scenario: SkoedCluster status conditions reflect Raft health
    Given a SkoedCluster with `spec.replicas: 3` in a Ready state
    Then the SkoedCluster status contains:
      | condition | value |
      | Ready     | True  |
      | Quorum    | True  |
    And `status.leader` names the current leader node
    And `status.voters` lists all 3 member node names
    And `kubectl get skoedcluster` prints Ready, Quorum, and leader in its default columns

  @fsid:FS-StatusConditionsOnFailure
  Scenario: Status reflects degraded state when a node is lost
    Given a SkoedCluster with `spec.replicas: 3` in a Ready state
    When one follower pod is force-deleted and does not recover
    Then the SkoedCluster status condition `Ready` becomes `False`
    And `status.voters` lists only 2 members
    And `kubectl get skoedcluster` shows the degraded state without additional commands

  @fsid:FS-HelmFallbackUnaffected
  Scenario: Non-operator Helm chart deployment is unaffected by operator presence
    Given the skoed operator is installed in namespace `skoed-system`
    When the admin installs the plain Helm chart (deploy/helm/skoed/) in a separate namespace
    Then that Helm-managed installation runs without the operator managing it
    And the operator does not adopt or modify pods it did not create
    And the Helm chart DaemonSet serves DNS queries normally

  Non-goals:
    - Multi-cluster or federation management
    - Custom CNI integration
    - Managed upgrades of the skoed binary (operator manages lifecycle, not version rollout)
    - cert-manager dependency (operator manages ACME directly, mirroring M4 behavior)
