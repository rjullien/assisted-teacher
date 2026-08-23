import { screen, fireEvent } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import { renderWithI18n } from '../test/i18n-wrapper'
import ProgrammePanel from './ProgrammePanel'

const mockProgramme = {
  niveau: 'Seconde',
  cecrl: { LVA: 'B1+', LVB: 'A2+', LVC: 'A1/A2' },
  axes_culturels: [
    {
      numero: 1,
      titre: 'Représentation de soi et rapport à autrui',
      description: "L'expression de l'identité personnelle.",
      exemples_objets_etude: ['Autobiographies', 'Identités numériques'],
      obligatoire: false,
    },
    {
      numero: 6,
      titre: 'Commonwealth',
      description: 'Diversité du monde anglophone.',
      exemples_objets_etude: ["L'Inde anglophone", "L'Australie"],
      obligatoire: true,
    },
  ],
  contraintes: {
    axes_a_traiter: 5,
    axes_total: 6,
    axe_obligatoire: 6,
    note: '5 axes sur 6 doivent être traités dans l\'année, dont l\'axe 6 (Commonwealth) obligatoirement.',
  },
  competences: {
    comprehension_orale: {
      code: 'CO',
      descripteur: 'Comprendre l\'essentiel de messages oraux.',
      niveau_attendu_LVA: 'B1+',
      niveau_attendu_LVB: 'A2+',
    },
    expression_ecrite: {
      code: 'EE',
      descripteur: 'Écrire un texte simple et cohérent.',
      niveau_attendu_LVA: 'B1+',
      niveau_attendu_LVB: 'A2+',
    },
  },
  grammaire: [
    'Present simple et present continuous',
    'Past simple et past continuous',
    'Present perfect simple',
  ],
  vocabulaire_thematique: {
    axe_1_representation_de_soi: ['identity', 'self-image', 'personality'],
    axe_6_commonwealth: ['Commonwealth', 'multicultural', 'indigenous'],
  },
}

describe('ProgrammePanel', () => {
  it('shows loading state when programme is null', () => {
    renderWithI18n(<ProgrammePanel programme={null} />)
    expect(screen.getByText(/Chargement du programme/)).toBeInTheDocument()
  })

  it('renders programme header with niveau', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    expect(screen.getByText(/Programme — Seconde/)).toBeInTheDocument()
  })

  it('renders CECRL badges', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    expect(screen.getByText('LVA B1+')).toBeInTheDocument()
    expect(screen.getByText('LVB A2+')).toBeInTheDocument()
    expect(screen.getByText('LVC A1/A2')).toBeInTheDocument()
  })

  it('renders axes culturels section with count', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    expect(screen.getByText(/Axes culturels \(5\/6/)).toBeInTheDocument()
  })

  it('renders all axes with their titles', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    expect(screen.getByText(/Représentation de soi/)).toBeInTheDocument()
    expect(screen.getByText('Commonwealth')).toBeInTheDocument()
  })

  it('shows OBLIGATOIRE badge on obligatory axes', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    expect(screen.getByText(/OBLIGATOIRE/)).toBeInTheDocument()
  })

  it('does NOT show OBLIGATOIRE badge on non-obligatory axes', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    // Only one badge should exist (axe 6)
    const badges = screen.getAllByText(/OBLIGATOIRE/)
    expect(badges).toHaveLength(1)
  })

  it('expands an axe to show description and examples on click', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)

    // Description should NOT be visible initially
    expect(screen.queryByText(/L'expression de l'identité/)).not.toBeInTheDocument()

    // Click axe 1 toggle
    const axe1Button = screen.getByText(/Représentation de soi/).closest('button')!
    fireEvent.click(axe1Button)

    // Description and examples should now be visible
    expect(screen.getByText(/L'expression de l'identité/)).toBeInTheDocument()
    expect(screen.getByText('Autobiographies')).toBeInTheDocument()
    expect(screen.getByText('Identités numériques')).toBeInTheDocument()
  })

  it('collapses an expanded axe on second click', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)

    const axe1Button = screen.getByText(/Représentation de soi/).closest('button')!
    fireEvent.click(axe1Button) // expand
    expect(screen.getByText(/L'expression de l'identité/)).toBeInTheDocument()

    fireEvent.click(axe1Button) // collapse
    expect(screen.queryByText(/L'expression de l'identité/)).not.toBeInTheDocument()
  })

  it('renders compétences section', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    expect(screen.getByText(/Compétences langagières/)).toBeInTheDocument()
  })

  it('renders competence cards with code and levels', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    expect(screen.getByText('CO')).toBeInTheDocument()
    expect(screen.getByText('EE')).toBeInTheDocument()
    expect(screen.getAllByText(/LVA B1\+ · LVB A2\+/).length).toBeGreaterThanOrEqual(1)
  })

  it('renders grammaire section with count', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    expect(screen.getByText(/Grammaire \(3 points\)/)).toBeInTheDocument()
  })

  it('shows grammar points when section is expanded', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)

    // Grammar section is closed by default — click to open
    const grammarHeader = screen.getByText(/Grammaire \(3 points\)/).closest('button')!
    fireEvent.click(grammarHeader)

    expect(screen.getByText('Present simple et present continuous')).toBeInTheDocument()
    expect(screen.getByText('Past simple et past continuous')).toBeInTheDocument()
    expect(screen.getByText('Present perfect simple')).toBeInTheDocument()
  })

  it('renders vocabulaire section', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    expect(screen.getByText(/Vocabulaire thématique/)).toBeInTheDocument()
  })

  it('shows vocabulary chips when section is expanded', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)

    // Vocabulary section is closed by default — click to open
    const vocabHeader = screen.getByText(/Vocabulaire thématique/).closest('button')!
    fireEvent.click(vocabHeader)

    expect(screen.getByText('identity')).toBeInTheDocument()
    expect(screen.getByText('self-image')).toBeInTheDocument()
    // "Commonwealth" appears both as an axe title and a vocab chip
    expect(screen.getAllByText('Commonwealth').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('multicultural')).toBeInTheDocument()
  })

  it('renders contrainte note text', () => {
    renderWithI18n(<ProgrammePanel programme={mockProgramme} />)
    expect(screen.getByText(/5 axes sur 6 doivent être traités/)).toBeInTheDocument()
  })
})
