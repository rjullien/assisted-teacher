import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
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

describe('FileTree', () => {
  const mockOnSelect = vi.fn()
  const mockOnRefresh = vi.fn()

  beforeEach(() => {
    vi.resetAllMocks()
    ;(globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve(mockTree),
    })
  })

  it('renders the header', async () => {
    render(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)
    expect(screen.getByText('Mes cours')).toBeInTheDocument()
  })

  it('fetches and displays file tree', async () => {
    render(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText('B1')).toBeInTheDocument()
    })

    expect(screen.getByText('Vocab')).toBeInTheDocument()
    expect(screen.getByText('unit5.md')).toBeInTheDocument()
    expect(screen.getByText('unit6.md')).toBeInTheDocument()
    expect(screen.getByText('animals.md')).toBeInTheDocument()
  })

  it('calls onSelect when clicking a file', async () => {
    render(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText('unit5.md')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByText('unit5.md'))
    expect(mockOnSelect).toHaveBeenCalledWith('B1/unit5.md')
  })

  it('does NOT call onSelect when clicking a directory', async () => {
    render(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText('B1')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByText('B1'))
    expect(mockOnSelect).not.toHaveBeenCalled()
  })

  it('collapses and expands directories on click', async () => {
    render(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

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
    ;(globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve([]),
    })

    render(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    await waitFor(() => {
      expect(screen.getByText(/Aucun cours/)).toBeInTheDocument()
    })
  })

  it('has a new file button', async () => {
    render(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)
    expect(screen.getByText('+ Nouveau')).toBeInTheDocument()
  })

  it('refetches when refreshKey changes', async () => {
    const { rerender } = render(
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

    render(<FileTree onSelect={mockOnSelect} onRefresh={mockOnRefresh} refreshKey={0} />)

    // Should not crash, show header at minimum
    await waitFor(() => {
      expect(screen.getByText('Mes cours')).toBeInTheDocument()
    })
  })
})
