import { useState } from 'react'
import FileTree from './FileTree'
import Editor from './Editor'
import Chat, { type ChatSession } from './Chat'
import ProgrammePanel from './ProgrammePanel'
import { useI18n } from '../i18n'

type Tab = 'files' | 'editor' | 'chat' | 'programme'

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

interface MobileLayoutProps {
  currentFile: string | null
  fileContent: string
  lastSavedContent: string
  refreshKey: number
  programme: ProgrammeData | null
  programmeError?: boolean
  onRetryProgramme?: () => void
  onFileSelect: (path: string) => void
  onFileTreeRefresh: () => void
  onFileContentChange: (content: string) => void
  onSave: (content: string) => Promise<void>
  onFlushRef: React.MutableRefObject<(() => Promise<void>) | null>
  onInsertFromChat: (text: string) => void
  agent?: 'lya' | 'pi'
  onFileChanged?: (path: string) => void
  /** Held by App: the chat tab is unmounted every time another tab is shown. */
  chatSession?: ChatSession
  onChatSessionChange?: (session: ChatSession) => void
  userName?: string
}

export default function MobileLayout({
  currentFile,
  fileContent,
  lastSavedContent,
  refreshKey,
  programme,
  programmeError,
  onRetryProgramme,
  onFileSelect,
  onFileTreeRefresh,
  onFileContentChange,
  onSave,
  onFlushRef,
  onInsertFromChat,
  agent,
  onFileChanged,
  chatSession,
  onChatSessionChange,
  userName,
}: MobileLayoutProps) {
  const { t } = useI18n()
  const [activeTab, setActiveTab] = useState<Tab>('files')

  // Auto-switch to editor when a file is selected
  const handleFileSelect = (path: string) => {
    onFileSelect(path)
    setActiveTab('editor')
  }

  return (
    <div className="mobile-layout">
      <div className="mobile-content">
        {activeTab === 'files' && (
          <FileTree
            onSelect={handleFileSelect}
            onRefresh={onFileTreeRefresh}
            refreshKey={refreshKey}
          />
        )}
        {activeTab === 'editor' && (
          <Editor
            content={fileContent}
            lastSavedContent={lastSavedContent}
            onChange={onFileContentChange}
            onSave={onSave}
            onFlushRef={onFlushRef}
            filePath={currentFile}
          />
        )}
        {activeTab === 'chat' && (
          <Chat
            currentFile={currentFile}
            fileContent={fileContent}
            onInsert={(text) => {
              onInsertFromChat(text)
              setActiveTab('editor')
            }}
            programme={programme}
            agent={agent}
            onFileChanged={onFileChanged}
            session={chatSession}
            onSessionChange={onChatSessionChange}
            userName={userName}
          />
        )}
        {activeTab === 'programme' && (
          <div className="mobile-programme-wrapper">
            <ProgrammePanel programme={programme} error={programmeError} onRetry={onRetryProgramme} />
          </div>
        )}
      </div>
      <nav className="mobile-tab-bar">
        <button
          className={`mobile-tab ${activeTab === 'files' ? 'active' : ''}`}
          onClick={() => setActiveTab('files')}
        >
          <span className="mobile-tab-icon">📁</span>
          <span className="mobile-tab-label">{t('mobile.files')}</span>
        </button>
        <button
          className={`mobile-tab ${activeTab === 'editor' ? 'active' : ''}`}
          onClick={() => setActiveTab('editor')}
        >
          <span className="mobile-tab-icon">✏️</span>
          <span className="mobile-tab-label">{t('mobile.editor')}</span>
          {currentFile && <span className="mobile-tab-dot" />}
        </button>
        <button
          className={`mobile-tab ${activeTab === 'chat' ? 'active' : ''}`}
          onClick={() => setActiveTab('chat')}
        >
          <span className="mobile-tab-icon">💬</span>
          <span className="mobile-tab-label">{t('mobile.ia')}</span>
        </button>
        <button
          className={`mobile-tab ${activeTab === 'programme' ? 'active' : ''}`}
          onClick={() => setActiveTab('programme')}
        >
          <span className="mobile-tab-icon">📋</span>
          <span className="mobile-tab-label">{t('mobile.programme')}</span>
        </button>
      </nav>
    </div>
  )
}
