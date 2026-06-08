import axios, { AxiosError, type AxiosInstance } from 'axios'

// The SPA is served from the same origin as the API in production (embedded
// in the skoed binary). In dev, vite.config.ts proxies /api → :8080.
const baseURL = ''

// Credentials live in sessionStorage so they survive page navigation but
// don't leak across browser sessions. Basic Auth header is rebuilt from
// here on every request.
const CREDS_KEY = 'skoed.creds'

interface Creds { user: string; pass: string }

export function getCreds(): Creds | null {
  const raw = sessionStorage.getItem(CREDS_KEY)
  if (!raw) return null
  try { return JSON.parse(raw) as Creds } catch { return null }
}

export function setCreds(c: Creds | null) {
  if (c) sessionStorage.setItem(CREDS_KEY, JSON.stringify(c))
  else sessionStorage.removeItem(CREDS_KEY)
}

function basicAuthHeader(c: Creds): string {
  return 'Basic ' + btoa(`${c.user}:${c.pass}`)
}

export const api: AxiosInstance = axios.create({ baseURL, timeout: 15000 })

api.interceptors.request.use((config) => {
  const c = getCreds()
  if (c) config.headers.Authorization = basicAuthHeader(c)
  return config
})

// Surface a typed error shape to call sites and let the auth store react
// to 401s by bouncing the user back to login.
api.interceptors.response.use(
  (r) => r,
  (err: AxiosError) => {
    if (err.response?.status === 401) {
      // Don't auto-clear creds here: setup flow + first-render race can
      // produce a benign 401 before creds are set. Views handle this.
    }
    return Promise.reject(err)
  },
)

// Convenience helper for callsites that want the raw JSON without unwrapping.
export async function getJSON<T>(path: string, params?: Record<string, unknown>): Promise<T> {
  const r = await api.get<T>(path, { params })
  return r.data
}

export async function postJSON<T, B = unknown>(path: string, body?: B): Promise<T> {
  const r = await api.post<T>(path, body ?? {})
  return r.data
}

export async function patchJSON<T, B = unknown>(path: string, body: B): Promise<T> {
  const r = await api.patch<T>(path, body)
  return r.data
}

export async function putJSON<T, B = unknown>(path: string, body: B): Promise<T> {
  const r = await api.put<T>(path, body)
  return r.data
}

export async function deleteRequest(path: string): Promise<void> {
  await api.delete(path)
}
