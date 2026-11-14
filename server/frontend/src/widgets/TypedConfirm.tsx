import { useCallback, useState } from 'react'
import { Button, Dialog, Input } from '@/ui/controls'

interface Ask {
  title: string
  body?: string
  /** The exact text the operator has to type before the action unlocks. */
  phrase: string
  confirm: string
  /** Return false to keep the dialog open (e.g. the request failed and a toast was shown). */
  run: () => Promise<unknown>
}

/**
 * Confirmation for privilege changes and irreversible deletes: the action only
 * unlocks once the operator types the name of what they are about to affect.
 * A plain "Are you sure" button is too easy to click through on these.
 */
export function useTypedConfirm() {
  const [state, setState] = useState<Ask | null>(null)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const ask = useCallback((o: Ask) => { setTyped(''); setState(o) }, [])
  const match = !!state && typed.trim() === state.phrase
  const submit = async () => {
    if (!state || !match) return
    setBusy(true)
    try { if ((await state.run()) !== false) setState(null) } finally { setBusy(false) }
  }
  const dialog = (
    <Dialog open={!!state} onOpenChange={(o) => { if (!o && !busy) setState(null) }} title={state?.title ?? ''} description={state?.body}
      footer={<>
        <Button variant="ghost" onClick={() => setState(null)} disabled={busy}>Cancel</Button>
        <Button variant="danger" loading={busy} disabled={!match} onClick={submit}>{state?.confirm}</Button>
      </>}>
      <form onSubmit={(e) => { e.preventDefault(); void submit() }} className="space-y-1.5">
        <label className="block text-xs text-muted" htmlFor="typed-confirm">
          Type <span className="rounded bg-hover px-1 py-0.5 font-mono text-ink">{state?.phrase}</span> to confirm
        </label>
        <Input id="typed-confirm" mono autoComplete="off" spellCheck={false} value={typed} onChange={(e) => setTyped(e.target.value)} />
      </form>
    </Dialog>
  )
  return { ask, dialog }
}
