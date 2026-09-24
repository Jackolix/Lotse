import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

// In development the hub runs on :8090 and Vite proxies API, event stream and agent traffic to it.
const hub = 'http://localhost:8090'

export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  server: {
    proxy: {
      '/api': { target: hub, ws: true },
      '/install.sh': hub,
      '/install.ps1': hub,
      '/download': hub,
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
