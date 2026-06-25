<template>
  <div class="space-y-4">
    <!-- Error banner -->
    <p v-if="errorMsg"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ errorMsg }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="errorMsg = ''">dismiss</button>
    </p>

    <!-- Header -->
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 class="text-lg font-semibold text-fg-strong">Custom Rules</h1>
        <p class="text-sm text-fg-muted">
          Cluster-wide rules that override blocklists and the global allowlist.
          Evaluated before all other filtering logic.
        </p>
      </div>
      <div class="flex items-center gap-2">
        <span v-if="savedAt" class="text-xs text-accent">Saved</span>
        <button class="btn-secondary" :disabled="saving || !dirty" @click="discardChanges">
          Discard
        </button>
        <button class="btn-primary" :disabled="saving" @click="save">
          {{ saving ? 'Saving…' : 'Save' }}
        </button>
      </div>
    </div>

    <!-- Syntax reference -->
    <div class="card p-4 text-sm space-y-1 text-fg-muted">
      <p class="font-medium text-fg">Syntax</p>
      <p><code class="font-mono bg-bg-canvas px-1 rounded">/regex/</code> — block domains matching the pattern</p>
      <p><code class="font-mono bg-bg-canvas px-1 rounded">@@/regex/</code> — allow domains matching the pattern (overrides blocklists)</p>
      <p><code class="font-mono bg-bg-canvas px-1 rounded">domain.example</code> — exact block (also matches all sub-domains)</p>
      <p><code class="font-mono bg-bg-canvas px-1 rounded">@@domain.example</code> — exact allow</p>
      <p><code class="font-mono bg-bg-canvas px-1 rounded"># comment</code> — ignored</p>
      <p class="text-xs pt-1">Allow rules always win over block rules for the same domain.</p>
    </div>

    <!-- Editor -->
    <div class="card p-4 space-y-2">
      <label class="label" for="custom-rules-editor">Rules</label>
      <textarea
        id="custom-rules-editor"
        v-model="draft"
        class="input font-mono text-sm w-full resize-y"
        rows="18"
        placeholder="# One rule per line&#10;/^ad[0-9]+\.example\.com$/&#10;@@safe.example.com"
        spellcheck="false"
        @input="onEdit"
      />
      <p class="text-xs text-fg-muted">
        {{ lineCount }} {{ lineCount === 1 ? 'rule' : 'rules' }} (empty lines and comments excluded)
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { getCustomRules, putCustomRules } from '@/api/endpoints'

const draft = ref('')
const original = ref('')
const saving = ref(false)
const errorMsg = ref('')
const savedAt = ref(false)
let savedAtTimer: ReturnType<typeof setTimeout> | null = null

const dirty = computed(() => draft.value !== original.value)

const lineCount = computed(() => {
  return draft.value
    .split('\n')
    .map(l => l.trim())
    .filter(l => l !== '' && !l.startsWith('#'))
    .length
})

onMounted(async () => {
  try {
    const r = await getCustomRules()
    draft.value = r.rules
    original.value = r.rules
  } catch {
    errorMsg.value = 'Failed to load custom rules.'
  }
})

function onEdit() {
  errorMsg.value = ''
  savedAt.value = false
}

function discardChanges() {
  draft.value = original.value
  errorMsg.value = ''
  savedAt.value = false
}

async function save() {
  saving.value = true
  errorMsg.value = ''
  try {
    const r = await putCustomRules(draft.value)
    original.value = r.rules
    draft.value = r.rules
    savedAt.value = true
    if (savedAtTimer) clearTimeout(savedAtTimer)
    savedAtTimer = setTimeout(() => { savedAt.value = false }, 3000)
  } catch (err: unknown) {
    const msg = (err as { message?: string })?.message ?? String(err)
    errorMsg.value = 'Save failed: ' + msg
  } finally {
    saving.value = false
  }
}
</script>
