import '../styles/globals.css'
import { createRoot } from 'react-dom/client'
import { initTheme } from '@/lib/theme'
import { TooltipProvider } from '@/ui/controls'
import { Toasts } from '@/app/Toasts'
import { Player } from '@/pages/public/player'

initTheme()
createRoot(document.getElementById('app')!).render(
  <TooltipProvider delayDuration={250}>
    <Player />
    <Toasts />
  </TooltipProvider>,
)
