import { useEffect, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Button, Dialog, Field, Input, Select, Switch } from '@/ui/controls'
import { Icon } from '@/ui/icons'
import { toast } from '@/lib/toast'
import { dj, qk, type DjConfig, type DjInstance } from './api'
import { DirectoryBrowser } from './DirectoryBrowser'

interface Form {
  name: string; mount: string; music_dir: string; format: string; bitrate: string
  loop: boolean; inject_metadata: boolean; visible: boolean
  mpd_enabled: boolean; mpd_port: string; mpd_password: string
  song_command: string; song_command_timeout: string; on_play_command: string; on_play_command_timeout: string
}

const blank: Form = {
  name: '', mount: '', music_dir: '', format: 'mp3', bitrate: '128', loop: true, inject_metadata: true, visible: true,
  mpd_enabled: false, mpd_port: '', mpd_password: '', song_command: '', song_command_timeout: '5', on_play_command: '', on_play_command_timeout: '10',
}

const fromInstance = (i: DjInstance): Form => ({
  name: i.name, mount: i.mount, music_dir: i.music_dir, format: i.format || 'mp3', bitrate: String(i.bitrate || 128),
  loop: i.loop, inject_metadata: i.inject_metadata, visible: i.visible, mpd_enabled: i.mpd_enabled, mpd_port: i.mpd_port, mpd_password: '',
  song_command: i.song_command, song_command_timeout: String(i.song_command_timeout || 5),
  on_play_command: i.on_play_command, on_play_command_timeout: String(i.on_play_command_timeout || 10),
})

type Errors = Partial<Record<keyof Form, string>>

function validate(f: Form, taken: string[]): Errors {
  const e: Errors = {}
  if (!f.name.trim()) e.name = 'Required'
  const mount = f.mount.trim()
  if (!mount) e.mount = 'Required'
  else if (!/^\/?[A-Za-z0-9._~\-/]+$/.test(mount)) e.mount = 'Use letters, digits and . _ - / only'
  else if (taken.includes(mount.startsWith('/') ? mount : `/${mount}`)) e.mount = 'Another AutoDJ already uses this mount'
  if (!f.music_dir.trim() && !f.song_command.trim()) e.music_dir = 'A music directory or a song command is required'
  const br = Number(f.bitrate)
  if (!Number.isInteger(br) || br < 8 || br > 512) e.bitrate = '8 to 512 kbps'
  if (f.mpd_enabled && !(/^\d+$/.test(f.mpd_port) && Number(f.mpd_port) >= 1 && Number(f.mpd_port) <= 65535)) e.mpd_port = 'A TCP port from 1 to 65535'
  if (f.song_command.trim()) { const t = Number(f.song_command_timeout); if (!Number.isInteger(t) || t < 1 || t > 30) e.song_command_timeout = '1 to 30 seconds' }
  if (f.on_play_command.trim()) { const t = Number(f.on_play_command_timeout); if (!Number.isInteger(t) || t < 1 || t > 60) e.on_play_command_timeout = '1 to 60 seconds' }
  return e
}

function Row({ label, help, children }: { label: string; help?: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0"><div className="text-[13px] text-ink">{label}</div>{help && <div className="text-xs text-muted">{help}</div>}</div>
      {children}
    </div>
  )
}

