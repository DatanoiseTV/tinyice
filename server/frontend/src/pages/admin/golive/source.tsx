import { useEffect, useRef, useState } from 'react'
import { Badge, EmptyState } from '@/ui/display'
import { Button, Select, Switch } from '@/ui/controls'
import { Checkbox } from '@/ui/kit'
import { Icon, type IconName } from '@/ui/icons'
import { Legend, Panel } from '@/ui/console'
import { cn } from '@/lib/cn'
import { broadcaster, capabilities, listDevices, queryPermission, type CaptureOpts, type PermState, type SourceKind, type Snapshot } from './engine'
import type { Prefs } from './store'

const SOURCES: { value: SourceKind; label: string; icon: IconName; video: boolean }[] = [
  { value: 'mic', label: 'Microphone', icon: 'golive', video: false },
  { value: 'system', label: 'System audio', icon: 'volume', video: false },
  { value: 'camera', label: 'Camera', icon: 'video', video: true },
  { value: 'screen', label: 'Screen', icon: 'gl-screen', video: true },
  { value: 'file', label: 'Video file', icon: 'upload', video: true },
]

const PERM_TEXT: Record<PermState, { tone: 'live' | 'warn' | 'danger' | 'neutral'; text: string }> = {
  granted: { tone: 'neutral', text: 'Allowed' },
  prompt: { tone: 'warn', text: 'Not asked yet' },
  denied: { tone: 'danger', text: 'Blocked' },
  unknown: { tone: 'neutral', text: 'Unknown' },
}

export function toCaptureOpts(p: Prefs, file: File | null): CaptureOpts {
  return { source: p.source, audioDeviceId: p.audioDeviceId, videoDeviceId: p.videoDeviceId, videoHeight: p.videoHeight, voiceProcessing: p.voiceProcessing, screenMic: p.screenMic, screenSystemAudio: p.screenSystemAudio, file }
}

