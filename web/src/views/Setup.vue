<template>
  <div class="min-h-screen flex items-center justify-center bg-bg-canvas p-4">
    <div class="card w-full max-w-md p-6">
      <div class="flex items-center gap-2 mb-4">
        <img src="/favicon.svg" class="w-7 h-7" alt="" />
        <h1 class="text-xl font-semibold text-fg-strong">Welcome to dblock</h1>
      </div>
      <p class="text-sm text-fg-muted mb-4">
        Set the first admin account. You can change credentials later from the Account page.
      </p>
      <form @submit.prevent="submit" class="space-y-3">
        <div>
          <label class="label" for="u">Admin username</label>
          <input id="u" v-model="user" class="input" autocomplete="username" required minlength="2" />
        </div>
        <div>
          <label class="label" for="p">Password</label>
          <input id="p" v-model="pass" type="password" class="input" autocomplete="new-password" required minlength="8" />
        </div>
        <div>
          <label class="label" for="p2">Confirm password</label>
          <input id="p2" v-model="pass2" type="password" class="input" autocomplete="new-password" required />
        </div>
        <p v-if="error" class="text-sm text-danger">{{ error }}</p>
        <button class="btn-primary w-full" :disabled="loading">
          {{ loading ? 'Creating…' : 'Create account' }}
        </button>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()

const user = ref(''), pass = ref(''), pass2 = ref('')
const error = ref(''), loading = ref(false)

async function submit() {
  error.value = ''
  if (pass.value !== pass2.value) { error.value = 'Passwords do not match.'; return }
  loading.value = true
  try {
    await auth.setupFirstRun(user.value, pass.value)
    router.replace({ name: 'dashboard' })
  } catch (err: any) {
    error.value = err?.response?.data?.error || 'Setup failed.'
  } finally { loading.value = false }
}
</script>
