# CLI Reference

skoed ships as a single binary with several subcommands. All subcommands communicate with a running daemon through the management API unless otherwise noted.

---

## `skoed` — Start the daemon

**Synopsis:**

```
skoed [--config <path>]
```

Starts the skoed daemon in the foreground. When run via systemd, stdout/stderr are captured by the journal.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--config <path>` | `/etc/skoed/config.yaml` | Path to the configuration file. |

**Example:**

```bash
skoed --config /etc/skoed/config.yaml
```

On startup, skoed logs the node ID, listen addresses, and cluster role, then begins accepting DNS queries.

---

## `skoed version` — Print version information

**Synopsis:**

```
skoed version
```

Prints the release version and the Git commit hash the binary was built from. No flags.

**Example output:**

```
skoed v1.4.2 (commit a3f9c12)
```

---

## `skoed status` — Query daemon health and cluster state

**Synopsis:**

```
skoed status [--api <url>] [--token <api-token>]
```

Connects to a running skoed daemon and prints a summary of its health and cluster membership. Exits with status code `0` if the daemon is healthy, non-zero otherwise.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--api <url>` | `http://localhost:8080` | Base URL of the management API. |
| `--token <api-token>` | — | Bearer token for authenticated API access. |

**Example output:**

```
Node:     skoed-1
Version:  v1.4.2
Status:   healthy
Role:     leader
Cluster:
  skoed-1  leader   reachable
  skoed-2  follower reachable
  skoed-3  follower reachable
Uptime:   14h32m
DNS queries (1m avg): 342/s
Blocked  (1m avg):    38/s
```

When the daemon is unreachable, `skoed status` prints the connection error and exits with a non-zero code.

---

## `skoed top` — Real-time TUI dashboard

**Synopsis:**

```
skoed top [--api <url>] [--token <api-token>]
```

Opens a terminal user interface (TUI) that refreshes every second. Similar in spirit to `htop` but for DNS traffic. Press `q` to exit.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--api <url>` | `http://localhost:8080` | Base URL of the management API to stream metrics from. |
| `--token <api-token>` | — | Bearer token for authenticated API access. |

**What the dashboard shows:**

- Queries per second (total, blocked, cached, forwarded) as a live sparkline.
- Top 10 blocked domains in the last 60 seconds, with per-domain query counts.
- Top 10 queried domains (allowed) in the last 60 seconds.
- Upstream resolver latency (p50 / p95) per configured upstream.
- Cluster node status panel (visible when running in cluster mode).

The TUI uses the alternate screen buffer and restores the terminal on exit.

---

## `skoed config validate` — Validate a config file

**Synopsis:**

```
skoed config validate <file>
```

Parses and validates the specified `config.yaml` without starting the daemon. Checks for required fields, invalid values, and obvious misconfiguration (e.g. conflicting listen ports). Does not make any network connections.

Exits with status `0` if the config is valid, `1` if validation errors are found.

**Example — valid config:**

```bash
$ skoed config validate /etc/skoed/config.yaml
config.yaml: OK
```

**Example — invalid config:**

```bash
$ skoed config validate /etc/skoed/config.yaml
config.yaml: 2 error(s)
  node.id: required field is empty
  node.dns.upstreams: at least one upstream resolver is required
```
