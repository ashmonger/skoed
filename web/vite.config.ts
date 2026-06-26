import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import path from 'node:path'
import { execSync } from 'node:child_process'

const gitCommit = (() => {
  try { return execSync('git rev-parse --short HEAD').toString().trim() }
  catch { return 'unknown' }
})()

// Build target: the SPA is embedded into the skoed Go binary via go:embed
// at internal/api/handlers/web/dist/. We emit a small bundle with no
// hashed asset names so the embed paths stay stable across rebuilds.
export default defineConfig({
  define: {
    __GIT_COMMIT__: JSON.stringify(gitCommit),
  },
  plugins: [vue()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2020',
    cssCodeSplit: false,
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks: undefined,
        entryFileNames: 'assets/app.js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name][extname]',
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      // M5.9.2 `make dev` loop: vite dev forwards backend calls to the
      // skoed daemon spawned by scripts/dev.sh on port 18099. The high
      // port is deliberate — it avoids clashing with any locally-running
      // production skoed the operator has on :8080. /metrics is the
      // Prometheus exporter; the SPA scrapes it via /api in normal use
      // but proxying it here means `curl http://localhost:5173/metrics`
      // also works during dev, matching the production routing.
      '/api':     'http://127.0.0.1:18099',
      '/metrics': 'http://127.0.0.1:18099',
    },
  },
})
