import { useEffect, useRef } from 'react'
import ProgrammePanel from './ProgrammePanel'

interface ProgrammeData {
  niveau: string
  cecrl: Record<string, string>
  axes_culturels: Array<{
    numero: number
    titre: string
    description: string
    exemples_objets_etude: string[]
    obligatoire: boolean
  }>
  contraintes: {
    axes_a_traiter: number
    axes_total: number
    axe_obligatoire: number
    note: string
  }
  competences: Record<string, {
    code: string
    descripteur: string
    niveau_attendu_LVA: string
    niveau_attendu_LVB: string
  }>
  grammaire: string[]
  vocabulaire_thematique: Record<string, string[]>
}

interface ProgrammeDrawerProps {
  open: boolean
  onClose: () => void
  programme: ProgrammeData | null
}

export default function ProgrammeDrawer({ open, onClose, programme }: ProgrammeDrawerProps) {
  const drawerRef = useRef<HTMLDivElement>(null)

  // Close on Escape key
  useEffect(() => {
    if (!open) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [open, onClose])

  // Trap focus inside drawer when open
  useEffect(() => {
    if (open && drawerRef.current) {
      drawerRef.current.focus()
    }
  }, [open])

  return (
    <>
      {/* Backdrop */}
      <div
        className={`programme-drawer-backdrop ${open ? 'visible' : ''}`}
        onClick={onClose}
        aria-hidden="true"
      />
      {/* Drawer panel */}
      <div
        ref={drawerRef}
        className={`programme-drawer ${open ? 'open' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-label="Programme officiel"
        tabIndex={-1}
      >
        <div className="programme-drawer-header">
          <h2>Programme officiel</h2>
          <button
            className="programme-drawer-close"
            onClick={onClose}
            aria-label="Fermer"
          >
            ✕
          </button>
        </div>
        <div className="programme-drawer-body">
          <ProgrammePanel programme={programme} />
        </div>
      </div>
    </>
  )
}