function Row({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="grid items-center gap-x-4 gap-y-1.5 sm:grid-cols-[148px_minmax(0,1fr)]">
      <div><div className="text-[13px] text-muted">{label}</div>{hint && <div className="text-xs text-faint">{hint}</div>}</div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

function Perm({ state, what }: { state: PermState; what: string }) {
  const p = PERM_TEXT[state]
  return <Badge tone={p.tone}>{what}: {p.text}</Badge>
}

export function SourcePanel({ prefs, set, file, setFile, snap }: {
  prefs: Prefs; set: (p: Partial<Prefs>) => void; file: File | null; setFile: (f: File | null) => void; snap: Snapshot
}) {
  const caps = capabilities()
  const simulated = snap.simulated || new URLSearchParams(window.location.search).has('fake')
  const [audio, setAudio] = useState<MediaDeviceInfo[]>([])
  const [video, setVideo] = useState<MediaDeviceInfo[]>([])
  const [mic, setMic] = useState<PermState>('unknown')
  const [cam, setCam] = useState<PermState>('unknown')
  const [loaded, setLoaded] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const videoEl = useRef<HTMLVideoElement>(null)

  const refresh = () => listDevices().then((d) => { setAudio(d.audio); setVideo(d.video); setLoaded(true) }).catch(() => setLoaded(true))
  useEffect(() => {
    void refresh()
    const md = navigator.mediaDevices
    md?.addEventListener?.('devicechange', refresh)
    let offs: (() => void)[] = []
    let dead = false
    void queryPermission('microphone', setMic).then((o) => (dead ? o() : offs.push(o)))
    void queryPermission('camera', setCam).then((o) => (dead ? o() : offs.push(o)))
    return () => { dead = true; md?.removeEventListener?.('devicechange', refresh); offs.forEach((o) => o()); offs = [] }
  }, [])
  // Device labels only appear once permission exists: re-read the list after an input opens.
  useEffect(() => { if (snap.phase === 'armed') void refresh() }, [snap.phase])
  useEffect(() => {
    const el = videoEl.current
    if (el) el.srcObject = broadcaster.preview
  }, [snap.previewId])

  const src = SOURCES.find((s) => s.value === prefs.source)!
  const locked = snap.phase !== 'idle' && snap.phase !== 'armed'
  const open = snap.phase === 'armed' || locked
  const needsMic = prefs.source === 'mic' || prefs.source === 'camera' || (prefs.source === 'screen' && prefs.screenMic)
  const needsCam = prefs.source === 'camera'
  const supported = simulated || (caps.secure && caps.capture)

  const deviceOptions = (list: MediaDeviceInfo[], noun: string) => [
    { value: 'default', label: 'System default' },
    ...list.map((d, i) => ({ value: d.deviceId, label: d.label || `${noun} ${i + 1}` })),
  ]
  const noLabels = audio.length > 0 && audio.every((d) => !d.label)
  const chosenAudio = audio.some((d) => d.deviceId === prefs.audioDeviceId) ? prefs.audioDeviceId : 'default'
  const chosenVideo = video.some((d) => d.deviceId === prefs.videoDeviceId) ? prefs.videoDeviceId : 'default'

  // Changing a device while the input is open (and not on air) reopens it on the new device.
  const reopen = (patch: Partial<Prefs>) => {
    set(patch)
    if (snap.phase === 'armed' && (prefs.source === 'mic' || prefs.source === 'camera')) {
      void broadcaster.arm(toCaptureOpts({ ...prefs, ...patch }, file))
    }
  }

  const pickSource = (v: SourceKind) => {
    if (locked || v === prefs.source) return
    if (snap.phase === 'armed') void broadcaster.release()
    set({ source: v })
  }

  return (
    <Panel
      legend="Input" title={snap.phase === 'idle' ? 'Closed' : snap.phase === 'arming' ? 'Opening' : 'Open'} lamp={open ? 'warn' : 'off'}
      actions={snap.phase === 'armed'
        ? <Button size="sm" variant="ghost" onClick={() => void broadcaster.release()}>Close input</Button>
        : snap.phase === 'idle' || snap.phase === 'arming'
          ? <Button size="sm" loading={snap.phase === 'arming'} disabled={!supported || (prefs.source === 'file' && !file && !simulated)} onClick={() => void broadcaster.arm(toCaptureOpts(prefs, file))}>Open input</Button>
          : null}
    >
      <div className="space-y-4">
        {simulated && (
          <div role="status" className="flex items-start gap-2 rounded-md border border-warn/30 bg-warn/10 px-3 py-2 text-xs text-warn">
            <Icon name="info" size={14} className="mt-0.5 shrink-0" />
            <span>Simulated source (development only). A generated signal replaces the microphone, nothing is audible and no media leaves this page.</span>
          </div>
        )}
        {!supported && (
          <div role="alert" className="flex items-start gap-2 rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-xs text-danger">
            <Icon name="alert" size={14} className="mt-0.5 shrink-0" />
            <span>{!caps.secure
              ? 'Browsers only allow capture on HTTPS or localhost. Open this admin over HTTPS to broadcast from the browser.'
              : 'This browser does not expose media capture. Use a current Chrome, Edge, Firefox or Safari.'}</span>
          </div>
        )}

        <div role="radiogroup" aria-label="Source" className="grid grid-cols-2 gap-2 sm:grid-cols-5">
          {SOURCES.map((s) => {
            const on = prefs.source === s.value
            const unavailable = s.value === 'system' || s.value === 'screen' ? !simulated && !caps.display : false
            return (
              <button
                key={s.value} type="button" role="radio" aria-checked={on} disabled={locked || unavailable}
                title={unavailable ? 'Not supported by this browser' : undefined}
                onClick={() => pickSource(s.value)}
                className={cn(
                  'flex h-[60px] flex-col items-center justify-center gap-1.5 rounded-md border text-xs font-medium transition-colors disabled:pointer-events-none disabled:opacity-45',
                  on ? 'border-accent/60 bg-accent/10 text-accent' : 'border-line-strong bg-raised text-muted hover:bg-hover hover:text-ink',
                )}
              >
                <Icon name={s.icon} size={18} />{s.label}
              </button>
            )
          })}
        </div>

        <div className="space-y-3.5">
          {needsMic && (
            <Row label="Microphone">
              {mic === 'denied' && !simulated ? (
                <div role="alert" className="rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-xs text-danger">
                  Microphone access is blocked for this site. Click the lock icon in the address bar, set Microphone to Allow, then open the input again.
                </div>
              ) : (
                <div className="flex min-w-0 items-center gap-2">
                  <Select
                    className="min-w-0 flex-1" value={chosenAudio} placeholder="No device"
                    onValueChange={(v) => reopen({ audioDeviceId: v === 'default' ? '' : v })}
                    options={deviceOptions(audio, 'Microphone')}
                  />
                  <Button size="icon" variant="ghost" aria-label="Refresh device list" onClick={() => void refresh()}><Icon name="refresh" size={14} /></Button>
                </div>
              )}
              <div className="mt-1.5 flex flex-wrap items-center gap-2">
                <Perm state={mic} what="Permission" />
                {loaded && audio.length === 0 && mic !== 'denied' && <span className="text-xs text-warn">No input device found. Connect a microphone or interface.</span>}
                {noLabels && <span className="text-xs text-faint">Device names appear after the first time access is granted.</span>}
              </div>
            </Row>
          )}

          {needsCam && (
            <>
              <Row label="Camera">
                {cam === 'denied' && !simulated ? (
                  <div role="alert" className="rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-xs text-danger">
                    Camera access is blocked for this site. Allow it from the lock icon in the address bar.
                  </div>
                ) : (
                  <Select className="w-full" value={chosenVideo} placeholder="No camera" onValueChange={(v) => reopen({ videoDeviceId: v === 'default' ? '' : v })} options={deviceOptions(video, 'Camera')} />
                )}
                <div className="mt-1.5 flex flex-wrap items-center gap-2">
                  <Perm state={cam} what="Permission" />
                  {loaded && video.length === 0 && cam !== 'denied' && <span className="text-xs text-warn">No camera found.</span>}
                </div>
              </Row>
              <Row label="Resolution" hint="Requested; the browser may pick the nearest mode">
                <Select
                  className="w-full sm:w-40" value={String(prefs.videoHeight)} onValueChange={(v) => reopen({ videoHeight: Number(v) as 480 | 720 | 1080 })}
                  options={[{ value: '480', label: '480p' }, { value: '720', label: '720p' }, { value: '1080', label: '1080p' }]}
                />
              </Row>
            </>
          )}

          {prefs.source === 'screen' && (
            <Row label="Audio">
              <div className="flex flex-wrap gap-x-5 gap-y-2">
                <Checkbox label="Include microphone" checked={prefs.screenMic} onChange={(v) => set({ screenMic: v })} disabled={locked || snap.phase === 'armed'} />
                <Checkbox label="Capture system or tab audio" checked={prefs.screenSystemAudio} onChange={(v) => set({ screenSystemAudio: v })} disabled={locked || snap.phase === 'armed'} />
              </div>
              <p className="mt-1.5 text-xs text-faint">The browser asks what to share when the input opens. System audio is only offered for tabs on macOS and for the whole screen on Windows.</p>
            </Row>
          )}

          {prefs.source === 'system' && (
            <p className="text-xs text-muted">Opens the browser's share dialog and sends only the audio of the tab or screen you pick. Tick "Share audio" in that dialog; the picture is discarded.</p>
          )}

          {prefs.source === 'file' && (
            <Row label="File" hint="Loops while on air">
              <input ref={fileInput} type="file" accept="video/*" className="sr-only" tabIndex={-1} aria-label="Video file" onChange={(e) => { setFile(e.target.files?.[0] ?? null); e.target.value = '' }} />
              <div className="flex min-w-0 items-center gap-2">
                <Button size="sm" disabled={snap.phase !== 'idle'} onClick={() => fileInput.current?.click()}>Choose file</Button>
                <span className="truncate font-mono text-xs text-muted">{file?.name ?? (simulated ? 'simulated video file' : 'No file chosen')}</span>
              </div>
            </Row>
          )}

          {needsMic && (
            <Row label="Voice processing" hint="Echo cancel, noise suppression, AGC">
              <div className="flex items-center gap-3">
                <Switch label="Browser voice processing" checked={prefs.voiceProcessing} disabled={snap.phase !== 'idle'} onCheckedChange={(v) => set({ voiceProcessing: v })} />
                <span className="text-xs text-faint">{prefs.voiceProcessing ? 'On: suits a laptop microphone, forces mono, mangles music.' : 'Off: clean signal for interfaces and headsets.'}</span>
              </div>
            </Row>
          )}
        </div>

        {src.video && (
          <div>
            <div className="mb-1.5 flex items-center justify-between"><Legend>Preview</Legend>{snap.hasVideo && <span className="num font-mono text-[11px] text-muted">{snap.videoInfo}</span>}</div>
            <div className={cn('relative flex w-full items-center justify-center overflow-hidden rounded-md border border-line bg-bg', snap.hasVideo ? 'aspect-video max-h-80' : 'h-44')}>
              <video ref={videoEl} autoPlay muted playsInline className={cn('size-full object-contain', !snap.hasVideo && 'hidden')} aria-label="Video preview (muted)" />
              {!snap.hasVideo && (
                <EmptyState icon={<Icon name="video" size={20} />} title="No picture yet" body="The preview is muted. It shows the exact picture that is sent once the input is open." />
              )}
            </div>
          </div>
        )}
      </div>
    </Panel>
  )
}
