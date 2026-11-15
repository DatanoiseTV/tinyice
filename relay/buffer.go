// Package relay provides the core audio streaming and relay functionality for TinyIce.
//
// The relay package implements the fundamental components needed for internet radio
// streaming including circular buffers for audio data, stream management for multiple
// mount points, listener connection handling, and various audio format support.
package relay

import (
	"sync"
)

// CircularBuffer is a thread-safe fixed-size ring buffer for stream data.
//
// This buffer implementation is optimized for audio streaming scenarios where
// multiple listeners may connect at different times and need instant access
// to recent audio data. Old data can be safely overwritten when the buffer
// is full.
//
// The buffer uses absolute positioning (Head) to track write positions,
// allowing listeners to subscribe at different offsets and catch up to
// the live stream.
//
// Performance Characteristics:
//   - O(1) write operations (amortized)
//   - O(1) read operations for non-wrapping reads
//   - Thread-safe with RWMutex for concurrent access
//   - Fixed memory footprint (no dynamic allocations during operation)
//
// Typical Usage:
//
//	buffer := NewCircularBuffer(512 * 1024) // 512KB buffer
//	buffer.Write(audioData)
//	bytesRead, newOffset, skipped := buffer.ReadAt(offset, readBuffer)
type CircularBuffer struct {
	Data []byte       // The actual buffer storage
	Size int64        // Maximum size of the buffer in bytes
	Head int64        // Current write position (absolute, monotonically increasing)
	mu   sync.RWMutex // Mutex for thread-safe operations

	// Keyframe tracking for video streams
	keyframes  []int64 // Circular list of absolute offsets where keyframes start
	kfHead     int     // Next write position in keyframes
	kfCount    int     // Number of valid entries
	kfCapacity int     // Max tracked keyframes
}

// NewCircularBuffer creates a new CircularBuffer with the specified size.
//
// The size parameter determines the maximum amount of audio data that can be
// buffered. Typical values range from 64KB to 2MB depending on the expected
// listener latency requirements. Larger buffers allow listeners to rewind further
// but consume more memory.
func NewCircularBuffer(size int) *CircularBuffer {
	return &CircularBuffer{
		Data: make([]byte, size),
		Size: int64(size),
	}
}

// Write appends data to the circular buffer.
//
// Write is thread-safe and can be called concurrently from multiple goroutines.
// The data is written in chunks, automatically handling buffer wrap-around.
// The method locks the buffer for the entire duration of the write operation.
//
// Performance characteristics:
//   - O(1) complexity for typical writes
//   - Locks the buffer during the entire operation
//   - Handles partial writes when data spans buffer boundaries
func (cb *CircularBuffer) Write(p []byte) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Write data in chunks, handling buffer wrap-around
	// This loop continues until all data is written
	for len(p) > 0 {
		pos := cb.Head % cb.Size    // Calculate position within buffer bounds
		n := copy(cb.Data[pos:], p) // Copy as much as fits in current segment
		cb.Head += int64(n)         // Advance write position
		p = p[n:]                   // Advance source pointer
	}
}

// ReadAt reads data from the buffer starting at the absolute offset 'start'.
//
// ReadAt is designed for audio streaming scenarios where listeners may connect
// at different times and need to catch up to the live stream. It handles
// buffer wrap-around automatically and limits reads to available data.
//
// If the requested position is ahead of the current write position, ReadAt
// returns (0, start, false) indicating no data is available. If the listener
// is too far behind (> buffer size), it skips to the oldest available data
// and returns the skipped flag as true.
//
// ReadAt uses RLock for read operations, allowing concurrent reads from
// multiple goroutines. This makes it safe to call from any goroutine.
//
// Example:
//
//	bytesRead, newOffset, wasSkipped := buffer.ReadAt(currentOffset, audioBuffer)
func (cb *CircularBuffer) ReadAt(start int64, p []byte) (int, int64, bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	skipped := false

	// If requested position is ahead of current write position, no data available
	if start >= cb.Head {
		return 0, start, false
	}

	// Don't read more than we have or what's available in the buffer
	// If listener is too far behind (> buffer size), skip to oldest available data
	if cb.Head-start > cb.Size {
		start = cb.Head - cb.Size // Listener is too slow, skip to oldest available
		skipped = true
	}

	pos := start % cb.Size       // Calculate read position within buffer bounds
	available := cb.Head - start // Calculate available bytes from start position
	n := int64(len(p))           // Requested read size

	// Limit read to available data
	if n > available {
		n = available
	}

	// Handle wrap-around: if read would cross buffer boundary, read in two parts
	if pos+n > cb.Size {
		firstPart := cb.Size - pos
		copy(p, cb.Data[pos:pos+firstPart])
		secondPart := n - firstPart
		copy(p[firstPart:], cb.Data[0:secondPart])
		return int(n), start + n, skipped
	}

	// Perform the actual read (no wrap needed)
	actual := copy(p, cb.Data[pos:pos+n])
	return actual, start + int64(actual), skipped
}

