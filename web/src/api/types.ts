// Mirrors of the JSON shapes documented in specs/technical/management-api.openapi.yaml.
// Kept hand-written rather than codegen'd to avoid pulling another toolchain
// just for type definitions.

export interface BlocklistSource {
  type: string
  url?: string
  format?: string
}

export interface Blocklist {
  id: string
  name: string
  enabled: boolean
  source: BlocklistSource
  block_policy?: string
  domain_count: number
  last_updated?: string
  // M5.4 — automated refresh state
  refresh_interval_seconds?: number
  last_refresh_at?: string
  last_refresh_status?: 'ok' | 'error' | 'unchanged'
  last_refresh_error?: string
}

export interface LocalDNSEntry {
  id: string
  hostname: string
  type: 'A' | 'AAAA' | 'CNAME'
  value: string
  ttl: number
}

export interface QueryLogEntry {
  id: string
  timestamp: string
  client: string
  domain: string
  query_type: string
  outcome: 'forwarded' | 'blocked' | 'local' | 'cached'
  blocklist_id?: string
  node_id?: string
}

export interface QueryLogPage {
  entries: QueryLogEntry[]
  total: number
  per_node?: PerNodeStatus[]
}

export interface PerNodeStatus {
  node_id: string
  status: 'ok' | 'timeout' | 'error'
  entry_count?: number
  error?: string
}

export interface ClusterNode {
  node_id: string
  role: 'leader' | 'follower' | 'learner' | 'removed'
  raft_address: string
  api_address: string
  last_contact: string
  commit_index: number
  sync_state: 'in_sync' | 'behind' | 'unreachable'
}

export interface ClusterStatus {
  cluster_id: string
  raft_term: number
  leader_id: string
  nodes: ClusterNode[]
}

export interface ClusterHealth {
  status: 'ok' | 'degraded'
  node_id: string
  role: 'leader' | 'follower'
  mode: 'single-node' | 'cluster'
  has_leader: boolean
  members: number
  reachable_members: number
  raft_term: number
  commit_index: number
  version?: string
  commit?: string
}

export interface ClusterSelf {
  node_id: string
  role: 'leader' | 'follower'
  raft_term: number
  commit_index: number
}

export interface NameCount {
  domain?: string
  client?: string
  count: number
}

export interface HourAggregate {
  node_id: string
  hour_start: string
  total: number
  blocked: number
  forwarded: number
  cached: number
  local: number
  top_domains: NameCount[]
  top_clients: NameCount[]
}

export interface ClusterStats {
  window_from: string
  window_to: string
  per_node: HourAggregate[]
  cluster_totals: {
    total: number
    blocked: number
    forwarded: number
    cached: number
    local: number
  }
  top_domains: NameCount[]
  top_clients: NameCount[]
}

export interface DNSConfig {
  listen: { port: number; ipv4: boolean; ipv6: boolean }
  mode: 'forwarding' | 'recursive'
  upstream_resolvers?: string[]
  upstream_timeout_seconds: number
  trusted_subnets?: string[]
  cache: { enabled: boolean; max_entries: number }
}

export interface Settings {
  dns: DNSConfig
  filtering: { block_policy: 'nxdomain' | 'null' | 'nodata' | 'redirect' }
  query_log: { max_entries: number; aggregate_retention_days: number }
}

// M26 — block page config
export interface BlockPageConfig {
  ip?: string
  port?: number
  title?: string
  message?: string
  contact_email?: string
}

export interface JoinTokenResponse {
  token: string
  expires_at: string
  leader_address: string
}

export interface ApiError {
  error: string
  leader_id?: string
  leader_address?: string
}

// ─── M3 ───────────────────────────────────────────────────────────────────

export interface Profile {
  id: string
  name: string
  blocklists?: string[]
  allowlist?: string[]
  safesearch?: string[]
  client_ips?: string[]
  client_cidrs?: string[]
  // M3.6: stable identity match keys (priority: ids > macs > hostnames > IP)
  client_ids?: string[]
  client_macs?: string[]
  client_hostnames?: string[]
}

export type ScheduleMode = 'block_only_inside' | 'allow_only_inside'

export interface TimeWindow {
  days: string[]   // "Mon","Tue",…,"Sun"
  start: string    // "HH:MM"
  end: string      // "HH:MM"
}

export interface Schedule {
  id: string
  name: string
  mode: ScheduleMode
  windows: TimeWindow[]
}

export interface ScheduleBinding {
  schedule_id: string
  profile_id: string
  blocklist_id: string
}

