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
  filtering: { block_policy: 'nxdomain' | 'null' | 'nodata' }
  query_log: { max_entries: number; aggregate_retention_days: number }
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

// M3.5 — per-client DoH status
export interface ClientDohStatus {
  client: string
  using_doh: boolean
  doh_probes_1h: number
  last_doh_query: string | null
  suspected_provider: string | null
}
