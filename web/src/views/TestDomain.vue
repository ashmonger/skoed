<template>
  <div class="space-y-4">
    <div>
      <h1 class="text-lg font-semibold text-fg-strong">Test a domain</h1>
      <p class="text-sm text-fg-muted">
        Ask the cluster how it would answer a DNS query <em>right now</em>,
        without actually issuing one. Uses the same evaluator the DNS
        handler does — verdicts here are byte-identical to real queries.
      </p>
    </div>

    <div class="card p-4 space-y-3">
      <form class="grid sm:grid-cols-[1fr_12rem_12rem_auto] gap-3 items-end"
            @submit.prevent="submit">
        <div>
          <label class="label" for="td-domain">Domain</label>
          <input id="td-domain"
                 v-model="domain"
                 class="input font-mono text-sm"
                 placeholder="doubleclick.net"
                 spellcheck="false"
                 autocomplete="off"
                 data-testid="test-domain-input" />
        </div>
        <div>
          <label class="label" for="td-client-ip">Client IP <span class="text-fg-muted">(optional)</span></label>
          <input id="td-client-ip"
                 v-model="clientIP"
                 class="input font-mono text-sm"
                 placeholder="192.0.2.10"
                 spellcheck="false"
                 autocomplete="off" />
        </div>
        <div>
          <label class="label" for="td-profile">Profile override <span class="text-fg-muted">(optional)</span></label>
          <select id="td-profile" v-model="profileID" class="input text-sm">
            <option value="">— auto —</option>
            <option v-for="p in profiles" :key="p.id" :value="p.id">
              {{ p.id }}{{ p.name ? ` — ${p.name}` : '' }}
            </option>
          </select>
        </div>
        <button class="btn-primary"
                :disabled="loading || !domain"
                data-testid="test-domain-submit">
          {{ loading ? 'Testing…' : 'Test' }}
        </button>
      </form>
      <p v-if="error" class="text-xs text-danger font-mono">{{ error }}</p>
    </div>

    <div v-if="result"
         class="card p-4 space-y-4"
         data-testid="test-domain-result">
      <!-- Headline verdict -->
      <div class="flex items-center gap-3">
        <span v-if="result.would_block"
              class="inline-flex items-center gap-1.5 px-3 py-1 rounded
                     bg-danger-subtle text-danger font-semibold">
          <ShieldExclamationIcon class="w-4 h-4" />
          Blocked
        </span>
        <span v-else
              class="inline-flex items-center gap-1.5 px-3 py-1 rounded
                     bg-success-subtle text-success font-semibold">
          <CheckCircleIcon class="w-4 h-4" />
          Allowed
        </span>
        <span class="font-mono text-fg-strong">{{ result.domain }}</span>
        <span class="text-xs text-fg-muted ml-auto">
          evaluated {{ result.evaluated_at }}
        </span>
      </div>

      <!-- Step-by-step chain -->
      <ol class="space-y-1.5 text-sm">
        <li v-for="(step, idx) in chain" :key="idx"
            class="flex items-start gap-2">
          <span class="w-5 mt-0.5">
            <CheckIcon v-if="step.status === 'matched'" class="w-4 h-4 text-success" />
            <XMarkIcon v-else-if="step.status === 'skipped'" class="w-4 h-4 text-fg-subtle" />
            <MinusIcon v-else class="w-4 h-4 text-fg-subtle" />
          </span>
          <span class="font-medium w-44 shrink-0">{{ step.label }}</span>
          <span class="text-fg-muted">{{ step.detail }}</span>
        </li>
      </ol>

      <!-- Attribution table -->
      <dl class="grid grid-cols-[10rem_1fr] gap-y-1.5 text-sm border-t border-border pt-3">
        <dt class="text-fg-muted">reason</dt>
        <dd class="font-mono">
          <span class="px-2 py-0.5 rounded text-xs" :class="reasonChipClass">{{ result.reason }}</span>
        </dd>
        <template v-if="result.matched_profile_id">
          <dt class="text-fg-muted">matched profile</dt>
          <dd class="font-mono">{{ result.matched_profile_id }}</dd>
        </template>
        <template v-if="result.matched_blocklist_id">
          <dt class="text-fg-muted">matched blocklist</dt>
          <dd class="font-mono">{{ result.matched_blocklist_id }}</dd>
        </template>
        <template v-if="result.block_policy">
          <dt class="text-fg-muted">block policy</dt>
          <dd class="font-mono">{{ result.block_policy }}</dd>
        </template>
        <template v-if="result.local_dns_answer">
          <dt class="text-fg-muted">local-DNS answer</dt>
          <dd class="font-mono">{{ result.local_dns_answer }}</dd>
        </template>
        <template v-if="result.safesearch_rewrite">
          <dt class="text-fg-muted">SafeSearch rewrite</dt>
          <dd class="font-mono">{{ result.safesearch_rewrite }}</dd>
        </template>
        <template v-if="result.client_ip">
          <dt class="text-fg-muted">client IP</dt>
          <dd class="font-mono">{{ result.client_ip }}</dd>
        </template>
      </dl>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  CheckCircleIcon, ShieldExclamationIcon, CheckIcon, XMarkIcon, MinusIcon,
} from '@heroicons/vue/24/outline'
import type { Profile } from '@/api/types'
import { listProfiles, testDomain, type TestDomainResponse } from '@/api/endpoints'

