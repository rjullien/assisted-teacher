import { screen } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import { renderWithI18n } from '../test/i18n-wrapper'
import Editor from './Editor'

// Mock Milkdown — ProseMirror requires a full browser DOM that jsdom doesn't provide.
vi.mock('@milkdown/react', () => ({
  Milkdown: () => <div data-testid="milkdown-editor">Milkdown WYSIWYG Editor</div>,
  MilkdownProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useEditor: () => ({ get: vi.fn(), loading: false }),
}))

vi.mock('@milkdown/core', () => ({
  Editor: { make: vi.fn(() => ({ config: vi.fn().mockReturnThis(), use: vi.fn().mockReturnThis() })) },
  defaultValueCtx: 'defaultValueCtx',
  rootCtx: 'rootCtx',
  editorViewCtx: 'editorViewCtx',
}))

vi.mock('@milkdown/preset-commonmark', () => ({ commonmark: [] }))
vi.mock('@milkdown/preset-gfm', () => ({ gfm: [] }))
vi.mock('@milkdown/plugin-history', () => ({ history: [] }))
vi.mock('@milkdown/plugin-listener', () => ({
  listener: [],
  listenerCtx: 'listenerCtx',
}))

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
    renderWithI18n(<Editor {...defaultProps} />)
    expect(screen.getByText(/Sélectionnez un fichier/)).toBeInTheDocument()
  })

  it('shows file path in header when file is selected', () => {
    renderWithI18n(<Editor {...defaultProps} content="# Test" lastSavedContent="# Test" filePath="B1/unit5.md" />)
    expect(screen.getByText('B1/unit5.md')).toBeInTheDocument()
  })

  it('renders Milkdown WYSIWYG editor when file is selected', () => {
    renderWithI18n(<Editor {...defaultProps} content="# Test" lastSavedContent="# Test" filePath="B1/unit5.md" />)
    expect(screen.getByTestId('milkdown-editor')).toBeInTheDocument()
  })

  it('does not render Milkdown when no file is selected', () => {
    renderWithI18n(<Editor {...defaultProps} />)
    expect(screen.queryByTestId('milkdown-editor')).not.toBeInTheDocument()
  })

  it('shows unsaved indicator when content differs from lastSavedContent', () => {
    renderWithI18n(<Editor {...defaultProps} content="modified" lastSavedContent="original" filePath="test.md" />)
    expect(screen.getByText(/Non sauvegardé/)).toBeInTheDocument()
  })

  it('does not show indicator when content matches lastSavedContent', () => {
    renderWithI18n(<Editor {...defaultProps} content="same" lastSavedContent="same" filePath="test.md" />)
    expect(screen.queryByText(/Non sauvegardé/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Sauvegardé/)).not.toBeInTheDocument()
  })
})
