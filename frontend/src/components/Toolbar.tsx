export type Niveau = 'seconde' | 'premiere' | 'terminale'

interface ToolbarProps {
  currentFile: string | null
  niveau: Niveau
  onNiveauChange: (niveau: Niveau) => void
  onExportPDF: () => void
  onExportDOCX: () => void
  onToggleProgramme?: () => void
}

export default function Toolbar({ currentFile, niveau, onNiveauChange, onExportPDF, onExportDOCX, onToggleProgramme }: ToolbarProps) {
  return (
    <div className="toolbar">
      <h1>📚 Assistant Pédagogique</h1>

      <div className="toolbar-niveau">
        <label htmlFor="niveau-select">Niveau :</label>
        <select
          id="niveau-select"
          value={niveau}
          onChange={(e) => onNiveauChange(e.target.value as Niveau)}
        >
          <option value="seconde">Seconde</option>
          <option value="premiere">Première</option>
          <option value="terminale">Terminale</option>
        </select>
      </div>

      {onToggleProgramme && (
        <button onClick={onToggleProgramme} title="Voir le programme officiel">
          📋 Programme
        </button>
      )}

      <span className="current-file">
        {currentFile || 'Aucun fichier sélectionné'}
      </span>
      {currentFile && (
        <>
          <button onClick={onExportPDF} title="Exporter en PDF">
            📄 PDF
          </button>
          <button onClick={onExportDOCX} title="Exporter en Word">
            📄 DOCX
          </button>
        </>
      )}
    </div>
  )
}