const domain = ref('')
const clientIP = ref('')
const profileID = ref('')
const profiles = ref<Profile[]>([])
const result = ref<TestDomainResponse | null>(null)
const loading = ref(false)
const error = ref('')

onMounted(async () => {
  try {
    profiles.value = await listProfiles()
  } catch {
    /* non-fatal — operator can still type a profile id manually */
  }
})

async function submit() {
  if (!domain.value) return
  error.value = ''
  result.value = null
  loading.value = true
  try {
    const body: Record<string, string> = { domain: domain.value.trim() }
    if (clientIP.value.trim()) body.client_ip = clientIP.value.trim()
    if (profileID.value) body.profile_id = profileID.value
    result.value = await testDomain(body as { domain: string })
  } catch (err: any) {
    error.value = err?.response?.data?.error || err?.message || 'request failed'
  } finally {
    loading.value = false
  }
}

type Step = {
  label: string
  status: 'matched' | 'skipped' | 'n/a'
  detail: string
}

const chain = computed<Step[]>(() => {
  const r = result.value
  if (!r) return []
  const reason = r.reason
  const step = (label: string, when: string, detail: string): Step => {
    if (reason === when) return { label, status: 'matched', detail }
    return { label, status: 'skipped', detail: 'no match' }
  }
  return [
    step('Local DNS', 'local-dns', r.local_dns_answer
      ? `→ ${r.local_dns_answer}`
      : 'matched'),
    step('Allowlist', 'allowlist', 'domain is on the allowlist'),
    step('Blocklist', 'blocklist',
      r.matched_blocklist_id
        ? `${r.matched_blocklist_id} (policy=${r.block_policy ?? 'nxdomain'})`
        : 'matched'),
    step('SafeSearch', 'safesearch', r.safesearch_rewrite
      ? `rewrite → ${r.safesearch_rewrite}`
      : 'rewrite'),
    {
      label: 'Forwarded',
      status: reason === 'forwarded' ? 'matched' : 'n/a',
      detail: reason === 'forwarded'
        ? 'no rule matched — query goes upstream'
        : 'short-circuited above',
    },
  ]
})

const reasonChipClass = computed(() => {
  switch (result.value?.reason) {
    case 'blocklist':  return 'bg-danger-subtle text-danger'
    case 'allowlist':  return 'bg-success-subtle text-success'
    case 'local-dns':  return 'bg-accent-subtle text-accent'
    case 'safesearch': return 'bg-warning-subtle text-warning'
    case 'forwarded':  return 'bg-bg-hover text-fg-muted'
    default:           return 'bg-bg-hover text-fg-muted'
  }
})
</script>
