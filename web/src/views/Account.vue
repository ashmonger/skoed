<template>
  <div class="space-y-6">
    <!-- Profile -->
    <section class="card p-5">
      <header class="flex items-center gap-2 mb-4">
        <UserCircleIcon class="w-5 h-5 text-fg-muted" />
        <h2 class="text-sm font-semibold text-fg-strong">Profile</h2>
      </header>

      <div class="space-y-3">
        <div>
          <label class="label">Signed in as</label>
          <p class="font-mono text-sm text-fg-strong">{{ auth.user ?? '—' }}</p>
        </div>
        <div>
          <button type="button" class="btn-secondary" @click="onLogout">
            <ArrowRightStartOnRectangleIcon class="w-4 h-4" />
            Sign out
          </button>
        </div>
      </div>
    </section>

    <!-- Change password -->
    <section class="card p-5">
      <header class="flex items-center gap-2 mb-4">
        <KeyIcon class="w-5 h-5 text-fg-muted" />
        <h2 class="text-sm font-semibold text-fg-strong">Change password</h2>
      </header>

      <form class="space-y-3 max-w-md" @submit.prevent="onSubmit">
        <div>
          <label class="label" for="cur-pw">Current password</label>
          <input
            id="cur-pw"
            v-model="currentPw"
            type="password"
            class="input"
            autocomplete="current-password"
            required
          />
        </div>
        <div>
          <label class="label" for="new-pw">New password</label>
          <input
            id="new-pw"
            v-model="newPw"
            type="password"
            class="input"
            autocomplete="new-password"
            minlength="8"
            required
          />
          <p class="text-xs text-fg-subtle mt-1">Minimum 8 characters.</p>
        </div>
        <div>
          <label class="label" for="confirm-pw">Confirm new password</label>
          <input
            id="confirm-pw"
            v-model="confirmPw"
            type="password"
            class="input"
            autocomplete="new-password"
            required
          />
          <p
            v-if="confirmPw && !confirmMatches"
            class="text-xs text-danger mt-1"
          >
            Passwords do not match.
          </p>
        </div>

        <div v-if="error" class="text-sm text-danger">{{ error }}</div>
        <div v-if="success" class="text-sm text-success">{{ success }}</div>

        <div>
          <button
            type="submit"
            class="btn-primary"
            :disabled="!canSubmit || submitting"
          >
            {{ submitting ? 'Changing…' : 'Change password' }}
          </button>
        </div>
      </form>
    </section>

    <!-- Appearance -->
    <section class="card p-5">
      <header class="flex items-center gap-2 mb-4">
        <SwatchIcon class="w-5 h-5 text-fg-muted" />
        <h2 class="text-sm font-semibold text-fg-strong">Appearance</h2>
      </header>

      <div class="space-y-4 max-w-md">
        <!-- Mode segmented control -->
        <div>
          <label class="label">Mode</label>
          <div
            class="inline-flex rounded border border-border bg-bg-input p-0.5"
            role="group"
            aria-label="Theme mode"
          >
            <button
              type="button"
              class="btn px-3 py-1 text-xs"
              :class="theme.mode === 'light'
                ? 'bg-accent text-fg-inverse'
                : 'text-fg hover:bg-bg-hover'"
              @click="setMode('light')"
            >
              <SunIcon class="w-4 h-4" />
              Light
            </button>
            <button
              type="button"
              class="btn px-3 py-1 text-xs"
              :class="theme.mode === 'dark'
                ? 'bg-accent text-fg-inverse'
                : 'text-fg hover:bg-bg-hover'"
              @click="setMode('dark')"
            >
              <MoonIcon class="w-4 h-4" />
              Dark
            </button>
          </div>
        </div>

        <!-- Palette dropdown -->
        <div>
          <label class="label" for="palette">Palette</label>
          <select
            id="palette"
            class="input w-64"
            :value="theme.palette"
            @change="onPaletteChange"
          >
            <option value="monokai-solarized">Monokai Solarized</option>
            <option value="monokai">Monokai (vivid)</option>
          </select>
        </div>

        <!-- Live preview -->
        <div>
          <label class="label">Preview</label>
          <div class="card p-3 space-y-2">
            <p class="text-sm text-fg">
              Sample text in <span class="text-fg-strong font-semibold">strong</span>,
              <span class="text-fg-muted">muted</span>, and
              <span class="text-accent">accent</span> tones.
            </p>
            <div class="flex flex-wrap gap-2">
              <span class="badge-success">success</span>
              <span class="badge-warning">warning</span>
              <span class="badge-danger">danger</span>
              <span class="badge-accent">accent</span>
            </div>
            <div class="flex gap-2 pt-1">
              <button type="button" class="btn-primary">Primary</button>
              <button type="button" class="btn-secondary">Secondary</button>
              <button type="button" class="btn-ghost">Ghost</button>
            </div>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  UserCircleIcon, KeyIcon, SwatchIcon, SunIcon, MoonIcon,
  ArrowRightStartOnRectangleIcon,
} from '@heroicons/vue/24/outline'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { changePassword } from '@/api/endpoints'

type Mode = 'light' | 'dark'
type Palette = 'monokai' | 'monokai-solarized'

const auth = useAuthStore()
const theme = useThemeStore()
const router = useRouter()

const currentPw = ref('')
const newPw = ref('')
const confirmPw = ref('')
const submitting = ref(false)
const error = ref<string | null>(null)
const success = ref<string | null>(null)

const confirmMatches = computed(() => newPw.value === confirmPw.value)
const canSubmit = computed(() =>
  currentPw.value.length > 0 &&
  newPw.value.length >= 8 &&
  confirmMatches.value,
)

async function onLogout() {
  await auth.logout()
  router.replace({ name: 'login' })
}

async function onSubmit() {
  error.value = null
  success.value = null
  if (!canSubmit.value) return
  submitting.value = true
  try {
    await changePassword(currentPw.value, newPw.value)
    currentPw.value = ''
    newPw.value = ''
    confirmPw.value = ''
    success.value =
      'Password changed. Re-authenticate next session — your current ' +
      'session keeps the old credentials in memory and will fail on next page load.'
  } catch (err: any) {
    error.value = err?.response?.data?.error ?? 'Could not change password.'
  } finally {
    submitting.value = false
  }
}

// Theme mode is toggled rather than set directly (store only exposes toggleMode).
// Clicking the active button is a no-op; clicking the inactive one flips state.
function setMode(target: Mode) {
  if (theme.mode !== target) theme.toggleMode()
}

function onPaletteChange(e: Event) {
  theme.setPalette((e.target as HTMLSelectElement).value as Palette)
}
</script>
