// Typed wrappers per resource. Views call these; never axios directly.
import { api, deleteRequest, getJSON, patchJSON, postJSON, putJSON } from './client'
import type {
  Blocklist, BlocklistSource, ClusterHealth, ClusterSelf, ClusterStats,
  ClusterStatus, JoinTokenResponse, LocalDNSEntry, QueryLogPage, Settings,
} from './types'

// ─── Auth ────────────────────────────────────────────────────────────────

export function setupCredentials(username: string, password: string) {
  return api.post('/api/v1/auth/setup', { username, password })
}

export function changePassword(current_password: string, new_password: string) {
  return api.put('/api/v1/auth/password', { current_password, new_password })
}

// ─── Health ──────────────────────────────────────────────────────────────

export function healthLiveness(): Promise<{ status: string }> {
  return getJSON('/api/v1/health')
}

export function clusterHealth(): Promise<ClusterHealth> {
  return getJSON('/api/v1/cluster/health')
}

export function clusterSelf(): Promise<ClusterSelf> {
  return getJSON('/api/v1/cluster/self')
}

export function clusterStatus(): Promise<ClusterStatus> {
  return getJSON('/api/v1/cluster/status')
}

export function clusterStats(): Promise<ClusterStats> {
  return getJSON('/api/v1/cluster/stats')
}

// ─── Blocklists ──────────────────────────────────────────────────────────

export function listBlocklists(): Promise<Blocklist[]> {
  return getJSON('/api/v1/blocklists')
}

export interface CreateBlocklistInput {
  id?: string
  name: string
  enabled?: boolean
  source: BlocklistSource
  block_policy?: string
  domains?: string[]
}

export function createBlocklist(input: CreateBlocklistInput): Promise<Blocklist> {
  return postJSON('/api/v1/blocklists', input)
}

export function getBlocklist(id: string): Promise<Blocklist> {
  return getJSON(`/api/v1/blocklists/${encodeURIComponent(id)}`)
}

export function updateBlocklist(id: string, patch: Partial<Pick<Blocklist, 'name' | 'enabled' | 'block_policy'>>): Promise<Blocklist> {
  return patchJSON(`/api/v1/blocklists/${encodeURIComponent(id)}`, patch)
}

export function deleteBlocklist(id: string): Promise<void> {
  return deleteRequest(`/api/v1/blocklists/${encodeURIComponent(id)}`)
}

export function refreshBlocklist(id: string): Promise<Blocklist> {
  return postJSON(`/api/v1/blocklists/${encodeURIComponent(id)}/refresh`)
}

// ─── Allowlist ───────────────────────────────────────────────────────────

export function listAllowlist(): Promise<{ entries: string[] }> {
  return getJSON('/api/v1/allowlist')
}

export function addAllowlist(domain: string): Promise<void> {
  return postJSON('/api/v1/allowlist', { domain })
}

export function removeAllowlist(domain: string): Promise<void> {
  return deleteRequest(`/api/v1/allowlist/${encodeURIComponent(domain)}`)
}

// ─── Local DNS ───────────────────────────────────────────────────────────

export function listLocalDNS(): Promise<LocalDNSEntry[]> {
  return getJSON('/api/v1/local-dns')
}

export function createLocalDNS(entry: Omit<LocalDNSEntry, 'id'> & { id?: string }): Promise<LocalDNSEntry> {
  return postJSON('/api/v1/local-dns', entry)
}

export function updateLocalDNS(id: string, entry: Partial<LocalDNSEntry>): Promise<LocalDNSEntry> {
  return putJSON(`/api/v1/local-dns/${encodeURIComponent(id)}`, entry)
}

export function deleteLocalDNS(id: string): Promise<void> {
  return deleteRequest(`/api/v1/local-dns/${encodeURIComponent(id)}`)
}

// ─── Settings ────────────────────────────────────────────────────────────

export function getSettings(): Promise<Settings> {
  return getJSON('/api/v1/settings')
}

export function patchSettings(patch: Partial<Settings>): Promise<Settings> {
  return patchJSON('/api/v1/settings', patch)
}

// ─── Query log ───────────────────────────────────────────────────────────

export interface QueryLogParams {
  client?: string
  outcome?: 'forwarded' | 'blocked' | 'local' | 'cached' | ''
  limit?: number
  offset?: number
}

export function getQueryLog(params: QueryLogParams = {}): Promise<QueryLogPage> {
  return getJSON('/api/v1/query-log', cleanParams(params))
}

export function getClusterQueryLog(params: QueryLogParams = {}): Promise<QueryLogPage> {
  return getJSON('/api/v1/cluster/query-log', cleanParams(params))
}

function cleanParams(p: QueryLogParams): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  if (p.client) out.client = p.client
  if (p.outcome) out.outcome = p.outcome
  if (p.limit) out.limit = p.limit
  if (p.offset) out.offset = p.offset
  return out
}

// ─── Cluster ops ─────────────────────────────────────────────────────────

export function createJoinToken(): Promise<JoinTokenResponse> {
  return postJSON('/api/v1/cluster/tokens')
}

export function transferLeadership(target_node_id: string): Promise<void> {
  return postJSON('/api/v1/cluster/leadership/transfer', { target_node_id })
}

export function removeNode(node_id: string): Promise<void> {
  return deleteRequest(`/api/v1/cluster/nodes/${encodeURIComponent(node_id)}`)
}
