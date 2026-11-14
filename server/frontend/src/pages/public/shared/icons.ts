import { registerIcons } from '@/ui/icons'

// Glyphs only the public pages need; drawn on the same 20px grid, 1.5 stroke.
registerIcons({
  'eye-off': { d: 'M2 10s3-5.5 8-5.5c1.2 0 2.3.3 3.2.8 M18 10s-3 5.5-8 5.5c-1.2 0-2.3-.3-3.2-.8 M8.2 8.2a2.2 2.2 0 0 0 3.1 3.1 M3.5 16.5l13-13' },
  fingerprint: { d: 'M5.2 7.5a5.5 5.5 0 0 1 9.6 0 M4.5 11.5c0-.9 0-1.6.2-2.3 M10 7.2a3.6 3.6 0 0 1 3.6 3.6c0 2.2.5 4 1.4 5.4 M7 10.8a3 3 0 0 1 6 0c0 1.8.4 3.3 1 4.7 M10 10.8c0 2.1-.3 3.7-1.1 5.2 M7 15.6c.5-1.1.8-2.5.8-4.2' },
  book: { d: 'M4 4.5A1.5 1.5 0 0 1 5.5 3H16v12H5.5A1.5 1.5 0 0 0 4 16.5z M4 16.5A1.5 1.5 0 0 0 5.5 18H16v-3 M8 7h5' },
  terminal: { d: 'M3.5 4.5h13v11h-13z M6.5 8l2.5 2-2.5 2 M10.5 12.5h3' },
})
