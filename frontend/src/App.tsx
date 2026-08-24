import { Allotment } from 'allotment'
import 'allotment/dist/style.css'
import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import FileTree from './components/FileTree'
import Editor from './components/Editor'
import Chat, { emptyChatSession, type ChatSession } from './components/Chat'
import LyaChat, { type LyaChatMessage } from './components/LyaChat'
import MobileLayout from './components/MobileLayout'
import ProgrammeDrawer from './components/ProgrammeDrawer'
import Toolbar, { type Niveau, type AppMode } from './components/Toolbar'
import { useIsMobile } from './hooks/useIsMobile'
import { I18nContext, detectLocale, saveLocale, t as tFn, type Locale } from './i18n'
import { getText, putText, postBlob, getJSON, handleAuthExpired } from './api'

// --- Programme types ---

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

// --- localStorage persistence ---

const STORAGE_KEY = 'assisted-teacher-niveau'

function loadNiveau(): Niveau {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'seconde' || saved === 'premiere' || saved === 'terminale') {
    return saved
  }
  return 'seconde'
}

function saveNiveau(niveau: Niveau) {
  localStorage.setItem(STORAGE_KEY, niveau)
}

// --- App Mode persistence ---

const MODE_STORAGE_KEY = 'assisted-teacher-mode'

function loadMode(): AppMode {
  const saved = localStorage.getItem(MODE_STORAGE_KEY)
  if (saved === 'desk' || saved === 'pi' || saved === 'lya') return saved
  return 'desk'
}

function saveMode(mode: AppMode) {
  localStorage.setItem(MODE_STORAGE_KEY, mode)
}

// --- App ---

