import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // Embedded into the Go binary by internal/webui.
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    // The backend runs on :8686 during development (`make run`).
    proxy: { '/api': 'http://localhost:8686' },
  },
})
