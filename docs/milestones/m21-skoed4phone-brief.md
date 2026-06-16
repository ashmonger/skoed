# M21 — Skoed4Phone: DNS-over-VPN

## Purpose of this document

This brief is the starting point for implementing Skoed4Phone. It is meant to be handed to a developer or a new Claude session as the foundation for functional spec authoring. It does **not** replace the BDD-First spec process — it is pre-spec input.

---

## Problem

Mobile devices (iOS, Android) use the OS DNS resolver, which is controlled by the Wi-Fi/cellular network — not by a self-hosted skoed instance. Browsers, iOS, and Android increasingly use their own DoH/DoT resolvers (Cloudflare, Google), bypassing any local DNS filter entirely. The result: skoed's filtering is invisible to phones unless the phone explicitly directs DNS to skoed.

We want phones on the LAN to be filtered by skoed automatically, and phones roaming (cellular / guest Wi-Fi) to be filtered by a designated skoed instance using a lightweight, battery-friendly mechanism.

---

## Chosen approach: local VPN tunnel

A local VPN tunnel (WireGuard or the OS VPN API) is established on the device. The tunnel intercepts only UDP/53 and TCP/53 packets (not all traffic). Intercepted DNS queries are forwarded to a skoed node over DoH3 (`/dns-query`) authenticated with a read-scoped API token (from M7/M20).

This is the same approach used by AdGuard for iOS / Blokada / NextDNS app.

Key property: **no traffic leaves the device except DNS queries**. This is not a full VPN — it is a DNS-only interception shim.

---

## Behaviors to specify (functional spec scope)

### 1. Remote skoed mode (primary mode)

- User configures a skoed node URL (e.g. `https://skoed.home.example.com`) and an API read token.
- App establishes a local VPN tunnel that captures UDP/53 + TCP/53.
- All captured DNS queries are forwarded to the configured node via DoH3 (`GET /dns-query?dns=<base64>`).
- Node applies the user's assigned profile filtering rules and returns the answer.
- App surfaces the node connection status: connected / unreachable / auth failed.

### 2. LAN detection — auto-disable

- When the device joins a Wi-Fi network, the app checks whether a skoed node is reachable on the LAN (strategy: try a sentinel DNS lookup or ping a configurable LAN hostname).
- If the LAN already runs skoed and the device's DHCP-assigned DNS is the skoed VIP, the local VPN is suspended: the OS uses the LAN DNS directly.
- When the device leaves the LAN (Wi-Fi disconnected or a different SSID), the VPN resumes.
- LAN detection is best-effort — if detection fails, the VPN stays active (safe default).

### 3. On-device blocklist mode (offline / no external node)

- If no external node is configured (or the node is unreachable for > N minutes), the app switches to on-device mode.
- A bundled minimal blocklist (pre-compiled, no Raft) is loaded into memory.
- DNS queries are resolved using the system upstream (DHCP-assigned resolver or a hardcoded fallback like 9.9.9.9), with blocked domains returning NXDOMAIN.
- Blocklist updates are downloaded once per day, on Wi-Fi only, in the background.
- On-device mode is explicitly disclosed to the user: "Using local filter — not connected to skoed".

### 4. App UI

- Node configuration screen: URL + token entry, connection test button, status badge.
- Filtering toggle: enable / disable the VPN (persists across reboots).
- Last-query view: scrollable list of the last N DNS queries handled by the VPN (domain, outcome: allowed/blocked, timestamp). Local to the device — not synced to the node.
- Mode indicator: "Connected to skoed" / "Local filter" / "Disabled".

---

## Out of scope for M21

- The phone joining the Raft cluster as a peer (phone is a leaf client only)
- Traffic proxying beyond DNS (HTTPS filtering, content inspection)
- App Store / Play Store submission (manual sideload or TestFlight for M21)
- Push notifications from the skoed node to the phone
- Per-app DNS routing (all apps share the same VPN tunnel)
- Remote query log from the node (that is M22 — companion app)

---

## Technical constraints

### iOS