export default function App() {
  const isMobile = useIsMobile()
  const [locale, setLocaleState] = useState<Locale>(detectLocale)
  const [mode, setModeState] = useState<AppMode>(loadMode)
  const [currentFile, setCurrentFile] = useState<string | null>(null)
  const [fileContent, setFileContent] = useState<string>('')
  const [lastSavedContent, setLastSavedContent] = useState<string>('')
  const [refreshKey, setRefreshKey] = useState(0)
  const [niveau, setNiveau] = useState<Niveau>(loadNiveau)
  const [programme, setProgramme] = useState<ProgrammeData | null>(null)
  const [programmeError, setProgrammeError] = useState(false)
  const [programmeDrawerOpen, setProgrammeDrawerOpen] = useState(false)
  const [userName, setUserName] = useState<string>('')
  const [piAvailable, setPiAvailable] = useState(false)
  const [lyaMessages, setLyaMessages] = useState<LyaChatMessage[]>([])
  // Chat state lives here, not in Chat, because Chat is unmounted whenever the
  // mobile tab or the app mode changes. One session per agent: per design
  // decision D11 the two histories stay separate and are never replayed to the
  // other agent.
  const [deskSession, setDeskSession] = useState<ChatSession>(emptyChatSession)
  const [piSession, setPiSession] = useState<ChatSession>(emptyChatSession)
  const flushRef = useRef<(() => Promise<void>) | null>(null)

  // Derived: is the workspace layout visible?
  const isWorkspace = mode === 'desk' || mode === 'pi'
  // Derived: which agent backend does the chat panel connect to?
  const chatAgent: 'lya' | 'pi' = mode === 'pi' ? 'pi' : 'lya'
  const chatSession = chatAgent === 'pi' ? piSession : deskSession
  const setChatSession = chatAgent === 'pi' ? setPiSession : setDeskSession

  // i18n context value
  const handleSetLocale = useCallback((l: Locale) => {
    setLocaleState(l)
    saveLocale(l)
  }, [])

  const handleModeChange = useCallback((m: AppMode) => {
    setModeState(m)
    saveMode(m)
  }, [])

  const i18nValue = useMemo(() => ({
    locale,
    setLocale: handleSetLocale,
    t: (key: string, params?: Record<string, string | number>) => tFn(locale, key, params),
  }), [locale, handleSetLocale])

  // Fetch current user name (from Authelia headers)
  useEffect(() => {
    async function fetchUser() {
      try {
        const res = await getJSON<{ name: string; user: string; email: string }>('/api/me')
        if (res.ok && res.data && res.data.name) {
          setUserName(res.data.name)
        }
      } catch { /* ignore */ }
    }
    fetchUser()
  }, [])

  // Fetch available agents — hide pi button if not configured
  useEffect(() => {
    async function fetchAgents() {
      try {
        const res = await getJSON<Array<{ id: string }>>('/api/agents')
        if (res.ok && res.data) {
          const hasPi = res.data.some((a) => a.id === 'pi')
          setPiAvailable(hasPi)
          // If pi is not available but mode is 'pi', fall back to desk
          if (!hasPi && mode === 'pi') {
            setModeState('desk')
            saveMode('desk')
          }
        }
      } catch { /* ignore — pi button stays hidden */ }
    }
    fetchAgents()
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // Fetch programme data when niveau or locale changes
  const fetchProgramme = useCallback(async (niv: string, lang: string) => {
    setProgramme(null)
    setProgrammeError(false)
    try {
      const res = await getJSON<ProgrammeData>(`/api/programme?niveau=${niv}&lang=${lang}`)
      if (res.ok && res.data) {
        setProgramme(res.data)
        setProgrammeError(false)
      } else {
        setProgrammeError(true)
      }
    } catch {
      setProgrammeError(true)
    }
  }, [])

  useEffect(() => {
    fetchProgramme(niveau, locale)
  }, [niveau, locale, fetchProgramme])

  const handleNiveauChange = useCallback((n: Niveau) => {
    setNiveau(n)
    saveNiveau(n)
  }, [])

  const handleSave = useCallback(async (content: string) => {
    if (!currentFile) return
    const res = await putText(`/api/file?path=${encodeURIComponent(currentFile)}`, content)
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok) {
      setLastSavedContent(content)
    }
  }, [currentFile])

  const handleFileSelect = async (path: string) => {
    if (flushRef.current) {
      await flushRef.current()
    }

    const res = await getText(`/api/file?path=${encodeURIComponent(path)}`)
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok && res.data !== null) {
      setCurrentFile(path)
      setFileContent(res.data)
      setLastSavedContent(res.data)
    }
  }

  const handleInsertFromChat = (text: string) => {
    const newContent = fileContent ? fileContent + '\n\n' + text : text
    setFileContent(newContent)
  }

  const handleExport = async (format: 'pdf' | 'docx') => {
    if (!currentFile) return
    if (flushRef.current) {
      await flushRef.current()
    }
    const res = await postBlob(`/api/export/${format}`, { path: currentFile })
    if (res.authExpired) {
      handleAuthExpired()
      return
    }
    if (res.ok && res.blob) {
      const url = URL.createObjectURL(res.blob)
      const a = document.createElement('a')
      a.href = url
      a.download = currentFile.replace(/\.md$/, `.${format}`)
      a.click()
      URL.revokeObjectURL(url)
    }
  }

  const handleFileTreeRefresh = () => setRefreshKey((k) => k + 1)

  // Called by Chat when pi writes a file — reload it in the editor
  const handleFileChanged = useCallback(async (path: string) => {
    if (path === currentFile) {
      const res = await getText(`/api/file?path=${encodeURIComponent(path)}`)
      if (res.ok && res.data !== null) {
        setFileContent(res.data)
        setLastSavedContent(res.data)
      }
    }
    // Refresh file tree in case a new file was created
    setRefreshKey((k) => k + 1)
  }, [currentFile])

  if (isMobile) {
    return (
      <I18nContext.Provider value={i18nValue}>
        <div className="app app--mobile">
          <Toolbar
            currentFile={currentFile}
            niveau={niveau}
            mode={mode}
            piAvailable={piAvailable}
            onNiveauChange={handleNiveauChange}
            onModeChange={handleModeChange}
            onExportPDF={() => handleExport('pdf')}
            onExportDOCX={() => handleExport('docx')}
          />
          {isWorkspace ? (
            <MobileLayout
              currentFile={currentFile}
              fileContent={fileContent}
              lastSavedContent={lastSavedContent}
              refreshKey={refreshKey}
              programme={programme}
              programmeError={programmeError}
              onRetryProgramme={() => fetchProgramme(niveau, locale)}
              onFileSelect={handleFileSelect}
              onFileTreeRefresh={handleFileTreeRefresh}
              onFileContentChange={setFileContent}
              onSave={handleSave}
              onFlushRef={flushRef}
              onInsertFromChat={handleInsertFromChat}
              agent={chatAgent}
              onFileChanged={handleFileChanged}
              chatSession={chatSession}
              onChatSessionChange={setChatSession}
              userName={userName}
            />
          ) : (
            <LyaChat userName={userName} messages={lyaMessages} onMessagesChange={setLyaMessages} />
          )}
        </div>
      </I18nContext.Provider>
    )
  }

  return (
    <I18nContext.Provider value={i18nValue}>
      <div className="app">
        <Toolbar
          currentFile={currentFile}
          niveau={niveau}
          mode={mode}
          piAvailable={piAvailable}
          onNiveauChange={handleNiveauChange}
          onModeChange={handleModeChange}
          onExportPDF={() => handleExport('pdf')}
          onExportDOCX={() => handleExport('docx')}
          onToggleProgramme={() => setProgrammeDrawerOpen((o) => !o)}
        />
        {isWorkspace ? (
          <div className="workspace">
            <Allotment>
              <Allotment.Pane preferredSize={220} minSize={160} maxSize={400}>
                <FileTree
                  onSelect={handleFileSelect}
                  onRefresh={handleFileTreeRefresh}
                  refreshKey={refreshKey}
                />
              </Allotment.Pane>
              <Allotment.Pane preferredSize="50%">
                <Editor
                  content={fileContent}
                  lastSavedContent={lastSavedContent}
                  onChange={setFileContent}
                  onSave={handleSave}
                  onFlushRef={flushRef}
                  filePath={currentFile}
                />
              </Allotment.Pane>
              <Allotment.Pane preferredSize={360} minSize={280}>
                <Chat
                  currentFile={currentFile}
                  onInsert={handleInsertFromChat}
                  programme={programme}
                  agent={chatAgent}
                  onFileChanged={handleFileChanged}
                  session={chatSession}
                  onSessionChange={setChatSession}
                  userName={userName}
                />
              </Allotment.Pane>
            </Allotment>
          </div>
        ) : (
          <LyaChat userName={userName} messages={lyaMessages} onMessagesChange={setLyaMessages} />
        )}
        <ProgrammeDrawer
          open={programmeDrawerOpen}
          onClose={() => setProgrammeDrawerOpen(false)}
          programme={programme}
          programmeError={programmeError}
          onRetryProgramme={() => fetchProgramme(niveau, locale)}
        />
      </div>
    </I18nContext.Provider>
  )
}