// HeadOffset returns the buffer's current write head under the buffer mutex.
// Use this from any goroutine other than the exclusive writer.
func (cb *CircularBuffer) HeadOffset() int64 {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.Head
}

// ResetKeyframes drops every recorded keyframe offset. Use when the
// source's codec config changes mid-stream — old keyframe offsets point
// at bytes the new decoder state can't interpret, so they must not be
// handed out as safe starting points for new listeners.
func (cb *CircularBuffer) ResetKeyframes() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.kfHead = 0
	cb.kfCount = 0
}

// FindNextPageBoundaryLocked holds the buffer's RWMutex for the duration of
// the scan, so it's safe to use against a buffer that may be concurrently
// written by a source goroutine. Returns the absolute offset of the next
// valid Ogg page boundary at-or-after `start`, or Head if none is found.
func (cb *CircularBuffer) FindNextPageBoundaryLocked(start int64) int64 {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return FindNextPageBoundary(cb.Data, cb.Size, cb.Head, start)
}

// FindNextMP3FrameLocked returns the first valid MPEG audio frame header
// at or after start. A relay subscriber joins at a random byte in the buffer.
// An MP3 decoder must start on a frame boundary.
// A return value of Head means that no full header is available now.
func (cb *CircularBuffer) FindNextMP3FrameLocked(start int64) int64 {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	oldest := cb.Head - cb.Size
	if oldest < 0 {
		oldest = 0
	}
	if start < oldest {
		start = oldest
	}

	for pos := start; pos+1 < cb.Head; pos++ {
		first := cb.Data[pos%cb.Size]
		second := cb.Data[(pos+1)%cb.Size]
		if first != 0xFF || second&0xE0 != 0xE0 {
			continue
		}
		// The MPEG standard does not use version 01 or layer 00.
		// This check stops most random 0xFFE0 bit patterns.
		if (second>>3)&0x03 == 0x01 || (second>>1)&0x03 == 0x00 {
			continue
		}
		return pos
	}
	return cb.Head
}

// Available returns the number of bytes currently available in the buffer.
// If the buffer has not yet been filled, it returns Head (total bytes written).
// Once the buffer has been filled at least once, it returns the buffer Size.
func (cb *CircularBuffer) Available() int64 {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	if cb.Head < cb.Size {
		return cb.Head
	}
	return cb.Size
}

// Reset zeroes the buffer contents and resets the write position to 0.
func (cb *CircularBuffer) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.Head = 0
	for i := range cb.Data {
		cb.Data[i] = 0
	}
}

// Keyframe tracking for video streams
// These fields track absolute offsets where keyframes (IDR frames for H.264) start,
// allowing new video listeners to begin playback at the nearest keyframe.

// RecordKeyframe records a keyframe at the given absolute offset.
func (cb *CircularBuffer) RecordKeyframe(offset int64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.keyframes == nil {
		cb.kfCapacity = 64
		cb.keyframes = make([]int64, cb.kfCapacity)
	}
	cb.keyframes[cb.kfHead] = offset
	cb.kfHead = (cb.kfHead + 1) % cb.kfCapacity
	if cb.kfCount < cb.kfCapacity {
		cb.kfCount++
	}
}

// NearestKeyframe returns the absolute offset of the nearest keyframe
// at or before the given offset that is still within the valid buffer range.
// Returns -1 if no valid keyframe is found.
func (cb *CircularBuffer) NearestKeyframe(offset int64) int64 {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	validStart := cb.Head - cb.Size
	if validStart < 0 {
		validStart = 0
	}

	best := int64(-1)
	for i := 0; i < cb.kfCount; i++ {
		idx := (cb.kfHead - cb.kfCount + i + cb.kfCapacity) % cb.kfCapacity
		kf := cb.keyframes[idx]
		if kf >= validStart && kf <= offset && kf > best {
			best = kf
		}
	}
	return best
}

// LatestKeyframe returns the most recent keyframe offset, or -1.
func (cb *CircularBuffer) LatestKeyframe() int64 {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	if cb.kfCount == 0 {
		return -1
	}
	idx := (cb.kfHead - 1 + cb.kfCapacity) % cb.kfCapacity
	return cb.keyframes[idx]
}
