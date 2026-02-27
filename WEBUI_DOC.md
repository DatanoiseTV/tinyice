# TinyIce WebUI Documentation

## Overview
Single-page admin interface (`admin.html` ~2700 lines) with tabs for different sections.

## Routes & Handlers

### Admin Routes (POST)
| Route | Handler | Tab | Status |
|-------|---------|-----|--------|
| `/admin/add-mount` | handleAddMount | Mounts | submitForm |
| `/admin/remove-mount` | handleRemoveMount | Mounts | htmx |
| `/admin/toggle-mount` | handleToggleMount | Mounts | htmx |
| `/admin/toggle-visible` | handleToggleVisible | Mounts | htmx |
| `/admin/update-fallback` | handleUpdateFallback | Mounts | submitForm |
| `/admin/add-relay` | handleAddRelay | Relays | submitForm |
| `/admin/toggle-relay` | handleToggleRelay | Relays | htmx |
| `/admin/delete-relay` | handleDeleteRelay | Relays | htmx |
| `/admin/add-transcoder` | handleAddTranscoder | Transcoders | submitForm |
| `/admin/toggle-transcoder` | handleToggleTranscoder | Transcoders | htmx |
| `/admin/delete-transcoder` | handleDeleteTranscoder | Transcoders | htmx |
| `/admin/add-user` | handleAddUser | Users | htmx |
| `/admin/remove-user` | handleRemoveUser | Users | htmx |
| `/admin/add-banned-ip` | handleAddBannedIP | Security | htmx |
| `/admin/remove-banned-ip` | handleRemoveBannedIP | Security | htmx |
| `/admin/add-whitelisted-ip` | handleAddWhitelistedIP | Security | htmx |
| `/admin/remove-whitelisted-ip` | handleRemoveWhitelistedIP | Security | htmx |
| `/admin/add-webhook` | handleAddWebhook | Webhooks | submitForm |
| `/admin/delete-webhook` | handleDeleteWebhook | Webhooks | submitForm |
| `/admin/autodj/add` | handleAddAutoDJ | AutoDJ | submitForm |
| `/admin/autodj/toggle` | handleToggleAutoDJ | AutoDJ | submitForm |
| `/admin/autodj/delete` | handleDeleteAutoDJ | AutoDJ | submitForm |
| `/admin/autodj/update` | handleUpdateAutoDJ | AutoDJ | submitForm |
| `/admin/player/save-playlist` | handleSavePlaylist | AutoDJ | submitForm |
| `/admin/player/shuffle` | handlePlayerShuffle | AutoDJ | submitForm |
| `/admin/player/loop` | handlePlayerLoop | AutoDJ | submitForm |
| `/admin/player/next` | handlePlayerNext | AutoDJ | submitForm |
| `/admin/player/toggle` | handlePlayerToggle | AutoDJ | submitForm |
| `/admin/player/queue` | handlePlayerQueue | AutoDJ | submitForm |
| `/admin/player/playlist-action` | handlePlayerPlaylistAction | AutoDJ | submitForm |
| `/admin/player/clear-playlist` | handleClearPlaylist | AutoDJ | submitForm |
| `/admin/player/scan` | handlePlayerScan | AutoDJ | submitForm |
| `/admin/player/reorder` | handlePlayerReorder | AutoDJ | submitForm |
| `/admin/kick` | handleKick | Live | submitForm |
| `/admin/hotswap` | handleHotSwap | Dashboard | submitForm |

### Admin Routes (GET)
| Route | Handler | Returns |
|-------|---------|---------|
| `/admin` | handleAdmin | Full page |
| `/admin/security-stats` | handleGetSecurityStats | JSON |
| `/admin/transcoder-stats` | handleTranscoderStats | JSON |
| `/admin/statistics` | handleGetStats | JSON |
| `/admin/history` | handleHistory | JSON |
| `/admin/player/files` | handlePlayerFiles | JSON |
| `/admin/insights` | handleInsights | JSON |

## Tabs & Content

### 1. Dashboard (`#tab-dashboard`)
- Traffic chart (canvas)
- Live streams table (real-time updates via polling)
- Quick stats

### 2. Mounts (`#tab-mounts`)
- Add mount form
- Mounts table with:
  - Fallback config (inline input)
  - Toggle visibility button
  - Toggle enable/disable button
  - Remove button

