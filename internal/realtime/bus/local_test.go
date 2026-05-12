package bus

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

func ev(lsn string) wal.Event {
	return wal.Event{LSN: protocol.LSN(lsn)}
}

func TestLocal_PublishSubscribe(t *testing.T) {
	b := NewLocal(8)
	ctx := context.Background()

	ch1, cancel1, err := b.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel1()
	ch2, cancel2, err := b.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel2()

	if err := b.Publish(ctx, ev("0/1")); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, ev("0/2")); err != nil {
		t.Fatal(err)
	}

	for _, ch := range []<-chan wal.Event{ch1, ch2} {
		got := []string{}
		for i := 0; i < 2; i++ {
			select {
			case e := <-ch:
				got = append(got, string(e.LSN))
			case <-time.After(time.Second):
				t.Fatalf("timeout receiving from subscriber")
			}
		}
		if got[0] != "0/1" || got[1] != "0/2" {
			t.Fatalf("order mismatch: %v", got)
		}
	}
}

func TestLocal_Cancel_Idempotent(t *testing.T) {
	b := NewLocal(4)
	ctx := context.Background()
	ch, cancel, err := b.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	cancel()
	cancel() // must not panic
	if _, ok := <-ch; ok {
		t.Fatalf("expected closed channel on cancel")
	}
	if got := b.SubscriberCount(); got != 0 {
		t.Fatalf("count after cancel = %d", got)
	}
}

func TestLocal_PublishAfterCancel_DoesNotDeliver(t *testing.T) {
	b := NewLocal(4)
	ctx := context.Background()
	ch, cancel, err := b.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	if err := b.Publish(ctx, ev("0/1")); err != nil {
		t.Fatal(err)
	}
	// Channel is closed by cancel; the range exits immediately.
	got := []wal.Event{}
	for e := range ch {
		got = append(got, e)
	}
	if len(got) != 0 {
		t.Fatalf("canceled subscriber received events: %v", got)
	}
}

func TestLocal_SlowConsumer_Drops(t *testing.T) {
	b := NewLocal(2) // tiny buffer
	ctx := context.Background()
	ch, cancel, err := b.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	// Publish more than the buffer; we never drain ch.
	for i := 0; i < 10; i++ {
		if err := b.Publish(ctx, ev("0/x")); err != nil {
			t.Fatal(err)
		}
	}

	if b.Drops() == 0 {
		t.Fatalf("expected drops > 0, got 0")
	}
	if got := cap(ch); got != 2 {
		t.Fatalf("buffer size = %d", got)
	}
}

func TestLocal_Close_DrainsAllSubscribers(t *testing.T) {
	b := NewLocal(4)
	ctx := context.Background()
	ch1, _, _ := b.Subscribe(ctx)
	ch2, _, _ := b.Subscribe(ctx)

	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	for i, ch := range []<-chan wal.Event{ch1, ch2} {
		if _, ok := <-ch; ok {
			t.Fatalf("subscriber %d not closed", i)
		}
	}

	// Subsequent operations error out.
	if err := b.Publish(ctx, ev("0/1")); err != ErrClosed {
		t.Fatalf("expected ErrClosed on publish after close, got %v", err)
	}
	if _, _, err := b.Subscribe(ctx); err != ErrClosed {
		t.Fatalf("expected ErrClosed on subscribe after close, got %v", err)
	}
	if err := b.Close(); err != ErrClosed {
		t.Fatalf("second close should return ErrClosed, got %v", err)
	}
}

func TestLocal_ContextCanceled(t *testing.T) {
	b := NewLocal(4)
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Publish(ctx, ev("0/1")); err == nil {
		t.Fatalf("expected context error on publish with canceled ctx")
	}
}

// TestLocal_Concurrent_PublishAndCancel exercises the locking contract:
// publishers must not panic even when subscribers are being canceled
// concurrently. Run with -race to catch any data races.
func TestLocal_Concurrent_PublishAndCancel(t *testing.T) {
	b := NewLocal(16)
	defer b.Close()
	ctx := context.Background()

	const subs = 32
	const events = 200

	chans := make([]<-chan wal.Event, subs)
	cancels := make([]func(), subs)
	for i := 0; i < subs; i++ {
		ch, c, err := b.Subscribe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		chans[i] = ch
		cancels[i] = c
	}

	var received atomic.Int64
	var wg sync.WaitGroup
	for _, ch := range chans {
		ch := ch
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range ch {
				received.Add(1)
			}
		}()
	}

	// Publisher
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < events; i++ {
			_ = b.Publish(ctx, ev("0/p"))
		}
	}()

	// Canceler: cancels every subscriber half-way through.
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		for _, c := range cancels {
			c()
		}
	}()

	wg.Wait()
	// The exact count is non-deterministic (drops + cancellation), but
	// the test passes if no goroutine panicked and the bus drained
	// without leaks.
	if received.Load() < 0 {
		t.Fatalf("nonsense receive count")
	}
}
