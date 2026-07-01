<template>
  <div class="space-y-6">
    <!-- Page header -->
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold text-fg-strong">Settings</h1>
    </div>

    <!-- Global error banner -->
    <p v-if="lastError"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ lastError }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="lastError = ''">dismiss</button>
    </p>

    <p v-if="loading" class="card p-5 text-sm text-fg-muted text-center">Loading settings…</p>

    <template v-else-if="settings">
      <!-- ─── DNS section ──────────────────────────────────────────────── -->
      <section class="card p-5 space-y-4">
        <header class="flex items-center gap-2">
          <GlobeAltIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">DNS</h2>
        </header>

        <p v-if="dnsError" class="text-sm text-danger">{{ dnsError }}</p>

        <div>
          <span class="label">Mode</span>
          <div class="flex gap-4 text-sm">
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="forwarding" v-model="dnsForm.mode" @change="markDnsDirty" />
              Forwarding
            </label>
            <label class="inline-flex items-center gap-2">
              <input type="radio" value="recursive" v-model="dnsForm.mode" @change="markDnsDirty" />
              Recursive
            </label>
          </div>
          <p class="text-xs text-fg-muted mt-1">
            <span v-if="dnsForm.mode === 'forwarding'">Queries are forwarded to upstream resolvers.</span>
            <span v-else>Queries are resolved recursively from the root servers.</span>
          </p>
        </div>

        <div v-if="dnsForm.mode === 'forwarding'">
          <label class="label" for="dns-upstreams">
            Upstream resolvers
            <span class="text-fg-subtle font-normal">(one per line, host:port)</span>
          </label>
          <textarea id="dns-upstreams"
                    v-model="dnsForm.upstreamsText"
                    rows="4"
                    class="input font-mono text-xs"
                    placeholder="9.9.9.9:53&#10;1.1.1.1:53"
                    @input="markDnsDirty" />
        </div>

        <!-- M32: per-domain upstream routes -->
        <div v-if="dnsForm.mode === 'forwarding'">
          <div class="flex items-center justify-between mb-2">
            <label class="label !mb-0">
              Per-domain routes
              <span class="text-fg-subtle font-normal">(optional)</span>
            </label>
            <button class="btn-secondary text-xs"
                    type="button"
                    @click="addRoute">
              + Add route
            </button>
          </div>
          <div v-if="dnsForm.routes.length === 0" class="text-xs text-fg-muted">
            No routes configured. All queries use the upstream resolvers above.
          </div>
          <div v-for="(route, idx) in dnsForm.routes" :key="idx"
               class="border border-border rounded-md p-3 space-y-2 mb-2">
            <div class="flex items-center gap-2">
              <input :id="`route-match-${idx}`"
                     v-model="route.match"
                     type="text"
                     class="input font-mono text-xs flex-1"
                     placeholder="*.corp.internal or api.example.com"
                     @input="markDnsDirty" />
              <button class="btn-ghost text-xs text-danger"
                      type="button"
                      @click="removeRoute(idx)">
                Remove
              </button>
            </div>
            <textarea :id="`route-resolvers-${idx}`"
                      v-model="route.resolversText"
                      rows="2"
                      class="input font-mono text-xs"
                      placeholder="10.0.0.1:53&#10;10.0.0.2:53"
                      @input="markDnsDirty" />
          </div>
        </div>

        <div v-else>
          <label class="label" for="dns-trusted">
            Trusted subnets
            <span class="text-fg-subtle font-normal">(one CIDR per line)</span>
          </label>
          <textarea id="dns-trusted"
                    v-model="dnsForm.trustedText"
                    rows="4"
                    class="input font-mono text-xs"
                    placeholder="192.168.0.0/16&#10;10.0.0.0/8"
                    @input="markDnsDirty" />
        </div>

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label class="label" for="dns-timeout">Upstream timeout (seconds)</label>
            <input id="dns-timeout"
                   v-model.number="dnsForm.timeout"
                   type="number" min="1" max="30"
                   class="input"
                   @input="markDnsDirty" />
          </div>
          <div>
            <div class="flex items-center justify-between">
              <label class="label !mb-0" for="dns-cache-max">Cache max entries</label>
              <label class="inline-flex items-center gap-1.5 text-xs text-fg-muted">
                <input type="checkbox"
                       v-model="dnsForm.cacheEnabled"
                       @change="markDnsDirty" />
                Enable DNS cache
              </label>
            </div>
            <input id="dns-cache-max"
                   v-model.number="dnsForm.cacheMax"
                   type="number" min="0"
                   class="input mt-1"
                   :disabled="!dnsForm.cacheEnabled"
                   @input="markDnsDirty" />
          </div>
        </div>

        <!-- M4.7 — DNS cache stats + purge button -->
        <div class="border-t border-border pt-3 space-y-2" v-if="dnsForm.cacheEnabled">
          <div class="flex items-center justify-between">
            <span class="label !mb-0">DNS cache</span>
            <button class="btn-secondary text-xs"
                    :disabled="cachePurging"
                    @click="onPurgeCache">
              {{ cachePurging ? 'Purging…' : 'Clear DNS cache' }}
            </button>
          </div>
          <div v-if="cacheStats" class="grid grid-cols-2 sm:grid-cols-5 gap-2 text-xs text-fg-muted">
            <span>size <b class="text-fg-strong">{{ cacheStats.size }}</b> / {{ cacheStats.max_entries }}</span>
            <span>hits <b class="text-fg-strong">{{ cacheStats.hits }}</b></span>
            <span>misses <b class="text-fg-strong">{{ cacheStats.misses }}</b></span>
            <span>evictions <b class="text-fg-strong">{{ cacheStats.evictions }}</b></span>
            <span v-if="cachePurgedAt" class="text-success">Purged {{ cachePurgedCount }}.</span>
          </div>
        </div>

        <div class="flex items-center justify-end gap-3 pt-1">
          <span v-if="dnsSavedAt" class="text-xs text-success">Saved.</span>
          <button class="btn-primary"
                  :disabled="!dnsDirty || dnsSaving"
                  @click="saveDns">
            {{ dnsSaving ? 'Saving…' : 'Save DNS settings' }}
          </button>
        </div>
      </section>

      <!-- ─── Filtering section ────────────────────────────────────────── -->
      <section class="card p-5 space-y-4">
        <header class="flex items-center gap-2">
          <ShieldCheckIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">Filtering</h2>
        </header>

        <p v-if="filteringError" class="text-sm text-danger">{{ filteringError }}</p>

        <div>
          <span class="label">Default block policy</span>
          <div class="space-y-2 text-sm">
            <label class="flex items-start gap-2">
              <input type="radio" value="nxdomain"
                     v-model="filteringForm.policy"
                     class="mt-1"
                     @change="markFilteringDirty" />
              <span>
                <span class="font-medium text-fg-strong">NXDOMAIN</span>
                <span class="block text-xs text-fg-muted">
                  Pretend the domain doesn't exist (recommended)
                </span>
              </span>
            </label>
            <label class="flex items-start gap-2">
              <input type="radio" value="null"
                     v-model="filteringForm.policy"
                     class="mt-1"
                     @change="markFilteringDirty" />
              <span>
                <span class="font-medium text-fg-strong">NULL</span>
                <span class="block text-xs text-fg-muted">Return 0.0.0.0 / ::</span>
              </span>
            </label>
            <label class="flex items-start gap-2">
              <input type="radio" value="nodata"
                     v-model="filteringForm.policy"
                     class="mt-1"
                     @change="markFilteringDirty" />
              <span>
                <span class="font-medium text-fg-strong">NODATA</span>
                <span class="block text-xs text-fg-muted">Return empty success</span>
              </span>
            </label>
            <label class="flex items-start gap-2">
              <input type="radio" value="redirect"
                     v-model="filteringForm.policy"
                     class="mt-1"
                     @change="markFilteringDirty" />
              <span>
                <span class="font-medium text-fg-strong">Redirect</span>
                <span class="block text-xs text-fg-muted">
                  Return block page IP — shows a human-readable block page
                </span>
              </span>
            </label>
          </div>
        </div>

        <div class="flex items-center justify-end gap-3 pt-1">
          <span v-if="filteringSavedAt" class="text-xs text-success">Saved.</span>
          <button class="btn-primary"
                  :disabled="!filteringDirty || filteringSaving"
                  @click="saveFiltering">
            {{ filteringSaving ? 'Saving…' : 'Save filtering' }}
          </button>
        </div>
      </section>

      <!-- ─── Block Page section (M26) ──────────────────────────────────── -->
      <section class="card p-5 space-y-4">
        <header class="flex items-center gap-2">
          <ShieldCheckIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">Block Page</h2>
          <span class="text-xs text-fg-muted ml-1">
            Used when block policy is set to <span class="font-mono">redirect</span>
          </span>
        </header>

        <p class="text-sm text-fg-muted">
          When the redirect block policy is active, blocked A queries return this IP.
          The built-in HTTP server on the configured port serves a human-readable page.
        </p>

        <p v-if="blockPageError" class="text-sm text-danger">{{ blockPageError }}</p>

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label class="label" for="bp-ip">Block page IP (IPv4)</label>
            <input id="bp-ip"
                   v-model="blockPageForm.ip"
                   type="text"
                   placeholder="e.g. 192.168.1.1"
                   class="input"
                   @input="markBlockPageDirty" />
            <p class="text-xs text-fg-muted mt-1">
              IPv4 address returned for blocked A queries. Leave empty to use the node's API host IP.
            </p>
          </div>
          <div>
            <label class="label" for="bp-ipv6">Block page IPv6 (optional)</label>
            <input id="bp-ipv6"
                   v-model="blockPageForm.redirect_address_v6"
                   type="text"
                   placeholder="e.g. 2001:db8::1"
                   class="input"
                   @input="markBlockPageDirty" />
            <p class="text-xs text-fg-muted mt-1">
              IPv6 address returned for blocked AAAA queries. Leave empty to return NXDOMAIN for AAAA.
            </p>
          </div>
          <div>
            <label class="label" for="bp-port">Block page port</label>
            <input id="bp-port"
                   v-model.number="blockPageForm.port"
                   type="number" min="1" max="65535"
                   class="input"
                   @input="markBlockPageDirty" />
            <p class="text-xs text-fg-muted mt-1">
              TCP port the block page HTTP server listens on (default 8053).
            </p>
          </div>
          <div>
            <label class="label" for="bp-title">Page title</label>
            <input id="bp-title"
                   v-model="blockPageForm.title"
                   type="text"
                   placeholder="Access Blocked"
                   class="input"
                   @input="markBlockPageDirty" />
          </div>
          <div>
            <label class="label" for="bp-email">Contact email</label>
            <input id="bp-email"
                   v-model="blockPageForm.contact_email"
                   type="email"
                   placeholder="admin@example.com"
                   class="input"
                   @input="markBlockPageDirty" />
          </div>
          <div class="sm:col-span-2">
            <label class="label" for="bp-message">Message</label>
            <textarea id="bp-message"
                      v-model="blockPageForm.message"
                      rows="2"
                      placeholder="This website has been blocked by your network administrator."
                      class="input resize-none"
                      @input="markBlockPageDirty" />
          </div>
        </div>

        <div class="flex items-center justify-end gap-3 pt-1">
          <span v-if="blockPageSavedAt" class="text-xs text-success">Saved.</span>
          <button class="btn-primary"
                  :disabled="!blockPageDirty || blockPageSaving"
                  @click="saveBlockPage">
            {{ blockPageSaving ? 'Saving…' : 'Save block page' }}
          </button>
        </div>
      </section>

      <!-- ─── Query log section ────────────────────────────────────────── -->
      <section class="card p-5 space-y-4">
        <header class="flex items-center gap-2">
          <ClipboardDocumentListIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">Query log</h2>
        </header>

        <p v-if="queryLogError" class="text-sm text-danger">{{ queryLogError }}</p>

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label class="label" for="ql-max">Max entries per node</label>
            <input id="ql-max"
                   v-model.number="queryLogForm.maxEntries"
                   type="number" min="100"
                   class="input"
                   @input="markQueryLogDirty" />
            <p class="text-xs text-fg-muted mt-1">
              Older entries are evicted on each node when this limit is reached.
            </p>
          </div>
          <div>
            <label class="label" for="ql-retention">Aggregate retention (days)</label>
            <input id="ql-retention"
                   v-model.number="queryLogForm.retentionDays"
                   type="number" min="1" max="365"
                   class="input"
                   @input="markQueryLogDirty" />
            <p class="text-xs text-fg-muted mt-1">
              Hourly aggregates used by the Dashboard are kept for this long.
            </p>
          </div>
        </div>

        <div class="flex items-center justify-end gap-3 pt-1">
          <span v-if="queryLogSavedAt" class="text-xs text-success">Saved.</span>
          <button class="btn-primary"
                  :disabled="!queryLogDirty || queryLogSaving"
                  @click="saveQueryLog">
            {{ queryLogSaving ? 'Saving…' : 'Save query log' }}
          </button>
        </div>
      </section>

      <!-- ─── Audit log link (M5.2) ─────────────────────────────────────── -->
      <!-- ─── Session timeout section (M34.5) ─────────────────────────────── -->
      <section class="card p-5 space-y-4">
        <header class="flex items-center gap-2">
          <ShieldCheckIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">Session timeout</h2>
        </header>

        <p class="text-sm text-fg-muted">
          Controls how long a web UI login session remains valid before re-authentication
          is required. Applies to new sessions only — active sessions keep their original expiry.
        </p>

        <p v-if="sessionTimeoutError" class="text-sm text-danger">{{ sessionTimeoutError }}</p>

        <div>
          <label class="label" for="session-timeout">Timeout duration</label>
          <select id="session-timeout"
                  v-model="sessionTimeoutForm.seconds"
                  class="input w-48"
                  @change="markSessionTimeoutDirty">
            <option :value="1800">30 minutes</option>
            <option :value="3600">1 hour</option>
            <option :value="14400">4 hours</option>
            <option :value="28800">8 hours (default)</option>
            <option :value="86400">24 hours</option>
            <option :value="604800">7 days</option>
          </select>
        </div>

        <div class="flex items-center gap-3">
          <button class="btn-primary"
                  :disabled="!sessionTimeoutDirty || sessionTimeoutSaving"
                  @click="saveSessionTimeout">
            {{ sessionTimeoutSaving ? 'Saving…' : 'Save' }}
          </button>
          <span v-if="sessionTimeoutSavedAt && Date.now() - sessionTimeoutSavedAt < 4000"
                class="text-xs text-success">Saved</span>
        </div>
      </section>

      <!-- ─── Audit log section ─────────────────────────────────────────── -->
      <section class="card p-5">
        <header class="flex items-center gap-2">
          <DocumentTextIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">Audit log</h2>
        </header>
        <p class="text-sm text-fg-muted mt-1">
          Every state-changing API call is recorded with actor, action, target, and result.
          Replicated through Raft &mdash; identical on every node.
        </p>
        <div class="mt-3">
          <router-link :to="{ name: 'audit' }" class="btn-secondary">
            Open audit log
          </router-link>
        </div>
      </section>

      <!-- ─── Configuration backup (M31) ──────────────────────────────── -->
      <section class="card p-5 space-y-4">
        <header class="flex items-center gap-2">
          <ArchiveBoxIcon class="h-5 w-5 text-accent" />
          <h2 class="text-base font-semibold text-fg-strong">Configuration backup</h2>
        </header>

        <p class="text-sm text-fg-muted">
          Download the full configuration as a portable archive, or restore a previously
          downloaded backup. Admin credentials are <strong>not</strong> included in the
          backup and are never changed by a restore.
        </p>

        <!-- Export -->
        <div class="space-y-2 pt-1">
          <h3 class="text-sm font-semibold text-fg-strong">Export</h3>
          <div class="flex flex-wrap items-end gap-3">
            <div>
              <label class="label" for="export-passphrase">Passphrase (optional)</label>
              <input id="export-passphrase"
                     v-model="exportPassphrase"
                     type="password"
                     placeholder="Leave empty for plain .tar.gz"
                     class="input w-64" />
              <p class="text-xs text-fg-muted mt-1">When set, archive is age-encrypted (.age)</p>
            </div>
            <button class="btn-secondary" :disabled="exporting" @click="downloadBackup">
              {{ exporting ? 'Preparing…' : 'Download backup' }}
            </button>
          </div>
          <span v-if="exportError" class="text-xs text-danger">{{ exportError }}</span>
        </div>

        <!-- Restore -->
        <div class="border-t border-border pt-4 space-y-3">
          <h3 class="text-sm font-semibold text-fg-strong">Restore backup</h3>

          <p v-if="backupError" class="text-sm text-danger">{{ backupError }}</p>
          <p v-if="backupSuccess" class="text-sm text-success">{{ backupSuccess }}</p>

          <div class="flex flex-wrap items-end gap-3">
            <label class="btn-secondary cursor-pointer self-end">
              <input ref="fileInputEl" type="file" accept=".tar.gz,.tgz,.age" class="sr-only"
                     :disabled="restoring"
                     @change="onBackupFileSelected" />
              {{ selectedFile ? selectedFile.name : 'Choose archive…' }}
            </label>
            <div>
              <label class="label" for="import-passphrase">Passphrase (if encrypted)</label>
              <input id="import-passphrase"
                     v-model="importPassphrase"
                     type="password"
                     placeholder="For .age archives"
                     class="input w-48" />
            </div>
            <button class="btn-danger self-end"
                    :disabled="!selectedFile || restoring"
                    @click="restoreConfirm = true">
              {{ restoring ? 'Restoring…' : 'Restore' }}
            </button>
          </div>
        </div>

        <!-- Scheduled backup -->
        <div class="border-t border-border pt-4 space-y-3">
          <h3 class="text-sm font-semibold text-fg-strong">Scheduled backup</h3>
          <p class="text-xs text-fg-muted">
            Automatically snapshot the configuration at regular intervals. Backups are deduplicated —
            no snapshot is created when the config has not changed.
          </p>
          <p v-if="backupScheduleError" class="text-sm text-danger">{{ backupScheduleError }}</p>

          <div class="flex flex-wrap items-center gap-4">
            <label class="flex items-center gap-2 text-sm">
              <input type="checkbox" v-model="backupSchedule.enabled" class="rounded" />
              Enable scheduled backups
            </label>
            <div class="flex items-center gap-2">
              <label class="text-sm text-fg-muted" for="sched-interval">Every</label>
              <input id="sched-interval"
                     v-model.number="backupSchedule.interval_hours"
                     type="number" min="1" max="168"
                     class="input w-20"
                     :disabled="!backupSchedule.enabled" />
              <span class="text-sm text-fg-muted">hours</span>
            </div>
            <div class="flex items-center gap-2">
              <label class="text-sm text-fg-muted" for="sched-retain">Keep</label>
              <input id="sched-retain"
                     v-model.number="backupSchedule.retain_count"
                     type="number" min="1" max="100"
                     class="input w-20"
                     :disabled="!backupSchedule.enabled" />
              <span class="text-sm text-fg-muted">backups</span>
            </div>
          </div>

          <div class="flex items-center gap-3">
            <span v-if="backupScheduleSavedAt" class="text-xs text-success">Saved.</span>
            <button class="btn-primary" :disabled="backupScheduleSaving" @click="saveBackupSchedule">
              {{ backupScheduleSaving ? 'Saving…' : 'Save schedule' }}
            </button>
          </div>
        </div>

        <!-- Stored backups -->
        <div class="border-t border-border pt-4 space-y-3">
          <div class="flex items-center justify-between">
            <h3 class="text-sm font-semibold text-fg-strong">
              Stored backups
              <span v-if="storedBackups.length" class="text-fg-muted font-normal">({{ storedBackups.length }})</span>
            </h3>
            <div class="flex items-center gap-2">
              <span v-if="triggerResult" class="text-xs" :class="triggerResult.includes('skipped') ? 'text-fg-muted' : 'text-success'">
                {{ triggerResult }}
              </span>
              <button class="btn-secondary text-xs" :disabled="triggeringBackup" @click="doTriggerBackup">
                {{ triggeringBackup ? 'Running…' : 'Trigger now' }}
              </button>
            </div>
          </div>

          <p v-if="!storedBackups.length" class="text-xs text-fg-muted italic">
            No stored backups yet. Enable the schedule or trigger one manually.
          </p>
          <table v-else class="w-full text-xs">
            <thead>
              <tr class="text-fg-muted border-b border-border">
                <th class="text-left py-1">Created</th>
                <th class="text-right py-1">Size</th>
                <th class="text-right py-1"></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="bk in storedBackups" :key="bk.id" class="border-b border-border/50">
                <td class="py-1.5 font-mono">{{ fmtDate(bk.created_at) }}</td>
                <td class="py-1.5 text-right text-fg-muted">{{ fmtBytes(bk.size_bytes) }}</td>
                <td class="py-1.5 text-right">
                  <button class="text-accent hover:underline" @click="downloadStoredBackup(bk.id)">
                    Download
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
  </div>

  <!-- ─── Restore confirmation modal ──────────────────────────────────────── -->
  <div v-if="restoreConfirm"
       class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
       @click.self="restoreConfirm = false">
    <div class="card max-w-lg w-full p-6 space-y-4">
      <h2 class="text-base font-semibold text-fg-strong">Restore configuration?</h2>
      <p class="text-sm text-fg">
        This will replace the current configuration with the contents of
        <span class="font-mono text-xs text-fg-strong">{{ selectedFile?.name }}</span>.
        <span class="text-fg-muted">Your admin credentials will not be changed.</span>
      </p>
      <p v-if="backupError" class="text-sm text-danger">{{ backupError }}</p>
      <div class="flex justify-end gap-2">
        <button class="btn-secondary" :disabled="restoring" @click="restoreConfirm = false">
          Cancel
        </button>
        <button class="btn-danger" :disabled="restoring" @click="doRestore">
          {{ restoring ? 'Restoring…' : 'Yes, restore' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import {
  ArchiveBoxIcon, ClipboardDocumentListIcon, DocumentTextIcon, GlobeAltIcon,
  ShieldCheckIcon,
} from '@heroicons/vue/24/outline'
import {
  getDNSCacheStats, getSettings, patchSettings, purgeDNSCache,
  getBlockPageConfig, patchBlockPageConfig,
  getBackupSettings, putBackupSettings, listBackups, triggerBackup,
} from '@/api/endpoints'
import { api, getToken } from '@/api/client'
import type { AuthSettings, BackupEntry, BackupSettings, BlockPageConfig, DNSCacheStats, DNSConfig, Settings, UpstreamRoute } from '@/api/types'

// ─── State ─────────────────────────────────────────────────────────────────

const settings = ref<Settings | null>(null)
const loading = ref(true)
const lastError = ref('')

// Per-section forms mirror the loaded Settings; dirty flags enable Save buttons.
interface RouteEntry {
  match: string
  resolversText: string
}

interface DnsForm {
  mode: 'forwarding' | 'recursive'
  upstreamsText: string
  trustedText: string
  routes: RouteEntry[]
  timeout: number
  cacheEnabled: boolean
  cacheMax: number
}

interface FilteringForm {
  policy: 'nxdomain' | 'null' | 'nodata' | 'redirect'
}

interface QueryLogForm {
  maxEntries: number
  retentionDays: number
}

const dnsForm = reactive<DnsForm>({
  mode: 'forwarding', upstreamsText: '', trustedText: '',
  routes: [],
  timeout: 5, cacheEnabled: true, cacheMax: 10000,
})
const filteringForm = reactive<FilteringForm>({ policy: 'nxdomain' })
const queryLogForm = reactive<QueryLogForm>({ maxEntries: 10000, retentionDays: 30 })

const dnsDirty = ref(false)
const filteringDirty = ref(false)
const queryLogDirty = ref(false)

const dnsSaving = ref(false)
const filteringSaving = ref(false)
const queryLogSaving = ref(false)

const dnsError = ref('')
const filteringError = ref('')
const queryLogError = ref('')

const dnsSavedAt = ref(0)
const filteringSavedAt = ref(0)
const queryLogSavedAt = ref(0)

// M34.5 — session timeout
interface SessionTimeoutForm { seconds: number }
const sessionTimeoutForm = reactive<SessionTimeoutForm>({ seconds: 28800 })
const sessionTimeoutDirty = ref(false)
const sessionTimeoutSaving = ref(false)
const sessionTimeoutError = ref('')
const sessionTimeoutSavedAt = ref(0)

// M26/M33 — block page config (M33 adds redirect_address_v6)
const blockPageForm = reactive<BlockPageConfig>({
  ip: '',
  port: 8053,
  title: '',
  message: '',
  contact_email: '',
  redirect_address_v6: '',
})
const blockPageDirty = ref(false)
const blockPageSaving = ref(false)
const blockPageError = ref('')
const blockPageSavedAt = ref(0)

// M4.7 — DNS cache controls
const cacheStats = ref<DNSCacheStats | null>(null)
const cachePurging = ref(false)
const cachePurgedAt = ref(0)
const cachePurgedCount = ref(0)

async function refreshCacheStats() {
  try {
    cacheStats.value = await getDNSCacheStats()
  } catch { /* leave previous snapshot in place */ }
}

async function onPurgeCache() {
  cachePurging.value = true
  try {
    const out = await purgeDNSCache()
    cachePurgedCount.value = out.purged
    cachePurgedAt.value = Date.now()
    await refreshCacheStats()
    window.setTimeout(() => { cachePurgedAt.value = 0 }, 4000)
  } catch (err) {
    dnsError.value = errMsg(err, 'Failed to purge DNS cache')
  } finally {
    cachePurging.value = false
  }
}

// ─── Loading ───────────────────────────────────────────────────────────────

onMounted(async () => {
  try {
    const [s, bp] = await Promise.all([getSettings(), getBlockPageConfig()])
    applySettings(s)
    applyBlockPage(bp)
    await refreshCacheStats()
    await loadBackupData()
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to load settings')
  } finally {
    loading.value = false
  }
})

function applySettings(s: Settings) {
  settings.value = s

  // DNS form
  dnsForm.mode = s.dns.mode
  dnsForm.upstreamsText = (s.dns.upstream_resolvers ?? []).join('\n')
  dnsForm.trustedText = (s.dns.trusted_subnets ?? []).join('\n')
  dnsForm.routes = (s.dns.upstream_routes ?? []).map(r => ({
    match: r.match,
    resolversText: r.resolvers.join('\n'),
  }))
  dnsForm.timeout = s.dns.upstream_timeout_seconds
  dnsForm.cacheEnabled = s.dns.cache.enabled
  dnsForm.cacheMax = s.dns.cache.max_entries
  dnsDirty.value = false

  // Filtering form
  filteringForm.policy = s.filtering.block_policy
  filteringDirty.value = false

  // Query log form
  queryLogForm.maxEntries = s.query_log.max_entries
  queryLogForm.retentionDays = s.query_log.aggregate_retention_days
  queryLogDirty.value = false

  // Session timeout form
  sessionTimeoutForm.seconds = s.auth?.session_timeout_seconds ?? 28800
  sessionTimeoutDirty.value = false
}

function applyBlockPage(bp: BlockPageConfig) {
  blockPageForm.ip = bp.ip ?? ''
  blockPageForm.port = bp.port ?? 8053
  blockPageForm.title = bp.title ?? ''
  blockPageForm.message = bp.message ?? ''
  blockPageForm.contact_email = bp.contact_email ?? ''
  blockPageForm.redirect_address_v6 = bp.redirect_address_v6 ?? ''
  blockPageDirty.value = false
}

// ─── Dirty tracking ────────────────────────────────────────────────────────

function markDnsDirty() { dnsDirty.value = true }
function addRoute() { dnsForm.routes.push({ match: '', resolversText: '' }); markDnsDirty() }
function removeRoute(idx: number) { dnsForm.routes.splice(idx, 1); markDnsDirty() }
function markFilteringDirty() { filteringDirty.value = true }
function markQueryLogDirty() { queryLogDirty.value = true }
function markBlockPageDirty() { blockPageDirty.value = true }
function markSessionTimeoutDirty() { sessionTimeoutDirty.value = true }

// ─── Saves ─────────────────────────────────────────────────────────────────

async function saveDns() {
  if (!settings.value) return
  dnsError.value = ''
  dnsSaving.value = true
  try {
    const current = settings.value.dns
    const dns: DNSConfig = {
      // listen.* is node-local (node.yaml) and not editable via the API,
      // but the PATCH contract still expects a full DNSConfig — preserve it.
      listen: { ...current.listen },
      mode: dnsForm.mode,
      upstream_timeout_seconds: dnsForm.timeout,
      cache: {
        enabled: dnsForm.cacheEnabled,
        max_entries: dnsForm.cacheMax,
      },
    }
    if (dnsForm.mode === 'forwarding') {
      dns.upstream_resolvers = parseLines(dnsForm.upstreamsText)
    } else {
      dns.trusted_subnets = parseLines(dnsForm.trustedText)
    }
    dns.upstream_routes = dnsForm.routes
      .filter(r => r.match.trim() !== '')
      .map(r => ({ match: r.match.trim(), resolvers: parseLines(r.resolversText) }))
    const updated = await patchSettings({ dns })
    applySettings(updated)
    flashSaved(dnsSavedAt)
  } catch (err) {
    dnsError.value = errMsg(err, 'Failed to save DNS settings')
  } finally {
    dnsSaving.value = false
  }
}

async function saveFiltering() {
  filteringError.value = ''
  filteringSaving.value = true
  try {
    const updated = await patchSettings({
      filtering: { block_policy: filteringForm.policy },
    })
    applySettings(updated)
    flashSaved(filteringSavedAt)
  } catch (err) {
    filteringError.value = errMsg(err, 'Failed to save filtering settings')
  } finally {
    filteringSaving.value = false
  }
}

async function saveQueryLog() {
  queryLogError.value = ''
  queryLogSaving.value = true
  try {
    const updated = await patchSettings({
      query_log: {
        max_entries: queryLogForm.maxEntries,
        aggregate_retention_days: queryLogForm.retentionDays,
      },
    })
    applySettings(updated)
    flashSaved(queryLogSavedAt)
  } catch (err) {
    queryLogError.value = errMsg(err, 'Failed to save query log settings')
  } finally {
    queryLogSaving.value = false
  }
}

async function saveSessionTimeout() {
  sessionTimeoutError.value = ''
  sessionTimeoutSaving.value = true
  try {
    const updated = await patchSettings({
      auth: { session_timeout_seconds: sessionTimeoutForm.seconds },
    })
    applySettings(updated)
    flashSaved(sessionTimeoutSavedAt)
  } catch (err) {
    sessionTimeoutError.value = errMsg(err, 'Failed to save session timeout')
  } finally {
    sessionTimeoutSaving.value = false
  }
}

async function saveBlockPage() {
  blockPageError.value = ''
  blockPageSaving.value = true
  try {
    const patch: BlockPageConfig = {}
    if (blockPageForm.ip) patch.ip = blockPageForm.ip
    if (blockPageForm.port) patch.port = blockPageForm.port
    if (blockPageForm.title !== undefined) patch.title = blockPageForm.title
    if (blockPageForm.message !== undefined) patch.message = blockPageForm.message
    if (blockPageForm.contact_email !== undefined) patch.contact_email = blockPageForm.contact_email
    if (blockPageForm.redirect_address_v6 !== undefined) patch.redirect_address_v6 = blockPageForm.redirect_address_v6
    const updated = await patchBlockPageConfig(patch)
    applyBlockPage(updated)
    flashSaved(blockPageSavedAt)
  } catch (err) {
    blockPageError.value = errMsg(err, 'Failed to save block page settings')
  } finally {
    blockPageSaving.value = false
  }
}

// ─── Configuration backup (M31: encrypted export, scheduled backups) ──────

// Export
const exporting = ref(false)
const exportError = ref('')
const exportPassphrase = ref('')

async function downloadBackup() {
  exporting.value = true
  exportError.value = ''
  try {
    let blob: Blob
    let filename: string
    if (exportPassphrase.value) {
      const token = getToken()
      const headers: Record<string, string> = { 'Content-Type': 'application/json' }
      if (token) headers.Authorization = `Bearer ${token}`
      const resp = await fetch('/api/v1/config/export', {
        method: 'POST',
        headers,
        body: JSON.stringify({ passphrase: exportPassphrase.value }),
      })
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      blob = await resp.blob()
      filename = 'skoed-config.age'
    } else {
      const res = await api.get('/api/v1/config/export', { responseType: 'blob' })
      blob = new Blob([res.data as BlobPart], { type: 'application/gzip' })
      filename = 'skoed-config.tar.gz'
    }
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.style.cssText = 'position:fixed;top:-100px;left:-100px'
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    setTimeout(() => { URL.revokeObjectURL(url); a.remove() }, 1000)
  } catch (err) {
    exportError.value = errMsg(err, 'Export failed — check your session and try again.')
  } finally {
    exporting.value = false
  }
}

// Scheduled backup
const backupSchedule = ref<BackupSettings>({ enabled: false, interval_hours: 24, retain_count: 7 })
const backupScheduleSaving = ref(false)
const backupScheduleError = ref('')
const backupScheduleSavedAt = ref(0)
const storedBackups = ref<BackupEntry[]>([])
const triggeringBackup = ref(false)
const triggerResult = ref<string>('')

async function loadBackupData() {
  try {
    const [sched, backups] = await Promise.all([getBackupSettings(), listBackups()])
    backupSchedule.value = sched
    storedBackups.value = backups
  } catch {
    // backup settings not available — keep defaults
  }
}

async function saveBackupSchedule() {
  backupScheduleSaving.value = true
  backupScheduleError.value = ''
  try {
    const updated = await putBackupSettings(backupSchedule.value)
    backupSchedule.value = updated
    flashSaved(backupScheduleSavedAt)
  } catch (err) {
    backupScheduleError.value = errMsg(err, 'Failed to save backup schedule')
  } finally {
    backupScheduleSaving.value = false
  }
}

async function doTriggerBackup() {
  triggeringBackup.value = true
  triggerResult.value = ''
  try {
    const r = await triggerBackup()
    triggerResult.value = r.created ? 'Backup created.' : 'No changes — backup skipped (dedup).'
    storedBackups.value = await listBackups()
  } catch (err) {
    triggerResult.value = errMsg(err, 'Trigger failed')
  } finally {
    triggeringBackup.value = false
  }
}

async function downloadStoredBackup(id: string) {
  const token = getToken()
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = `Bearer ${token}`
  const resp = await fetch(`/api/v1/config/backups/${encodeURIComponent(id)}/download`, { headers })
  if (!resp.ok) return
  const blob = await resp.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `skoed-backup-${id}.tar.gz`
  a.style.cssText = 'position:fixed;top:-100px;left:-100px'
  document.body.appendChild(a)
  a.click()
  setTimeout(() => { URL.revokeObjectURL(url); a.remove() }, 1000)
}

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}

function fmtDate(iso: string): string {
  return new Date(iso).toLocaleString()
}

const selectedFile = ref<File | null>(null)
const fileInputEl = ref<HTMLInputElement | null>(null)
const restoring = ref(false)
const restoreConfirm = ref(false)
const backupError = ref('')
const backupSuccess = ref('')
const importPassphrase = ref('')

function onBackupFileSelected(e: Event) {
  const input = e.target as HTMLInputElement
  selectedFile.value = input.files?.[0] ?? null
  backupError.value = ''
  backupSuccess.value = ''
}

async function doRestore() {
  if (!selectedFile.value) return
  backupError.value = ''
  backupSuccess.value = ''
  restoring.value = true
  restoreConfirm.value = false
  try {
    const token = getToken()
    const headers: Record<string, string> = {}
    if (token) headers.Authorization = `Bearer ${token}`
    const form = new FormData()
    form.append('archive', selectedFile.value, selectedFile.value.name)
    if (importPassphrase.value) form.append('passphrase', importPassphrase.value)
    const resp = await fetch('/api/v1/config/import', { method: 'POST', headers, body: form })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}))
      throw new Error(body.error || `HTTP ${resp.status}`)
    }
    selectedFile.value = null
    if (fileInputEl.value) fileInputEl.value.value = ''
    const [s, bp] = await Promise.all([getSettings(), getBlockPageConfig()])
    applySettings(s)
    applyBlockPage(bp)
    backupSuccess.value = 'Configuration restored successfully.'
  } catch (err) {
    backupError.value = errMsg(err, 'Restore failed')
  } finally {
    restoring.value = false
  }
}

// ─── Helpers ───────────────────────────────────────────────────────────────

function parseLines(text: string): string[] {
  return text.split(/\r?\n/).map(l => l.trim()).filter(Boolean)
}

function flashSaved(slot: { value: number }) {
  const token = Date.now()
  slot.value = token
  window.setTimeout(() => {
    if (slot.value === token) slot.value = 0
  }, 2000)
}

function errMsg(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: { error?: string } }, message?: string }
  return e?.response?.data?.error || e?.message || fallback
}
</script>
