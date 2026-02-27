# WebUI Refactoring Plan

## Current State
- `admin.html`: 2691 lines (massive, hard to maintain)
- Many inline styles, duplicated patterns
- 29 remaining `submitForm()` calls not using htmx
- Template elements used as JS cloning sources
- Inline JS generating HTML for dynamic content

---

## Phase 1: Extract Components (Low Risk)

### 1.1 Create partial templates directory
```
templates/
  partials/
    stream-row.html      # Live streams table row
    relay-row.html       # Relays table row
    autodj-card.html     # AutoDJ instance card
    user-row.html        # User management row
    banned-ip-row.html    # Banned IP row
    whitelist-ip-row.html
```

### 1.2 Extract reusable sections
- Tab navigation header
- Sidebar/sidebar-stats
- Bottom stats bar
- Modal templates (edit autodj)
- Template elements (currently at bottom of admin.html)

---

## Phase 2: Add Remaining HTMX Support (Medium Risk)

### Already converted:
- Toggle mount enabled/disabled
- Toggle mount visibility
- Remove mount
- Add/remove users
- Add/remove banned IPs
- Add/remove whitelisted IPs

### Still need conversion (29 remaining):

| Category | Forms | Priority |
|----------|-------|----------|
| **Mounts tab** | Add mount, toggle fallback | High |
| **Relays tab** | Add relay, toggle/delete | High |
| **Webhooks** | Add/delete webhook | Medium |
| **AutoDJ** | All controls (start/stop, queue, playlist) | Medium |
| **Live streams** | Kick, toggle visible/relay | Medium |
| **Transcoders** | Toggle/delete | Medium |
| **Player actions** | Queue, scan | Low |

### Strategy:
- Simple toggles: Convert to htmx (return button HTML)
- Add operations: Convert to htmx (append to list)
- Delete operations: Convert to htmx (remove from DOM)
- Complex forms (Add mount/relay): Keep full page or use modal

---

## Phase 3: Style Refactoring (Higher Risk)

### 3.1 Extract inline styles to CSS
- Move inline `style="..."` to CSS classes
- Create design tokens (colors, spacing)
- Use CSS custom properties

### 3.2 Common CSS classes
```css
.card-padding { padding: 1.5rem; }
.card-table { padding: 0; overflow: hidden; }
.form-row { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
.btn-sm-padding { padding: 0.3rem 0.8rem; }
```

---

## Phase 4: Template Organization

### 4.1 Extract tab content to partials
```
templates/
  admin/
    base.html           # Common layout (head, sidebar)
    mounts.html         # Tab content
    relays.html
    users.html
    autodj.html
    security.html
    ...
```

### 4.2 Create admin layout
- `admin/base.html` - Contains head, sidebar, tabs, footer
- Each tab content as separate included template

---

## Risk Assessment

| Phase | Risk | Reason |
|-------|------|--------|
| Phase 1 | Low | Just extracting files, no logic changes |
| Phase 2 | Medium | Handler changes, could break functionality |
| Phase 3 | Medium | CSS changes could affect visual appearance |
| Phase 4 | High | Major restructuring, template includes |

---

## Recommended Approach

1. **Phase 1**: Extract components first (safe)
2. **Phase 2**: Continue htmx conversion incrementally
3. **Phase 3 & 4**: Only do if really needed for maintainability

The current admin.html, while large, does work. The main value-add is htmx conversion for better UX. Full extraction is a lot of work with marginal benefit.
