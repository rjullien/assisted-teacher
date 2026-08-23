import { useI18n } from '../i18n'

export type Niveau = 'seconde' | 'premiere' | 'terminale'
export type AppMode = 'desk' | 'lya'

interface ToolbarProps {
  currentFile: string | null
  niveau: Niveau
  mode: AppMode
  onNiveauChange: (niveau: Niveau) => void
  onModeChange: (mode: AppMode) => void
  onExportPDF: () => void
  onExportDOCX: () => void
  onToggleProgramme?: () => void
}

export default function Toolbar({
  currentFile,
  niveau,
  mode,
  onNiveauChange,
  onModeChange,
  onExportPDF,
  onExportDOCX,
  onToggleProgramme,
}: ToolbarProps) {
  const { t, locale, setLocale } = useI18n()

  return (
    <div className="toolbar">
      {/* Mode switcher */}
      <div className="toolbar-mode-switcher">
        <button
          className={`toolbar-mode-btn ${mode === 'desk' ? 'active' : ''}`}
          onClick={() => onModeChange('desk')}
        >
          {t('mode.desk')}
        </button>
        <button
          className={`toolbar-mode-btn ${mode === 'lya' ? 'active' : ''}`}
          onClick={() => onModeChange('lya')}
        >
          {t('mode.lya')}
        </button>
      </div>

      {/* Desk-specific controls */}
      {mode === 'desk' && (
        <>
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
        </>
      )}

      {/* Lya mode — just show the title */}
      {mode === 'lya' && (
        <span className="toolbar-lya-title">{t('lya.title')}</span>
      )}

      {/* Always visible: language toggle */}
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
