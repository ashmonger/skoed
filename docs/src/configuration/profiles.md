# Profiles and schedules

A **profile** is a named set of filtering rules — blocklists, allowlist
overrides, and SafeSearch providers — that is assigned to one or more
clients. Different clients can have different filtering policies without
needing separate skoed instances.

---

## What profiles are

Every DNS query is resolved in the context of the client that sent it.
skoed identifies the client by one of the following, in priority order:

1. **DHCP Client-ID** (`client_ids`) — the stable identifier sent by the DHCP client (most reliable for Wi-Fi and managed devices).
2. **MAC address** (`client_macs`) — works on locally-attached L2 segments.
3. **Hostname** (`client_hostnames`) — matched against the DHCP lease hostname.
4. **IP address / CIDR** (`client_ips`, `client_cidrs`) — fallback for static-IP hosts.

If no profile matches the client, the `default` profile applies.

---

## Creating a profile

```bash
curl -s -u admin:password \
  -X POST http://skoed:8080/api/v1/profiles \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "kids",
    "name": "Kids devices",
    "blocklists": ["bl-adult-oisd", "bl-gambling"],
    "allowlist": ["school.example.edu"],
    "safesearch": ["google", "bing", "youtube"],
    "client_ips": ["192.168.1.20", "192.168.1.21"],
    "client_macs": ["aa:bb:cc:dd:ee:ff"]
  }'
```

**Profile fields:**

| Field | Description |
|-------|-------------|
| `id` | Unique identifier (required). Use `"default"` for the fallback profile |
| `name` | Human-readable label |
| `blocklists` | List of blocklist IDs to enforce for this profile |
| `allowlist` | Per-profile domain allowlist (in addition to the global allowlist) |
| `safesearch` | SafeSearch providers to enforce: `"google"`, `"bing"`, `"youtube"`, `"duckduckgo"` |
| `client_ips` | Exact IPv4 or IPv6 addresses to match |
| `client_cidrs` | CIDR ranges to match |
| `client_ids` | DHCP Client-IDs to match |
| `client_macs` | MAC addresses to match (format `aa:bb:cc:dd:ee:ff`) |
| `client_hostnames` | DHCP hostnames to match |
| `block_dynamic_clients` | When `true`, all clients whose DHCP lease origin is `dhcp_dynamic` are matched. Not allowed on the `"default"` profile |

---

## Assigning clients in the Web UI

In the Web UI, open **Clients**, find the client, and select a profile from
the **Profile** dropdown. The assignment is stored as `client_ips` or
`client_ids` on the profile depending on how the client was identified.

---

## Updating a profile

Use `PATCH /api/v1/profiles/<id>` to update individual fields without
replacing the whole profile:

```bash
curl -s -u admin:password \
  -X PATCH http://skoed:8080/api/v1/profiles/kids \
  -H 'Content-Type: application/json' \
  -d '{
    "blocklists": ["bl-adult-oisd", "bl-gambling", "bl-social"],
    "client_ips": ["192.168.1.20", "192.168.1.21", "192.168.1.22"]
  }'
```

Only fields present in the request body are changed; omitted fields keep
their current values.

---

## Deleting a profile

```bash
curl -s -u admin:password \
  -X DELETE http://skoed:8080/api/v1/profiles/kids
```

The reserved `"default"` profile cannot be deleted.

---

## Schedules

A **schedule** defines time windows during which a blocklist is active or
inactive for a given profile. This lets you apply stricter filtering during
school hours and relax it in the evenings.

### Schedule modes

| Mode | Behaviour |
|------|-----------|
| `block_only_inside` | The bound blocklist is enforced **only during** the defined windows |
| `allow_only_inside` | The bound blocklist is enforced **outside** the windows (i.e., the blocklist is suspended during the window) |

Times use 24-hour `HH:MM` format in the node's local timezone. An `End`
earlier than `Start` wraps midnight (e.g. `Start: "22:00"`, `End: "06:00"`
covers 10 pm to 6 am).

### Creating a schedule

```bash
curl -s -u admin:password \
  -X POST http://skoed:8080/api/v1/schedules \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "school-hours",
    "name": "School hours Mon-Fri",
    "mode": "block_only_inside",
    "windows": [
      {
        "days": ["Mon", "Tue", "Wed", "Thu", "Fri"],
        "start": "08:00",
        "end": "15:00"
      }
    ]
  }'
```

### Binding a schedule to a profile and blocklist

```bash
curl -s -u admin:password \
  -X POST http://skoed:8080/api/v1/schedules/school-hours/bindings \
  -H 'Content-Type: application/json' \
  -d '{
    "profile_id": "kids",
    "blocklist_id": "bl-social"
  }'
```

This binding means: enforce `bl-social` on the `kids` profile **only**
during school hours on weekdays.

### Removing a binding

```bash
curl -s -u admin:password \
  -X DELETE http://skoed:8080/api/v1/schedules/school-hours/bindings/kids/bl-social
```

---

## Default profile

Any client not matched by an explicit profile falls through to the
`default` profile. Configure it exactly like any other profile (blocklists,
allowlist, SafeSearch), but note:

- Its `id` is always `"default"`.
- It cannot be deleted.
- `block_dynamic_clients` is not allowed on the default profile; create a
  dedicated profile (e.g. `"untrusted"`) if you need to block dynamically
  assigned clients.

---

## Priority

When multiple matching rules exist, the resolution order is:

1. Per-client profile match (by Client-ID > MAC > hostname > IP/CIDR).
2. `default` profile (if no explicit match).

Within a profile, the allowlist always wins over any blocklist.
