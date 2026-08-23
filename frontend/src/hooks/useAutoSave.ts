import { useCallback, useEffect, useRef, useState } from 'react'

export type SaveStatus = 'idle' | 'unsaved' | 'saving' | 'saved' | 'error'

interface UseAutoSaveOptions {
  /** Debounce delay in ms (default: 1000) */
  debounceMs?: number
  /** How long to show "saved" status before going back to idle (default: 2000) */
  savedDisplayMs?: number
}

interface UseAutoSaveReturn {
  /** Current save status */
  status: SaveStatus
  /** Call this to trigger an immediate save (flush pending changes) */
  flush: () => Promise<void>
}

/**
 * Auto-save hook with debounce.
 *
 * - Watches `content` for changes compared to `lastSavedContent`
 * - After `debounceMs` of inactivity, calls `saveFn`
 * - Exposes a `flush()` for forcing a save (e.g. before switching files)
 * - Prevents Ctrl+S browser dialog by intercepting the shortcut
 */
export function useAutoSave(
  content: string,
  lastSavedContent: string,
  saveFn: (content: string) => Promise<void>,
  options: UseAutoSaveOptions = {}
): UseAutoSaveReturn {
  const { debounceMs = 1000, savedDisplayMs = 2000 } = options

  const [status, setStatus] = useState<SaveStatus>('idle')
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const savedTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const saveFnRef = useRef(saveFn)
  const contentRef = useRef(content)
  const lastSavedRef = useRef(lastSavedContent)
  const isSavingRef = useRef(false)

  // Keep refs up to date
  saveFnRef.current = saveFn
  contentRef.current = content
  lastSavedRef.current = lastSavedContent

  // Cleanup timers on unmount
  useEffect(() => {
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
      if (savedTimerRef.current) clearTimeout(savedTimerRef.current)
    }
  }, [])

  // Core save function
  const doSave = useCallback(async () => {
    const currentContent = contentRef.current
    // Don't save if content hasn't changed or already saving
    if (currentContent === lastSavedRef.current || isSavingRef.current) {
      return
    }

    isSavingRef.current = true
    setStatus('saving')

    try {
      await saveFnRef.current(currentContent)
      setStatus('saved')
      // Clear "saved" indicator after a delay
      if (savedTimerRef.current) clearTimeout(savedTimerRef.current)
      savedTimerRef.current = setTimeout(() => {
        setStatus('idle')
      }, savedDisplayMs)
    } catch {
      setStatus('error')
    } finally {
      isSavingRef.current = false
    }
  }, [savedDisplayMs])

  // Watch content changes and debounce save
  useEffect(() => {
    // If content matches last saved, nothing to do
    if (content === lastSavedContent) {
      // Only reset to idle if we're in 'unsaved' state
      if (status === 'unsaved') {
        setStatus('idle')
      }
      return
    }

    // Mark as unsaved
    setStatus('unsaved')

    // Debounce: schedule a save
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      doSave()
    }, debounceMs)

    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [content, lastSavedContent, debounceMs, doSave]) // eslint-disable-line react-hooks/exhaustive-deps

  // Flush: immediately save if there are pending changes
  const flush = useCallback(async () => {
    if (timerRef.current) {
      clearTimeout(timerRef.current)
      timerRef.current = null
    }
    if (contentRef.current !== lastSavedRef.current) {
      await doSave()
    }
  }, [doSave])

  // Intercept Ctrl+S to prevent browser "Save As" dialog
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault()
        // Trigger an immediate save
        flush()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [flush])

  return { status, flush }
}
