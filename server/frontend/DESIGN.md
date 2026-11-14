# TinyIce web UI: design contract

Read this fully before writing UI. It binds every contributor.

## Identity

A broadcast console, rendered flat. Dense, calm, exact. The product is a
tool people leave open for hours, so: no decoration that does not carry
information, no shadows on cards, no gradients except chart fills, no glow.

* **Legends.** Section captions and units are etched mono caps (`<Legend>`).
  Every top-level block on a page is a `<Panel legend="...">`.
* **Tally lamps** (`<Tally>`, `Panel lamp=`) mean "this is live". Never use a
  green dot for anything else.
* **Numbers** are tabular mono (`num font-mono`, `<Readout>`, `<Timecode>`).
  A value that changes live must never shift its neighbours.
* **Controls** are our own: `Knob`, `Fader`, `TransportButton`, `Segmented`,
  `Switch`. Do not reach for a stock look.
* **Icons** are ours (`ui/icons.tsx`). No other icon source, no emoji, no
  inline ad-hoc SVG icons. Need a glyph? `registerIcons({...})` from your own
  module (20px grid, 1.5 stroke, round caps).
* Copy is plain and specific. No slogans, no exclamation marks, no
  "powerful", "seamless", "professional". Say what a thing does.

## Tokens (never hard-code colours)

Tailwind names map to CSS variables in `src/styles/globals.css`:
`bg surface raised hover line line-strong ink muted faint accent accent-fg live warn danger info`.
Use `text-ink` (primary), `text-muted` (secondary), `text-faint` (tertiary).
Both themes must work: test dark and light. The accent is the operator's
branding colour, so never assume it is orange and never put accent text on
`accent` backgrounds (use `accent-fg`).

Spacing rhythm: 4px base; page gutter 20px; gap between panels 12px; panel
padding 16px. Radius: 6px controls, 8px panels. Body 13px, page title 18px.
Interactive targets are at least 32px tall (36px on touch-primary screens).

## Building blocks

* `src/ui/controls.tsx`: Button, Input, Textarea, Field, Switch, Segmented, Select, Dialog, Tip.
* `src/ui/display.tsx`: Card, PageHeader, Badge, Kbd, Skeleton, EmptyState, Table/Th/Td.
* `src/ui/console.tsx`: Legend, Panel, Readout, Timecode, Tally, Knob, Fader, TransportButton.
* `src/ui/charts.tsx`: AreaChart, Sparkline, LevelMeter, Bar.
* `src/ui/kit.tsx`: Tabs, Menu, Checkbox, useConfirm, CopyButton, CodeBlock, SettingRow, SearchInput, useAction.
* `src/lib/`: `api` (JSON client, `adminPost` for legacy form endpoints, `page()`), `live` (`useLive()`: the one shared /admin/events feed), `format`, `toast`, `theme`.

Do **not** edit these shared files. If you need a change, say so in your
report. New shared widgets go in `src/widgets/<Name>.tsx` (new files only).
Everything else you build lives in your own folder.

Every list/table needs: loading skeleton, empty state with a next action,
error state. Every destructive action goes through `useConfirm`. Every
mutation goes through `useAction` (busy flag + toast). Forms validate before
sending and show the server's error text.

## Pages and entries

* **Admin page**: `src/pages/admin/<nav-id>.tsx`, default export, no props.
  Ids are in `src/app/nav.ts`. Discovered by glob; no router edits. Put
  page-private components in `src/pages/admin/<nav-id>/`.
* **Public entry** (landing, player, embed, explore, developers, login,
  setup, kiosk): `src/entries/<name>.html` + `<name>.tsx`. Copy
  `src/entries/admin.html` and keep the literals `<title>TinyIce</title>`
  and `<div id="app"></div>`: the Go `ShellRenderer` string-replaces them.
  The script tag must be root-absolute: `/src/entries/<name>.tsx`.
  Entry names are a contract with `server/shell.go` and `vite.config.ts`.
