import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import Chat from './Chat'

// Helper to get the mock WebSocket instance
function getLastWebSocket(): any {
  // Access the last created instance through the mock
  const instances = (WebSocket as any)
  return instances
}

describe('Chat', () => {
  const mockOnInsert = vi.fn()

  beforeEach(() => {
    vi.resetAllMocks()
  })

  it('renders the chat header', () => {
    render(<Chat currentFile={null} onInsert={mockOnInsert} />)
    expect(screen.getByText('💬 Assistant IA')).toBeInTheDocument()
  })

  it('shows example prompts when empty', () => {
    render(<Chat currentFile={null} onInsert={mockOnInsert} />)
    expect(screen.getByText(/Posez une question/)).toBeInTheDocument()
    expect(screen.getByText(/gap-fill/)).toBeInTheDocument()
  })

  it('has an input textarea and send button', () => {
    render(<Chat currentFile="B1/unit5.md" onInsert={mockOnInsert} />)
    expect(screen.getByPlaceholderText("Demandez à l'IA...")).toBeInTheDocument()
    expect(screen.getByText('Envoyer')).toBeInTheDocument()
  })

  it('send button is disabled when input is empty', () => {
    render(<Chat currentFile="B1/unit5.md" onInsert={mockOnInsert} />)
    const btn = screen.getByText('Envoyer')
    expect(btn).toBeDisabled()
  })

  it('send button is enabled when input has text', () => {
    render(<Chat currentFile="B1/unit5.md" onInsert={mockOnInsert} />)
    const textarea = screen.getByPlaceholderText("Demandez à l'IA...")
    fireEvent.change(textarea, { target: { value: 'Hello' } })
    const btn = screen.getByText('Envoyer')
    expect(btn).not.toBeDisabled()
  })

  it('displays user message after sending', async () => {
    render(<Chat currentFile="B1/unit5.md" onInsert={mockOnInsert} />)

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
    render(<Chat currentFile="B1/unit5.md" onInsert={mockOnInsert} />)

    const textarea = screen.getByPlaceholderText("Demandez à l'IA...")
    fireEvent.change(textarea, { target: { value: 'Test message' } })
    fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: false })

    await waitFor(() => {
      expect(screen.getByText('Test message')).toBeInTheDocument()
    })
  })

  it('Shift+Enter does NOT submit', () => {
    render(<Chat currentFile="B1/unit5.md" onInsert={mockOnInsert} />)

    const textarea = screen.getByPlaceholderText("Demandez à l'IA...")
    fireEvent.change(textarea, { target: { value: 'multiline' } })
    fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: true })

    // Message should NOT appear in chat messages (only in textarea)
    const chatMessages = document.querySelector('.chat-messages')
    expect(chatMessages?.textContent).not.toContain('multiline')
    // But textarea should still have content
    expect(textarea).toHaveValue('multiline')
  })
})
