import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import { bootstrapTheme } from './lib/theme'
import { bootRouteRestore } from './lib/bootRoute'
import { registerServiceWorker } from './lib/push'
import { installUnhandledRejectionToast } from './lib/unhandledRejection'
import { notify } from './components/Toast'
import './index.css'

// Set the dark class + color-scheme BEFORE React renders so the first paint
// already matches the user's stored choice (avoids a flash of the wrong theme).
bootstrapTheme()

// Jump to the last visited route before mount so a PWA cold-boot lands on the
// right screen instead of flashing the root (SearchPage) for a frame.
bootRouteRestore()

// Push-only service worker (no fetch handler — see web/public/sw.js). Needed
// so Web Push arrives with the tab closed; iOS additionally requires the PWA
// installed to the home screen.
registerServiceWorker()

// Last-resort feedback for rejections no handler caught (see the module doc).
installUnhandledRejectionToast((msg) => notify(msg, 'error'))

import './lib/i18n'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>,
)