- `NEPacketTunnelProvider` extension (Network Extension entitlement required).
- DoH3 requires URLSession with HTTP/3 enabled (`URLSessionConfiguration.http3Enabled`).
- Background blocklist refresh: `BGAppRefreshTask` (iOS 13+), Wi-Fi-only via `BGProcessingTaskRequest.requiresNetworkConnectivity` + `requiresExternalPower: false`.
- Minimum target: iOS 16 (HTTP/3 stable, NEPacketTunnelProvider widely available).

### Android

- `VpnService` API (requires `BIND_VPN_SERVICE` permission + user acceptance dialog).
- DoH3 requires OkHttp 5.x or Cronet (bundled or via Google Play Services).
- Background blocklist refresh: `WorkManager` with `NetworkType.UNMETERED` constraint.
- Minimum target: Android 10 (API 29) for stable `VpnService` builder + DoH support.

### Shared

- API token stored in OS keychain (iOS Keychain Services / Android Keystore), never in SharedPreferences or NSUserDefaults.
- TLS certificate pinning for the skoed node connection (optional; operator can disable for self-signed certs).
- The VPN tunnel does not route non-DNS traffic; the OS routing table entry covers only 0.0.0.0/0 with a packet filter that passes only UDP/53 + TCP/53 to the tunnel and routes everything else to the default interface.

---

## Dependencies (skoed server-side)

| Dependency | Why |
|-----------|-----|
| M7 API tokens | Phone authenticates with a read-scoped token |
| M20 token scoping | Read scope prevents the phone from mutating cluster state |
| M6 / M8 DoH3 | Phone uses HTTP3 to the cluster for battery-efficient multiplexed queries |

---

## Suggested functional spec FSIDs (to author in specs/functional/skoed4phone.feature)

| FSID | Scenario |
|------|---------|
| `FS-PhoneRemoteNodeSetup` | User configures node URL + token; app verifies connection |
| `FS-PhoneRemoteNodeFiltering` | DNS query from phone is filtered by the remote node |
| `FS-PhoneRemoteNodeUnreachable` | Node unreachable → app falls back to on-device mode |
| `FS-PhoneLanDetectionDisable` | Phone joins LAN with skoed → VPN auto-suspends |
| `FS-PhoneLanDetectionResume` | Phone leaves LAN → VPN resumes |
| `FS-PhoneOnDeviceFiltering` | On-device blocklist blocks known bad domain |
| `FS-PhoneOnDeviceBlocklistUpdate` | Blocklist updates on Wi-Fi in background |
| `FS-PhoneLocalQueryLog` | App shows last-N queries with outcome |
| `FS-PhoneTokenStoredSecurely` | Token survives app restart; not readable from app sandbox |

---

## Implementation language recommendation

- **React Native** with `react-native-vpn-service` (Android) + custom Native Module for iOS `NEPacketTunnelProvider`.
  - Shares UI code between platforms; large ecosystem.
  - Downside: Native Module for packet tunnel is non-trivial and must be written in Swift/Kotlin.
- **Flutter** with platform channels to `NEPacketTunnelProvider` / `VpnService`.
  - Better performance; similar pattern for platform channels.
- **Native (Swift + Kotlin separately)**.
  - Maximum control; most maintenance burden.

**Recommendation**: Flutter. The DNS tunnel code is entirely platform-native (minimal; ~200 lines of Swift, ~150 lines of Kotlin for the VPN shim), the UI is shared, and Flutter's Dart async model fits the event-driven VPN state machine well.

---

## Open questions for UoR before spec authoring

1. **Target users**: is M21 for the operator themselves (power user, okay with manual install), or for family members (must be zero-config)? This determines how much setup complexity is acceptable.
2. **On-device blocklist source**: should it reuse the same blocklist URLs already configured on the skoed node, or use a separate curated list bundled with the app?
3. **Certificate pinning**: should the app default to trusting Let's Encrypt / public CAs only (rejects self-signed), or default to trust-on-first-use (TOFU) for self-signed?
