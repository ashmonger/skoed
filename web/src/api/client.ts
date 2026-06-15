import axios, { AxiosError, type AxiosInstance } from 'axios'

// The SPA is served from the same origin as the API in production (embedded
// in the skoed binary). In dev, vite.config.ts proxies /api → :8080.
const baseURL = ''

// Session token lives in sessionStorage — survives page navigation but
// not browser close. The token is issued by POST /api/v1/auth/login and
// revoked by DELETE /api/v1/auth/session.
const TOKEN_KEY = 'skoed.token'

export function getToken(): string | null {
  return sessionStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string | null) {
  if (token) sessionStorage.setItem(TOKEN_KEY, token)
  else sessionStorage.removeItem(TOKEN_KEY)
}

export const api: AxiosInstance = axios.create({ baseURL, timeout: 15000 })

api.interceptors.request.use((config) => {
  const token = getToken()
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

api.interceptors.response.use(
  (r) => r,
  (err: AxiosError) => {
    if (err.response?.status === 401) {
      // Don't auto-clear token: setup flow + first-render race can
      // produce a benign 401 before login completes. Views handle this.
    }
    return Promise.reject(err)
  },
)

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
