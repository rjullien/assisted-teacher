import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { readFileSync } from 'fs'
import { resolve } from 'path'

/**
 * Build version: prefer VITE_APP_VERSION (set by CI / Docker build arg),
 * fall back to version.json (semver source of truth), then to 'dev'.
 */
function resolveVersion(): string {
  const fromEnv = process.env.VITE_APP_VERSION?.trim()
  if (fromEnv) return fromEnv
  try {
    const versionFile = resolve(__dirname, '..', 'version.json')
    const { version } = JSON.parse(readFileSync(versionFile, 'utf-8'))
    return version
  } catch {
    return 'dev'
  }
}

const appVersion = resolveVersion()

export default defineConfig({
  plugins: [react()],
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
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
