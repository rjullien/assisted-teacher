import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { execSync } from 'child_process'

/**
 * Build version: prefer VITE_APP_VERSION (set by CI / Docker build arg),
 * fall back to git SHA for local dev, then to 'dev' when neither works
 * (e.g. inside a Docker build context without .git).
 */
function resolveVersion(): string {
  const fromEnv = process.env.VITE_APP_VERSION?.trim()
  if (fromEnv) return fromEnv
  try {
    return execSync('git rev-parse --short HEAD', { stdio: ['ignore', 'pipe', 'ignore'] })
      .toString()
      .trim()
  } catch {
    return 'dev'
  }
}

const gitSha = resolveVersion()

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
