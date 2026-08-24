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
  build: {
    rollupOptions: {
      output: {
        // Split the heavy, rarely-changing dependencies out of the app chunk.
        // Without this the single bundle is ~780 kB and Vite warns on every
        // build. Milkdown (WYSIWYG editor) and its ProseMirror core are by far
        // the biggest, and they change only on dependency bumps — so caching
        // them separately also means an app-code release does not invalidate
        // them in the browser.
        manualChunks: {
          milkdown: [
            '@milkdown/core',
            '@milkdown/ctx',
            '@milkdown/react',
            '@milkdown/preset-commonmark',
            '@milkdown/preset-gfm',
            '@milkdown/plugin-history',
            '@milkdown/plugin-listener',
            '@milkdown/theme-nord',
          ],
          react: ['react', 'react-dom'],
          markdown: ['react-markdown'],
        },
      },
    },
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
