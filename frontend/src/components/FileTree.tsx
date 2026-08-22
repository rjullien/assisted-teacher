import { useEffect, useState } from 'react'
import { getJSON, putText, handleAuthExpired } from '../api'

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

export default function FileTree({ onSelect, refreshKey }: FileTreeProps) {
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
    const name = prompt('Nom du nouveau cours (ex: unit7.md)')
    if (!name) return
    const path = name.endsWith('.md') ? name : name + '.md'
    const res = await putText(
      `/api/file?path=${encodeURIComponent(path)}`,
      `# ${name.replace(/\.md$/, '')}\n\n`
    )
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    loadTree()
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
        />
      ))}
    </div>
  )
}

function TreeNode({
  node,
  activePath,
  onSelect,
}: {
  node: FileNode
  activePath: string | null
  onSelect: (node: FileNode) => void
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
        <span>{node.isDir ? (expanded ? '📂' : '📁') : '📄'}</span>
        <span>{node.name}</span>
      </div>
      {node.isDir && expanded && node.children && (
        <div className="file-tree-children">
          {node.children.map((child) => (
            <TreeNode
              key={child.path}
              node={child}
              activePath={activePath}
              onSelect={onSelect}
            />
          ))}
        </div>
      )}
    </div>
  )
}