### 3. Relays (`#tab-relays`)
- Add relay form
- Relays table with:
  - Edit button (TODO)
  - Toggle visible button
  - Toggle start/stop button
  - Delete button

### 4. Transcoders (`#tab-transcoding`)
- Add transcoder form
- Transcoders table with:
  - Toggle start/stop button
  - Delete button

### 5. Webhooks (`#tab-webhooks`)
- Add webhook form
- Webhooks table with delete button

### 6. AutoDJ (`#tab-streamer`)
- Add AutoDJ form
- AutoDJ cards with:
  - Instance info & status
  - Play/Stop button
  - Playlist management
  - Queue management
  - Library browser
  - Rescan/pause controls

### 7. Security (`#tab-security`)
- Ban IP form + list
- Whitelist IP form + list
- Auth failures table (polled)
- Connection scanners table (polled)

### 8. Users (`#tab-users`)
- Add user form
- Users table with delete button

## JavaScript Functions

### Form Handling
- `submitForm(event, reload, onSuccess)` - Handles all form submissions via fetch

### UI Components
- `showTab(id, updateHash)` - Switch tabs
- `toggleStatsBar()` - Toggle bottom stats bar

### AutoDJ
- `showEditAutoDJ(data)` - Open edit modal
- `closeEditAutoDJ()` - Close edit modal
- `loadLibrary(mount, container, subPath)` - Browse music directory

### Data Loading (Polling)
- `loadStats()` - Load global stats every 2s
- `loadStreamData()` - Load streams every 5s
- `loadRelayData()` - Load relays every 5s
- `loadTranscoderData()` - Load transcoders every 5s
- `loadSecurityStats()` - Load security stats every 10s
- `loadInsights()` - Load insights every 10s
- `loadHistory(mount)` - Load song history

### Real-time Updates
- WebSocket connection for live listener updates
- SSE for stats/events (optional)

### External Libraries
- `lucide.min.js` - Icons
- `Sortable.min.js` - Drag-drop playlist reordering
- `htmx.org` - Partial page updates (NEW)
- `alpine.js` - Reactive components (NEW)

## Inline Styles (TODO: Extract to CSS)

### Common Patterns
```css
/* Cards */
.card { background: var(--bg-alt); border-radius: 8px; }
.card-padding { padding: 1.5rem; }
.card-table { padding: 0; overflow: hidden; }

/* Forms */
.form-group { margin-bottom: 1rem; }
.form-row { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }

/* Buttons */
.btn-sm { padding: 0.3rem 0.8rem; font-size: 0.8rem; }
.btn-danger { background: var(--danger); }
.btn-primary { background: var(--primary); }

/* Tables */
table { width: 100%; border-collapse: collapse; }
th, td { padding: 0.75rem; text-align: left; }

/* Status */
.status-pill { display: inline-flex; align-items: center; gap: 0.3rem; }
.dot { width: 8px; height: 8px; border-radius: 50%; }
.active .dot { background: var(--success); }
```

## Components to Extract

### 1. Modal (Edit AutoDJ)
- Currently inline in admin.html
- Has form fields for all AutoDJ settings
- Uses `showEditAutoDJ()` / `closeEditAutoDJ()`

### 2. Stream Row (Template)
- Cloned for live streams
- Has kick button

### 3. Relay Row (Template)
- Cloned for relays
- Has edit/visible/toggle/delete buttons

### 4. Transcoder Row (Template)
- Cloned for transcoders
- Has toggle/delete buttons

### 5. Library Browser
- Dynamic JS-generated content
- Recursive folder navigation

## UI Framework Recommendation

### Option A: DaisyUI + Tailwind CSS
- Beautiful pre-built components (modals, dropdowns, tabs, tables)
- Dark mode built-in
- Can embed locally (~100KB for both)
- Components: Alerts, Buttons, Cards, Forms, Modal, Navbar, Tables, Tabs

### Option B: Flowbite + Tailwind
- Similar to DaisyUI
- More components available
- Slightly larger

### Option C: Keep current + refactor
- Extract inline styles to CSS
- Use htmx for partial updates
- Add Alpine.js for reactivity

**Recommendation:** DaisyUI + Tailwind would give beautiful UI with minimal effort. Can be embedded locally.

## Migration Plan

1. Add Tailwind + DaisyUI to assets
2. Replace inline styles with Tailwind classes
3. Replace custom components with DaisyUI components
4. Keep htmx for form handling
5. Use Alpine.js for complex interactivity (library browser, etc)
