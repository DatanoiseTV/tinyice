import { useEffect, useState } from 'react'
import { Command } from 'cmdk'
import * as RDialog from '@radix-ui/react-dialog'
import { useLocation } from 'wouter'
import { Moon, Radio, Search, Sun } from 'lucide-react'
import { ALL_NAV } from './nav'
import { currentTheme, setTheme } from '@/lib/theme'
import { page } from '@/lib/api'
import { Kbd } from '@/ui/display'

const item = 'flex h-9 cursor-default items-center gap-2.5 rounded-md px-2.5 text-[13px] text-muted data-[selected=true]:bg-hover data-[selected=true]:text-ink'

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const [, go] = useLocation()
  const [q, setQ] = useState('')
  useEffect(() => { if (!open) setQ('') }, [open])
  const run = (fn: () => void) => () => { onOpenChange(false); fn() }
  const mounts = page().mounts ?? []

  return (
    <RDialog.Root open={open} onOpenChange={onOpenChange}>
      <RDialog.Portal>
        <RDialog.Overlay className="fixed inset-0 z-50 bg-black/60 animate-[overlay-in_.12s_ease-out]" />
        <RDialog.Content aria-label="Command palette" className="fixed left-1/2 top-[16vh] z-50 w-[calc(100%-2rem)] max-w-xl -translate-x-1/2 overflow-hidden rounded-lg border border-line-strong bg-surface animate-[pop-in_.12s_ease-out]">
          <RDialog.Title className="sr-only">Command palette</RDialog.Title>
          <Command label="Command palette" loop>
            <div className="flex items-center gap-2.5 border-b border-line px-4">
              <Search className="size-4 text-faint" />
              <Command.Input value={q} onValueChange={setQ} placeholder="Jump to a page, a mount, or run a command" className="h-12 flex-1 bg-transparent text-[13px] text-ink outline-none placeholder:text-faint" />
              <Kbd>esc</Kbd>
            </div>
            <Command.List className="max-h-80 overflow-y-auto p-2">
              <Command.Empty className="px-3 py-8 text-center text-xs text-muted">No results</Command.Empty>
              <Command.Group heading="Pages" className="[&_[cmdk-group-heading]]:px-2.5 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:text-faint">
                {ALL_NAV.map((n) => (
                  <Command.Item key={n.id} value={`${n.label} ${n.hint}`} onSelect={run(() => go(n.href))} className={item}>
                    <n.icon className="size-4 text-faint" strokeWidth={1.75} /><span className="text-ink">{n.label}</span><span className="ml-auto truncate text-xs text-faint">{n.hint}</span>
                  </Command.Item>
                ))}
              </Command.Group>
              {mounts.length > 0 && (
                <Command.Group heading="Open listener page" className="[&_[cmdk-group-heading]]:px-2.5 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:text-faint">
                  {mounts.map((m) => (
                    <Command.Item key={m} value={`mount ${m}`} onSelect={run(() => window.open(`/player${m}`, '_blank'))} className={item}>
                      <Radio className="size-4 text-faint" strokeWidth={1.75} /><span className="font-mono text-xs text-ink">{m}</span>
                    </Command.Item>
                  ))}
                </Command.Group>
              )}
              <Command.Group heading="Appearance" className="[&_[cmdk-group-heading]]:px-2.5 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:text-faint">
                <Command.Item value="toggle theme dark light" onSelect={run(() => setTheme(currentTheme() === 'dark' ? 'light' : 'dark'))} className={item}>
                  {currentTheme() === 'dark' ? <Sun className="size-4 text-faint" /> : <Moon className="size-4 text-faint" />}<span className="text-ink">Toggle theme</span>
                </Command.Item>
              </Command.Group>
            </Command.List>
          </Command>
        </RDialog.Content>
      </RDialog.Portal>
    </RDialog.Root>
  )
}
