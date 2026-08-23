import { useState } from 'react'

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

export default function ProgrammePanel({ programme }: ProgrammePanelProps) {
  if (!programme) {
    return (
      <div className="programme-panel">
        <div className="programme-empty">
          Chargement du programme…
        </div>
      </div>
    )
  }

  const { niveau, cecrl, axes_culturels, contraintes, competences, grammaire, vocabulaire_thematique } = programme

  return (
    <div className="programme-panel">
      {/* Header */}
      <div className="programme-panel-header">
        <h2>📋 Programme — {niveau}</h2>
        <div className="programme-cecrl-badges">
          {Object.entries(cecrl).map(([key, val]) => (
            <span key={key} className="programme-badge">
              {key} {val}
            </span>
          ))}
        </div>
      </div>

      {/* Axes culturels */}
      <Section title={`Axes culturels (${contraintes.axes_a_traiter}/${contraintes.axes_total} à traiter)`} icon="🎯" defaultOpen>
        <p className="programme-note">{contraintes.note}</p>
        <ul className="programme-axes-list">
          {axes_culturels.map((axe) => (
            <AxeItem key={axe.numero} axe={axe} />
          ))}
        </ul>
      </Section>

      {/* Compétences */}
      <Section title="Compétences langagières" icon="📝" defaultOpen>
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
      <Section title={`Grammaire (${grammaire.length} points)`} icon="📖">
        <ul className="programme-grammar-list">
          {grammaire.map((point, i) => (
            <li key={i}>{point}</li>
          ))}
        </ul>
      </Section>

      {/* Vocabulaire */}
      <Section title="Vocabulaire thématique" icon="🔤">
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
  const [expanded, setExpanded] = useState(false)

  return (
    <li className={`programme-axe-item ${axe.obligatoire ? 'obligatoire' : ''}`}>
      <button className="programme-axe-toggle" onClick={() => setExpanded(!expanded)}>
        <span className="programme-axe-numero">{axe.numero}.</span>
        <span className="programme-axe-titre">{axe.titre}</span>
        {axe.obligatoire && <span className="programme-axe-badge">⭐ OBLIGATOIRE</span>}
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
