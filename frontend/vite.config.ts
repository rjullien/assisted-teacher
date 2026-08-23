import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { execSync } from 'child_process'

const gitSha = execSync('git rev-parse --short HEAD').toString().trim()

export default defineConfig({
  plugins: [react()],
  define: {
    __APP_VERSION__: JSON.stringify(gitSha),
  },
  server: {
    proxy: {
      '/api': 'http://localhost:9847',
      '/ws': {
        target: 'http://localhost:9847',
        ws: true,
      },
    },
  },
})
