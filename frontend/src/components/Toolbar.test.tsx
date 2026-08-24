import { screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import { renderWithI18n } from '../test/i18n-wrapper'
import Toolbar from './Toolbar'

describe('Toolbar', () => {
  const mockExportPDF = vi.fn()
  const mockExportDOCX = vi.fn()
  const mockNiveauChange = vi.fn()
  const mockModeChange = vi.fn()
  const defaultProps = {
    currentFile: null as string | null,
    niveau: 'seconde' as const,
    mode: 'desk' as const,
    onNiveauChange: mockNiveauChange,
    onModeChange: mockModeChange,
    onExportPDF: mockExportPDF,
    onExportDOCX: mockExportDOCX,
  }

  it('renders the mode switcher', () => {
    renderWithI18n(<Toolbar {...defaultProps} />)
    expect(screen.getByText('Desk')).toBeInTheDocument()
    expect(screen.getByText('Lya')).toBeInTheDocument()
  })

  it('shows "Aucun fichier" when no file selected', () => {
    renderWithI18n(<Toolbar {...defaultProps} />)
    expect(screen.getByText('Aucun fichier sélectionné')).toBeInTheDocument()
  })

  it('shows current file name when selected', () => {
    renderWithI18n(<Toolbar {...defaultProps} currentFile="B1/unit5.md" />)
    expect(screen.getByText('B1/unit5.md')).toBeInTheDocument()
  })

  it('hides export buttons when no file selected', () => {
    renderWithI18n(<Toolbar {...defaultProps} />)
    expect(screen.queryByText('📄 PDF')).not.toBeInTheDocument()
    expect(screen.queryByText('📄 DOCX')).not.toBeInTheDocument()
  })

  it('shows export buttons when file is selected', () => {
    renderWithI18n(<Toolbar {...defaultProps} currentFile="B1/unit5.md" />)
    expect(screen.getByText('📄 PDF')).toBeInTheDocument()
    expect(screen.getByText('📄 DOCX')).toBeInTheDocument()
  })

  it('calls onExportPDF when PDF button clicked', () => {
    renderWithI18n(<Toolbar {...defaultProps} currentFile="B1/unit5.md" />)
    fireEvent.click(screen.getByText('📄 PDF'))
    expect(mockExportPDF).toHaveBeenCalledTimes(1)
  })

  it('calls onExportDOCX when DOCX button clicked', () => {
    renderWithI18n(<Toolbar {...defaultProps} currentFile="B1/unit5.md" />)
    fireEvent.click(screen.getByText('📄 DOCX'))
    expect(mockExportDOCX).toHaveBeenCalledTimes(1)
  })

  it('renders the niveau selector with correct default', () => {
    renderWithI18n(<Toolbar {...defaultProps} niveau="premiere" />)
    const select = screen.getByLabelText('Niveau :') as HTMLSelectElement
    expect(select.value).toBe('premiere')
  })

  it('calls onNiveauChange when niveau is changed', () => {
    renderWithI18n(<Toolbar {...defaultProps} />)
    const select = screen.getByLabelText('Niveau :')
    fireEvent.change(select, { target: { value: 'terminale' } })
    expect(mockNiveauChange).toHaveBeenCalledWith('terminale')
  })

  it('shows Pi button only when piAvailable=true', () => {
    // Without piAvailable (default false/undefined) - Pi button should NOT be visible
    const { unmount } = renderWithI18n(<Toolbar {...defaultProps} piAvailable={false} />)
    expect(screen.queryByText('Pi')).not.toBeInTheDocument()
    unmount()

    // With piAvailable=true - Pi button should be present and clickable
    renderWithI18n(<Toolbar {...defaultProps} piAvailable={true} />)
    const piButton = screen.getByText('Pi')
    expect(piButton).toBeInTheDocument()
    fireEvent.click(piButton)
    expect(mockModeChange).toHaveBeenCalledWith('pi')
  })
})
