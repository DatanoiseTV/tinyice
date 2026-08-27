import { useRef, useCallback, useState } from 'preact/hooks'

interface VolumeKnobProps {
  value: number // 0-100
  onChange: (value: number) => void
}

// Whole percent: finer granularity is inaudible and it keeps
// aria-valuenow (and the tooltip) readable.
const clamp = (v: number) => Math.round(Math.min(100, Math.max(0, v)))

export function VolumeKnob({ value, onChange }: VolumeKnobProps) {
  const trackRef = useRef<HTMLDivElement>(null)
  // Level to restore when unmuting. Kept in state (not a ref) so the
  // icon re-renders, and seeded with a sensible default so unmuting a
  // slider that started at 0 still produces audible sound.
  const [preMute, setPreMute] = useState(80)
  const muted = value === 0

  const valueFromClientX = useCallback((clientX: number) => {
    const track = trackRef.current
    if (!track) return null
    const rect = track.getBoundingClientRect()
    if (rect.width === 0) return null
    return clamp(((clientX - rect.left) / rect.width) * 100)
  }, [])

  // Pointer events cover mouse, touch and pen in one path — the previous
  // mouse-only handlers meant dragging did nothing on touchscreens.
  // Capturing the pointer keeps the drag alive when it leaves the track.
  const handlePointerDown = useCallback((e: PointerEvent) => {
    e.preventDefault()
    const track = trackRef.current
    if (!track) return
    const next = valueFromClientX(e.clientX)
    if (next !== null) onChange(next)
    try { track.setPointerCapture(e.pointerId) } catch { /* not supported */ }

    const move = (ev: PointerEvent) => {
      const v = valueFromClientX(ev.clientX)
      if (v !== null) onChange(v)
    }
    const up = (ev: PointerEvent) => {
      track.removeEventListener('pointermove', move)
      track.removeEventListener('pointerup', up)
      track.removeEventListener('pointercancel', up)
      try { track.releasePointerCapture(ev.pointerId) } catch { /* ignore */ }
    }
    track.addEventListener('pointermove', move)
    track.addEventListener('pointerup', up)
    track.addEventListener('pointercancel', up)
  }, [onChange, valueFromClientX])

  // role="slider" + tabIndex promised keyboard control that was never
  // wired up.
  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    const step = e.shiftKey ? 10 : 5
    let next: number | null = null
    switch (e.key) {
      case 'ArrowLeft': case 'ArrowDown': next = clamp(value - step); break
      case 'ArrowRight': case 'ArrowUp': next = clamp(value + step); break
      case 'Home': next = 0; break
      case 'End': next = 100; break
      case 'PageDown': next = clamp(value - 10); break
      case 'PageUp': next = clamp(value + 10); break
    }
    if (next === null) return
    e.preventDefault()
    onChange(next)
  }, [onChange, value])

  const toggleMute = useCallback(() => {
    if (muted) {
      onChange(preMute > 0 ? preMute : 80)
    } else {
      setPreMute(value)
      onChange(0)
    }
  }, [muted, onChange, preMute, value])

  return (
    <div class="flex items-center gap-3 w-full max-w-[180px]">
      {/* Mute toggle. This was a decorative icon; users reasonably
          expected the speaker to mute, and reported it as unresponsive. */}
      <button
        type="button"
        onClick={toggleMute}
        class="flex-shrink-0 text-text-tertiary hover:text-text-primary transition-colors"
        aria-label={muted ? 'Unmute' : 'Mute'}
        aria-pressed={muted}
        title={muted ? 'Unmute' : 'Mute'}
      >
        <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <polygon points="11 5 6 9 2 9 2 15 6 15 11 19" />
          {muted && <><line x1="23" y1="9" x2="17" y2="15" /><line x1="17" y1="9" x2="23" y2="15" /></>}
        </svg>
      </button>

      {/* Slider track */}
      <div
        ref={trackRef}
        onPointerDown={handlePointerDown}
        onKeyDown={handleKeyDown}
        // Without this a touch drag is claimed by the browser as a pan
        // and the pointermove events never arrive.
        style={{ touchAction: 'none' }}
        class="relative flex-1 h-8 flex items-center cursor-pointer group focus:outline-none focus-visible:ring-2 focus-visible:ring-accent rounded"
        role="slider"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={value}
        aria-valuetext={`${Math.round(value)}%`}
        aria-label="Volume"
        tabIndex={0}
      >
        {/* Track background */}
        <div class="absolute inset-x-0 h-1 rounded-full bg-[rgba(255,255,255,0.08)]">
          {/* Fill */}
          <div
            class="h-full rounded-full bg-accent transition-[width] duration-75"
            style={{ width: `${value}%` }}
          />
        </div>
        {/* Thumb */}
        <div
          class="absolute w-3.5 h-3.5 rounded-full bg-white shadow-[0_0_6px_rgba(0,0,0,0.4)] transition-[left] duration-75 group-hover:scale-110"
          style={{ left: `calc(${value}% - 7px)` }}
        />
      </div>

      {/* Volume high icon */}
      <svg class="w-4 h-4 text-text-tertiary flex-shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polygon points="11 5 6 9 2 9 2 15 6 15 11 19" />
        <path d="M15.54 8.46a5 5 0 0 1 0 7.07" />
        <path d="M19.07 4.93a10 10 0 0 1 0 14.14" />
      </svg>
    </div>
  )
}
