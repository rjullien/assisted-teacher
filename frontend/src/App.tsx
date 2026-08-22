import { Allotment } from 'allotment'
import 'allotment/dist/style.css'
import { useState } from 'react'
import FileTree from './components/FileTree'
import Editor from './components/Editor'
import Chat from './components/Chat'
import Toolbar from './components/Toolbar'

export default function App() {
  const [currentFile, setCurrentFile] = useState<string | null>(null)
  const [fileContent, setFileContent] = useState<string>('')
  const [refreshKey, setRefreshKey] = useState(0)

  const handleFileSelect = async (path: string) => {
    try {
      const res = await fetch(`/api/file?path=${encodeURIComponent(path)}`)
      if (res.ok) {
        const text = await res.text()
        setCurrentFile(path)
        setFileContent(text)
      }
    } catch (err) {
      console.error('Failed to load file:', err)
    }
  }

  const handleSave = async (content: string) => {
    if (!currentFile) return
    try {
      await fetch(`/api/file?path=${encodeURIComponent(currentFile)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'text/plain' },
        body: content,
      })
      setFileContent(content)
    } catch (err) {
      console.error('Failed to save file:', err)
    }
  }

  const handleInsertFromChat = (text: string) => {
    // Append AI-generated content to the current editor content
    const newContent = fileContent ? fileContent + '\n\n' + text : text
    setFileContent(newContent)
    handleSave(newContent)
  }

  const handleExport = async (format: 'pdf' | 'docx') => {
    if (!currentFile) return
    try {
      const res = await fetch(`/api/export/${format}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: currentFile }),
      })
      if (res.ok) {
        const blob = await res.blob()
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = currentFile.replace(/\.md$/, `.${format}`)
        a.click()
        URL.revokeObjectURL(url)
      }
    } catch (err) {
      console.error('Export failed:', err)
    }
  }

  const handleFileTreeRefresh = () => setRefreshKey((k) => k + 1)

  return (
    <div className="app">
      <Toolbar
        currentFile={currentFile}
        onExportPDF={() => handleExport('pdf')}
        onExportDOCX={() => handleExport('docx')}
      />
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
              onChange={setFileContent}
              onSave={handleSave}
              filePath={currentFile}
            />
          </Allotment.Pane>
          <Allotment.Pane preferredSize={360} minSize={280}>
            <Chat
              currentFile={currentFile}
              onInsert={handleInsertFromChat}
            />
          </Allotment.Pane>
        </Allotment>
      </div>
    </div>
  )
}
