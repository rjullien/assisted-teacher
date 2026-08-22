import { useEffect, useState } from 'react'
import { getJSON, putText, request, postJSON, handleAuthExpired } from '../api'

interface FileNode {
  name: string
  path: string
  isDir: boolean
  children?: FileNode[]
}

interface FileTreeProps {
  onSelect: (path: string) => void
  onRefresh: () => void
  refreshKey: number
}

/**
 * Sanitize a filename: replace spaces and forbidden chars with _,
 * collapse multiple underscores, trim leading/trailing _.
 */
function sanitizeFilename(raw: string): string {
  // Strip leading/trailing whitespace
  let name = raw.trim()
  // Replace all whitespace and forbidden chars with _
  name = name.replace(/[\s?!…,;:'"«»()[\]{}<>@#$%^&*+=|\\\/~`]+/g, '_')
  // Collapse multiple underscores
  name = name.replace(/_+/g, '_')
  // Remove leading/trailing underscores
  name = name.replace(/^_+|_+$/g, '')
  return name
}

export default function FileTree({ onSelect, onRefresh, refreshKey }: FileTreeProps) {
  const [tree, setTree] = useState<FileNode[]>([])
  const [activePath, setActivePath] = useState<string | null>(null)

  const loadTree = async () => {
    const res = await getJSON<FileNode[]>('/api/files')
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok && res.data) {
      setTree(res.data)
    }
  }

  useEffect(() => {
    loadTree()
  }, [refreshKey])

  const handleSelect = (node: FileNode) => {
    if (!node.isDir) {
      setActivePath(node.path)
      onSelect(node.path)
    }
  }

  const handleNewFile = async () => {
    const raw = prompt('Nom du nouveau cours (ex: unit7)')
    if (!raw) return
    // Sanitize: remove .md if user typed it, sanitize the name, then add .md
    const withoutExt = raw.replace(/\.md$/i, '')
    const sanitized = sanitizeFilename(withoutExt)
    if (!sanitized) return
    const path = sanitized + '.md'
    // Use the sanitized name (without underscores) as the heading
    const heading = withoutExt.trim()
    const res = await putText(
      `/api/file?path=${encodeURIComponent(path)}`,
      `# ${heading}\n\n`
    )
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok) {
      await loadTree()
      // Auto-select the new file
      setActivePath(path)
      onSelect(path)
    }
  }

  const handleDelete = async (node: FileNode) => {
    const confirmed = confirm(`Supprimer "${node.name}" ?`)
    if (!confirmed) return
    const res = await request(`/api/file?path=${encodeURIComponent(node.path)}`, {
      method: 'DELETE',
    })
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok) {
      // If deleted file was active, clear selection
      if (activePath === node.path) {
        setActivePath(null)
        onRefresh()
      }
      await loadTree()
    }
  }

  const handleRename = async (node: FileNode) => {
    const currentName = node.name.replace(/\.md$/i, '')
    const raw = prompt('Nouveau nom :', currentName)
    if (!raw || raw.trim() === currentName) return
    const sanitized = sanitizeFilename(raw.replace(/\.md$/i, ''))
    if (!sanitized) return
    const ext = node.name.endsWith('.md') ? '.md' : ''
    const newName = sanitized + ext
    // Compute new path (same directory)
    const dir = node.path.includes('/') ? node.path.substring(0, node.path.lastIndexOf('/') + 1) : ''
    const newPath = dir + newName
    const res = await postJSON('/api/files/rename', { from: node.path, to: newPath })
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok) {
      if (activePath === node.path) {
        setActivePath(newPath)
      }
      await loadTree()
    }
  }

  return (
    <div className="file-tree">
      <div className="file-tree-header">
        <h2>Mes cours</h2>
        <button onClick={handleNewFile} title="Nouveau cours">+ Nouveau</button>
      </div>
      {tree.length === 0 && (
        <div style={{ padding: '8px', fontSize: '12px', color: 'var(--text-muted)' }}>
          Aucun cours. Cliquez sur "+ Nouveau".
        </div>
      )}
      {tree.map((node) => (
        <TreeNode
          key={node.path}
          node={node}
          activePath={activePath}
          onSelect={handleSelect}
          onDelete={handleDelete}
          onRename={handleRename}
        />
      ))}
    </div>
  )
}

function TreeNode({
  node,
  activePath,
  onSelect,
  onDelete,
  onRename,
}: {
  node: FileNode
  activePath: string | null
  onSelect: (node: FileNode) => void
  onDelete: (node: FileNode) => void
  onRename: (node: FileNode) => void
}) {
  const [expanded, setExpanded] = useState(true)

  const handleClick = () => {
    if (node.isDir) {
      setExpanded(!expanded)
    } else {
      onSelect(node)
    }
  }

  return (
    <div>
      <div
        className={`file-tree-item ${node.isDir ? 'dir' : ''} ${
          activePath === node.path ? 'active' : ''
        }`}
        onClick={handleClick}
      >
        <span className="file-icon">{node.isDir ? (expanded ? '📂' : '📁') : '📄'}</span>
        <span className="file-name">{node.name}</span>
        {!node.isDir && (
          <span className="file-actions" onClick={(e) => e.stopPropagation()}>
            <button
              className="file-action-btn"
              onClick={() => onRename(node)}
              title="Renommer"
            >
              ✏️
            </button>
            <button
              className="file-action-btn"
              onClick={() => onDelete(node)}
              title="Supprimer"
            >
              🗑️
            </button>
          </span>
        )}
      </div>
      {node.isDir && expanded && node.children && (
        <div className="file-tree-children">
          {node.children.map((child) => (
            <TreeNode
              key={child.path}
              node={child}
              activePath={activePath}
              onSelect={onSelect}
              onDelete={onDelete}
              onRename={onRename}
            />
          ))}
        </div>
      )}
    </div>
  )
}