export interface Category {
  name: string
  description: string
  default_url: string
  url: string             // effective URL (override or default)
  format: string
  enabled_for_profiles: string[]
}

// M4.7 — DNS cache snapshot exposed by /api/v1/dns/cache/stats.
export interface DNSCacheStats {
  size: number
  max_entries: number
  hits: number
  misses: number
  evictions: number
}

// M5.2 — audit log entry as returned by /api/v1/audit.
export interface AuditEntry {
  id: string
  seq: number
  timestamp: string
  actor: string
  action: string
  target?: string
  result: 'ok' | 'error'
  error?: string
  diff?: string
  node_id?: string
  request_id?: string
}

export interface AuditPage {
  entries: AuditEntry[]
  total: number
  limit: number
  offset: number
}

// M3.5 — per-client DoH status
export interface ClientDohStatus {
  client: string
  using_doh: boolean
  doh_probes_1h: number
  last_doh_query: string | null
  suspected_provider: string | null
}

// ─── M3.6 — DHCP integration ─────────────────────────────────────────────

export interface Lease {
  ip: string
  mac: string
  hostname: string
  client_id: string
  source: string                // "kea" | "dnsmasq" | "http_json"
  expires_at: string            // RFC3339
}

export type AnomalyKind =
  | 'mac_changed_for_client_id'
  | 'client_id_changed_for_mac'
  | 'new_device_steals_hostname'

export interface Anomaly {
  id: string
  kind: AnomalyKind
  detected_at: string
  ip: string
  mac?: string
  hostname?: string
  client_id?: string
  prior_mac?: string
  prior_client_id?: string
  prior_hostname?: string
  acknowledged_at?: string | null
}

export interface ClientRecord {
  ip: string
  mac: string
  hostname: string
  client_id: string
  source: string                // "kea" | "dnsmasq" | "http_json" | "none"
  last_seen?: string | null
  anomalies?: Anomaly[]
}

// ─── M13 — Filtering pause ────────────────────────────────────────────────

export interface PauseState {
  active: boolean
  resumes_at?: string
  reason?: string
  profile_ids?: string[]
}

// ─── M22 — Webhooks ──────────────────────────────────────────────────────

export interface WebhookEndpoint {
  id: string
  url: string
  secret: string
  events: string[]
  enabled: boolean
}

// ─── M22.5 / Tokens — API token management ───────────────────────────────

export interface APIToken {
  id: string
  label: string
  scopes: string[]
  created_at: string
  last_used_at?: string | null
  expires_at?: string | null
}

export interface APITokenMinted extends APIToken {
  token: string  // raw value — shown once at creation
}

// ─── Client detail (GET /api/v1/clients/{ip}) ────────────────────────────

export interface ClientDetail {
  ip: string
  mac: string
  hostname: string
  client_id: string
  source: string
  last_seen?: string | null
  anomalies?: Anomaly[]
  origin?: string
  origin_confidence?: string
  ipv6_addresses?: string[]
  duid?: string
  is_dual_stack?: boolean
  profile_ids?: string[]
}

export interface ClientDohStatusDetail {
  client: string
  using_doh: boolean
  doh_probes_1h: number
  last_doh_query: string | null
  suspected_provider: string | null
}

// ─── M23.5/M23.6 — Built-in DHCP server ─────────────────────────────────

export interface DhcpServerStatus {
  enabled: boolean
  is_leader: boolean
  pool_start: string
  pool_end: string
  gateway: string
  lease_time_seconds: number
  domain: string
  dns_server: string
  leases_active: number
  pool_total: number
}

export interface DhcpStaticAssignment {
  mac: string
  ip: string
  hostname: string
}

export interface DhcpLease {
  ip: string
  mac: string
  hostname: string
  expires_at: string
  origin: string  // "dynamic" | "static"
}

// ─── M30 — DHCPv6 server ─────────────────────────────────────────────────────

export interface Dhcp6ServerStatus {
  enabled: boolean
  is_leader: boolean
  prefix: string
  pool_start: string
  pool_end: string
  lease_time: number
  search_domain: string
  leases_active: number
  pool_total: number
}

export interface Dhcp6Lease {
  address: string
  duid: string
  hostname: string
  profile_id?: string
  expires_at: string
  origin: string  // "dhcp6_dynamic" | "dhcp6_static"
}


// M30.5 — Custom filtering rules (TS-CustomRules).
export interface CustomRules {
  rules: string
}
