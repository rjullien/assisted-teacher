interface ToolbarProps {
  currentFile: string | null
  onExportPDF: () => void
  onExportDOCX: () => void
}

export default function Toolbar({ currentFile, onExportPDF, onExportDOCX }: ToolbarProps) {
  return (
    <div className="toolbar">
      <h1>📚 Cours IA</h1>
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
