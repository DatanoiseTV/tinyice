import '../styles/globals.css'
import { createRoot } from 'react-dom/client'
import { initTheme } from '@/lib/theme'
import { Embed } from '@/pages/public/embed'

initTheme()
createRoot(document.getElementById('app')!).render(<Embed />)
