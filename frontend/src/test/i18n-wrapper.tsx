import { type ReactNode } from 'react'
import { render, type RenderOptions } from '@testing-library/react'
import { I18nContext, t as tFn, type Locale } from '../i18n'

const TEST_LOCALE: Locale = 'fr'

const testI18nValue = {
  locale: TEST_LOCALE,
  setLocale: () => {},
  t: (key: string, params?: Record<string, string | number>) => tFn(TEST_LOCALE, key, params),
}

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nContext.Provider value={testI18nValue}>
      {children}
    </I18nContext.Provider>
  )
}

/**
 * Custom render that wraps components with I18nContext.
 * Use this instead of `render` from @testing-library/react in tests.
 */
export function renderWithI18n(ui: React.ReactElement, options?: Omit<RenderOptions, 'wrapper'>) {
  return render(ui, { wrapper: I18nWrapper, ...options })
}
