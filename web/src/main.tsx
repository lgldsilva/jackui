import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import { bootstrapTheme } from './lib/theme'
import { registerServiceWorker } from './lib/push'
import './index.css'

// Set the dark class + color-scheme BEFORE React renders so the first paint
// already matches the user's stored choice (avoids a flash of the wrong theme).
bootstrapTheme()

// Push-only service worker (no fetch handler — see web/public/sw.js). Needed
// so Web Push arrives with the tab closed; iOS additionally requires the PWA
// installed to the home screen.
registerServiceWorker()

import './lib/i18n'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>,
)
