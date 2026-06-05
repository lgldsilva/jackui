import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import { bootstrapTheme } from './lib/theme'
import './index.css'

// Set the dark class + color-scheme BEFORE React renders so the first paint
// already matches the user's stored choice (avoids a flash of the wrong theme).
bootstrapTheme()

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>,
)
