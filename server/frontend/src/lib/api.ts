// API client used by every admin page. Centralises one piece of
// cross-cutting behaviour: when the server replies 401, we forward
// the browser to /login. That covers the most common stale-session
// case (operator was logged in, the server restarted, our session
// cookie is no longer valid). Without this the dashboard sat on a
// blank page silently retrying every 15 s after a deploy.

// loginRedirected guards against ping-ponging from /login -> /login.
// Shared module state so concurrent failed requests only navigate once.
let loginRedirected = false

export function redirectToLogin() {
  if (loginRedirected) return
  // Already on /login? Don't re-navigate.
  if (typeof window !== 'undefined' && window.location.pathname.startsWith('/login')) {
    return
  }
  loginRedirected = true
  // Preserve the page the operator was looking at as ?next= so the
  // login flow can return them after auth.
  const next = encodeURIComponent(window.location.pathname + window.location.search)
  window.location.assign(`/login?next=${next}`)
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const res = await fetch(path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
    credentials: 'same-origin',
  })
  if (res.status === 401) {
    redirectToLogin()
    throw new Error('Unauthorized')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  del: <T>(path: string) => request<T>('DELETE', path),
}

/** Page data injected by the Go ShellRenderer (window.__TINYICE__). */
export function page<T = import('../types').AdminData>(): T {
  return window.__TINYICE__ as unknown as T
}

/**
 * Legacy /admin/* mutation endpoints are form-style and CSRF-checked
 * (server/auth.go isCSRFSafe). They accept the token as the `csrf` form
 * field or the X-CSRF-Token header; JSON endpoints under /api need neither.
 */
export async function adminPost(path: string, fields: Record<string, string | number | boolean> = {}): Promise<Response> {
  const body = new URLSearchParams()
  for (const [k, v] of Object.entries(fields)) body.set(k, String(v))
  const res = await fetch(path, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-CSRF-Token': page().csrfToken },
    body,
  })
  if (res.status === 401) {
    redirectToLogin()
    throw new Error('Unauthorized')
  }
  if (!res.ok) throw new Error((await res.text().catch(() => '')) || res.statusText)
  return res
}
