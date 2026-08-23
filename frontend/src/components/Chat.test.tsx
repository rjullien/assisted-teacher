import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderWithI18n } from '../test/i18n-wrapper'
import Chat from './Chat'

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
  async function renderConnected(props?: { currentFile?: string | null }) {
    const result = renderWithI18n(
      <Chat {...defaultProps} currentFile={props?.currentFile ?? null} />
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
})
