# Encrypted Mesh (mTLS)

Enable mutual TLS so that all inter-node traffic — Raft replication and API request forwarding — is encrypted and authenticated.

---

## What It Does

When `cluster.tls.enabled: true`, skoed wraps every TCP connection between cluster members in TLS 1.3. Both sides present a certificate signed by the shared cluster CA, so:

- **Confidentiality** — Raft log entries and forwarded API calls cannot be read in transit.
- **Authentication** — A rogue process cannot join or impersonate a node without a valid certificate from the cluster CA.
- **Integrity** — Replay and tampering attacks are blocked by TLS record MAC.

DNS queries from clients and the web UI are unaffected; this feature covers only node-to-node traffic.

---

## Configuration

Add the following block to each node's `config.yaml`:

```yaml
cluster:
  raft_listen: "0.0.0.0:9000"
  peers:
    - "node-2:9000"
    - "node-3:9000"

  tls:
    enabled: true
    ca_cert: "/etc/skoed/tls/ca.crt"
    cert:    "/etc/skoed/tls/node.crt"
    key:     "/etc/skoed/tls/node.key"
```

| Field | Description |
|-------|-------------|
| `cluster.tls.enabled` | Set to `true` to activate mTLS on all cluster connections |
| `cluster.tls.ca_cert` | Path to the PEM-encoded cluster CA certificate |
| `cluster.tls.cert` | Path to the PEM-encoded node certificate signed by the cluster CA |
| `cluster.tls.key` | Path to the PEM-encoded private key for the node certificate |

All nodes must share the **same CA certificate** but each node must have its **own** cert/key pair.

---

## Certificate Generation

The following example creates a self-signed cluster CA and signs a certificate for each node. Run these commands on a secure machine and distribute the resulting files.

### 1 — Create the cluster CA

```bash
# Generate CA private key
openssl genrsa -out ca.key 4096

# Self-sign the CA certificate (valid 10 years)
openssl req -x509 -new -nodes \
  -key ca.key \
  -sha256 \
  -days 3650 \
  -subj "/CN=skoed-cluster-ca" \
  -out ca.crt
```

### 2 — Create a certificate for each node

Repeat for `node-1`, `node-2`, `node-3` (replace `NODE` with the node name and `NODE_ADDRESS` with its DNS name or IP):

```bash
NODE="node-1"
NODE_ADDRESS="node-1"   # DNS name or IP that peers use to reach this node

# Generate node private key
openssl genrsa -out "${NODE}.key" 2048

# Create certificate signing request
openssl req -new \
  -key "${NODE}.key" \
  -subj "/CN=${NODE}" \
  -out "${NODE}.csr"

# Sign with the cluster CA, include SAN for the node address
openssl x509 -req \
  -in "${NODE}.csr" \
  -CA ca.crt \
  -CAkey ca.key \
  -CAcreateserial \
  -days 825 \
  -sha256 \
  -extfile <(printf "subjectAltName=DNS:%s,DNS:localhost,IP:127.0.0.1" "${NODE_ADDRESS}") \
  -out "${NODE}.crt"
```

### 3 — Distribute files to each node

| Node | Files needed |
|------|-------------|
| node-1 | `ca.crt`, `node-1.crt`, `node-1.key` |
| node-2 | `ca.crt`, `node-2.crt`, `node-2.key` |
| node-3 | `ca.crt`, `node-3.crt`, `node-3.key` |

Mount or copy them to the path specified in `config.yaml` (e.g., `/etc/skoed/tls/`). Keep the CA private key (`ca.key`) offline after signing.

---

## Certificate Rotation

To rotate certificates without cluster downtime:

1. Generate new CA or new node certs (following the steps above).
2. On each node **one at a time**:
   a. Replace the cert/key files at the configured paths.
   b. Send `SIGHUP` to the process, or restart the container.
   c. Verify the node rejoins the cluster (check `/api/v1/cluster/status`).
3. Proceed to the next node only after the previous one is back in the `follower` or `leader` state.

The remaining nodes maintain quorum while each node restarts, so there is no service interruption.

---

## Verification

Use `openssl s_client` to confirm a node is serving TLS with a certificate from the cluster CA:

```bash
openssl s_client \
  -connect node-2:9000 \
  -CAfile ca.crt \
  -verify_return_error \
  </dev/null
```

A successful handshake prints `Verify return code: 0 (ok)`. If you see `certificate verify failed`, check that the node certificate was signed by the correct CA and that the SAN matches the address you are connecting to.
