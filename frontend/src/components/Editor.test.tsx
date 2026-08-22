import { render, screen } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import Editor from './Editor'

// Mock Milkdown — it requires a browser DOM with full ProseMirror support
// which jsdom doesn't provide. We mock the heavy parts.
vi.mock('@milkdown/react', () => ({
  Milkdown: () => <div data-testid="milkdown-editor">Milkdown Editor</div>,
  MilkdownProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useEditor: () => ({ get: vi.fn() }),
}))

vi.mock('@milkdown/crepe', () => ({
  Crepe: vi.fn().mockImplementation(() => ({
    on: vi.fn(),
  })),
}))

describe('Editor', () => {
  const mockOnChange = vi.fn()
  const mockOnSave = vi.fn()

  it('shows empty state when no file selected', () => {
    render(
      <Editor content="" onChange={mockOnChange} onSave={mockOnSave} filePath={null} />
    )
    expect(screen.getByText(/Sélectionnez un cours/)).toBeInTheDocument()
  })

  it('shows file path in header when file is selected', () => {
    render(
      <Editor content="# Test" onChange={mockOnChange} onSave={mockOnSave} filePath="B1/unit5.md" />
    )
    expect(screen.getByText('B1/unit5.md')).toBeInTheDocument()
  })

  it('shows save hint in header', () => {
    render(
      <Editor content="# Test" onChange={mockOnChange} onSave={mockOnSave} filePath="B1/unit5.md" />
    )
    expect(screen.getByText(/Ctrl\+S/)).toBeInTheDocument()
  })

  it('renders Milkdown editor when file is selected', () => {
    render(
      <Editor content="# Test" onChange={mockOnChange} onSave={mockOnSave} filePath="B1/unit5.md" />
    )
    expect(screen.getByTestId('milkdown-editor')).toBeInTheDocument()
  })

  it('does not render Milkdown when no file is selected', () => {
    render(
      <Editor content="" onChange={mockOnChange} onSave={mockOnSave} filePath={null} />
    )
    expect(screen.queryByTestId('milkdown-editor')).not.toBeInTheDocument()
  })
})
