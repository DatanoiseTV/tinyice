import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useLive } from '@/lib/live'
import { Knob, Legend, Panel } from '@/ui/console'
import { Switch } from '@/ui/controls'
import { Kbd } from '@/ui/display'
import { useAction } from '@/ui/kit'
import { dj, qk, type DjInstance } from '../autodj/api'
import { useTransport, useVolume, type Deck } from '../autodj/deckState'
import { NowPlaying, Progress, StateBadge, TransportRow } from '../autodj/DeckParts'

/** The big deck of the Studio: timecode, transport, gain, metadata switch, listener count and a monitor of the mount. */
export function StudioDeck({ inst, deck, send, isBusy, canEdit, className }: {
  inst: DjInstance; deck: Deck; send: ReturnType<typeof useTransport>['send']; isBusy: (k: string) => boolean; canEdit: boolean; className?: string
}) {
  const qc = useQueryClient()
  const { streams } = useLive()
  const { run } = useAction()
  const vol = useVolume(inst.mount)
  const listeners = streams.find((s) => s.mount === inst.mount)?.listeners
  const [monitorError, setMonitorError] = useState('')
  const setMeta = (on: boolean) => run('meta', async () => { await dj.metadata(inst.mount, on); qc.invalidateQueries({ queryKey: qk.instances }) })

  return (
    <Panel legend="Deck" title={inst.mount} lamp={deck.state === 'playing' ? 'live' : deck.state === 'paused' ? 'warn' : 'off'} className={className} actions={<StateBadge state={deck.state} />}>
      <div className="space-y-5">
        <NowPlaying deck={deck} big />
        <Progress deck={deck} big />
        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
          <TransportRow deck={deck} send={send} isBusy={isBusy} size="lg" />
          <Knob label="Gain" value={vol.percent / 100} onChange={(v) => vol.set(v * 100)} defaultValue={1} display={`${vol.percent}%`} size={56} disabled={!canEdit} />
        </div>
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2 border-t border-line pt-4">
          <div className="flex items-baseline gap-2"><Legend>Listeners</Legend><span className="num font-mono text-base text-ink">{listeners ?? '-'}</span></div>
          <div className="flex items-center gap-2"><Switch label="Send track titles" checked={inst.inject_metadata} onCheckedChange={setMeta} disabled={!canEdit} /><Legend>Send titles</Legend></div>
          <div className="flex items-center gap-3 text-xs text-faint ml-auto hidden 2xl:flex"><span className="flex items-center gap-1"><Kbd>Space</Kbd>play</span><span className="flex items-center gap-1"><Kbd>N</Kbd>next</span><span className="flex items-center gap-1"><Kbd>P</Kbd>prev</span></div>
        </div>
      </div>

      {/* Local playback of the mount so the operator hears what listeners hear. preload="none" keeps it from
          opening a listener connection until play is pressed. It is only live while the AutoDJ is playing. */}
      <div className="mt-5 border-t border-line pt-4">
        <Legend className="mb-2 block">Monitor</Legend>
        <audio key={inst.mount} src={inst.mount} controls preload="none" className="h-9 w-full" onError={() => setMonitorError(`Could not play ${inst.mount}. The mount is only live while the AutoDJ is playing.`)} onPlay={() => setMonitorError('')} />
        {monitorError && <p role="alert" className="mt-2 text-xs text-danger">{monitorError}</p>}
        <p className="mt-2 text-xs text-faint">Gain is the volume the AutoDJ encodes at, so it changes what listeners hear. The server does not report it back; the knob shows the last value set from this browser.</p>
      </div>
    </Panel>
  )
}
