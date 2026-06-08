<template>
  <div class="min-h-screen flex flex-col bg-bg-canvas">
    <!-- Top bar: logo + name on the left, login button on the right. -->
    <header class="flex items-center justify-between px-6 py-4 border-b border-border">
      <div class="flex items-center gap-2">
        <img src="/favicon.svg" class="w-7 h-7" alt="" />
        <span class="text-lg font-semibold text-fg-strong">skoed</span>
        <span class="text-xs text-fg-muted ml-2 hidden sm:inline">
          self-hosted DNS filtering
        </span>
      </div>
      <router-link to="/login" class="btn-primary" data-testid="login-cta">
        Login
      </router-link>
    </header>

    <!-- Hero. -->
    <section class="flex-1 flex flex-col items-center justify-start px-6 py-12 gap-10">
      <div class="text-center max-w-2xl">
        <h1 class="text-3xl sm:text-4xl font-semibold text-fg-strong">
          Sanity-check any blocklist
          <span class="text-accent">before</span> you install skoed.
        </h1>
        <p class="mt-3 text-fg-muted text-sm sm:text-base">
          skoed is self-hosted DNS filtering with multi-node sync, profiles,
          schedules, and DoH/DoT.
          Paste a blocklist URL below to see what it would do.
        </p>
      </div>

      <!-- URL tester card. -->
      <div class="card w-full max-w-2xl p-6 space-y-4" data-testid="url-tester-card">
        <div>
          <label class="label" for="url-input">Blocklist URL</label>
          <input
            id="url-input"
            v-model="url"
            class="input font-mono text-xs"
            placeholder="https://example.com/hosts.txt"
            spellcheck="false"
            autocomplete="off"
            @keyup.enter="testUrl"
            data-testid="url-input"
          />
        </div>
        <div class="flex items-end gap-3">
          <div class="flex-1">
            <label class="label" for="fmt">Format</label>
            <select id="fmt" v-model="format" class="input" data-testid="format-select">
              <option value="auto">auto (detect)</option>
              <option value="hosts">hosts</option>
              <option value="domainlist">domainlist</option>
              <option value="askoed">askoed</option>
            </select>
          </div>
          <button
            class="btn-primary px-5"
            :disabled="loading || !url"
            @click="testUrl"
            data-testid="test-btn"
          >
            {{ loading ? 'Testing…' : 'Test' }}
          </button>
        </div>

        <!-- Result. -->
        <div v-if="result" class="rounded border border-border p-4 text-sm space-y-1.5"
             :class="result.ok ? 'bg-success-subtle' : 'bg-danger-subtle'"
             data-testid="url-tester-result">
          <div v-if="result.ok" class="flex items-center gap-2 text-success font-medium">
            <CheckCircleIcon class="w-5 h-5" />
            <span>Parsed successfully</span>
          </div>
          <div v-else class="flex items-center gap-2 text-danger font-medium">
            <ExclamationTriangleIcon class="w-5 h-5" />
            <span>Could not test this URL</span>
          </div>

          <dl v-if="result.ok" class="grid grid-cols-[8rem_1fr] gap-y-1 text-xs mt-2">
            <dt class="text-fg-muted">domains</dt>
            <dd class="font-mono text-fg-strong">{{ (result.count ?? 0).toLocaleString() }}</dd>
            <dt class="text-fg-muted">format</dt>
            <dd class="font-mono">{{ result.format }}</dd>
            <dt class="text-fg-muted">elapsed</dt>
            <dd class="font-mono">{{ result.elapsed_ms }} ms</dd>
          </dl>
          <p v-else class="text-xs text-danger font-mono mt-1">{{ result.error }}</p>
        </div>
      </div>

      <!-- Domain tester card (M5.9.7). -->
      <div class="card w-full max-w-2xl p-6 space-y-4" data-testid="domain-tester-card">
        <div>
          <label class="label" for="domain-input">
            Would this domain be blocked?
          </label>
          <input
            id="domain-input"
            v-model="domain"
            class="input font-mono text-xs"
            placeholder="doubleclick.net"
            spellcheck="false"
            autocomplete="off"
            @keyup.enter="testDomain"
            data-testid="domain-input"
          />
          <p class="text-xs text-fg-muted mt-1">
            Verdict for the default profile. Log in for the full chain
            (matched profile, blocklist, schedule).
          </p>
        </div>
        <div class="flex justify-end">
          <button
            class="btn-primary px-5"
            :disabled="domainLoading || !domain"
            @click="testDomain"
            data-testid="domain-test-btn"
          >
            {{ domainLoading ? 'Testing…' : 'Test' }}
          </button>
        </div>

        <div v-if="domainResult" class="rounded border border-border p-4 text-sm space-y-1.5"
             :class="domainVerdictClass"
             data-testid="domain-tester-result">
          <div v-if="domainResult.ok && domainResult.would_block === true"
               class="flex items-center gap-2 text-danger font-medium">
            <ShieldExclamationIcon class="w-5 h-5" />
            <span>Blocked</span>
          </div>
          <div v-else-if="domainResult.ok && domainResult.would_block === false"
               class="flex items-center gap-2 text-success font-medium">
            <CheckCircleIcon class="w-5 h-5" />
            <span>Allowed</span>
          </div>
          <div v-else class="flex items-center gap-2 text-danger font-medium">
            <ExclamationTriangleIcon class="w-5 h-5" />
            <span>Could not test this domain</span>
          </div>

          <dl v-if="domainResult.ok" class="grid grid-cols-[8rem_1fr] gap-y-1 text-xs mt-2">
            <dt class="text-fg-muted">reason</dt>
            <dd class="font-mono">{{ domainResult.reason }}</dd>
          </dl>
          <p v-else class="text-xs text-danger font-mono mt-1">{{ domainResult.error }}</p>
        </div>
      </div>

      <!-- Tagline strip. -->
      <div class="grid sm:grid-cols-3 gap-4 max-w-3xl w-full text-sm">
        <div class="card p-4">
          <div class="text-accent font-semibold mb-1">DNS filtering</div>
          <div class="text-fg-muted text-xs">
            Plain DNS, DoH, and DoT — block at the network edge.
          </div>
        </div>
        <div class="card p-4">
          <div class="text-accent font-semibold mb-1">Multi-node sync</div>
          <div class="text-fg-muted text-xs">
            Raft-replicated config across every skoed node in the cluster.
          </div>
        </div>
        <div class="card p-4">
          <div class="text-accent font-semibold mb-1">Profiles &amp; schedules</div>
          <div class="text-fg-muted text-xs">
            Per-client policy with time-of-day windows. Self-hosted, your data stays home.
          </div>
        </div>
      </div>
    </section>

    <!-- Footer. -->
    <footer class="px-6 py-4 border-t border-border flex flex-wrap items-center justify-between text-xs text-fg-muted">
      <span>skoed &mdash; self-hosted DNS filtering &middot; v1</span>
      <span class="flex gap-4">
        <router-link to="/about" class="hover:text-accent">about</router-link>
        <a href="https://docs.skoed.io" class="hover:text-accent" target="_blank" rel="noopener">docs</a>
        <a href="https://github.com/skoed/skoed" class="hover:text-accent" target="_blank" rel="noopener">github</a>
      </span>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  CheckCircleIcon,
  ExclamationTriangleIcon,
  ShieldExclamationIcon,
} from '@heroicons/vue/24/outline'

