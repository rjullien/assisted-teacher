import { useI18n } from '../i18n'

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
  const { t, locale, setLocale } = useI18n()

  return (
    <div className="toolbar">
      <h1>{t('app.title')}</h1>

      <div className="toolbar-niveau">
        <label htmlFor="niveau-select">{t('toolbar.niveau')}</label>
        <select
          id="niveau-select"
          value={niveau}
          onChange={(e) => onNiveauChange(e.target.value as Niveau)}
        >
          <option value="seconde">{t('toolbar.seconde')}</option>
          <option value="premiere">{t('toolbar.premiere')}</option>
          <option value="terminale">{t('toolbar.terminale')}</option>
        </select>
      </div>

      {onToggleProgramme && (
        <button onClick={onToggleProgramme} title={t('programme.title')}>
          {t('toolbar.programme')}
        </button>
      )}

      <span className="current-file">
        {currentFile || t('toolbar.noFile')}
      </span>

      {currentFile && (
        <>
          <button onClick={onExportPDF} title="Export PDF">
            {t('toolbar.exportPdf')}
          </button>
          <button onClick={onExportDOCX} title="Export DOCX">
            {t('toolbar.exportDocx')}
          </button>
        </>
      )}

      <button
        className="toolbar-lang-toggle"
        onClick={() => setLocale(locale === 'fr' ? 'en' : 'fr')}
        title={locale === 'fr' ? 'Switch to English' : 'Passer en français'}
      >
        {locale === 'fr' ? '🇬🇧 EN' : '🇫🇷 FR'}
      </button>
    </div>
  )
}
