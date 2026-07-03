import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../ui/dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks: {
          // Isolate hls.js so it lands in its own long-cache chunk, loaded only
          // when the (lazy) player mounts — not in the initial bundle.
          hls: ['hls.js'],
          // Framework vendor split for stable long-term caching.
          'react-vendor': ['react', 'react-dom', 'react-router-dom'],
        },
      },
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8989',
      // /status is a root endpoint (build metadata), not under /api.
      '/status': 'http://localhost:8989'
    }
  }
})