type TestResult = {
  ok: boolean
  count?: number
  format?: string
  elapsed_ms?: number
  error?: string
}

const url = ref('')
const format = ref<'auto' | 'hosts' | 'domainlist' | 'askoed'>('auto')
const loading = ref(false)
const result = ref<TestResult | null>(null)

// M5.9.7 — domain tester (guest endpoint).
type DomainResult = {
  ok: boolean
  would_block?: boolean
  reason?: string
  error?: string
}
const domain = ref('')
const domainLoading = ref(false)
const domainResult = ref<DomainResult | null>(null)
const domainVerdictClass = computed(() => {
  if (!domainResult.value?.ok) return 'bg-danger-subtle'
  return domainResult.value.would_block ? 'bg-danger-subtle' : 'bg-success-subtle'
})

async function testDomain() {
  if (!domain.value) return
  domainResult.value = null
  domainLoading.value = true
  try {
    const resp = await fetch('/api/v1/_public/test-domain', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ domain: domain.value.trim() }),
    })
    const body = await resp.json().catch(() => ({}))
    if (!resp.ok) {
      domainResult.value = {
        ok: false,
        error:
          body?.error ||
          (resp.status === 429
            ? 'Rate limited — try again in a minute.'
            : resp.status === 404
            ? 'The public tester is disabled on this node.'
            : `HTTP ${resp.status}`),
      }
      return
    }
    domainResult.value = { ok: true, ...body }
  } catch (err: any) {
    domainResult.value = { ok: false, error: err?.message || 'Network error.' }
  } finally {
    domainLoading.value = false
  }
}

async function testUrl() {
  if (!url.value) return
  result.value = null
  loading.value = true
  try {
    const resp = await fetch('/api/v1/_public/test-blocklist', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: url.value, format: format.value }),
    })
    const body = await resp.json().catch(() => ({}))
    if (!resp.ok) {
      result.value = {
        ok: false,
        error:
          body?.error ||
          (resp.status === 429
            ? 'Rate limited — try again in a minute.'
            : resp.status === 403
            ? 'This URL was refused (private/loopback/link-local address).'
            : resp.status === 404
            ? 'The public tester is disabled on this node.'
            : `HTTP ${resp.status}`),
      }
      return
    }
    result.value = body as TestResult
  } catch (err: any) {
    result.value = { ok: false, error: err?.message || 'Network error.' }
  } finally {
    loading.value = false
  }
}
</script>
