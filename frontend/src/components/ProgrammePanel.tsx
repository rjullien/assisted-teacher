import { useState } from 'react'
import { useI18n } from '../i18n'

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

interface ProgrammePanelProps {
  programme: ProgrammeData | null
  error?: boolean
  onRetry?: () => void
}

function Section({ title, icon, defaultOpen = false, children }: {
  title: string
  icon: string
  defaultOpen?: boolean
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)

  return (
    <div className="programme-section">
      <button
        className="programme-section-header"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        <span className="programme-section-icon">{icon}</span>
        <span className="programme-section-title">{title}</span>
        <span className={`programme-section-chevron ${open ? 'open' : ''}`}>▸</span>
      </button>
      {open && <div className="programme-section-content">{children}</div>}
    </div>
  )
}

export default function ProgrammePanel({ programme, error, onRetry }: ProgrammePanelProps) {
  const { t } = useI18n()

  if (!programme) {
    return (
      <div className="programme-panel">
        <div className="programme-empty">
          {error ? (
            <div className="programme-error">
              <p>⚠️ {t('programme.loadError')}</p>
              {onRetry && (
                <button className="programme-retry-btn" onClick={onRetry}>
                  🔄 {t('programme.retry')}
                </button>
              )}
            </div>
          ) : (
            <div className="programme-loading">
              <span className="programme-spinner">⏳</span>
              {t('programme.loading')}
            </div>
          )}
        </div>
      </div>
    )
  }

  const { niveau, cecrl, axes_culturels, contraintes, competences, grammaire, vocabulaire_thematique } = programme

  return (
    <div className="programme-panel">
      {/* Header */}
      <div className="programme-panel-header">
        <h2>{t('programme.header', { niveau })}</h2>
        <div className="programme-cecrl-badges">
          {Object.entries(cecrl).map(([key, val]) => (
            <span key={key} className="programme-badge">
              {key} {val}
            </span>
          ))}
        </div>
      </div>

      {/* Axes culturels */}
      <Section
        title={t('programme.axes', { count: String(contraintes.axes_a_traiter), total: String(contraintes.axes_total) })}
        icon="🎯"
        defaultOpen
      >
        <p className="programme-note">{contraintes.note}</p>
        <ul className="programme-axes-list">
          {axes_culturels.map((axe) => (
            <AxeItem key={axe.numero} axe={axe} />
          ))}
        </ul>
      </Section>

      {/* Compétences */}
      <Section title={t('programme.competences')} icon="📝" defaultOpen>
        <div className="programme-competences-grid">
          {Object.values(competences).map((comp) => (
            <div key={comp.code} className="programme-competence-card">
              <div className="programme-competence-header">
                <span className="programme-competence-code">{comp.code}</span>
                <span className="programme-competence-levels">
                  LVA {comp.niveau_attendu_LVA} · LVB {comp.niveau_attendu_LVB}
                </span>
              </div>
              <p className="programme-competence-desc">{comp.descripteur}</p>
            </div>
          ))}
        </div>
      </Section>

      {/* Grammaire */}
      <Section title={t('programme.grammaire', { count: String(grammaire.length) })} icon="📖">
        <ul className="programme-grammar-list">
          {grammaire.map((point, i) => (
            <li key={i}>{point}</li>
          ))}
        </ul>
      </Section>

      {/* Vocabulaire */}
      <Section title={t('programme.vocabulaire')} icon="🔤">
        <div className="programme-vocab-sections">
          {Object.entries(vocabulaire_thematique).map(([key, words]) => {
            const label = key
              .replace(/^axe_\d+_/, '')
              .replace(/_/g, ' ')
              .replace(/^\w/, c => c.toUpperCase())
            return (
              <div key={key} className="programme-vocab-group">
                <h4>{label}</h4>
                <div className="programme-vocab-words">
                  {words.map((w) => (
                    <span key={w} className="programme-vocab-chip">{w}</span>
                  ))}
                </div>
              </div>
            )
          })}
        </div>
      </Section>
    </div>
  )
}

function AxeItem({ axe }: { axe: ProgrammeData['axes_culturels'][0] }) {
  const { t } = useI18n()
  const [expanded, setExpanded] = useState(false)

  return (
    <li className={`programme-axe-item ${axe.obligatoire ? 'obligatoire' : ''}`}>
      <button className="programme-axe-toggle" onClick={() => setExpanded(!expanded)}>
        <span className="programme-axe-numero">{axe.numero}.</span>
        <span className="programme-axe-titre">{axe.titre}</span>
        {axe.obligatoire && <span className="programme-axe-badge">{t('programme.obligatoire')}</span>}
        <span className={`programme-axe-chevron ${expanded ? 'open' : ''}`}>▸</span>
      </button>
      {expanded && (
        <div className="programme-axe-details">
          <p className="programme-axe-desc">{axe.description}</p>
          <ul className="programme-axe-exemples">
            {axe.exemples_objets_etude.map((ex, i) => (
              <li key={i}>{ex}</li>
            ))}
          </ul>
        </div>
      )}
    </li>
  )
}
