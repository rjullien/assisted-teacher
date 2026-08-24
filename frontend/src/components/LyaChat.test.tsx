import { screen, fireEvent, waitFor, act } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderWithI18n } from '../test/i18n-wrapper'
import LyaChat from './LyaChat'

describe('LyaChat', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  // Helper: render LyaChat and wait for WebSocket to connect
  async function renderConnected(props?: { userName?: string }) {
    const result = renderWithI18n(<LyaChat {...props} />)
    await act(async () => {
      await new Promise((r) => setTimeout(r, 10))
    })
    return result
  }

  it('renders the Lya header/title', () => {
    renderWithI18n(<LyaChat />)
    expect(screen.getByText('💬 Lya')).toBeInTheDocument()
  })

  it('shows empty state hint when no messages', () => {
    renderWithI18n(<LyaChat />)
    expect(
      screen.getByText(/Discute avec Lya/)
    ).toBeInTheDocument()
  })

  it('sends userName in system field on every message', async () => {
    // Track all WebSocket instances created during this test
    const wsInstances: Array<{ sentMessages: string[]; onmessage: ((ev: MessageEvent) => void) | null }> = []
    const OrigMock = globalThis.WebSocket as unknown as new (url: string) => { sentMessages: string[]; onmessage: ((ev: MessageEvent) => void) | null; onopen: ((ev: Event) => void) | null; readyState: number }

    // Replace WebSocket with a tracking wrapper
    const WrappedWS = function (this: unknown, url: string) {
      const instance = new OrigMock(url)
      wsInstances.push(instance)
      return instance
    } as unknown as typeof WebSocket
    Object.assign(WrappedWS, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 })
    Object.defineProperty(WrappedWS, 'prototype', { value: OrigMock.prototype, writable: false })
    vi.stubGlobal('WebSocket', WrappedWS)

    await renderConnected({ userName: 'Bob' })

    const textarea = screen.getByPlaceholderText('Écris à Lya...')

    // Send first message
    fireEvent.change(textarea, { target: { value: 'Hello Lya' } })
    fireEvent.click(screen.getByText('Envoyer'))

    await waitFor(() => {
      expect(screen.getByText('Hello Lya')).toBeInTheDocument()
    })

    // Simulate WS completing first response via the actual instance
    const lastWs = wsInstances[wsInstances.length - 1]
    await act(async () => {
      lastWs.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ seq: 1, type: 'meta', jobId: 'job1' }) }))
      lastWs.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ seq: 2, type: 'done', reply: 'Hi!' }) }))
    })

    // Wait for loading state to clear (button text returns to "Envoyer")
    await waitFor(() => {
      expect(screen.getByText('Envoyer')).toBeInTheDocument()
    })

    // Send second message (type first so button becomes enabled)
    fireEvent.change(textarea, { target: { value: 'Second msg' } })

    await waitFor(() => {
      expect(screen.getByText('Envoyer')).not.toBeDisabled()
    })

    fireEvent.click(screen.getByText('Envoyer'))

    await waitFor(() => {
      expect(screen.getByText('Second msg')).toBeInTheDocument()
    })

    // Verify system field contains userName in both prompts
    const allSent = wsInstances.flatMap((i) => i.sentMessages)
    const promptPayloads = allSent
      .map((s) => JSON.parse(s))
      .filter((p: { type: string }) => p.type === 'prompt')

    expect(promptPayloads.length).toBe(2)
    expect(promptPayloads[0].system).toBe('Tu parles avec Bob.')
    expect(promptPayloads[1].system).toBe('Tu parles avec Bob.')

    // Restore original mock
    vi.stubGlobal('WebSocket', OrigMock)
  })

  it('has no Desk sub-mode selector — mode Lya never touches files', async () => {
    await renderConnected()
    expect(screen.queryByText('Copie / insertion')).not.toBeInTheDocument()
    expect(screen.queryByText('Mise à jour directe')).not.toBeInTheDocument()
    expect(document.querySelector('.chat-workfile')).toBeNull()
  })

  it('has input and send button', async () => {
    await renderConnected()
    expect(screen.getByPlaceholderText('Écris à Lya...')).toBeInTheDocument()
    expect(screen.getByText('Envoyer')).toBeInTheDocument()
  })

  it('displays user message after sending', async () => {
    await renderConnected({ userName: 'Alice' })

    const textarea = screen.getByPlaceholderText('Écris à Lya...')
    fireEvent.change(textarea, { target: { value: 'Test message' } })
    fireEvent.click(screen.getByText('Envoyer'))

    await waitFor(() => {
      expect(screen.getByText('Test message')).toBeInTheDocument()
    })

    // Input should be cleared
    expect(textarea).toHaveValue('')
  })
})
