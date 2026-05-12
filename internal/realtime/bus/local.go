package bus

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// Local is the single-process Bus implementation. It fans out every
// published event to every active subscriber over buffered Go channels.
//
// Concurrency model:
//
//   - Publish holds an RLock for the duration of the fan-out. The send
//     is non-blocking (select with default), so the lock is never held
//     for longer than the time it takes to enqueue into N channels.
//   - Subscribe and Cancel take the WLock, which means they wait for
//     in-flight publishes to finish. Once a Cancel acquires the WLock,
//     no Publish can be reading the map, so it is safe to close the
//     subscriber's channel.
//
// Slow consumers are dropped, not back-pressured. Producers never block;
// dropped events are counted on the recorder so operators can alert on
// excessive churn.
type Local struct {
	mu      sync.RWMutex
	subs    map[*subscription]struct{}
	bufSize int
	closed  bool

	drops atomic.Uint64
}

type subscription struct {
	ch chan wal.Event
}

// NewLocal constructs a Local bus. bufSize is the per-subscriber channel
// buffer; pass 0 to use the default (1024).
func NewLocal(bufSize int) *Local {
	if bufSize <= 0 {
		bufSize = 1024
	}
	return &Local{
		subs:    make(map[*subscription]struct{}),
		bufSize: bufSize,
	}
}

// Publish implements Bus.
//
// Returns ErrClosed if the bus has been closed. A canceled context
// short-circuits before the fan-out begins; it is not re-checked
// between subscribers (fan-out is non-blocking and over very quickly).
func (l *Local) Publish(ctx context.Context, ev wal.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return ErrClosed
	}
	for s := range l.subs {
		select {
		case s.ch <- ev:
		default:
			l.drops.Add(1)
		}
	}
	return nil
}

// Subscribe implements Bus.
//
// The returned cancel function is idempotent: calling it more than once
// is safe and a no-op after the first call. It also blocks any in-flight
// publish that is currently iterating the subscriber map.
func (l *Local) Subscribe(ctx context.Context) (<-chan wal.Event, func(), error) {
	_ = ctx
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, func() {}, ErrClosed
	}
	s := &subscription{ch: make(chan wal.Event, l.bufSize)}
	l.subs[s] = struct{}{}

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if _, ok := l.subs[s]; ok {
				delete(l.subs, s)
				close(s.ch)
			}
		})
	}
	return s.ch, cancel, nil
}

// Close implements Bus. Closing more than once returns ErrClosed.
func (l *Local) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	l.closed = true
	for s := range l.subs {
		close(s.ch)
	}
	l.subs = nil
	return nil
}

// Drops returns the number of events dropped due to slow consumers
// across the lifetime of the bus. Used by the metrics subsystem.
func (l *Local) Drops() uint64 { return l.drops.Load() }

// SubscriberCount returns the number of active subscribers. Useful for
// tests and admin endpoints.
func (l *Local) SubscriberCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.subs)
}
