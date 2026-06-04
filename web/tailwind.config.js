/** @type {import('tailwindcss').Config} */
// Color palettes mix Monokai (dark) and Monokai Solarized (lighter
// variant) so the UI carries a coherent, slightly retro feel reminiscent
// of Pi-hole's tinted dark mode and AdGuard Home's accent-on-neutral
// layout. Light mode swaps the bases for Solarized Light tones.
//
// Token strategy:
//   - `bg.*`        — background surfaces (canvas → card → input)
//   - `fg.*`        — text (primary / muted / subtle)
//   - `accent.*`    — primary action accent (Monokai cyan)
//   - `success.*`   — Monokai green
//   - `warning.*`   — Monokai orange/yellow
//   - `danger.*`    — Monokai pink/red
//
// The `dark:` variant flips between Monokai-Solarized-Light (default)
// and Monokai (dark mode). All colour references in components MUST go
// through these tokens; never inline hex.
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{vue,ts,tsx,js,jsx}'],
  theme: {
    extend: {
      colors: {
        // Monokai-Solarized Light defaults
        bg: {
          canvas: '#FDF6E3', // solarized base3
          card: '#EEE8D5',   // solarized base2
          input: '#FFFFFF',
          hover: '#E4DBC1',
        },
        fg: {
          DEFAULT: '#586E75', // solarized base01
          strong: '#073642',  // solarized base02 → near-black
          muted: '#839496',
          subtle: '#93A1A1',
          inverse: '#FDF6E3',
        },
        border: {
          DEFAULT: '#DCD1B0',
          strong: '#93A1A1',
        },
        accent: {
          DEFAULT: '#268BD2', // solarized blue
          hover: '#1A6FA0',
          active: '#0F537C',
          subtle: '#D6EAF8',
        },
        success: {
          DEFAULT: '#859900', // solarized green ≈ monokai green
          subtle: '#E8F0CC',
        },
        warning: {
          DEFAULT: '#B58900', // solarized yellow / monokai orange
          subtle: '#FAF3D0',
        },
        danger: {
          DEFAULT: '#DC322F', // solarized red / monokai pink
          subtle: '#FBE3E2',
        },

        // Pure Monokai dark mode (used under .dark via CSS variables)
        monokai: {
          bg: '#272822',
          bg2: '#1E1F1A',
          card: '#3E3D32',
          fg: '#F8F8F2',
          fgMuted: '#75715E',
          comment: '#75715E',
          cyan: '#66D9EF',
          green: '#A6E22E',
          orange: '#FD971F',
          pink: '#F92672',
          purple: '#AE81FF',
          yellow: '#E6DB74',
          border: '#49483E',
        },
      },
      fontFamily: {
        sans: ['"Inter"', '-apple-system', 'BlinkMacSystemFont', '"Segoe UI"', 'sans-serif'],
        mono: ['"JetBrains Mono"', '"Fira Code"', 'Menlo', 'monospace'],
      },
      boxShadow: {
        soft: '0 1px 2px rgba(0,0,0,0.05), 0 1px 3px rgba(0,0,0,0.05)',
      },
    },
  },
  plugins: [],
}
