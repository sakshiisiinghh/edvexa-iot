import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Proxies both the REST API and the WebSocket upgrade through the same /v1
// prefix, so the dashboard can call relative paths (/v1/devices, /v1/ws)
// without needing to know the API's host in dev.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/v1': {
        target: 'http://localhost:8080',
        ws: true,
      },
    },
  },
})
