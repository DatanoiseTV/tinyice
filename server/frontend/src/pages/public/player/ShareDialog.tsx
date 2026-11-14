import { useState } from 'react'
import { Dialog, Segmented } from '@/ui/controls'
import { CodeBlock, CopyButton } from '@/ui/kit'
import { Legend } from '@/ui/console'
import { mountPath } from './source'

const SIZES = {
  '300x120': { w: '300', h: '120', label: '300 x 120' },
  '400x200': { w: '400', h: '200', label: '400 x 200' },
  '100%x80': { w: '100%', h: '80', label: 'Full width, 80' },
} as const
type SizeKey = keyof typeof SIZES

function UrlRow({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <Legend>{label}</Legend>
      <div className="mt-1.5 flex h-8 items-center gap-1 rounded-md border border-line-strong bg-bg pl-2.5 pr-0.5">
        <input readOnly value={value} aria-label={label} onFocus={(e) => e.currentTarget.select()} className="min-w-0 flex-1 bg-transparent font-mono text-xs text-ink focus:outline-none" />
        <CopyButton text={value} label={`Copy ${label.toLowerCase()}`} />
      </div>
    </div>
  )
}

export function ShareDialog({ open, onOpenChange, mount, name }: { open: boolean; onOpenChange: (o: boolean) => void; mount: string; name: string }) {
  const [size, setSize] = useState<SizeKey>('400x200')
  const path = mountPath(mount)
  const origin = window.location.origin
  const embedUrl = `${origin}/embed${path}`
  const s = SIZES[size]
  const snippet = `<iframe src="${embedUrl}" width="${s.w}" height="${s.h}" style="border:0;border-radius:8px" allow="autoplay" title="${name.replace(/"/g, '&quot;')}"></iframe>`

  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Share this stream" description="Link to this page, embed a compact player, or open the raw stream in another app." width="max-w-xl">
      <div className="space-y-4">
        <UrlRow label="Player page" value={`${origin}/player${path}`} />
        <UrlRow label="Direct stream URL" value={`${origin}${path}`} />
        <div className="space-y-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <Legend>Embed code</Legend>
            <Segmented label="Embed size" value={size} onChange={setSize} options={(Object.keys(SIZES) as SizeKey[]).map((k) => ({ value: k, label: SIZES[k].label }))} />
          </div>
          <CodeBlock label="HTML">{snippet}</CodeBlock>
          <div className="overflow-hidden rounded-md border border-line bg-bg p-3">
            <Legend>Preview</Legend>
            <iframe key={size} src={embedUrl} title="Embed preview" width={s.w === '100%' ? '100%' : s.w} height={s.h} className="mt-2 block max-w-full rounded-lg border-0" />
          </div>
        </div>
      </div>
    </Dialog>
  )
}
