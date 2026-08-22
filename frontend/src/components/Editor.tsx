import { Milkdown, MilkdownProvider, useEditor } from '@milkdown/react'
import { Crepe } from '@milkdown/crepe'
import { useEffect, useRef } from 'react'

interface EditorProps {
  content: string
  onChange: (content: string) => void
  onSave: (content: string) => void
  filePath: string | null
}

// Inner editor component that uses Milkdown
function MilkdownEditor({ content, onChange, onSave }: Omit<EditorProps, 'filePath'>) {
  const contentRef = useRef(content)
  contentRef.current = content

  const { get } = useEditor((root) => {
    // Use Crepe for a batteries-included WYSIWYG Markdown experience
    const crepe = new Crepe({
      root,
      defaultValue: content,
    })
    crepe.on((listener) => {
      listener.markdownUpdated((_ctx, markdown) => {
        onChange(markdown)
      })
    })
    return crepe
  })

  // Save on Ctrl/Cmd+S
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault()
        onSave(contentRef.current)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [onSave, get])

  return <Milkdown />
}

export default function Editor({ content, onChange, onSave, filePath }: EditorProps) {
  if (!filePath) {
    return (
      <div className="editor-panel">
        <div className="editor-empty">
          Sélectionnez un cours à gauche, ou créez-en un nouveau.
        </div>
      </div>
    )
  }

  return (
    <div className="editor-panel">
      <div className="editor-header">
        <span>{filePath}</span>
        <span style={{ fontSize: '11px' }}>Ctrl+S pour sauvegarder</span>
      </div>
      <div className="editor-content">
        {/* key forces remount when file changes so Milkdown reloads content */}
        <MilkdownProvider key={filePath}>
          <MilkdownEditor content={content} onChange={onChange} onSave={onSave} />
        </MilkdownProvider>
      </div>
    </div>
  )
}
