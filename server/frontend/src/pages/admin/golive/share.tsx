import { Icon } from '@/ui/icons'
import { Legend, Panel } from '@/ui/console'
import { CodeBlock, CopyButton } from '@/ui/kit'

function UrlRow({ label, url, open }: { label: string; url: string; open?: boolean }) {
  return (
    <div>
      <Legend>{label}</Legend>
      <div className="mt-1 flex items-center gap-1 rounded-md border border-line bg-bg pl-3">
        <span className="min-w-0 flex-1 truncate font-mono text-[13px] text-ink" title={url}>{url}</span>
        <CopyButton text={url} label={`Copy ${label.toLowerCase()}`} />
        {open && (
          <a href={url} target="_blank" rel="noreferrer" aria-label={`Open ${label.toLowerCase()}`} title="Open in a new tab"
            className="flex size-7 items-center justify-center rounded-md text-faint transition-colors hover:bg-hover hover:text-ink">
            <Icon name="external" size={14} />
          </a>
        )}
      </div>
    </div>
  )
}

/** Where listeners find the mount. Shown before going live too, so the link can be shared in advance. */
export function ListenPanel({ mount, live }: { mount: string; live: boolean }) {
  const origin = window.location.origin
  return (
    <Panel legend="Listen" title={live ? 'Listeners hear you at' : 'Listeners will hear you at'} lamp={live ? 'live' : 'off'}>
      <div className="space-y-3">
        <UrlRow label="Player page" url={`${origin}/player${mount}`} open />
        <UrlRow label="Stream URL" url={`${origin}${mount}`} />
        <p className="text-xs text-faint">
          {live ? 'Share the player page. The stream URL is for media players and apps.' : 'The links work once you are on air.'}
        </p>
      </div>
    </Panel>
  )
}

/** Settings for a desktop encoder (BUTT, Mixxx, OBS, ffmpeg) that sends to the same mount over the Icecast source protocol. */
export function ExternalAppPanel({ mount }: { mount: string }) {
  const loc = window.location
  const tls = loc.protocol === 'https:'
  const port = loc.port || (tls ? '443' : '80')
  const rows: [string, string][] = [
    ['Server type', 'Icecast 2'],
    ['Address', loc.hostname],
    ['Port', port],
    ['TLS / SSL', tls ? 'On' : 'Off'],
    ['Mount', mount],
    ['User', 'source'],
    ['Password', "The mount's source password"],
  ]
  const ffmpeg = `ffmpeg -re -i input.mp3 -c:a libmp3lame -b:a 192k \\\n  -content_type audio/mpeg -f mp3 ${tls ? '-tls 1 ' : ''}\\\n  icecast://source:PASSWORD@${loc.hostname}:${port}${mount}`
  return (
    <Panel legend="Other apps" title="Broadcast from an encoder">
      <details>
        <summary className="cursor-pointer text-[13px] text-ink marker:text-faint">
          BUTT, Mixxx, OBS, ffmpeg and other Icecast clients
        </summary>
        <div className="mt-3 space-y-3">
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[13px]">
            {rows.flatMap(([k, v]) => [
              <dt key={`${k}-k`} className="text-xs text-faint">{k}</dt>,
              <dd key={`${k}-v`} className="min-w-0 truncate font-mono text-ink">{v}</dd>,
            ])}
          </dl>
          <CodeBlock label="ffmpeg">{ffmpeg}</CodeBlock>
          <p className="text-xs text-faint">Use one source at a time per mount: an encoder and this page cannot both send to the same mount.</p>
        </div>
      </details>
    </Panel>
  )
}
