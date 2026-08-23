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
  let name = raw.trim()
  name = name.replace(/[\s?!…,;:'"«»()[\]{}<>@#$%^&*+=|\\\/~`]+/g, '_')
  name = name.replace(/_+/g, '_')
  name = name.replace(/^_+|_+$/g, '')
  return name
}

export default function FileTree({ onSelect, onRefresh, refreshKey }: FileTreeProps) {
  const [tree, setTree] = useState<FileNode[]>([])
  const [activePath, setActivePath] = useState<string | null>(null)
  const [toast, setToast] = useState<{ message: string; type: 'error' | 'success' } | null>(null)

  const showToast = (message: string, type: 'error' | 'success' = 'error') => {
    setToast({ message, type })
    setTimeout(() => setToast(null), 4000)
  }

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

  // Create a new file at a given directory path (empty string = root)
  const handleNewFile = async (dirPath: string = '') => {
    const raw = prompt('Nom du nouveau cours (ex: unit7)')
    if (!raw) return
    const withoutExt = raw.replace(/\.md$/i, '')
    const sanitized = sanitizeFilename(withoutExt)
    if (!sanitized) {
      showToast('Nom invalide après nettoyage.')
      return
    }
    const fileName = sanitized + '.md'
    const fullPath = dirPath ? `${dirPath}/${fileName}` : fileName
    const heading = withoutExt.trim()
    const res = await putText(
      `/api/file?path=${encodeURIComponent(fullPath)}`,
      `# ${heading}\n\n`
    )
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (!res.ok) {
      showToast(`Création échouée : ${res.error || 'erreur inconnue'}`)
      return
    }
    await loadTree()
    setActivePath(fullPath)
    onSelect(fullPath)
    showToast(`"${fileName}" créé`, 'success')
  }

  // Create a new folder at a given directory path (empty string = root)
  const handleNewFolder = async (dirPath: string = '') => {
    const raw = prompt('Nom du nouveau dossier (ex: B2)')
    if (!raw) return
    const sanitized = sanitizeFilename(raw)
    if (!sanitized) {
      showToast('Nom invalide après nettoyage.')
      return
    }
    const fullPath = dirPath ? `${dirPath}/${sanitized}` : sanitized
    const res = await postJSON('/api/files/mkdir', { path: fullPath })
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (!res.ok) {
      showToast(`Création dossier échouée : ${res.error || 'erreur inconnue'}`)
      return
    }
    await loadTree()
    showToast(`Dossier "${sanitized}" créé`, 'success')
  }

  const handleDelete = async (node: FileNode) => {
    const label = node.isDir ? `le dossier "${node.name}" et tout son contenu` : `"${node.name}"`
    const confirmed = confirm(`Supprimer ${label} ?`)
    if (!confirmed) return
    const res = await request(`/api/file?path=${encodeURIComponent(node.path)}`, {
      method: 'DELETE',
    })
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (!res.ok) {
      showToast(`Suppression échouée : ${res.error || 'erreur inconnue'}`)
      return
    }
    showToast(`"${node.name}" supprimé`, 'success')
    if (activePath === node.path || (node.isDir && activePath?.startsWith(node.path + '/'))) {
      setActivePath(null)
    }
    onRefresh()
    await loadTree()
  }

  const handleRename = async (node: FileNode) => {
    const currentName = node.isDir ? node.name : node.name.replace(/\.md$/i, '')
    const raw = prompt('Nouveau nom :', currentName)
    if (!raw) return
    if (raw.trim() === currentName) {
      showToast('Nom inchangé.', 'success')
      return
    }
    const sanitized = sanitizeFilename(node.isDir ? raw : raw.replace(/\.md$/i, ''))
    if (!sanitized) {
      showToast('Nom invalide après nettoyage.')
      return
    }
    const ext = (!node.isDir && node.name.endsWith('.md')) ? '.md' : ''
    const newName = sanitized + ext
    const dir = node.path.includes('/') ? node.path.substring(0, node.path.lastIndexOf('/') + 1) : ''
    const newPath = dir + newName
    if (newPath === node.path) {
      showToast('Le nom est déjà le même.', 'success')
      return
    }
    const res = await postJSON('/api/files/rename', { from: node.path, to: newPath })
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (!res.ok) {
      showToast(`Renommage échoué : ${res.error || 'erreur inconnue'}`)
      return
    }
    showToast(`Renommé → "${newName}"`, 'success')
    if (activePath === node.path) {
      setActivePath(newPath)
    } else if (node.isDir && activePath?.startsWith(node.path + '/')) {
      // Update active path if it was inside the renamed folder
      setActivePath(activePath.replace(node.path, newPath))
    }
    onRefresh()
    await loadTree()
  }

  return (
    <div className="file-tree">
      <div className="file-tree-header">
        <h2>Mes cours</h2>
        <div className="file-tree-header-actions">
          <button onClick={() => handleNewFolder('')} title="Nouveau dossier">📁+</button>
          <button onClick={() => handleNewFile('')} title="Nouveau cours">📄+</button>
        </div>
      </div>

      {/* Toast notification */}
      {toast && (
        <div
          className={`file-tree-toast ${toast.type}`}
          onClick={() => setToast(null)}
        >
          {toast.type === 'error' ? '❌ ' : '✅ '}
          {toast.message}
        </div>
      )}

      {tree.length === 0 && (
        <div style={{ padding: '8px', fontSize: '12px', color: 'var(--text-muted)' }}>
          Aucun cours. Cliquez sur "📄+" pour créer un cours ou "📁+" pour créer un dossier.
        </div>
      )}
      <div className="file-tree-list">
        {tree.map((node) => (
          <TreeNode
            key={node.path}
            node={node}
            activePath={activePath}
            onSelect={handleSelect}
            onDelete={handleDelete}
            onRename={handleRename}
            onNewFile={handleNewFile}
            onNewFolder={handleNewFolder}
          />
        ))}
      </div>
      <div className="file-tree-version" title={__APP_VERSION__}>
        {__APP_VERSION__.slice(0, 7)}
      </div>
    </div>
  )
}

function TreeNode({
  node,
  activePath,
  onSelect,
  onDelete,
  onRename,
  onNewFile,
  onNewFolder,
}: {
  node: FileNode
  activePath: string | null
  onSelect: (node: FileNode) => void
  onDelete: (node: FileNode) => void
  onRename: (node: FileNode) => void
  onNewFile: (dirPath: string) => void
  onNewFolder: (dirPath: string) => void
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
        <span className="file-actions" onClick={(e) => e.stopPropagation()}>
          {node.isDir && (
            <>
              <button
                className="file-action-btn"
                onClick={() => onNewFile(node.path)}
                title="Nouveau cours ici"
              >
                📄+
              </button>
              <button
                className="file-action-btn"
                onClick={() => onNewFolder(node.path)}
                title="Nouveau sous-dossier"
              >
                📁+
              </button>
            </>
          )}
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
      </div>
      {node.isDir && expanded && node.children && (
        <div className="file-tree-children">
          {node.children.length === 0 && (
            <div className="file-tree-empty-folder">
              <span style={{ fontSize: '11px', color: 'var(--text-muted)', padding: '4px 8px' }}>
                Dossier vide
              </span>
            </div>
          )}
          {node.children.map((child) => (
            <TreeNode
              key={child.path}
              node={child}
              activePath={activePath}
              onSelect={onSelect}
              onDelete={onDelete}
              onRename={onRename}
              onNewFile={onNewFile}
              onNewFolder={onNewFolder}
            />
          ))}
        </div>
      )}
    </div>
  )
}
