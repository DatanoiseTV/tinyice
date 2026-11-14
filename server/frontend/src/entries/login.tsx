import '../styles/globals.css'
import { createRoot } from 'react-dom/client'
import { initTheme } from '@/lib/theme'
import { App } from '@/pages/public/login'

initTheme()
createRoot(document.getElementById('app')!).render(<App />)
