Feature: Cluster Security Hardening — mTLS Certificate Rotation
  As a cluster administrator
  I want to inspect certificate expiry and trigger rotation
  So that the cluster mesh stays secure without downtime

  Non-goals:
    - Automatic scheduled rotation (operator must trigger manually)
    - Certificate revocation lists (CRL) or OCSP stapling
    - Rotation when mTLS is disabled (not applicable)
    - Per-node selective rotation (rotation is cluster-wide)

  @fsid:FS-CertStatusExposesCertExpiry
  Scenario: Admin queries certificate status and sees expiry dates
    Given a 1-node mTLS-enabled cluster
    When an admin calls GET /api/v1/cluster/certs/status
    Then the response is 200
    And the body contains "ca_expires_at" with a future RFC3339 timestamp
    And the body contains a "nodes" array with at least one entry
    And each node entry contains "cert_expires_at" with a future RFC3339 timestamp

  @fsid:FS-CertRotateTriggeredByAdmin
  Scenario: Admin triggers certificate rotation and new certs are distributed
    Given a 1-node mTLS-enabled cluster
    When an admin calls POST /api/v1/cluster/certs/rotate
    Then the response is 202
    And within 10 seconds GET /api/v1/cluster/certs/status returns rotation_pending=false for all nodes

  @fsid:FS-CertRotateRollingMaintainsQuorum
  Scenario: Certificate rotation on a 3-node cluster never drops below quorum
    Given a 3-node mTLS-enabled cluster
    When an admin triggers POST /api/v1/cluster/certs/rotate
    Then at no point during rotation does the cluster health drop below 2 reachable members
    And after rotation completes GET /api/v1/cluster/health returns status=ok

  @fsid:FS-CertRotateRequiresClusterAdminScope
  Scenario: Read-only token cannot trigger certificate rotation
    Given a 1-node mTLS-enabled cluster
    And a bearer token with only "read" scope
    When that token calls POST /api/v1/cluster/certs/rotate
    Then the response is 403