* Page data the Go server injects is `window.__TINYICE__` (read via
  `page()`); its shape per page is in `server/handlers_*.go` (`pageData`).

## API fidelity (the hard requirement)

The Go handlers are the truth, not the old UI and not `openapi.yaml`.
1. Read the handler (`server/handlers_*.go`, routes in `server/server.go`
   around lines 237-704) and the old implementation (`.old-src/` in this
   folder, read-only reference of the previous UI).
2. Call the real endpoint on the **reference server**:
   `$S/tools/real.sh GET /api/users` (see Tools). Use real response shapes in
   your TypeScript types and your mock fixtures. Field names are exactly
   as on the wire (often snake_case).
3. JSON endpoints under `/api/*` need no CSRF token. Legacy `/admin/*`
   mutations are form-encoded and CSRF-checked: use `adminPost` (sends
   `X-CSRF-Token`). A 401 redirects to /login (handled in `lib/api`).
4. Preserve every capability and every behaviour the old page had (read its
   comments: they record bugs that were fixed). Then add what a product in
   this class should have. State in your report what you added.
5. If the backend lacks something the page needs, you may NOT edit Go.
   List the exact endpoint/field you need in your report; the integrator
   implements it.

## Mock backend (live preview)

`npm run dev` is already running at **http://localhost:5199** (do not start
or stop it; never use another port). Pages are served at production URLs
(`/admin/streams`, `/player/live`, `/login`, ...). It reloads on save.

Add fixtures in **your own** `dev/fixtures/<area>.ts` (see `dev/types.ts`,
`dev/fixtures/core.ts`): routes with the real response shapes, in-memory
state so create/delete/toggle actually work, `pages` for `window.__TINYICE__`,
`events` for extra SSE frames. Fixtures hot-reload. Never edit
`dev/mock.ts`, `dev/state.ts`, or other areas' fixtures.

## Tools (S = /private/tmp/claude-501/-Users-syso-dev-priv-tinyice/bcaabb8f-1653-4e2a-a7ce-38ef39f1a151/scratchpad)

* Screenshot: `$S/venv/bin/python $S/tools/shot.py URL OUT.png WxH [wait_s] [dark|light] [js] [--full]`
  Look at the PNG with Read. Do this at 1440x900 dark, 1280x800 light, and
  390x844 dark, for every page and every significant state (empty, loading,
  populated, dialog open: drive state via the `js` argument). Self-critique
  against this document and fix before reporting. Measure rather than
  eyeball: `js` can return `document.documentElement.scrollWidth`, element
  rects, computed styles. There must be no horizontal page overflow at 390px.
* Real server: `$S/tools/real.sh METHOD /path [json]`, throwaway instance on
  :8999 (admin/devpass), disposable data, mutate freely. Do not stop it.
* Type check: `cd server/frontend && npx tsc --noEmit` (other people's files
  may be mid-edit; only your own errors matter).
* Output pictures and temp files go in `$S/<your-area>/`. Never in the repo.

## Safety rules

* Never capture or drive the user's real screen; headless only, via shot.py.
* Do not run `npm install`, edit `package.json`, `vite.config.ts`, `tsconfig.json`.
* Do not run git commands that change state (no add/commit/checkout/stash/reset).
  The integrator commits. Do not delete files you did not create.
* No emojis anywhere. Code, identifiers, comments and commit-style text in English.
* Accessibility: every control labelled, visible focus (already global), keyboard
  reachable, `aria-live` for async status, colour is never the only signal,
  respects `prefers-reduced-motion`.
* Secrets (tokens, passwords) are shown once at creation and never logged.
* Clean up: delete any scratch you created before you finish. Disk is nearly full.

## Definition of done

`tsc` clean for your files; dark/light/phone screenshots viewed and critiqued;
all old capabilities present; fixtures mirror real shapes; real-server calls
made for each endpoint you use; report lists files, endpoints (method, path,
body, response shape), what you added beyond the old UI, known gaps, and any
backend change you need.
