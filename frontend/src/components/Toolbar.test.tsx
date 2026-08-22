import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import Toolbar from './Toolbar'

describe('Toolbar', () => {
  const mockExportPDF = vi.fn()
  const mockExportDOCX = vi.fn()

  it('renders the app title', () => {
    render(
      <Toolbar currentFile={null} onExportPDF={mockExportPDF} onExportDOCX={mockExportDOCX} />
    )
    expect(screen.getByText('📚 Assistant Pédagogique')).toBeInTheDocument()
  })

  it('shows "Aucun fichier" when no file selected', () => {
    render(
      <Toolbar currentFile={null} onExportPDF={mockExportPDF} onExportDOCX={mockExportDOCX} />
    )
    expect(screen.getByText('Aucun fichier sélectionné')).toBeInTheDocument()
  })

  it('shows current file name when selected', () => {
    render(
      <Toolbar currentFile="B1/unit5.md" onExportPDF={mockExportPDF} onExportDOCX={mockExportDOCX} />
    )
    expect(screen.getByText('B1/unit5.md')).toBeInTheDocument()
  })

  it('hides export buttons when no file selected', () => {
    render(
      <Toolbar currentFile={null} onExportPDF={mockExportPDF} onExportDOCX={mockExportDOCX} />
    )
    expect(screen.queryByText('📄 PDF')).not.toBeInTheDocument()
    expect(screen.queryByText('📄 DOCX')).not.toBeInTheDocument()
  })

  it('shows export buttons when file is selected', () => {
    render(
      <Toolbar currentFile="B1/unit5.md" onExportPDF={mockExportPDF} onExportDOCX={mockExportDOCX} />
    )
    expect(screen.getByText('📄 PDF')).toBeInTheDocument()
    expect(screen.getByText('📄 DOCX')).toBeInTheDocument()
  })

  it('calls onExportPDF when PDF button clicked', () => {
    render(
      <Toolbar currentFile="B1/unit5.md" onExportPDF={mockExportPDF} onExportDOCX={mockExportDOCX} />
    )
    fireEvent.click(screen.getByText('📄 PDF'))
    expect(mockExportPDF).toHaveBeenCalledTimes(1)
  })

  it('calls onExportDOCX when DOCX button clicked', () => {
    render(
      <Toolbar currentFile="B1/unit5.md" onExportPDF={mockExportPDF} onExportDOCX={mockExportDOCX} />
    )
    fireEvent.click(screen.getByText('📄 DOCX'))
    expect(mockExportDOCX).toHaveBeenCalledTimes(1)
  })
})
