import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import path from 'node:path'

// Build target: the SPA is embedded into the dblock Go binary via go:embed
// at internal/api/handlers/web/dist/. We emit a small bundle with no
// hashed asset names so the embed paths stay stable across rebuilds.
export default defineConfig({
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
      // Dev: proxy every API call to the running dblock binary on :8080
      // so the SPA can be developed against a live cluster.
      '/api': 'http://127.0.0.1:8080',
    },
  },
})
