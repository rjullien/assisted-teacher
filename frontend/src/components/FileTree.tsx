import { useEffect, useState } from 'react'
import { getJSON, putText, request, postJSON, handleAuthExpired } from '../api'
import { useI18n } from '../i18n'

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
  const { t } = useI18n()
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
    const raw = prompt(t('fileTree.newFilePrompt'))
    if (!raw) return
    const withoutExt = raw.replace(/\.md$/i, '')
    const sanitized = sanitizeFilename(withoutExt)
    if (!sanitized) {
      showToast(t('fileTree.invalidName'))
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
      showToast(`${t('fileTree.createFailed')} : ${res.error || 'erreur inconnue'}`)
      return
    }
    await loadTree()
    setActivePath(fullPath)
    onSelect(fullPath)
    showToast(t('fileTree.created', { name: fileName }), 'success')
  }

  // Create a new folder at a given directory path (empty string = root)
  const handleNewFolder = async (dirPath: string = '') => {
    const raw = prompt(t('fileTree.newFolderPrompt'))
    if (!raw) return
    const sanitized = sanitizeFilename(raw)
    if (!sanitized) {
      showToast(t('fileTree.invalidName'))
      return
    }
    const fullPath = dirPath ? `${dirPath}/${sanitized}` : sanitized
    const res = await postJSON('/api/files/mkdir', { path: fullPath })
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (!res.ok) {
      showToast(`${t('fileTree.folderCreateFailed')} : ${res.error || 'erreur inconnue'}`)
      return
    }
    await loadTree()
    showToast(t('fileTree.folderCreated', { name: sanitized }), 'success')
  }

  const handleDelete = async (node: FileNode) => {
    const msg = node.isDir
      ? t('fileTree.deleteConfirmFolder', { name: node.name })
      : t('fileTree.deleteConfirmFile', { name: node.name })
    const confirmed = confirm(msg)
    if (!confirmed) return
    const res = await request(`/api/file?path=${encodeURIComponent(node.path)}`, {
      method: 'DELETE',
    })
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (!res.ok) {
      showToast(`${t('fileTree.deleteFailed')} : ${res.error || 'erreur inconnue'}`)
      return
    }
    showToast(t('fileTree.deleted', { name: node.name }), 'success')
    if (activePath === node.path || (node.isDir && activePath?.startsWith(node.path + '/'))) {
      setActivePath(null)
    }
    onRefresh()
    await loadTree()
  }

  const handleRename = async (node: FileNode) => {
    const currentName = node.isDir ? node.name : node.name.replace(/\.md$/i, '')
    const raw = prompt(t('fileTree.renamePrompt'), currentName)
    if (!raw) return
    if (raw.trim() === currentName) {
      showToast(t('fileTree.unchanged'), 'success')
      return
    }
    const sanitized = sanitizeFilename(node.isDir ? raw : raw.replace(/\.md$/i, ''))
    if (!sanitized) {
      showToast(t('fileTree.invalidName'))
      return
    }
    const ext = (!node.isDir && node.name.endsWith('.md')) ? '.md' : ''
    const newName = sanitized + ext
    const dir = node.path.includes('/') ? node.path.substring(0, node.path.lastIndexOf('/') + 1) : ''
    const newPath = dir + newName
    if (newPath === node.path) {
      showToast(t('fileTree.alreadySame'), 'success')
      return
    }
    const res = await postJSON('/api/files/rename', { from: node.path, to: newPath })
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (!res.ok) {
      showToast(`${t('fileTree.renameFailed')} : ${res.error || 'erreur inconnue'}`)
      return
    }
    showToast(t('fileTree.renamed', { name: newName }), 'success')
    if (activePath === node.path) {
      setActivePath(newPath)
    } else if (node.isDir && activePath?.startsWith(node.path + '/')) {
      setActivePath(activePath.replace(node.path, newPath))
    }
    onRefresh()
    await loadTree()
  }

  return (
    <div className="file-tree">
      <div className="file-tree-header">
        <h2>{t('fileTree.title')}</h2>
        <div className="file-tree-header-actions">
          <button onClick={() => handleNewFolder('')} title={t('fileTree.newFolder')}>📁+</button>
          <button onClick={() => handleNewFile('')} title={t('fileTree.newFile')}>📄+</button>
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
          {t('fileTree.empty')}
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
      <div className="file-tree-lya-link">
        <a
          href="https://lya-drive.bapttf.com/files/data/cours_esclavage/"
          target="_blank"
          rel="noopener noreferrer"
        >
          {t('fileTree.lyaLink')}
        </a>
      </div>
      <div className="file-tree-version" title={`Version ${__APP_VERSION__}`}>
        v{__APP_VERSION__}
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
  const { t } = useI18n()
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
                title={t('fileTree.newFileHere')}
              >
                📄+
              </button>
              <button
                className="file-action-btn"
                onClick={() => onNewFolder(node.path)}
                title={t('fileTree.newSubfolder')}
              >
                📁+
              </button>
            </>
          )}
          <button
            className="file-action-btn"
            onClick={() => onRename(node)}
            title={t('fileTree.rename')}
          >
            ✏️
          </button>
          <button
            className="file-action-btn"
            onClick={() => onDelete(node)}
            title={t('fileTree.delete')}
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
                {t('fileTree.emptyFolder')}
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
