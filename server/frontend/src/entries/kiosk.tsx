import '../styles/globals.css'
import { createRoot } from 'react-dom/client'
import { initTheme } from '@/lib/theme'
import { LiveProvider } from '@/lib/live'
import { Kiosk } from '@/pages/public/kiosk'

initTheme()
createRoot(document.getElementById('app')!).render(
  <LiveProvider>
    <Kiosk />
  </LiveProvider>,
)
