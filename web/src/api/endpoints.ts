// Typed wrappers per resource. Views call these; never axios directly.
import { api, deleteRequest, getJSON, patchJSON, postJSON, putJSON } from './client'
import type {
  Anomaly, APIToken, APITokenMinted, AuditPage, BlockPageConfig, Blocklist, BlocklistSource, Category,
  ClientDetail, ClientDohStatus, ClientDohStatusDetail, ClientRecord,
  ClusterHealth, ClusterSelf, ClusterStats, ClusterStatus, CustomRules, DNSCacheStats,
  Dhcp6Lease, Dhcp6ServerStatus, DhcpLease, DhcpServerStatus, DhcpStaticAssignment,
  JoinTokenResponse, Lease, LocalDNSEntry, PauseState, Profile, QueryLogPage, Schedule,
  ScheduleBinding, Settings, WebhookEndpoint,
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
  refresh_interval_seconds?: number
}

export function createBlocklist(input: CreateBlocklistInput): Promise<Blocklist> {
  return postJSON('/api/v1/blocklists', input)
}

export function getBlocklist(id: string): Promise<Blocklist> {
  return getJSON(`/api/v1/blocklists/${encodeURIComponent(id)}`)
}

export interface BlocklistPatch {
  name?: string
  enabled?: boolean
  block_policy?: string
  refresh_interval_seconds?: number
  source?: { url: string; format?: string }
}

export function updateBlocklist(id: string, patch: BlocklistPatch): Promise<Blocklist> {
  return patchJSON(`/api/v1/blocklists/${encodeURIComponent(id)}`, patch)
}

export function deleteBlocklist(id: string): Promise<void> {
  return deleteRequest(`/api/v1/blocklists/${encodeURIComponent(id)}`)
}

export function refreshBlocklist(id: string): Promise<Blocklist> {
  return postJSON(`/api/v1/blocklists/${encodeURIComponent(id)}/refresh`)
}

// ─── Allowlist ───────────────────────────────────────────────────────────

export function listAllowlist(): Promise<string[]> {
  return getJSON('/api/v1/allowlist')
}

export function addAllowlist(domain: string): Promise<void> {
  return postJSON('/api/v1/allowlist', { domain })
}

export function removeAllowlist(domain: string): Promise<void> {
  return deleteRequest(`/api/v1/allowlist/${encodeURIComponent(domain)}`)
}

// Per-profile allowlist

export function listProfileAllowlist(profileId: string): Promise<string[]> {
  return getJSON(`/api/v1/profiles/${encodeURIComponent(profileId)}/allowlist`)
}

export function addProfileAllowlist(profileId: string, domain: string): Promise<void> {
  return postJSON(`/api/v1/profiles/${encodeURIComponent(profileId)}/allowlist`, { domain })
}

export function removeProfileAllowlist(profileId: string, domain: string): Promise<void> {
  return deleteRequest(`/api/v1/profiles/${encodeURIComponent(profileId)}/allowlist/${encodeURIComponent(domain)}`)
}

