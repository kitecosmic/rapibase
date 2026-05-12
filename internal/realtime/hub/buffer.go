package hub

import (
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// Buffer is the per-channel ring of recent events used to answer
// resume(from_lsn). It is bounded; once full, the oldest entry is
// overwritten and resume requests for LSNs older than the surviving
// window return ErrTruncated.
type Buffer struct {
	mu      sync.RWMutex
	entries []wal.Event
	head    int
	size    int
	cap     int
}

// NewBuffer creates a ring buffer with the given capacity. A zero or
// negative capacity disables the buffer (resume always returns
// ErrTruncated).
func NewBuffer(capacity int) *Buffer {
	if capacity < 0 {
		capacity = 0
	}
	return &Buffer{entries: make([]wal.Event, capacity), cap: capacity}
}

// Push records an event in the buffer. Overwrites the oldest entry when
// at capacity. Events with an empty LSN are still buffered but won't
// satisfy resume requests beyond them.
func (b *Buffer) Push(ev wal.Event) {
	if b.cap == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	idx := (b.head + b.size) % b.cap
	b.entries[idx] = ev
	if b.size < b.cap {
		b.size++
	} else {
		b.head = (b.head + 1) % b.cap
	}
}

// Replay returns every event with LSN strictly greater than after, in
// commit order. Returns ErrTruncated when after is older than the
// earliest retained LSN. An empty after is treated as "from the very
// beginning of the retained window" and returns every retained event.
func (b *Buffer) Replay(after protocol.LSN) ([]wal.Event, error) {
	if b.cap == 0 {
		return nil, ErrTruncated
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 {
		// Nothing buffered. Conservative: if the caller asked for
		// catchup from a specific LSN we cannot prove it is safe, so
		// return ErrTruncated. An empty after means "from the start",
		// which is trivially satisfied by an empty slice.
		if after == "" {
			return nil, nil
		}
		return nil, ErrTruncated
	}

	earliest := b.entries[b.head].LSN
	if after != "" && compareLSN(after, earliest) < 0 {
		return nil, ErrTruncated
	}

	out := make([]wal.Event, 0, b.size)
	for i := 0; i < b.size; i++ {
		ev := b.entries[(b.head+i)%b.cap]
		if after == "" || compareLSN(ev.LSN, after) > 0 {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Earliest returns the LSN of the oldest retained entry, or empty if
// the buffer is empty.
func (b *Buffer) Earliest() protocol.LSN {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.size == 0 {
		return ""
	}
	return b.entries[b.head].LSN
}

// Latest returns the LSN of the newest retained entry, or empty when
// the buffer is empty. Useful for diagnostics and the welcome frame.
func (b *Buffer) Latest() protocol.LSN {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.size == 0 {
		return ""
	}
	idx := (b.head + b.size - 1) % b.cap
	return b.entries[idx].LSN
}

// Size returns the number of events currently retained.
func (b *Buffer) Size() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.size
}

// ErrTruncated indicates the requested LSN is older than what the buffer
// still retains. Callers translate this into protocol.ErrSlotTruncated.
var ErrTruncated = errors.New("hub: resume LSN truncated")

// compareLSN compares two Postgres LSN strings ("X/Y" hex) numerically.
// Returns -1, 0 or 1. An empty LSN is treated as the smallest value.
// Malformed input falls back to lexicographic comparison so the buffer
// never panics on a bogus LSN.
func compareLSN(a, b protocol.LSN) int {
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	aHi, aLo, aOK := parseLSN(string(a))
	bHi, bLo, bOK := parseLSN(string(b))
	if !aOK || !bOK {
		return strings.Compare(string(a), string(b))
	}
	switch {
	case aHi < bHi:
		return -1
	case aHi > bHi:
		return 1
	case aLo < bLo:
		return -1
	case aLo > bLo:
		return 1
	default:
		return 0
	}
}

// parseLSN parses "X/Y" with X and Y hex into two uint64 components.
// Returns ok=false for malformed input.
func parseLSN(s string) (hi, lo uint64, ok bool) {
	slash := strings.IndexByte(s, '/')
	if slash <= 0 || slash == len(s)-1 {
		return 0, 0, false
	}
	hi, err := strconv.ParseUint(s[:slash], 16, 64)
	if err != nil {
		return 0, 0, false
	}
	lo, err = strconv.ParseUint(s[slash+1:], 16, 64)
	if err != nil {
		return 0, 0, false
	}
	return hi, lo, true
}
