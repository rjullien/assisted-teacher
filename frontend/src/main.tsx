import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './styles.css'

// iOS standalone PWA: force reflow on orientation change to fix safe-area-inset
// recalculation bug (toolbar disappears behind status bar/notch after rotate)
if ('standalone' in window.navigator || window.matchMedia('(display-mode: standalone)').matches) {
  window.addEventListener('orientationchange', () => {
    // Small delay to let iOS recalculate viewport
    setTimeout(() => {
      document.documentElement.style.setProperty('--ios-reflow', '0')
      requestAnimationFrame(() => {
        document.documentElement.style.removeProperty('--ios-reflow')
      })
    }, 100)
  })
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
)
