import { useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useAction } from '@/ui/kit'
import { toast } from '@/lib/toast'
import { dj, qk } from './api'

/**
 * Add files or folders to the playlist. The server only appends, so a drop at a
 * position appends first and then pulls the new tail up to the gap, one move per
 * track from the front (each move lands left of the ones already placed).
 */
export function useAddPaths(mount: string | undefined) {
  const qc = useQueryClient()
  const { run, isBusy } = useAction()
  const add = useCallback(async (paths: string[], at: number | null = null) => {
    if (!mount || !paths.length) return
    const read = () => qc.fetchQuery({ queryKey: qk.playlist(mount), queryFn: () => dj.playlist(mount), staleTime: 0 })
    await run('add', async () => {
      const prev = (await read()).length
      await dj.add(mount, paths)
      const added = (await read()).length - prev
      if (at !== null && at < prev && added > 0) for (let k = 0; k < added; k++) await dj.move(mount, prev + k, at + k)
      qc.invalidateQueries({ queryKey: qk.playlist(mount) })
      qc.invalidateQueries({ queryKey: qk.instances })
      if (added > 0) toast.success(`Added ${added} track${added === 1 ? '' : 's'}`)
      else toast.info('Nothing new to add')
    })
  }, [mount, qc, run])
  return { add, adding: isBusy('add') }
}
