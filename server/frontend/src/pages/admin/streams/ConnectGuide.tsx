import { useState } from 'react'
import { Segmented } from '@/ui/controls'
import { CodeBlock } from '@/ui/kit'
import { Legend } from '@/ui/console'
import { CopyButton } from '@/ui/kit'

type Tool = 'ffmpeg' | 'butt' | 'liquidsoap'

export function serverEndpoint() {
  const { hostname, port, protocol } = window.location
  const secure = protocol === 'https:'
  return { host: hostname, port: port || (secure ? '443' : '80'), secure }
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-line px-3 py-2 last:border-b-0">
      <span className="text-xs text-muted">{k}</span>
      <span className="flex items-center gap-1"><span className="num font-mono text-xs text-ink">{v}</span><CopyButton text={v} label={`Copy ${k}`} /></span>
    </div>
  )
}

/**
 * Encoder connection details for one mount. The source username is ignored by
 * the server (only the password is checked), so "source" is the Icecast convention.
 */
export function ConnectGuide({ mount, password }: { mount: string; password?: string }) {
  const [tool, setTool] = useState<Tool>('ffmpeg')
  const { host, port, secure } = serverEndpoint()
  const pw = password || 'SOURCE_PASSWORD'
  const proto = secure ? 'icecast+tls' : 'icecast'
  const snippets: Record<Tool, string> = {
    ffmpeg: `ffmpeg -re -i input.mp3 \\\n  -c:a libmp3lame -b:a 128k -content_type audio/mpeg -f mp3 \\\n  "${proto}://source:${pw}@${host}:${port}${mount}"`,
    butt: '',
    liquidsoap: `output.icecast(\n  %mp3(bitrate=128),\n  host="${host}", port=${port},\n  password="${pw}", mount="${mount}",\n  ${secure ? 'transport=http.transport.ssl(),\n  ' : ''}mksafe(input.http("http://example.org/in.mp3"))\n)`,
  }
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Legend>Encoder settings</Legend>
        <Segmented<Tool> label="Encoder" value={tool} onChange={setTool} options={[{ value: 'ffmpeg', label: 'ffmpeg' }, { value: 'butt', label: 'BUTT / generic' }, { value: 'liquidsoap', label: 'Liquidsoap' }]} />
      </div>
      {tool === 'butt' ? (
        <div className="overflow-hidden rounded-md border border-line bg-bg">
          <Row k="Server type" v="Icecast" />
          <Row k="Address" v={host} />
          <Row k="Port" v={port} />
          <Row k="Mount" v={mount} />
          <Row k="User" v="source" />
          <Row k="Password" v={pw} />
        </div>
      ) : <CodeBlock label={tool === 'ffmpeg' ? 'Shell' : 'Liquidsoap'}>{snippets[tool]}</CodeBlock>}
      <p className="text-xs text-faint">The username is not checked; the mount's source password is.{password ? '' : ' Replace SOURCE_PASSWORD with it.'}</p>
    </div>
  )
}
