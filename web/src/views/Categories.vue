<template>
  <div class="space-y-4">
    <!-- Global error banner -->
    <p v-if="lastError"
       class="card px-4 py-2 text-sm text-danger bg-danger-subtle border-danger">
      {{ lastError }}
      <button class="btn-ghost ml-2 !py-0 !px-1" @click="lastError = ''">dismiss</button>
    </p>

    <!-- Toolbar -->
    <div class="flex items-center justify-between">
      <h1 class="text-lg font-semibold text-fg-strong">Categories</h1>
      <button class="btn-secondary" :disabled="loading" @click="refresh">
        <ArrowPathIcon class="h-4 w-4" :class="{ 'animate-spin': loading }" />
        Refresh
      </button>
    </div>

    <p v-if="loading" class="card p-6 text-sm text-fg-muted text-center">Loading…</p>

    <div v-else-if="categories.length === 0"
         class="card p-12 text-sm text-fg-muted text-center">
      No categories available.
    </div>

    <!-- Card grid -->
    <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
      <article v-for="cat in categories" :key="cat.name" class="card p-4 space-y-3">
        <!-- Header -->
        <header class="flex items-start gap-2">
          <TagIcon class="h-5 w-5 text-accent shrink-0 mt-0.5" />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="badge-accent font-mono">{{ cat.name }}</span>
              <span class="text-xs text-fg-subtle uppercase tracking-wide">{{ cat.format }}</span>
            </div>
            <p class="text-sm text-fg-muted mt-1">{{ cat.description }}</p>
          </div>
        </header>

        <!-- Per-card row error -->
        <p v-if="cardErrors[cat.name]" class="text-xs text-danger">
          {{ cardErrors[cat.name] }}
        </p>

        <!-- Effective URL row -->
        <div class="space-y-1">
          <div class="flex items-center justify-between gap-2">
            <span class="label !mb-0">Upstream URL</span>
            <span class="text-xs"
                  :class="isOverride(cat) ? 'text-warning' : 'text-fg-subtle'">
              {{ isOverride(cat) ? '(override)' : '(default)' }}
            </span>
          </div>
          <div v-if="editingUrl !== cat.name"
               class="flex items-center gap-2">
            <code class="font-mono text-xs text-fg break-all flex-1 min-w-0">
              {{ cat.url }}
            </code>
            <button class="btn-ghost shrink-0"
                    title="Edit upstream URL"
                    @click="openUrlEditor(cat)">
              <PencilSquareIcon class="h-4 w-4" />
            </button>
          </div>

          <!-- Inline URL editor -->
          <div v-else class="space-y-2 border border-border rounded p-3 bg-bg-input/30">
            <div>
              <label class="label" :for="`url-${cat.name}`">URL</label>
              <input :id="`url-${cat.name}`"
                     v-model="urlForm.url"
                     class="input font-mono text-xs"
                     type="url"
                     required
                     placeholder="https://…" />
            </div>
            <div>
              <label class="label" :for="`fmt-${cat.name}`">Format</label>
              <select :id="`fmt-${cat.name}`"
                      v-model="urlForm.format"
                      class="input text-sm">
                <option v-for="f in FORMATS" :key="f" :value="f">{{ f }}</option>
              </select>
            </div>
            <div class="flex items-center justify-between gap-2 pt-1">
              <button type="button"
                      class="btn-ghost text-xs"
                      :disabled="urlSaving || cat.url === cat.default_url"
                      @click="resetUrl(cat)">
                Reset to default
              </button>
              <div class="flex gap-2">
                <button type="button"
                        class="btn-secondary"
                        :disabled="urlSaving"
                        @click="closeUrlEditor">
                  Cancel
                </button>
                <button type="button"
                        class="btn-primary"
                        :disabled="urlSaving || !urlForm.url.trim()"
                        @click="saveUrl(cat)">
                  {{ urlSaving ? 'Saving…' : 'Save' }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- Subscribed profiles -->
        <div class="space-y-1">
          <span class="label !mb-0">Enabled for</span>
          <div v-if="cat.enabled_for_profiles.length === 0"
               class="text-xs text-fg-subtle">
            No profiles subscribed.
          </div>
          <div v-else class="flex flex-wrap gap-1.5">
            <span v-for="pid in cat.enabled_for_profiles" :key="pid"
                  class="badge-success">
              <span class="font-mono">{{ pid }}</span>
              <button class="ml-1 hover:opacity-80"
                      :title="`Disable ${cat.name} for ${pid}`"
                      :disabled="disablingKey === `${cat.name}:${pid}`"
                      @click="askDisable(cat, pid)">
                <XMarkIcon class="h-3 w-3" />
              </button>
            </span>
          </div>
        </div>

        <!-- Enable button -->
        <div class="flex justify-end pt-1">
          <button class="btn-primary"
                  :disabled="availableProfilesFor(cat).length === 0"
                  :title="availableProfilesFor(cat).length === 0
                          ? 'Already enabled on every profile'
                          : 'Enable on a profile'"
                  @click="openEnable(cat)">
            <PlusIcon class="h-4 w-4" /> Enable
          </button>
        </div>
      </article>
    </div>

    <!-- Enable modal -->
    <div v-if="enableTarget"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="closeEnable">
      <form class="card max-w-md w-full p-6 space-y-4"
            @submit.prevent="confirmEnable">
        <h2 class="text-base font-semibold text-fg-strong">
          Enable <span class="font-mono">{{ enableTarget.name }}</span>
        </h2>
        <p v-if="enableError" class="text-sm text-danger">{{ enableError }}</p>
        <div>
          <label class="label" for="enable-profile">Target profile</label>
          <select id="enable-profile"
                  v-model="enableProfileId"
                  class="input"
                  required>
            <option value="" disabled>Select a profile…</option>
            <option v-for="p in availableProfilesFor(enableTarget)"
                    :key="p.id"
                    :value="p.id">
              {{ p.name }} ({{ p.id }})
            </option>
          </select>
          <p v-if="availableProfilesFor(enableTarget).length === 0"
             class="text-xs text-fg-subtle mt-1">
            Already enabled on every profile.
          </p>
        </div>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn-secondary"
                  :disabled="enableSubmitting"
                  @click="closeEnable">
            Cancel
          </button>
          <button type="submit" class="btn-primary"
                  :disabled="enableSubmitting || !enableProfileId">
            {{ enableSubmitting ? 'Enabling…' : 'Enable' }}
          </button>
        </div>
      </form>
    </div>

    <!-- Disable confirmation modal -->
    <div v-if="pendingDisable"
         class="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
         @click.self="pendingDisable = null">
      <div class="card max-w-md w-full p-6 space-y-4">
        <h2 class="text-base font-semibold text-fg-strong">Disable category?</h2>
        <p class="text-sm text-fg">
          Disable <span class="font-mono font-semibold">{{ pendingDisable.category }}</span>
          for profile <span class="font-mono font-semibold">{{ pendingDisable.profileId }}</span>?
        </p>
        <div class="flex justify-end gap-2">
          <button class="btn-secondary"
                  :disabled="disableSubmitting"
                  @click="pendingDisable = null">
            Cancel
          </button>
          <button class="btn-danger"
                  :disabled="disableSubmitting"
                  @click="confirmDisable">
            {{ disableSubmitting ? 'Disabling…' : 'Disable' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  ArrowPathIcon, PencilSquareIcon, PlusIcon, TagIcon, XMarkIcon,
} from '@heroicons/vue/24/outline'
import {
  disableCategory, enableCategory, listCategories, listProfiles, updateCategoryURL,
} from '@/api/endpoints'
import type { Category, Profile } from '@/api/types'

// ─── Constants ───────────────────────────────────────────────────────────

const FORMATS = ['hosts', 'domainlist', 'adblock'] as const

// ─── State ───────────────────────────────────────────────────────────────

const categories = ref<Category[]>([])
const profiles = ref<Profile[]>([])
const loading = ref(true)
const lastError = ref('')
const cardErrors = reactive<Record<string, string>>({})

// Inline URL editor — one card at a time.
const editingUrl = ref<string>('')
const urlForm = reactive<{ url: string; format: string }>({ url: '', format: 'hosts' })
const urlSaving = ref(false)

// Enable modal.
const enableTarget = ref<Category | null>(null)
const enableProfileId = ref('')
const enableSubmitting = ref(false)
const enableError = ref('')

// Disable confirmation.
const pendingDisable = ref<{ category: string; profileId: string } | null>(null)
const disableSubmitting = ref(false)
const disablingKey = ref('')

// ─── Derived ─────────────────────────────────────────────────────────────

function isOverride(cat: Category): boolean {
  return cat.url !== cat.default_url
}

function availableProfilesFor(cat: Category | null): Profile[] {
  if (!cat) return []
  const enabled = new Set(cat.enabled_for_profiles)
  return profiles.value.filter(p => !enabled.has(p.id))
}

// ─── Data loading ────────────────────────────────────────────────────────

async function refresh() {
  loading.value = true
  try {
    const [cats, profs] = await Promise.all([listCategories(), listProfiles()])
    categories.value = cats
    profiles.value = profs
    lastError.value = ''
  } catch (err) {
    lastError.value = errMsg(err, 'Failed to load categories')
  } finally {
    loading.value = false
  }
}

function replaceCategory(updated: Category) {
  const idx = categories.value.findIndex(c => c.name === updated.name)
  if (idx >= 0) categories.value.splice(idx, 1, updated)
  else categories.value = [...categories.value, updated]
}

async function reloadCategory(name: string) {
  // The category PATCH returns 200 OK but the catalog state is what we want
  // to render — refetch the list to keep enabled_for_profiles authoritative.
  try {
    const cats = await listCategories()
    categories.value = cats
  } catch (err) {
    lastError.value = errMsg(err, `Failed to reload ${name}`)
  }
}

// ─── URL editor ──────────────────────────────────────────────────────────

function openUrlEditor(cat: Category) {
  editingUrl.value = cat.name
  urlForm.url = cat.url
  urlForm.format = cat.format || 'hosts'
  cardErrors[cat.name] = ''
}

function closeUrlEditor() {
  if (urlSaving.value) return
  editingUrl.value = ''
}

async function saveUrl(cat: Category) {
  const url = urlForm.url.trim()
  if (!url) return
  urlSaving.value = true
  cardErrors[cat.name] = ''
  try {
    await updateCategoryURL(cat.name, url, urlForm.format)
    await reloadCategory(cat.name)
    editingUrl.value = ''
  } catch (err) {
    cardErrors[cat.name] = errMsg(err, 'Failed to update URL')
  } finally {
    urlSaving.value = false
  }
}

async function resetUrl(cat: Category) {
  urlSaving.value = true
  cardErrors[cat.name] = ''
  try {
    await updateCategoryURL(cat.name, cat.default_url)
    await reloadCategory(cat.name)
    editingUrl.value = ''
  } catch (err) {
    cardErrors[cat.name] = errMsg(err, 'Failed to reset URL')
  } finally {
    urlSaving.value = false
  }
}

// ─── Enable ──────────────────────────────────────────────────────────────

function openEnable(cat: Category) {
  enableTarget.value = cat
  enableProfileId.value = ''
  enableError.value = ''
}

function closeEnable() {
  if (enableSubmitting.value) return
  enableTarget.value = null
}

async function confirmEnable() {
  const cat = enableTarget.value
  if (!cat || !enableProfileId.value) return
  enableSubmitting.value = true
  enableError.value = ''
  try {
    await enableCategory(cat.name, enableProfileId.value)
    await reloadCategory(cat.name)
    enableTarget.value = null
  } catch (err) {
    enableError.value = errMsg(err, 'Failed to enable category')
  } finally {
    enableSubmitting.value = false
  }
  void replaceCategory  // silence unused-import-style lint in lean builds
}

// ─── Disable ─────────────────────────────────────────────────────────────

function askDisable(cat: Category, profileId: string) {
  pendingDisable.value = { category: cat.name, profileId }
}

async function confirmDisable() {
  const target = pendingDisable.value
  if (!target) return
  disableSubmitting.value = true
  disablingKey.value = `${target.category}:${target.profileId}`
  cardErrors[target.category] = ''
  try {
    await disableCategory(target.category, target.profileId)
    await reloadCategory(target.category)
    pendingDisable.value = null
  } catch (err) {
    cardErrors[target.category] = errMsg(err, 'Failed to disable category')
    lastError.value = cardErrors[target.category]
  } finally {
    disableSubmitting.value = false
    disablingKey.value = ''
  }
}

// ─── Helpers ─────────────────────────────────────────────────────────────

function errMsg(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: { error?: string } }, message?: string }
  return e?.response?.data?.error || e?.message || fallback
}

// ─── Keyboard: close modals/editor on Escape ─────────────────────────────

function onKey(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  if (pendingDisable.value) { pendingDisable.value = null; return }
  if (enableTarget.value && !enableSubmitting.value) { enableTarget.value = null; return }
  if (editingUrl.value && !urlSaving.value) { editingUrl.value = '' }
}

onMounted(() => {
  refresh()
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
})
</script>
