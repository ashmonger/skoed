<!--
  TS-FwRuleUi — Shared "Copy DoH-gap rules" surface used by:
    - Clients.vue per-row overflow action     (modal)
    - Profiles.vue per-row "Copy DoH-gap…"    (modal)
    - Stats.vue "Closing the DoH gap" callout (inline, via :inline prop)

  Owns the platform tabset (ARIA tablist + keyboard model — ArrowLeft/Right,
  Home/End), the preview <pre> block, the copy-to-clipboard button, the
  stale-snapshot banner parsed out of the generator response header, and
  the empty-state when no resolver snapshot has ever been fetched.

  Delegates every API call to /api/v1/firewall-rules (TS-FwRuleGen) — no
  new routes are introduced by this spec.
-->
<template>
  <div v-if="inline">
    <FirewallRulesBody
      :scope="scope"
      :active-platform="activePlatform"
      :platforms="platforms"
      :preview="preview"
      :loading="loading"
      :fetch-error="fetchError"
      :stale="stale"
      :fetched-at="fetchedAt"
      :is-empty="isEmpty"
      :copied="copied"
      @activate="onActivate"
      @copy="copy"
      @retry="fetchPreview" />
  </div>
  <div v-else
       class="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4"
       role="dialog"
       aria-modal="true"
       :aria-label="modalLabel"
       data-testid="fw-rules-modal"
       @click.self="onClose">
    <div class="card w-full max-w-3xl max-h-[90vh] flex flex-col overflow-hidden">
      <header class="flex items-center justify-between px-5 py-3 border-b border-border">
        <div>
          <h2 class="text-base font-semibold text-fg-strong">Copy DoH-gap rules</h2>
          <p class="text-xs text-fg-muted">{{ scopeLabel }}</p>
        </div>
        <button class="btn-ghost p-1.5"
                aria-label="Close"
                @click="onClose">
          <XMarkIcon class="h-5 w-5" />
        </button>
      </header>

      <div class="flex-1 overflow-y-auto p-5">
        <FirewallRulesBody
          :scope="scope"
          :active-platform="activePlatform"
          :platforms="platforms"
          :preview="preview"
          :loading="loading"
          :fetch-error="fetchError"
          :stale="stale"
          :fetched-at="fetchedAt"
          :is-empty="isEmpty"
          :copied="copied"
          @activate="onActivate"
          @copy="copy"
          @retry="fetchPreview" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { XMarkIcon } from '@heroicons/vue/24/outline'
import FirewallRulesBody from './FirewallRulesBody.vue'
import {
  FW_RULE_PLATFORMS, getFirewallRules,
  type FwRuleAction, type FwRulePlatform, type FwRuleScope,
} from '@/api/endpoints'

const props = withDefaults(defineProps<{
  scope: FwRuleScope
  initialPlatform?: FwRulePlatform
  action?: FwRuleAction
  inline?: boolean
}>(), {
  initialPlatform: 'iptables',
  action: 'drop',
  inline: false,
})

const emit = defineEmits<{
  (e: 'close'): void
}>()

// ─── Reactive state ──────────────────────────────────────────────────────

const platforms = FW_RULE_PLATFORMS
const activePlatform = ref<FwRulePlatform>(props.initialPlatform)
const preview = ref<string>('')
const loading = ref(false)
const fetchError = ref<string | null>(null)
const copied = ref(false)
const snapshotFetchedAt = ref<string | null>(null)
const snapshotStale = ref(false)

// Per-(scope, platform, action) cache so tab-switching is free on revisit.
// (TS-FwRuleUi: "automatic activation … caches by (scope, platform, action)").
const cache = new Map<string, string>()

