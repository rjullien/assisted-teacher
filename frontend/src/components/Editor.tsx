import { useEffect, useRef, useCallback } from 'react'

interface EditorProps {
  content: string
  onChange: (content: string) => void
  onSave: (content: string) => void
  filePath: string | null
}

export default function Editor({ content, onChange, onSave, filePath }: EditorProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const contentRef = useRef(content)
  contentRef.current = content

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
  }, [onSave])

  // Auto-resize textarea
  const autoResize = useCallback(() => {
    const ta = textareaRef.current
    if (ta) {
      ta.style.height = 'auto'
      ta.style.height = ta.scrollHeight + 'px'
    }
  }, [])

  useEffect(() => {
    autoResize()
  }, [content, autoResize])

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
        <textarea
          ref={textareaRef}
          className="editor-textarea"
          value={content}
          onChange={(e) => {
            onChange(e.target.value)
            autoResize()
          }}
          onBlur={() => onSave(contentRef.current)}
          spellCheck
          placeholder="Écrivez en Markdown..."
        />
      </div>
    </div>
  )
}
