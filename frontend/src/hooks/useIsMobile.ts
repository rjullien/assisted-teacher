import { useState, useEffect } from 'react'

const MOBILE_BREAKPOINT = 768

export function useIsMobile(): boolean {
  const [isMobile, setIsMobile] = useState(() => window.innerWidth < MOBILE_BREAKPOINT)

  useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)

    const handler = (e: MediaQueryListEvent) => {
      setIsMobile(e.matches)
    }

    mql.addEventListener('change', handler)
    // Sync on mount in case SSR initial value differs
    setIsMobile(mql.matches)

    return () => mql.removeEventListener('change', handler)
  }, [])

  return isMobile
}
