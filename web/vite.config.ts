import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// ponytail: dev proxy на Go-сервер. /api и старые /keys/* проксируются,
// чтобы SPA на :5173 ходил на бэкенд :8080 без CORS-настройки.
// В проде SPA embed-ится в Go-бинарник и раздаётся с того же origin.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/keys': 'http://localhost:8080',
    },
  },
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
})
