import { useEffect, useRef, useCallback } from 'react'
import { useAutoSave, type SaveStatus } from '../hooks/useAutoSave'

interface EditorProps {
  content: string
  lastSavedContent: string
  onChange: (content: string) => void
  onSave: (content: string) => Promise<void>
  onFlushRef?: React.MutableRefObject<(() => Promise<void>) | null>
  filePath: string | null
}

function SaveIndicator({ status }: { status: SaveStatus }) {
  if (status === 'idle') return null

  const config: Record<SaveStatus, { icon: string; text: string; className: string }> = {
    idle: { icon: '', text: '', className: '' },
    unsaved: { icon: '●', text: 'Non sauvegardé', className: 'save-indicator unsaved' },
    saving: { icon: '⏳', text: 'Sauvegarde…', className: 'save-indicator saving' },
    saved: { icon: '✓', text: 'Sauvegardé', className: 'save-indicator saved' },
    error: { icon: '⚠️', text: 'Erreur de sauvegarde', className: 'save-indicator error' },
  }

  const { icon, text, className } = config[status]

  return (
    <span className={className}>
      {icon} {text}
    </span>
  )
}

export default function Editor({ content, lastSavedContent, onChange, onSave, onFlushRef, filePath }: EditorProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const { status, flush } = useAutoSave(content, lastSavedContent, onSave)

  // Expose flush to parent for use before file switching
  useEffect(() => {
    if (onFlushRef) {
      onFlushRef.current = flush
    }
    return () => {
      if (onFlushRef) {
        onFlushRef.current = null
      }
    }
  }, [flush, onFlushRef])

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
        <SaveIndicator status={status} />
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
          spellCheck
          placeholder="Écrivez en Markdown..."
        />
      </div>
    </div>
  )
}
