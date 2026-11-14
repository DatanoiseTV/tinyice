import { registerIcons } from '@/ui/icons'

// Glyphs only the public player needs. Prefixed so they cannot collide with
// another module registering a same-named icon.
registerIcons({
  'player-share': { d: 'M14.5 7a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4z M5.5 12.2a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4z M14.5 17.4a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4z M7.4 8.9l5.2-3 M7.4 11.1l5.2 3' },
  'player-expand': { d: 'M4 8V4h4 M12 4h4v4 M16 12v4h-4 M8 16H4v-4' },
  'player-collapse': { d: 'M8 4v4H4 M16 8h-4V4 M12 16v-4h4 M4 12h4v4' },
  'player-sliders': { d: 'M3 6h14 M3 14h14 M7 3.8v4.4 M13 11.8v4.4' },
})
