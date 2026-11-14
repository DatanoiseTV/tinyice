import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'path'
import { existsSync } from 'fs'
import { mockApi } from './dev/mock'

// Entry pages. The Go ShellRenderer reads dist/src/entries/<name>.html and
// string-replaces the title and the #app div, so the file names here are a
// contract with server/shell.go.
const entryNames = ['landing', 'player', 'explore', 'embed', 'developers', 'login', 'admin', 'kiosk', 'setup']
const input = Object.fromEntries(
  entryNames
    .map((n) => [n, resolve(__dirname, `src/entries/${n}.html`)] as const)
    .filter(([, p]) => existsSync(p)),
)

// In dev, serve the entry pages at their production URLs (/admin/streams,
// /player/x ...) with the same window.__TINYICE__ the Go server injects.
function devRoutes(): Plugin {
  return {
    name: 'tinyice-dev-routes',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const url = req.url ?? ''
        const path = url.split('?')[0]
        const map: [RegExp, string][] = [
          [/^\/admin(\/.*)?$/, 'admin'],
          [/^\/kiosk\/?$/, 'kiosk'],
          [/^\/login\/?$/, 'login'],
          [/^\/setup\/?$/, 'setup'],
          [/^\/explore\/?$/, 'explore'],
          [/^\/developers\/?$/, 'developers'],
          [/^\/player\/.+/, 'player'],
          [/^\/embed\/.+/, 'embed'],
          [/^\/$/, 'landing'],
        ]
        for (const [re, name] of map) {
          if (re.test(path) && !path.includes('.') && !path.startsWith('/admin/events')) {
            req.url = `/src/entries/${name}.html`
            break
          }
        }
        next()
      })
    },
  }
}

export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss(), devRoutes(), mode !== 'production' && process.env.MOCK !== '0' ? mockApi() : null],
  resolve: { alias: { '@': resolve(__dirname, 'src') } },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 900,
    rollupOptions: { input },
  },
  server: {
    port: 5199,
    strictPort: true,
    // MOCK=0 proxies to a real server on :8000 instead of the fixtures.
    proxy:
      process.env.MOCK === '0'
        ? {
            '/api': 'http://localhost:8000',
            '/admin/events': 'http://localhost:8000',
            '/events': 'http://localhost:8000',
            '/webrtc': 'http://localhost:8000',
          }
        : undefined,
  },
}))
