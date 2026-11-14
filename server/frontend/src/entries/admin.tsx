import '../styles/globals.css'
import { createRoot } from 'react-dom/client'
import { initTheme } from '@/lib/theme'
import { AdminApp } from '@/app/AdminApp'

initTheme()
createRoot(document.getElementById('app')!).render(<AdminApp />)
