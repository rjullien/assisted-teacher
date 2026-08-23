import { useEffect, useRef } from 'react'
import { Milkdown, MilkdownProvider, useEditor } from '@milkdown/react'
import { Editor as MilkdownEditor } from '@milkdown/core'
import { defaultValueCtx, rootCtx } from '@milkdown/core'
import { commonmark } from '@milkdown/preset-commonmark'
import { gfm } from '@milkdown/preset-gfm'
import { history } from '@milkdown/plugin-history'
import { listener, listenerCtx } from '@milkdown/plugin-listener'
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

// Inner Milkdown editor component
function MilkdownEditorInner({ content, onChange }: { content: string; onChange: (md: string) => void }) {
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange

  useEditor((root) => {
    return MilkdownEditor.make()
      .config((ctx) => {
        ctx.set(rootCtx, root)
        ctx.set(defaultValueCtx, content)
        ctx.get(listenerCtx)
          .markdownUpdated((_ctx, markdown, _prevMarkdown) => {
            onChangeRef.current(markdown)
          })
      })
      .use(commonmark)
      .use(gfm)
      .use(history)
      .use(listener)
  }, [])

  return <Milkdown />
}

export default function Editor({ content, lastSavedContent, onChange, onSave, onFlushRef, filePath }: EditorProps) {
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

  // Intercept Ctrl+S to prevent browser "Save As" dialog
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault()
        flush()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [flush])

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
      <div className="editor-content milkdown-wrapper">
        {/* key forces remount when file changes so Milkdown reloads content */}
        <MilkdownProvider key={filePath}>
          <MilkdownEditorInner content={content} onChange={onChange} />
        </MilkdownProvider>
      </div>
    </div>
  )
}
