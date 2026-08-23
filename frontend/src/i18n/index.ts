import { createContext, useContext } from 'react'
import fr from './fr.json'
import en from './en.json'

export type Locale = 'fr' | 'en'

const messages: Record<Locale, Record<string, unknown>> = { fr, en }

/**
 * Detect the user's preferred language from the browser.
 * Returns 'fr' if the browser language starts with 'fr', otherwise 'en'.
 */
export function detectLocale(): Locale {
  const saved = localStorage.getItem('assisted-teacher-locale')
  if (saved === 'fr' || saved === 'en') return saved
  const lang = navigator.language || (navigator as { userLanguage?: string }).userLanguage || 'fr'
  return lang.startsWith('fr') ? 'fr' : 'en'
}

export function saveLocale(locale: Locale) {
  localStorage.setItem('assisted-teacher-locale', locale)
}

/**
 * Get a nested translation string by dot-path key.
 * Supports simple interpolation: {key} in the string is replaced by params[key].
 */
export function t(locale: Locale, key: string, params?: Record<string, string | number>): string {
  const parts = key.split('.')
  let value: unknown = messages[locale]
  for (const part of parts) {
    if (value && typeof value === 'object') {
      value = (value as Record<string, unknown>)[part]
    } else {
      return key // fallback: return the key itself
    }
  }
  if (typeof value !== 'string') return key

  if (!params) return value
  return value.replace(/\{(\w+)\}/g, (_, k) => String(params[k] ?? `{${k}}`))
}

// React context for locale
export interface I18nContextValue {
  locale: Locale
  setLocale: (l: Locale) => void
  t: (key: string, params?: Record<string, string | number>) => string
}

export const I18nContext = createContext<I18nContextValue>({
  locale: 'fr',
  setLocale: () => {},
  t: (key) => key,
})

export function useI18n(): I18nContextValue {
  return useContext(I18nContext)
}