export function replaceProfileAllowlist(profileId: string, domains: string[]): Promise<void> {
  return putJSON(`/api/v1/profiles/${encodeURIComponent(profileId)}/allowlist`, domains)
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

// ─── Block page (M26) ────────────────────────────────────────────────────

export function getBlockPageConfig(): Promise<BlockPageConfig> {
  return getJSON('/api/v1/blockpage')
}

export function patchBlockPageConfig(patch: BlockPageConfig): Promise<BlockPageConfig> {
  return patchJSON('/api/v1/blockpage', patch)
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

export function nodeSelfJoin(token: string, leader_address: string): Promise<{ cluster_id: string }> {
  return postJSON('/api/v1/node/join-cluster', { token, leader_address })
}

// ─── M3 — Profiles ───────────────────────────────────────────────────────

export function listProfiles(): Promise<Profile[]> {
  return getJSON('/api/v1/profiles')
}

export function getProfile(id: string): Promise<Profile> {
  return getJSON(`/api/v1/profiles/${encodeURIComponent(id)}`)
}

export function createProfile(p: Profile): Promise<Profile> {
  return postJSON('/api/v1/profiles', p)
}

export function updateProfile(id: string, patch: Partial<Profile>): Promise<Profile> {
  return patchJSON(`/api/v1/profiles/${encodeURIComponent(id)}`, patch)
}

export function deleteProfile(id: string): Promise<void> {
  return deleteRequest(`/api/v1/profiles/${encodeURIComponent(id)}`)
}

// ─── M3 — Schedules ──────────────────────────────────────────────────────

export function listSchedules(): Promise<Schedule[]> {
  return getJSON('/api/v1/schedules')
}

export function createSchedule(s: Schedule): Promise<Schedule> {
  return postJSON('/api/v1/schedules', s)
}

export function updateSchedule(id: string, patch: Partial<Schedule>): Promise<Schedule> {
  return patchJSON(`/api/v1/schedules/${encodeURIComponent(id)}`, patch)
}

export function deleteSchedule(id: string): Promise<void> {
  return deleteRequest(`/api/v1/schedules/${encodeURIComponent(id)}`)
}

export function addScheduleBinding(id: string, profile_id: string, blocklist_id: string): Promise<ScheduleBinding> {
  return postJSON(`/api/v1/schedules/${encodeURIComponent(id)}/bindings`, { profile_id, blocklist_id })
}

export function deleteScheduleBinding(id: string, profile: string, blocklist: string): Promise<void> {
  return deleteRequest(`/api/v1/schedules/${encodeURIComponent(id)}/bindings/${encodeURIComponent(profile)}/${encodeURIComponent(blocklist)}`)
}

// ─── M3 — Categories ─────────────────────────────────────────────────────

export function listCategories(): Promise<Category[]> {
  return getJSON('/api/v1/categories')
}

export function getCategoryView(name: string): Promise<Category> {
  return getJSON(`/api/v1/categories/${encodeURIComponent(name)}`)
}

export function updateCategoryURL(name: string, url: string, format?: string): Promise<unknown> {
  return patchJSON(`/api/v1/categories/${encodeURIComponent(name)}`, { url, format })
}

export function enableCategory(name: string, profile_id: string): Promise<unknown> {
  return postJSON(`/api/v1/categories/${encodeURIComponent(name)}/enable`, { profile_id })
}

export function disableCategory(name: string, profile_id: string): Promise<unknown> {
  return postJSON(`/api/v1/categories/${encodeURIComponent(name)}/disable`, { profile_id })
}

// ─── M3.5 — Per-client DoH status ────────────────────────────────────────

export function getClientDohStatus(ip: string): Promise<ClientDohStatus> {
  return getJSON(`/api/v1/clients/${encodeURIComponent(ip)}/doh-status`)
}

// ─── M3.6 — DHCP-enriched clients + anti-spoof ──────────────────────────

export function getClient(ip: string): Promise<ClientRecord> {
  return getJSON(`/api/v1/clients/${encodeURIComponent(ip)}`)
}

export function listLeases(): Promise<Lease[]> {
  return getJSON('/api/v1/clients/_leases')
}

export function listAnomalies(): Promise<Anomaly[]> {
  return getJSON('/api/v1/clients/anomalies')
}

export function acknowledgeAnomaly(id: string): Promise<void> {
  return postJSON(`/api/v1/clients/anomalies/${encodeURIComponent(id)}/acknowledge`)
}

export function exportReservationsURL(format: 'dnsmasq' | 'kea' | 'json'): string {
  return `/api/v1/clients/export-reservations?format=${format}`
}

// ─── M4.7 — DNS cache controls ──────────────────────────────────────────

export function getDNSCacheStats(): Promise<DNSCacheStats> {
  return getJSON('/api/v1/dns/cache/stats')
}

export function purgeDNSCache(domain?: string): Promise<{ purged: number }> {
  const q = domain ? `?domain=${encodeURIComponent(domain)}` : ''
  return postJSON('/api/v1/dns/cache/purge' + q)
}

// ─── M5.2 — audit log ───────────────────────────────────────────────────

export interface AuditQuery {
  actor?: string
  action?: string
  result?: 'ok' | 'error' | ''
  limit?: number
  offset?: number
}

export function listAudit(q: AuditQuery = {}): Promise<AuditPage> {
  const params: Record<string, string | number> = {}
  if (q.actor) params.actor = q.actor
  if (q.action) params.action = q.action
  if (q.result) params.result = q.result
  if (q.limit) params.limit = q.limit
  if (q.offset) params.offset = q.offset
  return getJSON('/api/v1/audit', params)
}

// ─── M5.6 — in-place upgrade ────────────────────────────────────────────

export interface UpgradeCheck {
  current_version: string
  available_version: string
  upgrade_available: boolean
  release_notes_url: string
  published_at: string
  checked_at: string
}

export function checkUpgrade(): Promise<UpgradeCheck> {
  return getJSON('/api/v1/upgrade/check')
}

export function startUpgrade(): Promise<unknown> {
  return postJSON('/api/v1/upgrade/start')
}

// ─── Test domain (M5.9.7) ────────────────────────────────────────────────

export interface TestDomainRequest {
  domain: string
  client_ip?: string
  profile_id?: string
}

export interface TestDomainResponse {
  domain: string
  client_ip?: string
  would_block: boolean
  reason: string
  matched_profile_id?: string
  matched_blocklist_id?: string
  block_policy?: string
  local_dns_answer?: string
  safesearch_rewrite?: string
  evaluated_at?: string
}

export function testDomain(req: TestDomainRequest): Promise<TestDomainResponse> {
  return postJSON('/api/v1/test-domain', req)
}

// ─── M13 — Filtering pause ───────────────────────────────────────────────

export function getGlobalPause(): Promise<PauseState> {
  return getJSON('/api/v1/filtering/pause')
}

export function setGlobalPause(duration_seconds: number, reason?: string, profile_ids?: string[]): Promise<PauseState> {
  return postJSON('/api/v1/filtering/pause', {
    duration_seconds,
    reason: reason || undefined,
    profile_ids: profile_ids?.length ? profile_ids : undefined,
  })
}

export function clearGlobalPause(): Promise<void> {
  return deleteRequest('/api/v1/filtering/pause')
}

export function getProfilePause(id: string): Promise<PauseState> {
  return getJSON(`/api/v1/profiles/${encodeURIComponent(id)}/pause`)
}

export function setProfilePause(id: string, duration_seconds: number, reason?: string): Promise<PauseState> {
  return postJSON(`/api/v1/profiles/${encodeURIComponent(id)}/pause`, { duration_seconds, reason: reason || undefined })
}

export function clearProfilePause(id: string): Promise<void> {
  return deleteRequest(`/api/v1/profiles/${encodeURIComponent(id)}/pause`)
}

// ─── M6 — Firewall rule generator (TS-FwRuleGen, consumed by TS-FwRuleUi) ─

export type FwRulePlatform =
  | 'iptables' | 'nftables' | 'mikrotik' | 'opnsense' | 'unifi'

export const FW_RULE_PLATFORMS: FwRulePlatform[] = [
  'iptables', 'nftables', 'mikrotik', 'opnsense', 'unifi',
]

export type FwRuleAction = 'drop' | 'reject'

export type FwRuleScope =
  | { kind: 'client'; ip: string }       // expands to scope=subnet&subnet=<ip>/32
  | { kind: 'subnet'; cidr: string }
  | { kind: 'profile'; profileId: string }
  | { kind: 'all' }

// getFirewallRules calls the M6 firewall-rule generator endpoint
// (TS-FwRuleGen). Returns the raw text payload — the UI renders it
// verbatim in a <pre> block per TS-FwRuleUi.
export async function getFirewallRules(
  scope: FwRuleScope,
  platform: FwRulePlatform,
  action: FwRuleAction = 'drop',
): Promise<string> {
  const params: Record<string, string> = {
    platform,
    action,
  }
  switch (scope.kind) {
    case 'client':
      params.scope = 'subnet'
      params.subnet = `${scope.ip}/32`
      break
    case 'subnet':
      params.scope = 'subnet'
      params.subnet = scope.cidr
      break
    case 'profile':
      params.scope = 'profile'
      params.profile = scope.profileId
      break
    case 'all':
      params.scope = 'all'
      break
  }
  // The endpoint returns text/plain; axios's default `responseType: 'json'`
  // would yield a string anyway for non-JSON bodies, but make it explicit
  // so axios doesn't try to JSON.parse the rule blob.
  const r = await api.get<string>('/api/v1/firewall-rules', {
    params,
    responseType: 'text',
    transformResponse: [(d) => d],
  })
  return r.data
}

// ─── M18 — Rolling cluster upgrade ──────────────────────────────────────

export interface RollingUpgradeStatus {
  in_progress: boolean
  pending_nodes: string[]
  completed_nodes: string[]
  failed_node: string | null
}

export function rollingUpgradeApply(url: string): Promise<{ accepted: boolean; message: string }> {
  return postJSON('/api/v1/cluster/upgrade/apply', { url })
}

export function rollingUpgradeStatus(): Promise<RollingUpgradeStatus> {
  return getJSON('/api/v1/cluster/upgrade/status')
}

// ─── M22 — Webhooks ──────────────────────────────────────────────────────

export function listWebhooks(): Promise<WebhookEndpoint[]> {
  return getJSON('/api/v1/webhooks')
}

export function createWebhook(ep: Omit<WebhookEndpoint, 'id'>): Promise<WebhookEndpoint> {
  return postJSON('/api/v1/webhooks', ep)
}

export function deleteWebhook(id: string): Promise<void> {
  return deleteRequest(`/api/v1/webhooks/${encodeURIComponent(id)}`)
}

export function testWebhook(id: string): Promise<void> {
  return postJSON(`/api/v1/webhooks/${encodeURIComponent(id)}/test`)
}

// ─── API Tokens ──────────────────────────────────────────────────────────

export function listTokens(): Promise<APIToken[]> {
  return getJSON('/api/v1/tokens')
}

export function createToken(
  label: string,
  scopes: string[],
  expires_at?: string,
): Promise<APITokenMinted> {
  return postJSON('/api/v1/tokens', { label, scopes, expires_at: expires_at || undefined })
}

export function deleteToken(id: string): Promise<void> {
  return deleteRequest(`/api/v1/tokens/${encodeURIComponent(id)}`)
}

export function renameToken(id: string, label: string): Promise<APIToken> {
  return patchJSON(`/api/v1/tokens/${encodeURIComponent(id)}`, { label })
}

// ─── Client detail ───────────────────────────────────────────────────────

export function getClientDetail(ip: string): Promise<ClientDetail> {
  return getJSON(`/api/v1/clients/${encodeURIComponent(ip)}`)
}

export function getClientDohStatusDetail(ip: string): Promise<ClientDohStatusDetail> {
  return getJSON(`/api/v1/clients/${encodeURIComponent(ip)}/doh-status`)
}

// ─── M23.5/M23.6 — Built-in DHCP server ─────────────────────────────────

export function getDhcpServerStatus(): Promise<DhcpServerStatus> {
  return getJSON('/api/v1/dhcp/server/status')
}

export interface DhcpSettingsPatch {
  enabled?: boolean
  pool_start?: string
  pool_end?: string
  gateway?: string
  lease_time_seconds?: number
  domain?: string
  dns_server?: string
}

export function putDhcpServerSettings(patch: DhcpSettingsPatch): Promise<DhcpServerStatus> {
  return putJSON('/api/v1/settings/dhcp', patch)
}

export function listDhcpStaticAssignments(): Promise<DhcpStaticAssignment[]> {
  return getJSON('/api/v1/dhcp/static-assignments')
}

export function createDhcpStaticAssignment(a: DhcpStaticAssignment): Promise<DhcpStaticAssignment> {
  return postJSON('/api/v1/dhcp/static-assignments', a)
}

export function deleteDhcpStaticAssignment(mac: string): Promise<void> {
  // colons in MAC addresses must not be percent-encoded — chi's router
  // does not decode %3A in path segments, so pass the MAC as-is.
  return deleteRequest(`/api/v1/dhcp/static-assignments/${mac}`)
}

export function listDhcpLeases(): Promise<DhcpLease[]> {
  return getJSON<{ leases: DhcpLease[] }>('/api/v1/dhcp/leases').then(r => r?.leases ?? [])
}

// ─── M30 — DHCPv6 ────────────────────────────────────────────────────────────

export function getDhcp6ServerStatus(): Promise<Dhcp6ServerStatus> {
  return getJSON('/api/v1/dhcp/server/status6')
}

export interface Dhcp6SettingsPatch {
  enabled?: boolean
  prefix?: string
  pool_start?: string
  pool_end?: string
  lease_time?: number
  search_domain?: string
}

export function putDhcp6ServerSettings(patch: Dhcp6SettingsPatch): Promise<Dhcp6ServerStatus> {
  return putJSON('/api/v1/settings/dhcp6', patch)
}

export function listDhcp6Leases(): Promise<Dhcp6Lease[]> {
  return getJSON<{ leases: Dhcp6Lease[] }>('/api/v1/dhcp/leases6').then(r => r?.leases ?? [])
}

// ─── M30.5: Custom filtering rules ───────────────────────────────────────────

export function getCustomRules(): Promise<CustomRules> {
  return getJSON('/api/v1/custom-rules')
}

export function putCustomRules(rules: string): Promise<CustomRules> {
  return putJSON('/api/v1/custom-rules', { rules })
}