const stale = computed(() => snapshotStale.value)
const fetchedAt = computed(() => snapshotFetchedAt.value)
const isEmpty = computed(() => {
  if (loading.value || fetchError.value) return false
  // Strip the leading "#"-comment header before deciding emptiness so a
  // header-only body (no rules) counts as empty.
  const noHeader = preview.value
    .split('\n')
    .filter(l => !/^\s*(#|\/\/|;)/.test(l) && l.trim() !== '')
    .join('\n')
    .trim()
  return noHeader === ''
})

const scopeLabel = computed(() => {
  switch (props.scope.kind) {
    case 'client':  return `Client ${props.scope.ip}`
    case 'subnet':  return `Subnet ${props.scope.cidr}`
    case 'profile': return `Profile ${props.scope.profileId}`
    case 'all':     return 'All clients'
  }
})
const modalLabel = computed(() => `Copy DoH-gap rules — ${scopeLabel.value}`)

// ─── Fetch ───────────────────────────────────────────────────────────────

function cacheKey(p: FwRulePlatform): string {
  const s = props.scope
  const scopeKey =
    s.kind === 'client'  ? `client:${s.ip}` :
    s.kind === 'subnet'  ? `subnet:${s.cidr}` :
    s.kind === 'profile' ? `profile:${s.profileId}` : 'all'
  return `${scopeKey}|${p}|${props.action ?? 'drop'}`
}

async function fetchPreview() {
  const key = cacheKey(activePlatform.value)
  if (cache.has(key)) {
    preview.value = cache.get(key) ?? ''
    parseHeader(preview.value)
    fetchError.value = null
    return
  }
  loading.value = true
  fetchError.value = null
  try {
    const body = await getFirewallRules(
      props.scope, activePlatform.value, props.action ?? 'drop',
    )
    preview.value = body
    cache.set(key, body)
    parseHeader(body)
  } catch (err: unknown) {
    const e = err as { response?: { status?: number; data?: { error?: string } | string }; message?: string }
    const status = e?.response?.status
    if (status === 400) {
      const d = e?.response?.data
      const msg = typeof d === 'string' ? d : d?.error
      fetchError.value = `Invalid request: ${msg ?? 'bad parameters.'}`
    } else if (status === 404) {
      fetchError.value = 'Profile not found.'
    } else if (status === 503) {
      // 503 → resolver snapshot unavailable. Render as empty-state.
      preview.value = ''
      cache.set(key, '')
      snapshotFetchedAt.value = null
      snapshotStale.value = false
    } else if (status && status >= 500) {
      fetchError.value = 'Generator unavailable. Retry?'
    } else {
      fetchError.value = e?.message ?? 'Could not fetch firewall rules.'
    }
  } finally {
    loading.value = false
  }
}

// Pull `snapshot_fetched` + the "snapshot is stale" warning out of the
// first ~10 lines of the body (TS-FwRuleUi). The renderer emits these
// as part of the leading comment block — we don't make a second call to
// /api/v1/doh-resolvers just to learn the timestamp.
function parseHeader(body: string) {
  const lines = body.split('\n').slice(0, 12)
  let fetched: string | null = null
  let isStale = false
  for (const raw of lines) {
    const line = raw.trim()
    const low = line.toLowerCase()
    if (low.includes('snapshot is stale') || low.includes('stale=true')) {
      isStale = true
    }
    // Match "snapshot_fetched: 2026-06-08T…" or "snapshot_fetched=…",
    // with any leading comment marker (# // ;).
    const m = line.match(/snapshot[_-]?fetched\s*[:=]\s*(\S+)/i)
    if (m && m[1]) {
      fetched = m[1].replace(/[,;]$/, '')
    }
  }
  snapshotFetchedAt.value = fetched
  snapshotStale.value = isStale
}

// ─── Tab activation ──────────────────────────────────────────────────────

function onActivate(p: FwRulePlatform) {
  if (p === activePlatform.value) return
  activePlatform.value = p
}

watch(activePlatform, () => {
  copied.value = false
  fetchPreview()
})

// ─── Copy-to-clipboard ───────────────────────────────────────────────────

async function copy() {
  if (!preview.value || isEmpty.value) return
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(preview.value)
    } else {
      // HTTP-origin / ancient browser fallback: select the <pre> text
      // and let the operator press Ctrl+C. We don't shim execCommand.
      const pre = document.querySelector<HTMLElement>('[data-testid="fw-rules-preview"]')
      if (pre) {
        const sel = window.getSelection()
        const range = document.createRange()
        range.selectNodeContents(pre)
        sel?.removeAllRanges()
        sel?.addRange(range)
      }
    }
    copied.value = true
    setTimeout(() => { copied.value = false }, 1500)
  } catch {
    fetchError.value = 'Clipboard unavailable in this browser.'
  }
}

// ─── Close handling (Esc) ────────────────────────────────────────────────

function onClose() {
  emit('close')
}

function onKey(e: KeyboardEvent) {
  if (props.inline) return
  if (e.key === 'Escape') onClose()
}

onMounted(() => {
  if (!props.inline) window.addEventListener('keydown', onKey)
  fetchPreview()
})
onBeforeUnmount(() => {
  if (!props.inline) window.removeEventListener('keydown', onKey)
})

// When the parent swaps `scope` (e.g. Stats subnet picker), refetch.
watch(() => props.scope, () => {
  cache.clear()
  snapshotFetchedAt.value = null
  snapshotStale.value = false
  fetchPreview()
}, { deep: true })
</script>