/** Create (instance undefined) or edit an AutoDJ. Edits PUT in place, so the playlist and queue survive a failed change. */
export function InstanceForm({ open, onOpenChange, instance, existingMounts }: {
  open: boolean; onOpenChange: (o: boolean) => void; instance?: DjInstance; existingMounts: string[]
}) {
  const qc = useQueryClient()
  const [f, setF] = useState<Form>(blank)
  const [errors, setErrors] = useState<Errors>({})
  const [serverError, setServerError] = useState('')
  const [busy, setBusy] = useState(false)
  const [browse, setBrowse] = useState(false)
  useEffect(() => { if (open) { setF(instance ? fromInstance(instance) : blank); setErrors({}); setServerError('') } }, [open, instance])
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setF((p) => ({ ...p, [k]: v }))

  const submit = async () => {
    const taken = existingMounts.filter((m) => m !== instance?.mount)
    const errs = validate(f, taken)
    setErrors(errs)
    if (Object.keys(errs).length) return
    const mount = f.mount.trim().startsWith('/') ? f.mount.trim() : `/${f.mount.trim()}`
    const cfg: DjConfig = {
      name: f.name.trim(), mount, music_dir: f.music_dir.trim(), format: f.format, bitrate: Number(f.bitrate),
      loop: f.loop, inject_metadata: f.inject_metadata, visible: f.visible, mpd_enabled: f.mpd_enabled, mpd_port: f.mpd_enabled ? f.mpd_port : '',
      song_command: f.song_command.trim(), on_play_command: f.on_play_command.trim(),
      ...(f.song_command.trim() ? { song_command_timeout: Number(f.song_command_timeout) } : {}),
      ...(f.on_play_command.trim() ? { on_play_command_timeout: Number(f.on_play_command_timeout) } : {}),
      // Sent only when typed: an empty value means "keep the stored password".
      ...(f.mpd_enabled && f.mpd_password ? { mpd_password: f.mpd_password } : {}),
    }
    setBusy(true); setServerError('')
    try {
      if (instance) await dj.update(instance.mount, cfg); else await dj.create(cfg)
      toast.success(instance ? `Saved ${cfg.mount}` : `Created ${cfg.mount}`)
      qc.invalidateQueries({ queryKey: qk.instances })
      onOpenChange(false)
    } catch (e) { setServerError((e as Error).message || 'Save failed') }
    finally { setBusy(false) }
  }

  return (
    <>
      <Dialog
        open={open} onOpenChange={(o) => !busy && onOpenChange(o)} width="max-w-xl"
        title={instance ? `Edit ${instance.mount}` : 'New AutoDJ'}
        description={instance ? 'The AutoDJ restarts with the new settings. Its playlist and queue are kept.' : 'Plays a music directory into a mount, with a playlist and a queue.'}
        footer={<>
          {serverError && <p role="alert" className="mr-auto max-w-[60%] text-xs text-danger">{serverError}</p>}
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button>
          <Button variant="primary" onClick={submit} loading={busy}>{instance ? 'Save' : 'Create'}</Button>
        </>}
      >
        <form className="max-h-[60vh] space-y-4 overflow-y-auto pr-1" onSubmit={(e) => { e.preventDefault(); void submit() }}>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Name" error={errors.name}><Input value={f.name} onChange={(e) => set('name', e.target.value)} placeholder="Main rotation" autoFocus aria-invalid={!!errors.name} /></Field>
            <Field label="Mount" error={errors.mount} hint="Where listeners tune in"><Input mono value={f.mount} onChange={(e) => set('mount', e.target.value)} placeholder="/radio" aria-invalid={!!errors.mount} /></Field>
          </div>
          <Field label="Music directory" error={errors.music_dir} hint="Pasting a path works even where no media root covers the library yet.">
            <div className="flex gap-2">
              <Input mono value={f.music_dir} onChange={(e) => set('music_dir', e.target.value)} placeholder="/srv/music" aria-invalid={!!errors.music_dir} />
              <Button type="button" onClick={() => setBrowse(true)}><Icon name="folder" size={14} />Browse</Button>
            </div>
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Format" hint="MP3 and Opus are the formats the encoder implements">
              <Select value={f.format} onValueChange={(v) => set('format', v)} options={[{ value: 'mp3', label: 'MP3' }, { value: 'opus', label: 'Opus' }]} />
            </Field>
            <Field label="Bitrate (kbps)" error={errors.bitrate}><Input mono type="number" inputMode="numeric" min={8} max={512} value={f.bitrate} onChange={(e) => set('bitrate', e.target.value)} aria-invalid={!!errors.bitrate} /></Field>
          </div>

          <div className="space-y-3 rounded-md border border-line p-3">
            <Row label="Loop the playlist" help="Start over when the last track ends"><Switch label="Loop the playlist" checked={f.loop} onCheckedChange={(v) => set('loop', v)} /></Row>
            <Row label="Send track titles" help="Publish the current song as stream metadata"><Switch label="Send track titles" checked={f.inject_metadata} onCheckedChange={(v) => set('inject_metadata', v)} /></Row>
            <Row label="Listed publicly" help="Off: still streamable by URL, but not listed"><Switch label="Listed publicly" checked={f.visible} onCheckedChange={(v) => set('visible', v)} /></Row>
          </div>

          <div className="space-y-3 rounded-md border border-line p-3">
            <Row label="MPD control port" help="Lets clients such as ncmpcpp drive this playlist. Needs a free TCP port."><Switch label="MPD control port" checked={f.mpd_enabled} onCheckedChange={(v) => set('mpd_enabled', v)} /></Row>
            {f.mpd_enabled && (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="Port" error={errors.mpd_port}><Input mono inputMode="numeric" value={f.mpd_port} onChange={(e) => set('mpd_port', e.target.value)} placeholder="6600" aria-invalid={!!errors.mpd_port} /></Field>
                <Field label="Password" hint={instance?.mpd_enabled ? 'Leave blank to keep the current one' : 'Optional'}><Input type="password" autoComplete="new-password" value={f.mpd_password} onChange={(e) => set('mpd_password', e.target.value)} /></Field>
              </div>
            )}
          </div>

          <div className="space-y-3 rounded-md border border-line p-3">
            <Field label="Song command" hint="Prints the path of the next song. Overrides the playlist; falls back to it on failure.">
              <Input mono value={f.song_command} onChange={(e) => set('song_command', e.target.value)} placeholder="node /home/radio/getSong.js" />
            </Field>
            {f.song_command.trim() && <Field label="Song command timeout (s)" error={errors.song_command_timeout}><Input mono type="number" className="w-28" min={1} max={30} value={f.song_command_timeout} onChange={(e) => set('song_command_timeout', e.target.value)} /></Field>}
            <Field label="On-play command" hint="Runs when a track starts. Environment: TINYICE_ARTIST, TINYICE_TITLE, TINYICE_ALBUM, TINYICE_FILE, TINYICE_MOUNT.">
              <Input mono value={f.on_play_command} onChange={(e) => set('on_play_command', e.target.value)} placeholder="/usr/local/bin/notify.sh" />
            </Field>
            {f.on_play_command.trim() && <Field label="On-play timeout (s)" error={errors.on_play_command_timeout}><Input mono type="number" className="w-28" min={1} max={60} value={f.on_play_command_timeout} onChange={(e) => set('on_play_command_timeout', e.target.value)} /></Field>}
            <p className="text-xs text-faint">Both commands run through a shell as the server user.</p>
          </div>
          <button type="submit" className="hidden" />
        </form>
      </Dialog>
      <DirectoryBrowser open={browse} onOpenChange={setBrowse} initialPath={f.music_dir} onPick={(p) => set('music_dir', p)} />
    </>
  )
}
