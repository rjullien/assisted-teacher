import { screen, waitFor, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderWithI18n } from '../test/i18n-wrapper'
import FileTree from './FileTree'

const mockTree = [
  {
    name: 'B1',
    path: 'B1',
    isDir: true,
    children: [
      { name: 'unit5.md', path: 'B1/unit5.md', isDir: false },
      { name: 'unit6.md', path: 'B1/unit6.md', isDir: false },
    ],
  },
  {
    name: 'Vocab',
    path: 'Vocab',
    isDir: true,
    children: [
      { name: 'animals.md', path: 'Vocab/animals.md', isDir: false },
    ],
  },
]

/** Create a mock Response that matches what the api.ts request() function expects */
function mockJsonResponse(data: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: {
      get: (name: string) => {
        if (name.toLowerCase() === 'content-type') return 'application/json'
        return null
      },
    },
    json: () => Promise.resolve(data),
    text: () => Promise.resolve(JSON.stringify(data)),
  }
}

function mockTextResponse(text: string, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: {
      get: (name: string) => {
        if (name.toLowerCase() === 'content-type') return 'text/plain'
        return null
      },
    },
    json: () => Promise.reject(new Error('not json')),
    text: () => Promise.resolve(text),
  }
}

describe('FileTree', () => {
  const mockOnSelect = vi.fn()
  const mockOnRefresh = vi.fn()

  beforeEach(() => {
    vi.resetAllMocks()
    ;(globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValue(
      mockJsonResponse(mockTree)
    )
  })

  it('renders the header', async () => {
    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)
    expect(screen.getByText('Mes cours')).toBeInTheDocument()
  })

  it('fetches and displays file tree', async () => {
    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText('B1')).toBeInTheDocument()
    })

    expect(screen.getByText('Vocab')).toBeInTheDocument()
    expect(screen.getByText('unit5.md')).toBeInTheDocument()
    expect(screen.getByText('unit6.md')).toBeInTheDocument()
    expect(screen.getByText('animals.md')).toBeInTheDocument()
  })

  it('calls onSelect when clicking a file', async () => {
    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText('unit5.md')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByText('unit5.md'))
    expect(mockOnSelect).toHaveBeenCalledWith('B1/unit5.md')
  })

  it('does NOT call onSelect when clicking a directory', async () => {
    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText('B1')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByText('B1'))
    expect(mockOnSelect).not.toHaveBeenCalled()
  })

  it('collapses and expands directories on click', async () => {
    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText('unit5.md')).toBeInTheDocument()
    })

    // Click B1 to collapse
    fireEvent.click(screen.getByText('B1'))
    expect(screen.queryByText('unit5.md')).not.toBeInTheDocument()

    // Click B1 again to expand
    fireEvent.click(screen.getByText('B1'))
    await waitFor(() => {
      expect(screen.getByText('unit5.md')).toBeInTheDocument()
    })
  })

  it('shows empty state when no files', async () => {
    ;(globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValue(
      mockJsonResponse([])
    )

    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText(/Aucun cours/)).toBeInTheDocument()
    })
  })

  it('has new file and new folder buttons in header', async () => {
    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)
    // Header buttons for creating at root
    const newFileButtons = screen.getAllByTitle('Nouveau cours')
    expect(newFileButtons.length).toBeGreaterThanOrEqual(1)
    const newFolderButtons = screen.getAllByTitle('Nouveau dossier')
    expect(newFolderButtons.length).toBeGreaterThanOrEqual(1)
  })

  it('shows folder actions (new file, new folder, rename, delete) on directories', async () => {
    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText('B1')).toBeInTheDocument()
    })

    // Directories should have "Nouveau cours ici" button
    const newFileHereButtons = screen.getAllByTitle('Nouveau cours ici')
    expect(newFileHereButtons.length).toBeGreaterThanOrEqual(1)
    const newSubfolderButtons = screen.getAllByTitle('Nouveau sous-dossier')
    expect(newSubfolderButtons.length).toBeGreaterThanOrEqual(1)
  })

  it('refetches when refreshKey changes', async () => {
    const { rerender } = renderWithI18n(
      <FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />
    )

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledTimes(1)
    })

    rerender(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={1} />)

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledTimes(2)
    })
  })

  it('handles fetch error gracefully', async () => {
    ;(globalThis.fetch as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('Network error'))

    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText('Mes cours')).toBeInTheDocument()
    })
  })

  it('creates a new folder when clicking the folder button', async () => {
    vi.spyOn(window, 'prompt').mockReturnValue('Grammaire')
    ;(globalThis.fetch as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce(mockJsonResponse(mockTree)) // initial loadTree
      .mockResolvedValueOnce(mockJsonResponse({ status: 'ok' })) // POST mkdir
      .mockResolvedValueOnce(mockJsonResponse(mockTree)) // reload after create

    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getAllByTitle('Nouveau dossier').length).toBeGreaterThanOrEqual(1)
    })

    fireEvent.click(screen.getAllByTitle('Nouveau dossier')[0])

    await waitFor(() => {
      const mkdirCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.find(
        (call: unknown[]) => String(call[0]).includes('/api/files/mkdir')
      )
      expect(mkdirCall).toBeDefined()
    })
  })

  it('sanitizes filename on creation (spaces → _, special chars removed)', async () => {
    vi.spyOn(window, 'prompt').mockReturnValue('Mon cours super! et génial?')
    ;(globalThis.fetch as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce(mockJsonResponse(mockTree)) // initial loadTree
      .mockResolvedValueOnce(mockTextResponse('ok'))     // PUT file
      .mockResolvedValueOnce(mockJsonResponse(mockTree)) // reload after create

    renderWithI18n(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getAllByTitle('Nouveau cours').length).toBeGreaterThanOrEqual(1)
    })

    fireEvent.click(screen.getAllByTitle('Nouveau cours')[0])

    await waitFor(() => {
      const putCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.find(
        (call: unknown[]) => String(call[0]).includes('/api/file?path=')
      )
      expect(putCall).toBeDefined()
      const url = String(putCall![0])
      const pathParam = decodeURIComponent(url.split('path=')[1])
      expect(pathParam).toBe('Mon_cours_super_et_génial.md')
    })
  })
})
