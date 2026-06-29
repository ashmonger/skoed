import { defineStore } from 'pinia'

type Mode = 'light' | 'dark'
type Palette = 'monokai' | 'monokai-solarized' | 'monokai-blue' | 'monokai-pro' | 'lipgloss' | 'pride' | 'bi' | 'trans'

interface ThemeState {
  mode: Mode
  palette: Palette
}

const STORAGE_KEY = 'skoed.theme'

function load(): ThemeState {
  const raw = localStorage.getItem(STORAGE_KEY)
  if (raw) {
    try { return { ...defaults, ...JSON.parse(raw) } } catch {}
  }
  // Default: solarized palette, mode follows OS pref.
  const prefersDark = typeof window !== 'undefined' &&
    window.matchMedia?.('(prefers-color-scheme: dark)').matches
  return { mode: prefersDark ? 'dark' : 'light', palette: 'monokai-solarized' }
}

const defaults: ThemeState = { mode: 'light', palette: 'monokai-solarized' }

function apply(state: ThemeState) {
  const html = document.documentElement
  html.classList.toggle('dark', state.mode === 'dark')
  html.dataset.palette = state.palette
}

export const useThemeStore = defineStore('theme', {
  state: (): ThemeState => load(),
  actions: {
    toggleMode() {
      this.mode = this.mode === 'dark' ? 'light' : 'dark'
      apply(this.$state)
      localStorage.setItem(STORAGE_KEY, JSON.stringify(this.$state))
    },
    setPalette(p: Palette) {
      this.palette = p
      apply(this.$state)
      localStorage.setItem(STORAGE_KEY, JSON.stringify(this.$state))
    },
    applyOnStartup() {
      apply(this.$state)
    },
  },
})
