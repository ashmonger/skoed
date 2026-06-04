import { defineStore } from 'pinia'
import axios from 'axios'
import { getCreds, setCreds } from '@/api/client'

interface AuthState {
  user: string | null
  ready: boolean   // true once we've probed setup-status
  isSetup: boolean // true if a credential already exists on the server
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => ({
    user: getCreds()?.user ?? null,
    ready: false,
    isSetup: false,
  }),
  getters: {
    isAuthenticated: (s) => s.user !== null,
  },
  actions: {
    // Probe /api/v1/health (always 200, no auth) — confirms server reachable.
    // Then attempt /api/v1/blocklists with current creds; 401 means we need
    // to (re)login, 200 means creds are good, 404 fallback for fresh node.
    async probe() {
      try {
        await axios.get('/api/v1/health', { timeout: 3000 })
      } catch {
        this.ready = true
        return
      }
      // setup probe: POST /api/v1/auth/setup with bogus body — 409 means
      // already configured, 201 (which we don't want here) would mean we
      // just set the first cred. Use GET /api/v1/blocklists with no creds
      // instead: 401 → setup is complete (creds required), 404/anything else
      // → odd state we treat as setup-complete.
      try {
        await axios.get('/api/v1/blocklists', { timeout: 3000 })
        this.isSetup = true
      } catch (err: any) {
        if (err?.response?.status === 401) this.isSetup = true
        else this.isSetup = true
      }
      this.ready = true
    },
    async login(user: string, pass: string) {
      // Validate creds against an authenticated endpoint.
      const headers = { Authorization: 'Basic ' + btoa(`${user}:${pass}`) }
      await axios.get('/api/v1/blocklists', { headers, timeout: 5000 })
      setCreds({ user, pass })
      this.user = user
    },
    async setupFirstRun(user: string, pass: string) {
      await axios.post('/api/v1/auth/setup', { username: user, password: pass })
      setCreds({ user, pass })
      this.user = user
      this.isSetup = true
    },
    logout() {
      setCreds(null)
      this.user = null
    },
  },
})
