import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, page } from '@/lib/api'
import { useLive } from '@/lib/live'
import { toast } from '@/lib/toast'
import { Badge, PageHeader } from '@/ui/display'
import { Button, Switch } from '@/ui/controls'
import { Knob, Legend, Panel } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { useConfirm } from '@/ui/kit'
import type { AdminData } from '@/types'
import { broadcaster, GAIN_MAX_DB, GAIN_MIN_DB } from './golive/engine'
import { DestinationPanel, GoLiveButton, mountError, Preflight, type MountInfo } from './golive/destination'
import { LinkPanel, OnAirBar } from './golive/link'
import { Meters } from './golive/meters'
import { SourcePanel, toCaptureOpts } from './golive/source'
import { loadPrefs, savePrefs, useBroadcast, type Prefs } from './golive/store'

const fmtDb = (db: number) => `${db >= 0 ? '+' : ''}${db.toFixed(1)} dB`
const gainToKnob = (db: number) => (db - GAIN_MIN_DB) / (GAIN_MAX_DB - GAIN_MIN_DB)
const knobToGain = (v: number) => Math.round((GAIN_MIN_DB + v * (GAIN_MAX_DB - GAIN_MIN_DB)) * 2) / 2

function Banner({ tone, children, onDismiss }: { tone: 'danger' | 'warn'; children: React.ReactNode; onDismiss?: () => void }) {
  const c = tone === 'danger' ? 'border-danger/30 bg-danger/10 text-danger' : 'border-warn/30 bg-warn/10 text-warn'
  return (
    <div role={tone === 'danger' ? 'alert' : 'status'} className={`mb-3 flex items-start gap-3 rounded-lg border px-4 py-3 text-[13px] ${c}`}>
      <Icon name="alert" size={16} className="mt-0.5 shrink-0" />
      <span className="min-w-0 flex-1 break-words">{children}</span>
      {onDismiss && <Button size="sm" variant="ghost" onClick={onDismiss}>Dismiss</Button>}
    </div>
  )
}

