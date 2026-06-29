import { defineStore } from 'pinia'
import axios from 'axios'
import { getToken, setToken, getSavedUser, setSavedUser, api } from '@/api/client'

interface AuthState {
  user: string | null
  ready: boolean   // true once we've probed setup-status
  isSetup: boolean // true if a credential already exists on the server
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => ({
    user: getToken() ? (getSavedUser() ?? '__session__') : null,
    ready: false,
    isSetup: false,
  }),
  getters: {
    isAuthenticated: (s) => s.user !== null,
  },
  actions: {
    // Probe /api/v1/health (always 200) to confirm the server is reachable,
    // then check whether credentials are configured on the server.
    async probe() {
      try {
        await axios.get('/api/v1/health', { timeout: 3000 })
      } catch {
        this.ready = true
        return
      }
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
      const resp = await axios.post<{ token: string }>(
        '/api/v1/auth/login',
        { username: user, password: pass },
        { timeout: 5000 },
      )
      setToken(resp.data.token)
      setSavedUser(user)
      this.user = user
    },
    async setupFirstRun(user: string, pass: string) {
      await axios.post('/api/v1/auth/setup', { username: user, password: pass })
      // After setup, log in to get a session token.
      await this.login(user, pass)
      this.isSetup = true
    },
    async logout() {
      try {
        await api.delete('/api/v1/auth/session')
      } catch {
        // Best-effort revocation; clear local state regardless.
      }
      setToken(null)
      setSavedUser(null)
      this.user = null
    },
  },
})
