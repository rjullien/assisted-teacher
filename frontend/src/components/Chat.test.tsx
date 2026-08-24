import { screen, fireEvent, waitFor, act } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderWithI18n } from '../test/i18n-wrapper'
import Chat, { ChatSession, emptyChatSession } from './Chat'

describe('Chat', () => {
  const mockOnInsert = vi.fn()
  const defaultProps = {
    currentFile: null as string | null,
    onInsert: mockOnInsert,
    programme: null,
  }

  beforeEach(() => {
    vi.resetAllMocks()
  })

  // Helper: render Chat and wait for WebSocket to connect
  async function renderConnected(props?: { currentFile?: string | null; userName?: string; session?: ChatSession; onSessionChange?: (s: ChatSession) => void }) {
    const result = renderWithI18n(
      <Chat {...defaultProps} {...props} currentFile={props?.currentFile ?? null} />
    )
    // Wait for the mock WebSocket onopen (setTimeout 0) to fire
    await act(async () => {
      await new Promise((r) => setTimeout(r, 10))
    })
    return result
  }

  it('renders the chat header', () => {
    renderWithI18n(<Chat {...defaultProps} />)
    expect(screen.getByText(/Assistant IA/)).toBeInTheDocument()
  })

  it('shows example prompts when empty', () => {
    renderWithI18n(<Chat {...defaultProps} />)
    expect(screen.getByText(/Posez une question/)).toBeInTheDocument()
  })

  it('has an input textarea and send button', async () => {
    await renderConnected({ currentFile: 'B1/unit5.md' })
    expect(screen.getByPlaceholderText("Demandez à l'IA...")).toBeInTheDocument()
    expect(screen.getByText('Envoyer')).toBeInTheDocument()
  })

  it('send button is disabled when input is empty', async () => {
    await renderConnected({ currentFile: 'B1/unit5.md' })
    const btn = screen.getByText('Envoyer')
    expect(btn).toBeDisabled()
  })

  it('send button is enabled when input has text and WS is connected', async () => {
    await renderConnected({ currentFile: 'B1/unit5.md' })
    const textarea = screen.getByPlaceholderText("Demandez à l'IA...")
    fireEvent.change(textarea, { target: { value: 'Hello' } })
    const btn = screen.getByText('Envoyer')
    expect(btn).not.toBeDisabled()
  })

  it('displays user message after sending', async () => {
    await renderConnected({ currentFile: 'B1/unit5.md' })

    const textarea = screen.getByPlaceholderText("Demandez à l'IA...")
    fireEvent.change(textarea, { target: { value: 'Generate exercises' } })
    fireEvent.click(screen.getByText('Envoyer'))

    await waitFor(() => {
      expect(screen.getByText('Generate exercises')).toBeInTheDocument()
    })

    // Input should be cleared
    expect(textarea).toHaveValue('')
  })

  it('sends Enter to submit (without Shift)', async () => {
    await renderConnected({ currentFile: 'B1/unit5.md' })

    const textarea = screen.getByPlaceholderText("Demandez à l'IA...")
    fireEvent.change(textarea, { target: { value: 'Test message' } })
    fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: false })

    await waitFor(() => {
      expect(screen.getByText('Test message')).toBeInTheDocument()
    })
  })

  it('Shift+Enter does NOT submit', async () => {
    await renderConnected({ currentFile: 'B1/unit5.md' })

    const textarea = screen.getByPlaceholderText("Demandez à l'IA...")
    fireEvent.change(textarea, { target: { value: 'multiline' } })
    fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: true })

    // Message should NOT appear in chat messages (only in textarea)
    const chatMessages = document.querySelector('.chat-messages')
    expect(chatMessages?.textContent).not.toContain('multiline')
    // But textarea should still have content
    expect(textarea).toHaveValue('multiline')
  })

  it('shows niveau badge when programme is provided', () => {
    const programme = {
      niveau: 'Seconde',
      cecrl: { LVA: 'B1+', LVB: 'A2+', LVC: 'A1/A2' },
      axes_culturels: [],
      contraintes: { axes_a_traiter: 5, axes_total: 6, axe_obligatoire: 6, note: '' },
      competences: {},
      grammaire: [],
      vocabulaire_thematique: {},
    }
    renderWithI18n(<Chat {...defaultProps} programme={programme} />)
    expect(screen.getByText('Seconde — B1+')).toBeInTheDocument()
  })

  it('session persists across unmount/remount', async () => {
    // Use lifted session state to simulate parent-managed persistence.
    // We pre-populate the session with a message, then verify it survives unmount/remount.
    const sessionWithMessage: ChatSession = {
      ...emptyChatSession,
      messages: [
        { id: 'msg-1', role: 'user', content: 'Hello from session' },
        { id: 'msg-2', role: 'assistant', content: 'Hi there!' },
      ],
      input: 'draft text',
    }
    const onSessionChange = vi.fn()

    // First mount - verify the messages show
    const { unmount } = renderWithI18n(
      <Chat {...defaultProps} currentFile="B1/unit5.md" session={sessionWithMessage} onSessionChange={onSessionChange} />
    )

    await act(async () => {
      await new Promise((r) => setTimeout(r, 10))
    })

    expect(screen.getByText('Hello from session')).toBeInTheDocument()
    expect(screen.getByText('Hi there!')).toBeInTheDocument()
    // Draft text is in the textarea
    expect(screen.getByPlaceholderText("Demandez à l'IA...")).toHaveValue('draft text')

    // Unmount
    unmount()

    // Remount with the same session object (simulating parent holding state)
    renderWithI18n(
      <Chat {...defaultProps} currentFile="B1/unit5.md" session={sessionWithMessage} onSessionChange={onSessionChange} />
    )

    await act(async () => {
      await new Promise((r) => setTimeout(r, 10))
    })

    // Messages and draft should still be visible
    expect(screen.getByText('Hello from session')).toBeInTheDocument()
    expect(screen.getByText('Hi there!')).toBeInTheDocument()
    expect(screen.getByPlaceholderText("Demandez à l'IA...")).toHaveValue('draft text')
  })

  it('identity is in every system prompt', async () => {
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

    await renderConnected({ currentFile: 'B1/unit5.md', userName: 'Alice' })

    const textarea = screen.getByPlaceholderText("Demandez à l'IA...")

    // Send first message
    fireEvent.change(textarea, { target: { value: 'First question' } })
    fireEvent.click(screen.getByText('Envoyer'))

    await waitFor(() => {
      expect(screen.getByText('First question')).toBeInTheDocument()
    })

    // Simulate WS completing the first response via the actual WS instance
    const lastWs = wsInstances[wsInstances.length - 1]
    await act(async () => {
      lastWs.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ seq: 1, type: 'meta', jobId: 'job1' }) }))
      lastWs.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ seq: 2, type: 'done', reply: 'Response 1' }) }))
    })

    // Wait for loading state to clear (button text returns to "Envoyer")
    await waitFor(() => {
      expect(screen.getByText('Envoyer')).toBeInTheDocument()
    })

    // Send second message (type first so button becomes enabled)
    fireEvent.change(textarea, { target: { value: 'Second question' } })

    await waitFor(() => {
      expect(screen.getByText('Envoyer')).not.toBeDisabled()
    })

    fireEvent.click(screen.getByText('Envoyer'))

    await waitFor(() => {
      expect(screen.getByText('Second question')).toBeInTheDocument()
    })

    // Collect all sent payloads
    const allSent = wsInstances.flatMap((i) => i.sentMessages)
    const promptPayloads = allSent
      .map((s) => JSON.parse(s))
      .filter((p: { type: string }) => p.type === 'prompt')

    expect(promptPayloads.length).toBe(2)
    expect(promptPayloads[0].system).toContain('Tu parles avec Alice.')
    expect(promptPayloads[1].system).toContain('Tu parles avec Alice.')

    // Restore original mock
    vi.stubGlobal('WebSocket', OrigMock)
  })
})