export default function GoLive() {
  const snap = useBroadcast()
  const { streams } = useLive()
  const { ask, dialog } = useConfirm()
  const data = page<AdminData>()
  const csrf = data?.csrfToken ?? ''

  const [prefs, setPrefs] = useState<Prefs>(() => {
    const p = loadPrefs()
    // Returning to the page while on air: show what is actually being sent.
    const s = broadcaster.getSnapshot()
    return { ...p, mount: s.mount ?? p.mount, gainDb: s.gainDb, limiter: s.limiter }
  })
  const [password, setPassword] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [known, setKnown] = useState<MountInfo[]>([])
  const [showErrors, setShowErrors] = useState(false)

  const set = useCallback((p: Partial<Prefs>) => setPrefs((cur) => ({ ...cur, ...p })), [])
  useEffect(() => savePrefs(prefs), [prefs])

  // Mount list: the page data when the server provides it, plus /api/streams (also carries the enabled flag).
  useEffect(() => {
    let dead = false
    const fromPage = (data?.mounts ?? []).map((m) => ({ mount: m, enabled: true }))
    api.get<{ mount: string; enabled?: boolean }[]>('/api/streams')
      .then((rows) => {
        if (dead) return
        const map = new Map<string, MountInfo>(fromPage.map((m) => [m.mount, m]))
        for (const r of rows ?? []) map.set(r.mount, { mount: r.mount, enabled: r.enabled !== false })
        setKnown([...map.values()])
      })
      .catch(() => { if (!dead) setKnown(fromPage) })
    return () => { dead = true }
  }, [data])
  useEffect(() => {
    if (!prefs.mount && known.length) set({ mount: known[0].mount })
  }, [known, prefs.mount, set])

  // Engine state is the source of truth for gain and limiter; seed it from saved prefs while nothing is open.
  useEffect(() => {
    if (broadcaster.getSnapshot().phase === 'idle') { broadcaster.setGainDb(prefs.gainDb); broadcaster.setLimiter(prefs.limiter) }
    return () => {
      // Leaving the page keeps a broadcast running but never leaves an idle open microphone behind.
      if (broadcaster.getSnapshot().phase === 'armed') void broadcaster.release()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useEffect(() => { set({ gainDb: snap.gainDb, limiter: snap.limiter }) }, [snap.gainDb, snap.limiter, set])

  const onAir = snap.phase === 'connecting' || snap.phase === 'live' || snap.phase === 'reconnecting'
  const open = snap.phase !== 'idle' && snap.phase !== 'arming'
  const simulated = snap.simulated || new URLSearchParams(window.location.search).has('fake')
  const server = streams.find((s) => s.mount === snap.mount)
  const liveElsewhere = !onAir && streams.some((s) => s.mount === prefs.mount)
  const monitorBlocked = simulated || prefs.source === 'system' || (prefs.source === 'screen' && prefs.screenSystemAudio)
  const mErr = mountError(prefs.mount)
  const aErr = prefs.authMode === 'password' ? !password : !csrf

  const requestStop = () => {
    if (snap.phase === 'connecting') { broadcaster.stop(); return }
    ask({
      title: 'Stop broadcasting?',
      body: `Listeners on ${snap.mount} lose the stream immediately. The input stays open so you can go live again.`,
      confirm: 'Stop broadcast',
      run: () => { broadcaster.stop(); toast.info('Broadcast stopped') },
    })
  }

  const go = async () => {
    setShowErrors(true)
    if (mErr || aErr) return
    const auth = prefs.authMode === 'password' ? { kind: 'password' as const, password } : { kind: 'session' as const, csrf }
    if (snap.phase === 'idle') {
      if (!(await broadcaster.arm(toCaptureOpts(prefs, file)))) return
    } else if (Date.now() - broadcaster.lastSignal > 3000) {
      ask({
        title: 'No input signal',
        body: 'Nothing above -50 dBFS arrived in the last 3 seconds, so listeners would hear silence. Check the input, or go live anyway.',
        confirm: 'Go live anyway',
        run: () => { void broadcaster.goLive(prefs.mount, auth) },
      })
      return
    }
    await broadcaster.goLive(prefs.mount, auth)
  }

  const toggleMonitor = (on: boolean) => {
    if (!on) { broadcaster.setMonitor(false); return }
    ask({
      title: 'Monitor the input?',
      body: 'This plays your input through this computer\'s speakers or headphones. With speakers the microphone picks it up again and howls. Put headphones on first.',
      confirm: 'Enable monitor',
      run: () => broadcaster.setMonitor(true),
    })
  }

  const badge = useMemo(() => {
    switch (snap.phase) {
      case 'live': return <Badge tone="live" dot>On air</Badge>
      case 'reconnecting': return <Badge tone="warn" dot>Reconnecting</Badge>
      case 'connecting': return <Badge tone="info" dot>Connecting</Badge>
      case 'armed': return <Badge tone="warn">Input open</Badge>
      case 'arming': return <Badge tone="info">Opening input</Badge>
      default: return <Badge>Input closed</Badge>
    }
  }, [snap.phase])

  return (
    <>
      <PageHeader title="Go live" sub="Broadcast a microphone, system audio, camera or screen from this browser to a mount." actions={badge} />

      {(snap.phase === 'live' || snap.phase === 'reconnecting') && <OnAirBar snap={snap} onStop={requestStop} />}
      {snap.error && <Banner tone="danger" onDismiss={() => broadcaster.clearError()}>{snap.error}</Banner>}
      {snap.notice && snap.phase !== 'reconnecting' && <Banner tone="warn" onDismiss={() => broadcaster.clearError()}>{snap.notice}</Banner>}

      <div className="grid gap-3 xl:grid-cols-3 [&>*]:min-w-0">
        <div className="space-y-3 xl:col-span-2">
          <SourcePanel prefs={prefs} set={set} file={file} setFile={setFile} snap={snap} />

          <Panel legend="Levels" title={open ? 'Input' : 'Open the input to meter it'} lamp={open ? 'warn' : 'off'}>
            <Meters active={open} />
            <div className="mt-5 flex flex-wrap items-start gap-x-8 gap-y-4 border-t border-line pt-4">
              <Knob
                label="Input gain" size={64} value={gainToKnob(snap.gainDb)} defaultValue={gainToKnob(0)} step={1 / (GAIN_MAX_DB - GAIN_MIN_DB)}
                display={fmtDb(snap.gainDb)} onChange={(v) => { broadcaster.setGainDb(knobToGain(v)) }}
              />
              <div className="min-w-[200px] flex-1 space-y-3">
                <label className="flex items-center justify-between gap-3">
                  <span><span className="block text-[13px] text-ink">Limiter</span><span className="block text-xs text-faint">Soft ceiling at -2 dBFS before encoding. Not a brickwall: keep the CLIP lamp dark.</span></span>
                  <Switch label="Limiter" checked={snap.limiter} onCheckedChange={(v) => broadcaster.setLimiter(v)} />
                </label>
                <div className="flex items-center justify-between gap-3">
                  <span><span className="block text-[13px] text-ink">Monitor</span><span className="block text-xs text-faint">{monitorBlocked ? (simulated ? 'Disabled for the simulated source.' : 'Unavailable for system audio: it would loop back into itself.') : 'Off by default. Plays the input here; use headphones.'}</span></span>
                  <Switch label="Monitor input through this device's output" checked={snap.monitor} disabled={!open || monitorBlocked} onCheckedChange={toggleMonitor} />
                </div>
              </div>
              {snap.monitor && (
                <Knob label="Monitor level" size={48} value={snap.monitorLevel} defaultValue={0.4} display={`${Math.round(snap.monitorLevel * 100)}%`} onChange={(v) => broadcaster.setMonitorLevel(v)} />
              )}
            </div>
            {snap.monitor && <p role="alert" className="mt-3 flex items-center gap-2 text-xs text-warn"><Icon name="alert" size={14} />Monitor is on. Use headphones; speakers will feed back into the microphone.</p>}
          </Panel>

          {(onAir || snap.stats.bytesSent > 0) && <LinkPanel snap={snap} server={server} />}
        </div>

        <div className="space-y-3">
          <DestinationPanel prefs={prefs} set={set} password={password} setPassword={setPassword} mounts={known} liveElsewhere={liveElsewhere} csrf={csrf} showErrors={showErrors} locked={onAir} />

          <Panel legend="Pre-flight" title={onAir ? 'On air' : 'Before you go live'}>
            <Preflight snap={snap} mountOk={!mErr} authOk={!aErr} mountFree={!liveElsewhere} simulated={simulated} />
            <div className="mt-4 space-y-2">
              <GoLiveButton snap={snap} onGo={() => void go()} onStop={requestStop} />
              <Legend className="block text-center">{snap.phase === 'connecting' ? 'Negotiating with the server' : onAir ? 'Stop is confirmed before it takes effect' : 'Opens the input first if it is closed'}</Legend>
            </div>
          </Panel>
        </div>
      </div>
      {dialog}
    </>
  )
}

