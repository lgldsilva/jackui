import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../ui/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8989',
      // /status is a root endpoint (build metadata), not under /api.
      '/status': 'http://localhost:8989'
    }
  }
})
