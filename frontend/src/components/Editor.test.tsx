import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import Editor from './Editor'

describe('Editor', () => {
  const mockOnChange = vi.fn()
  const mockOnSave = vi.fn().mockResolvedValue(undefined)
  const defaultProps = {
    content: '',
    lastSavedContent: '',
    onChange: mockOnChange,
    onSave: mockOnSave,
    filePath: null as string | null,
  }

  it('shows empty state when no file selected', () => {
    render(<Editor {...defaultProps} />)
    expect(screen.getByText(/Sélectionnez un cours/)).toBeInTheDocument()
  })

  it('shows file path in header when file is selected', () => {
    render(<Editor {...defaultProps} content="# Test" lastSavedContent="# Test" filePath="B1/unit5.md" />)
    expect(screen.getByText('B1/unit5.md')).toBeInTheDocument()
  })

  it('does not show Ctrl+S hint (replaced by auto-save)', () => {
    render(<Editor {...defaultProps} content="# Test" lastSavedContent="# Test" filePath="B1/unit5.md" />)
    expect(screen.queryByText(/Ctrl\+S/)).not.toBeInTheDocument()
  })

  it('renders textarea with content when file is selected', () => {
    render(<Editor {...defaultProps} content="# Test content" lastSavedContent="# Test content" filePath="B1/unit5.md" />)
    const textarea = screen.getByPlaceholderText('Écrivez en Markdown...')
    expect(textarea).toBeInTheDocument()
    expect(textarea).toHaveValue('# Test content')
  })

  it('does not render textarea when no file is selected', () => {
    render(<Editor {...defaultProps} />)
    expect(screen.queryByPlaceholderText('Écrivez en Markdown...')).not.toBeInTheDocument()
  })

  it('calls onChange when user types', () => {
    render(<Editor {...defaultProps} content="Hello" lastSavedContent="Hello" filePath="test.md" />)
    const textarea = screen.getByPlaceholderText('Écrivez en Markdown...')
    fireEvent.change(textarea, { target: { value: 'Hello world' } })
    expect(mockOnChange).toHaveBeenCalledWith('Hello world')
  })

  it('shows unsaved indicator when content differs from lastSavedContent', () => {
    render(<Editor {...defaultProps} content="modified" lastSavedContent="original" filePath="test.md" />)
    expect(screen.getByText(/Non sauvegardé/)).toBeInTheDocument()
  })

  it('does not show indicator when content matches lastSavedContent', () => {
    render(<Editor {...defaultProps} content="same" lastSavedContent="same" filePath="test.md" />)
    expect(screen.queryByText(/Non sauvegardé/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Sauvegardé/)).not.toBeInTheDocument()
  })
})
