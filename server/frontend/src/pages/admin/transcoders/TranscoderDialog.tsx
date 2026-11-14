import { useEffect, useState } from 'react'
import { Button, Dialog, Field, Input, Segmented, Select, Switch } from '@/ui/controls'
import { Legend } from '@/ui/console'
import { validateMount } from '../streams/MountDialog'
import { BITRATE, MP3_RATES, createTranscoder, updateTranscoder, type Transcoder, type TranscoderBody } from './model'

const FRAMES = ['5', '10', '20', '40', '60']

/**
 * Add or edit a transcoder. Editing restarts a running encoder (the server
 * stops it and starts it again with the new settings), which cuts listeners on
 * the output mount for a moment, so the footer says so.
 */
export function TranscoderDialog({ open, onOpenChange, edit, all, mounts, onDone }: {
  open: boolean; onOpenChange: (o: boolean) => void; edit: Transcoder | null; all: Transcoder[]; mounts: string[]; onDone: () => void
}) {
  const [name, setName] = useState('')
  const [input, setInput] = useState('')
  const [output, setOutput] = useState('')
  const [format, setFormat] = useState<'mp3' | 'opus'>('mp3')
  const [bitrate, setBitrate] = useState('128')
  const [enabled, setEnabled] = useState(true)
  const [visibility, setVisibility] = useState('follow')
  const [rate, setRate] = useState('0')
  const [channels, setChannels] = useState('0')
  const [app, setApp] = useState('audio')
  const [frame, setFrame] = useState('20')
  const [complexity, setComplexity] = useState('0')
  const [vbr, setVbr] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [touched, setTouched] = useState(false)

  useEffect(() => {
    if (!open) return
    const t = edit
    setName(t?.name ?? ''); setInput(t?.input ?? ''); setOutput(t?.output ?? ''); setFormat(t?.format === 'opus' ? 'opus' : 'mp3')
    setBitrate(String(t?.bitrate || 128)); setEnabled(t?.enabled ?? true); setVisibility(t?.visibility || 'follow')
    setRate(String(t?.sample_rate ?? 0)); setChannels(String(t?.channels ?? 0)); setApp(t?.opus_application || 'audio')
    setFrame(String(t?.opus_frame_size_ms || 20)); setComplexity(String(t?.opus_complexity ?? 0)); setVbr(t?.opus_vbr ?? true)
    setError(null); setTouched(false)
  }, [open, edit])

  const others = all.filter((t) => t.name !== edit?.name)
  const bitN = Number(bitrate), [lo, hi] = BITRATE[format]
  const cxN = Number(complexity)
  const errs = {
    name: !name.trim() ? 'Give the transcoder a name.' : others.some((t) => t.name === name.trim()) ? 'Another transcoder already has this name.' : null,
    input: validateMount(input, []),
    output: validateMount(output, []) ?? (output.trim().replace(/^(?!\/)/, '/') === input.trim().replace(/^(?!\/)/, '/') ? 'The output must differ from the input.' : others.some((t) => t.output === output.trim().replace(/^(?!\/)/, '/')) ? 'Another transcoder already writes to this mount.' : null),
    bitrate: !Number.isInteger(bitN) || bitN < lo || bitN > hi ? `${format === 'mp3' ? 'MP3' : 'Opus'} accepts ${lo} to ${hi} kbps.` : null,
    complexity: !Number.isInteger(cxN) || cxN < 0 || cxN > 10 ? 'Whole number from 0 to 10.' : null,
  }
  const bad = Object.values(errs).some(Boolean)
  const norm = (m: string) => (m.trim().startsWith('/') ? m.trim() : `/${m.trim()}`)

  const submit = async () => {
    setTouched(true); setError(null)
    if (bad) return
    const opus = format === 'opus'
    const body: TranscoderBody = {
      name: name.trim(), input_mount: norm(input), output_mount: norm(output), format, bitrate: bitN, enabled, visibility: visibility === 'follow' ? '' : visibility,
      sample_rate: Number(rate) || undefined, channels: Number(channels) || undefined,
      opus_application: opus ? app : undefined, opus_vbr: opus ? vbr : undefined,
      opus_complexity: opus && cxN > 0 ? cxN : undefined, opus_frame_size_ms: opus ? Number(frame) : undefined,
    }
    setBusy(true)
    try {
      if (edit) await updateTranscoder(edit.name, body); else await createTranscoder(body)
      onDone(); onOpenChange(false)
    } catch (e) { setError((e as Error).message || 'Save failed') }
    finally { setBusy(false) }
  }

  const show = (k: keyof typeof errs) => (touched ? errs[k] : null)
  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)} title={edit ? `Edit ${edit.name}` : 'Add transcoder'} width="max-w-xl"
      description="Decodes one mount and re-encodes it to another. The output mount appears as soon as the input is live."
      footer={<>
        {edit?.active && <span className="mr-auto self-center text-xs text-faint">Saving restarts the encoder; output listeners reconnect.</span>}
        <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button>
        <Button variant="primary" loading={busy} onClick={submit}>{edit ? 'Save' : 'Add transcoder'}</Button>
      </>}>
      <form className="max-h-[62vh] space-y-4 overflow-y-auto pr-1" onSubmit={(e) => { e.preventDefault(); if ((e.nativeEvent as SubmitEvent).submitter?.getAttribute('data-submit') !== null) void submit() }}>
        <button type="submit" data-submit className="hidden" aria-hidden tabIndex={-1} />
        <datalist id="tc-mounts">{mounts.map((m) => <option key={m} value={m} />)}</datalist>
        <Field label="Name" error={show('name')} hint="Identifies this transcoder in the list and in the API.">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Mobile 64k" autoFocus aria-invalid={!!show('name')} />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Input mount" error={show('input')} hint="Existing mount to decode.">
            <Input mono list="tc-mounts" value={input} onChange={(e) => setInput(e.target.value)} placeholder="/live" spellCheck={false} aria-invalid={!!show('input')} />
          </Field>
          <Field label="Output mount" error={show('output')} hint="New mount to publish.">
            <Input mono value={output} onChange={(e) => setOutput(e.target.value)} placeholder="/live-64" spellCheck={false} aria-invalid={!!show('output')} />
          </Field>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Format">
            <div><Segmented<'mp3' | 'opus'> label="Output format" value={format} onChange={(f) => { setFormat(f); setBitrate(f === 'opus' ? '96' : '128') }} options={[{ value: 'mp3', label: 'MP3' }, { value: 'opus', label: 'Opus' }]} /></div>
          </Field>
          <Field label="Bitrate (kbps)" error={show('bitrate')} hint={`${lo} to ${hi}. ${format === 'opus' ? '96 is transparent for most music.' : '128 is the usual stereo choice.'}`}>
            <Input mono inputMode="numeric" value={bitrate} onChange={(e) => setBitrate(e.target.value)} aria-invalid={!!show('bitrate')} />
          </Field>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Sample rate" hint={format === 'opus' ? 'Opus always emits 48000 Hz; this is ignored.' : 'Auto follows the input.'}>
            <Select value={rate} onValueChange={setRate} options={MP3_RATES.map((r) => ({ value: String(r), label: r ? `${r} Hz` : 'Auto' }))} />
          </Field>
          <Field label="Channels" hint="Auto produces stereo.">
            <Select value={channels} onValueChange={setChannels} options={[{ value: '0', label: 'Auto (stereo)' }, { value: '1', label: 'Mono' }, { value: '2', label: 'Stereo' }]} />
          </Field>
        </div>
        <Field label="Listing" hint="Whether the output is advertised on the landing page. Unlisted mounts still stream by URL.">
          <Select value={visibility} onValueChange={setVisibility} options={[{ value: 'follow', label: 'Follow the input mount' }, { value: 'public', label: 'Always listed' }, { value: 'unlisted', label: 'Never listed' }]} />
        </Field>
        {format === 'opus' && (
          <fieldset className="space-y-4 rounded-md border border-line p-3">
            <legend className="px-1"><Legend>Opus encoder</Legend></legend>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Application" hint="Audio for music, VoIP for speech, low-delay trades quality for latency.">
                <Select value={app} onValueChange={setApp} options={[{ value: 'audio', label: 'Audio (music)' }, { value: 'voip', label: 'VoIP (speech)' }, { value: 'lowdelay', label: 'Low delay' }]} />
              </Field>
              <Field label="Frame size (ms)" hint="Larger frames compress better and add latency.">
                <Select value={frame} onValueChange={setFrame} options={FRAMES.map((f) => ({ value: f, label: f }))} />
              </Field>
            </div>
            <div className="grid items-start gap-4 sm:grid-cols-2">
              <Field label="Complexity" error={show('complexity')} hint="0 uses the encoder default, 1 to 10 trades CPU for quality.">
                <Input mono inputMode="numeric" value={complexity} onChange={(e) => setComplexity(e.target.value)} aria-invalid={!!show('complexity')} />
              </Field>
              <div className="flex items-center justify-between gap-3 pt-6"><div><div className="text-[13px] text-ink">Variable bitrate</div><div className="text-xs text-muted">Saves bandwidth on quiet passages.</div></div><Switch label="Variable bitrate" checked={vbr} onCheckedChange={setVbr} /></div>
            </div>
          </fieldset>
        )}
        <div className="flex items-center justify-between gap-3 rounded-md border border-line px-3 py-2.5">
          <div><div className="text-[13px] text-ink">Run this transcoder</div><div className="text-xs text-muted">Off keeps the settings without using any CPU.</div></div>
          <Switch label="Run this transcoder" checked={enabled} onCheckedChange={setEnabled} />
        </div>
        {error && <p role="alert" className="text-xs text-danger">{error}</p>}
      </form>
    </Dialog>
  )
}
