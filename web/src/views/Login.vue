<template>
  <div class="min-h-screen flex items-center justify-center bg-bg-canvas p-4">
    <div class="card w-full max-w-sm p-6">
      <div class="flex items-center gap-2 mb-4">
        <img src="/favicon.svg" class="w-7 h-7" alt="" />
        <h1 class="text-xl font-semibold text-fg-strong">dblock</h1>
      </div>
      <h2 class="text-sm text-fg-muted mb-4">Sign in to manage your DNS cluster.</h2>
      <form @submit.prevent="submit" class="space-y-3">
        <div>
          <label class="label" for="u">Username</label>
          <input id="u" v-model="user" class="input" autocomplete="username" required />
        </div>
        <div>
          <label class="label" for="p">Password</label>
          <input id="p" v-model="pass" type="password" class="input" autocomplete="current-password" required />
        </div>
        <p v-if="error" class="text-sm text-danger">{{ error }}</p>
        <button class="btn-primary w-full" :disabled="loading">
          {{ loading ? 'Signing in…' : 'Sign in' }}
        </button>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const user = ref(''), pass = ref('')
const error = ref(''), loading = ref(false)

async function submit() {
  error.value = ''; loading.value = true
  try {
    await auth.login(user.value, pass.value)
    const next = (route.query.redirect as string) || '/'
    router.replace(next)
  } catch (err: any) {
    error.value = err?.response?.status === 401 ? 'Invalid credentials.' : 'Login failed.'
  } finally { loading.value = false }
}
</script>
