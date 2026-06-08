<!--
  Inner body for FirewallRulesModal — extracted so the Stats.vue inline
  callout can reuse the exact same tabset/preview/copy chrome WITHOUT a
  modal wrapper (TS-FwRuleUi: "callout reuses the modal body in inline
  form — same tabset, same preview pane, same copy button").
-->
<template>
  <div class="space-y-3">
    <!-- Stale-snapshot banner (FS-FwRuleUiStaleSnapshotBanner). -->
    <aside v-if="stale"
           role="status"
           class="rounded border border-warning bg-warning-subtle px-3 py-2 text-xs text-warning flex items-start gap-2"
           data-testid="fw-rules-stale-banner">
      <ExclamationTriangleIcon class="h-4 w-4 mt-0.5 shrink-0" />
      <span>
        Resolver snapshot last refreshed
        <span class="font-mono">{{ fetchedAt || 'unknown' }}</span>
        — rules may miss new resolvers.
      </span>
    </aside>

    <!-- Platform tabset (ARIA tablist, automatic activation). -->
    <div role="tablist"
         aria-label="Firewall platform"
         class="flex flex-wrap items-center gap-1 border-b border-border"
         data-testid="fw-rules-tablist">
      <button v-for="(p, i) in platforms"
              :key="p"
              :id="tabId(p)"
              ref="tabRefs"
              role="tab"
              type="button"
              :aria-selected="p === activePlatform"
              :aria-controls="panelId"
              :tabindex="p === activePlatform ? 0 : -1"
              :data-testid="`fw-rules-tab-${p}`"
              class="px-3 py-1.5 text-xs font-medium border-b-2 -mb-px transition-colors"
              :class="p === activePlatform
                ? 'border-accent text-accent'
                : 'border-transparent text-fg-muted hover:text-fg-strong'"
              @click="activate(p)"
              @focus="activate(p)"
              @keydown="(e) => onKey(e, i)">
        {{ p }}
      </button>

      <div class="ml-auto flex items-center gap-2">
        <span v-if="copied"
              class="text-xs text-success font-medium"
              data-testid="fw-rules-copied">
          Copied
        </span>
        <button class="btn-primary"
                type="button"
                :disabled="copyDisabled"
                data-testid="fw-rules-copy-btn"
                @click="emit('copy')">
          <ClipboardIcon class="h-4 w-4" />
          <span>Copy</span>
        </button>
      </div>
    </div>

    <!-- Preview / states. -->
    <div :id="panelId"
         role="tabpanel"
         :aria-labelledby="tabId(activePlatform)"
         class="min-h-[12rem]">
      <p v-if="loading"
         class="text-sm text-fg-muted text-center py-8">
        Generating {{ activePlatform }} rules…
      </p>

      <div v-else-if="fetchError"
           class="rounded border border-danger bg-danger-subtle px-3 py-2 text-xs text-danger flex items-center justify-between gap-3">
        <span>{{ fetchError }}</span>
        <button class="btn-ghost text-xs"
                type="button"
                @click="emit('retry')">
          Retry
        </button>
      </div>

      <div v-else-if="isEmpty"
           class="rounded border border-border bg-bg-canvas px-4 py-6 text-center space-y-2"
           data-testid="fw-rules-empty">
        <p class="text-sm text-fg-strong">
          No resolver snapshot yet.
        </p>
        <p class="text-xs text-fg-muted">
          The DoH/DoT resolver database has never been refreshed on this
          cluster. Once an admin runs a refresh, this preview will fill in.
        </p>
      </div>

      <pre v-else
           class="rounded border border-border bg-bg-input px-3 py-2 text-xs font-mono text-fg overflow-auto max-h-[28rem] whitespace-pre"
           data-testid="fw-rules-preview">{{ preview }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { ClipboardIcon, ExclamationTriangleIcon } from '@heroicons/vue/24/outline'
import type { FwRulePlatform, FwRuleScope } from '@/api/endpoints'

const props = defineProps<{
  scope: FwRuleScope
  activePlatform: FwRulePlatform
  platforms: readonly FwRulePlatform[]
  preview: string
  loading: boolean
  fetchError: string | null
  stale: boolean
  fetchedAt: string | null
  isEmpty: boolean
  copied: boolean
}>()

const emit = defineEmits<{
  (e: 'activate', p: FwRulePlatform): void
  (e: 'copy'): void
  (e: 'retry'): void
}>()

const tabRefs = ref<HTMLButtonElement[]>([])
const panelId = computed(() => `fw-rules-panel-${props.activePlatform}`)
function tabId(p: FwRulePlatform): string { return `fw-rules-tab-${p}` }

const copyDisabled = computed(() =>
  props.loading || !!props.fetchError || props.isEmpty || !props.preview,
)

function activate(p: FwRulePlatform) {
  emit('activate', p)
}

// FS-FwRuleUiKeyboardNavigablePlatformTabs:
//   ArrowRight / ArrowLeft → wrap-around horizontal nav
//   Home / End             → first / last
//   Space / Enter          → activate (focus already activates)
function onKey(e: KeyboardEvent, idx: number) {
  const n = props.platforms.length
  let next: number | null = null
  switch (e.key) {
    case 'ArrowRight': next = (idx + 1) % n; break
    case 'ArrowLeft':  next = (idx - 1 + n) % n; break
    case 'Home':       next = 0; break
    case 'End':        next = n - 1; break
    case 'Enter':
    case ' ':
      e.preventDefault()
      activate(props.platforms[idx])
      return
    default: return
  }
  e.preventDefault()
  emit('activate', props.platforms[next])
  // Wait for the v-for + tabindex re-render so the freshly-activated tab
  // is actually focusable before we hand it focus.
  nextTick(() => {
    const el = tabRefs.value[next!]
    el?.focus()
  })
}

// Keep DOM focus in sync when the parent flips activePlatform externally
// (e.g. initialPlatform prop arriving after first paint).
watch(() => props.activePlatform, (p) => {
  const idx = props.platforms.indexOf(p)
  if (idx < 0) return
  const el = tabRefs.value[idx]
  // Only refocus if focus is already inside the tablist — don't hijack
  // focus when the user is clicking inside the <pre>.
  if (el && document.activeElement?.getAttribute('role') === 'tab') {
    el.focus()
  }
})
</script>
