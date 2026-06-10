# Docker

Run skoed as a single container or as a 3-node cluster with Docker Compose.

---

## Prerequisites

- Docker 24+ or Podman 4+
- Port 53 must be free on the host (disable `systemd-resolved` stub listener if needed)

---

## Single Node

```bash
docker run -d \
  --name skoed \
  --restart unless-stopped \
  -p 53:53/udp \
  -p 53:53/tcp \
  -p 8080:8080/tcp \
  -v skoed_data:/var/lib/skoed \
  -e CONFIG='
dns:
  listen: "0.0.0.0:53"
api:
  listen: "0.0.0.0:8080"
upstream:
  - "1.1.1.1:53"
  - "8.8.8.8:53"
' \
  ghcr.io/ashmonger/skoed:latest
```

| Port | Protocol | Purpose |
|------|----------|---------|
| 53 | UDP + TCP | DNS listener |
| 8080 | TCP | Web UI and REST API |

The named volume `skoed_data` persists the block-list database, lease store, and Raft log across restarts.

**First run:** the admin password is printed once to stdout on initial startup.
See [First-run authentication setup](../first-run/auth-setup.md) to complete onboarding.

---

## 3-Node Cluster with Docker Compose

The compose file below bootstraps a self-contained cluster on a single machine using distinct host-port ranges. In production, deploy each node on a separate host and remove the port offsets.

```yaml
# compose.yaml
networks:
  skoed_net:
    driver: bridge

volumes:
  skoed_data_1:
  skoed_data_2:
  skoed_data_3:

x-skoed-common: &skoed-common
  image: ghcr.io/ashmonger/skoed:latest
  restart: unless-stopped
  networks:
    - skoed_net

services:
  node-1:
    <<: *skoed-common
    container_name: skoed-node-1
    ports:
      - "5380:53/udp"
      - "5380:53/tcp"
      - "8080:8080/tcp"
      - "9000:9000/tcp"
    volumes:
      - skoed_data_1:/var/lib/skoed
    environment:
      CONFIG: |
        node:
          id: "node-1"
          raft_advertise: "skoed-node-1:9000"
        dns:
          listen: "0.0.0.0:53"
        api:
          listen: "0.0.0.0:8080"
        cluster:
          raft_listen: "0.0.0.0:9000"
          peers:
            - "skoed-node-2:9000"
            - "skoed-node-3:9000"
        upstream:
          - "1.1.1.1:53"
          - "8.8.8.8:53"

  node-2:
    <<: *skoed-common
    container_name: skoed-node-2
    ports:
      - "5381:53/udp"
      - "5381:53/tcp"
      - "8081:8080/tcp"
      - "9001:9000/tcp"
    volumes:
      - skoed_data_2:/var/lib/skoed
    environment:
      CONFIG: |
        node:
          id: "node-2"
          raft_advertise: "skoed-node-2:9000"
        dns:
          listen: "0.0.0.0:53"
        api:
          listen: "0.0.0.0:8080"
        cluster:
          raft_listen: "0.0.0.0:9000"
          peers:
            - "skoed-node-1:9000"
            - "skoed-node-3:9000"
        upstream:
          - "1.1.1.1:53"
          - "8.8.8.8:53"

  node-3:
    <<: *skoed-common
    container_name: skoed-node-3
    ports:
      - "5382:53/udp"
      - "5382:53/tcp"
      - "8082:8080/tcp"
      - "9002:9000/tcp"
    volumes:
      - skoed_data_3:/var/lib/skoed
    environment:
      CONFIG: |
        node:
          id: "node-3"
          raft_advertise: "skoed-node-3:9000"
        dns:
          listen: "0.0.0.0:53"
        api:
          listen: "0.0.0.0:8080"
        cluster:
          raft_listen: "0.0.0.0:9000"
          peers:
            - "skoed-node-1:9000"
            - "skoed-node-2:9000"
        upstream:
          - "1.1.1.1:53"
          - "8.8.8.8:53"
```

Start the cluster:

```bash
docker compose up -d
```

Check that a leader is elected (usually within a few seconds):

```bash
curl -s http://localhost:8080/api/v1/cluster/status | jq .
```

### Port map (single-host layout)

| Node | DNS (UDP+TCP) | API | Raft |
|------|--------------|-----|------|
| node-1 | 5380 | 8080 | 9000 |
| node-2 | 5381 | 8081 | 9001 |
| node-3 | 5382 | 8082 | 9002 |

---

## Upgrade

```bash
docker pull ghcr.io/ashmonger/skoed:latest
docker compose up -d
```

Docker Compose performs a rolling restart. In a Raft cluster, nodes re-join automatically after restart; the remaining two nodes maintain quorum while each node updates.

---

## Next steps

- [First-run authentication setup](../first-run/auth-setup.md)
- [Cluster bootstrap](../cluster/bootstrap.md)
