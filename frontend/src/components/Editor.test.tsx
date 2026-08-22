import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import Editor from './Editor'

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

  it('renders textarea with content when file is selected', () => {
    render(
      <Editor content="# Test content" onChange={mockOnChange} onSave={mockOnSave} filePath="B1/unit5.md" />
    )
    const textarea = screen.getByPlaceholderText('Écrivez en Markdown...')
    expect(textarea).toBeInTheDocument()
    expect(textarea).toHaveValue('# Test content')
  })

  it('does not render textarea when no file is selected', () => {
    render(
      <Editor content="" onChange={mockOnChange} onSave={mockOnSave} filePath={null} />
    )
    expect(screen.queryByPlaceholderText('Écrivez en Markdown...')).not.toBeInTheDocument()
  })

  it('calls onChange when user types', () => {
    render(
      <Editor content="Hello" onChange={mockOnChange} onSave={mockOnSave} filePath="test.md" />
    )
    const textarea = screen.getByPlaceholderText('Écrivez en Markdown...')
    fireEvent.change(textarea, { target: { value: 'Hello world' } })
    expect(mockOnChange).toHaveBeenCalledWith('Hello world')
  })
})
