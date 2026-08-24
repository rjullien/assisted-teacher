import { screen, fireEvent, waitFor, act } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderWithI18n } from '../test/i18n-wrapper'
import Chat, { ChatSession, emptyChatSession, MAX_INLINED_FILE_CHARS } from './Chat'

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
  async function renderConnected(props?: { currentFile?: string | null; fileContent?: string; agent?: 'lya' | 'pi'; userName?: string; session?: ChatSession; onSessionChange?: (s: ChatSession) => void; onFileChanged?: (path: string) => void }) {
    const result = renderWithI18n(
      <Chat {...defaultProps} {...props} currentFile={props?.currentFile ?? null} />
    )
    // Wait for the mock WebSocket onopen (setTimeout 0) to fire
    await act(async () => {
      await new Promise((r) => setTimeout(r, 10))
    })
    return result
  }

  type TrackedWS = { sentMessages: string[]; onmessage: ((ev: MessageEvent) => void) | null }

  /**
   * Wraps the global MockWebSocket so every instance built during the test is
   * captured, which is the only way to read the raw payloads Chat sent.
   * Same pattern as 'identity is in every system prompt' below.
   */
  function trackWebSockets(): { instances: TrackedWS[]; restore: () => void } {
    const instances: TrackedWS[] = []
    const OrigMock = globalThis.WebSocket as unknown as new (url: string) => TrackedWS & { onopen: ((ev: Event) => void) | null; readyState: number }
    const WrappedWS = function (this: unknown, url: string) {
      const instance = new OrigMock(url)
      instances.push(instance)
      return instance
    } as unknown as typeof WebSocket
    Object.assign(WrappedWS, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 })
    Object.defineProperty(WrappedWS, 'prototype', { value: OrigMock.prototype, writable: false })
    vi.stubGlobal('WebSocket', WrappedWS)
    return { instances, restore: () => vi.stubGlobal('WebSocket', OrigMock) }
  }

  /** Types a question, sends it, and returns the last `prompt` payload put on the wire. */
  async function sendPrompt(instances: TrackedWS[], question: string) {
    const textarea = screen.getByPlaceholderText("Demandez à l'IA...")
    fireEvent.change(textarea, { target: { value: question } })
    fireEvent.click(screen.getByText('Envoyer'))
    await waitFor(() => {
      expect(screen.getByText(question)).toBeInTheDocument()
    })
    const payloads = instances
      .flatMap((i) => i.sentMessages)
      .map((s) => JSON.parse(s))
      .filter((p: { type: string }) => p.type === 'prompt')
    expect(payloads.length).toBeGreaterThan(0)
    return payloads[payloads.length - 1] as { content: string; mode: string; currentFile?: string }
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

  // --- File context (mode Desk vs mode Pi) ---------------------------------
  //
  // Lya (Hermes) runs in another pod and cannot open the app's workspace, so in
  // mode Desk the file text has to be carried inside the prompt. pi runs in this
  // pod with a `read` tool, so it must keep receiving the path only.

  const SAMPLE_FILE = '# Unit 5 — Travel\n\nThe quick brown fox jumps over the lazy dog.'

  it('mode Desk inlines the file content into the prompt', async () => {
    const ws = trackWebSockets()
    await renderConnected({ currentFile: 'B1/unit5.md', fileContent: SAMPLE_FILE, agent: 'lya' })

    const payload = await sendPrompt(ws.instances, 'Complète ce cours')

    expect(payload.mode).toBe('desk')
    // The whole point of the fix: the text, not just the name.
    expect(payload.content).toContain('The quick brown fox jumps over the lazy dog.')
    expect(payload.content).toContain('# Unit 5 — Travel')
    expect(payload.content).toContain('B1/unit5.md')
    // The question must stay identifiable and come last, after the document.
    expect(payload.content.trimEnd().endsWith('Complète ce cours')).toBe(true)
    // The path still travels as its own field for the backend log line.
    expect(payload.currentFile).toBe('B1/unit5.md')

    ws.restore()
  })

  it('mode Pi sends the path only, never the content', async () => {
    const ws = trackWebSockets()
    await renderConnected({ currentFile: 'B1/unit5.md', fileContent: SAMPLE_FILE, agent: 'pi' })

    const payload = await sendPrompt(ws.instances, 'Complète ce cours')

    expect(payload.mode).toBe('pi')
    expect(payload.content).not.toContain('The quick brown fox jumps over the lazy dog.')
    expect(payload.content).toContain('[Contexte: je travaille sur le fichier "B1/unit5.md"]')

    ws.restore()
  })

  it('mode Desk truncates an oversized file and flags the truncation', async () => {
    const ws = trackWebSockets()
    const tail = 'CECI_EST_LA_FIN_DU_FICHIER'
    const oversized = 'a'.repeat(MAX_INLINED_FILE_CHARS + 500) + tail
    await renderConnected({ currentFile: 'B1/long.md', fileContent: oversized, agent: 'lya' })

    const payload = await sendPrompt(ws.instances, 'Résume ce cours')

    // Everything past the cap is dropped...
    expect(payload.content).not.toContain(tail)
    expect(payload.content).toContain('a'.repeat(100))
    // ...and Lya is told so, rather than believing the file ends there.
    expect(payload.content).toContain('Contenu tronqué')
    expect(payload.content.length).toBeLessThan(oversized.length)
    // The question survives the truncation.
    expect(payload.content.trimEnd().endsWith('Résume ce cours')).toBe(true)

    ws.restore()
  })

  it('mode Desk falls back to the path when the file has no content yet', async () => {
    const ws = trackWebSockets()
    await renderConnected({ currentFile: 'B1/empty.md', fileContent: '', agent: 'lya' })

    const payload = await sendPrompt(ws.instances, 'Que faire ?')

    expect(payload.content).toBe('[Contexte: je travaille sur le fichier "B1/empty.md"]\n\nQue faire ?')

    ws.restore()
  })

  // --- Hermes file tools (read_file / write_file / patch_file) --------------
  //
  // The tool loop in mode Desk names its tools read_file, write_file and
  // patch_file. Before they were declared in tools.ts they were treated as
  // generic tools: only a transient "🔧 write_file" status line, no audit trail
  // in the thread and no "file updated" state on the answer.

  it('keeps a write_file event in the thread and marks the answer as having written files', async () => {
    const ws = trackWebSockets()
    const onFileChanged = vi.fn()
    await renderConnected({ currentFile: 'B1/unit5.md', fileContent: SAMPLE_FILE, agent: 'lya', onFileChanged })

    const lastWs = ws.instances[ws.instances.length - 1]
    await act(async () => {
      lastWs.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ seq: 1, type: 'meta', jobId: 'job1' }) }))
      lastWs.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ seq: 2, type: 'tool', tool: { name: 'write_file', path: 'B1/unit5.md', status: 'done' } }) }))
      lastWs.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ seq: 3, type: 'tool', tool: { name: 'file_changed', path: 'B1/unit5.md' } }) }))
      lastWs.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ seq: 4, type: 'done', reply: 'Fichier complété.' }) }))
    })

    // Persisted as a tool message, not just flashed in the transient status line.
    const toolMessages = document.querySelectorAll('.chat-message.tool')
    expect(toolMessages.length).toBe(1)
    expect(toolMessages[0].textContent).toBe('✏️ Écriture de B1/unit5.md')

    // file_changed is plumbing: it refreshes the editor and is never displayed.
    expect(onFileChanged).toHaveBeenCalledWith('B1/unit5.md')
    expect(document.querySelector('.chat-messages')?.textContent).not.toContain('file_changed')

    // jobWroteFiles: the answer offers the "file updated" note instead of Insert.
    expect(screen.getByText('Fichier mis à jour par Pi.')).toBeInTheDocument()
    expect(screen.queryByText(/Insérer/)).not.toBeInTheDocument()

    ws.restore()
  })

  it('keeps a read_file event in the thread', async () => {
    const ws = trackWebSockets()
    await renderConnected({ currentFile: 'B1/unit5.md', fileContent: SAMPLE_FILE, agent: 'lya' })

    const lastWs = ws.instances[ws.instances.length - 1]
    await act(async () => {
      lastWs.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ seq: 1, type: 'tool', tool: { name: 'read_file', path: 'B1/unit5.md', status: 'done' } }) }))
    })

    const toolMessages = document.querySelectorAll('.chat-message.tool')
    expect(toolMessages.length).toBe(1)
    expect(toolMessages[0].textContent).toBe('📄 Lecture de B1/unit5.md')

    ws.restore()
  })
})
