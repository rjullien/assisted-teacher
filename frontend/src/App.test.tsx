import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ReactNode } from 'react'

/**
 * App is tested through stubs for the three heavy children (file tree, Milkdown
 * editor, chat) because the behaviour under test is the WIRING between them:
 * what App does with a `file_changed` reported by an agent, and whether the
 * callback it hands to Chat is stable.
 *
 * That wiring is where the regression lived: shouldReloadBuffer was covered as a
 * pure function while the callback around it changed identity on every keystroke,
 * which recycled the chat WebSocket. A test of the pure function could not see it.
 */

const h = vi.hoisted(() => ({
  getText: vi.fn(),
  getJSON: vi.fn(),
  putText: vi.fn(),
  // Every distinct onFileChanged identity Chat ever received.
  seenCallbacks: new Set<unknown>(),
  // The latest one, so the test can fire a file_changed the way Chat does.
  latest: { onFileChanged: undefined as undefined | ((p: string) => unknown) },
}))

vi.mock('./api', () => ({
  getText: h.getText,
  getJSON: h.getJSON,
  putText: h.putText,
  postBlob: vi.fn(),
  handleAuthExpired: vi.fn(() => false),
}))

vi.mock('allotment', () => {
  const Pane = ({ children }: { children?: ReactNode }) => <div>{children}</div>
  const Allotment = ({ children }: { children?: ReactNode }) => <div>{children}</div>
  Allotment.Pane = Pane
  return { Allotment }
})

vi.mock('./components/FileTree', () => ({
  default: ({ onSelect }: { onSelect: (p: string) => void }) => (
    <button onClick={() => onSelect('B1/unit5.md')}>ouvrir-fichier</button>
  ),
}))

vi.mock('./components/Editor', () => ({
  default: ({ content, onChange }: { content: string; onChange: (c: string) => void }) => (
    <textarea
      data-testid="editor"
      value={content}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}))

vi.mock('./components/Chat', async () => {
  const actual = await vi.importActual<typeof import('./components/Chat')>('./components/Chat')
  return {
    ...actual,
    default: ({ onFileChanged }: { onFileChanged?: (p: string) => unknown }) => {
      h.seenCallbacks.add(onFileChanged)
      h.latest.onFileChanged = onFileChanged
      return <div data-testid="chat" />
    },
  }
})

import App from './App'

describe('App file_changed wiring', () => {
  beforeEach(() => {
    h.getText.mockReset()
    h.getJSON.mockReset()
    h.putText.mockReset()
    h.seenCallbacks.clear()
    h.latest.onFileChanged = undefined
    localStorage.clear()
    h.getJSON.mockResolvedValue({ ok: false })
    h.getText.mockResolvedValue({ ok: true, data: '# version disque' })
    // Desktop layout: the mobile branch renders MobileLayout instead.
    window.innerWidth = 1200
    window.matchMedia = ((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia
  })

  /** Mounts App and opens B1/unit5.md through the (stubbed) file tree. */
  async function renderWithOpenFile() {
    const result = render(<App />)
    await act(async () => {
      fireEvent.click(screen.getByText('ouvrir-fichier'))
    })
    await waitFor(() => {
      expect(screen.getByTestId('editor')).toHaveValue('# version disque')
    })
    h.getText.mockClear()
    return result
  }

  it('reloads the open file when the buffer holds no unsaved edits', async () => {
    await renderWithOpenFile()

    h.getText.mockResolvedValue({ ok: true, data: '# écrit par Lya' })
    let reloaded: unknown
    await act(async () => {
      reloaded = await h.latest.onFileChanged?.('B1/unit5.md')
    })

    expect(h.getText).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('editor')).toHaveValue('# écrit par Lya')
    expect(reloaded).toBe(true)
  })

  it('keeps unsaved edits and reports the divergence instead of reloading', async () => {
    await renderWithOpenFile()

    // The teacher types: the buffer is now dirty and is the only copy of that text.
    fireEvent.change(screen.getByTestId('editor'), { target: { value: '# texte du prof' } })

    let reloaded: unknown
    await act(async () => {
      reloaded = await h.latest.onFileChanged?.('B1/unit5.md')
    })

    expect(h.getText).not.toHaveBeenCalled()
    expect(screen.getByTestId('editor')).toHaveValue('# texte du prof')
    // false is what makes Chat warn that the editor and the disk now differ.
    expect(reloaded).toBe(false)
  })

  it('does not report a divergence for a file that is not open', async () => {
    await renderWithOpenFile()

    fireEvent.change(screen.getByTestId('editor'), { target: { value: '# texte du prof' } })

    let reloaded: unknown
    await act(async () => {
      reloaded = await h.latest.onFileChanged?.('B1/autre.md')
    })

    expect(h.getText).not.toHaveBeenCalled()
    expect(reloaded).toBe(true)
  })

  // The regression itself: onFileChanged used to depend on fileContent and
  // lastSavedContent, so every keystroke handed Chat a new function, which
  // rebuilt the chat WebSocket.
  it('hands Chat the same onFileChanged while the teacher types', async () => {
    await renderWithOpenFile()
    const afterOpen = h.seenCallbacks.size

    for (const typed of ['# a', '# ab', '# abc', '# abcd']) {
      fireEvent.change(screen.getByTestId('editor'), { target: { value: typed } })
    }
    await waitFor(() => {
      expect(screen.getByTestId('editor')).toHaveValue('# abcd')
    })

    expect(h.seenCallbacks.size).toBe(afterOpen)
    expect(h.seenCallbacks.size).toBe(1)
  })
})
