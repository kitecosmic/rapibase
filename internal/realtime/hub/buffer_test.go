package hub

import (
	"testing"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

func mkev(lsn string) wal.Event {
	return wal.Event{LSN: protocol.LSN(lsn)}
}

func TestCompareLSN(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "0/1", -1},
		{"0/1", "", 1},
		{"0/1", "0/1", 0},
		{"0/1", "0/2", -1},
		{"0/2", "0/1", 1},
		{"FF/0", "100/0", -1}, // 0xFF < 0x100 numerically
		{"100/0", "FF/0", 1},
		{"16/B3F40", "16/B3F41", -1},
		{"16/B3F41", "16/B3F40", 1},
		// Malformed fall back to lexicographic — both malformed:
		{"bogus", "bogus", 0},
	}
	for _, c := range cases {
		got := compareLSN(protocol.LSN(c.a), protocol.LSN(c.b))
		if got != c.want {
			t.Fatalf("compareLSN(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestBuffer_PushAndSize(t *testing.T) {
	b := NewBuffer(3)
	if b.Size() != 0 {
		t.Fatalf("empty size: %d", b.Size())
	}
	b.Push(mkev("0/1"))
	b.Push(mkev("0/2"))
	if b.Size() != 2 {
		t.Fatalf("size: %d", b.Size())
	}
	b.Push(mkev("0/3"))
	if b.Size() != 3 {
		t.Fatalf("size: %d", b.Size())
	}
	// Overflow: oldest dropped.
	b.Push(mkev("0/4"))
	if b.Size() != 3 {
		t.Fatalf("size after overflow: %d", b.Size())
	}
	if got := b.Earliest(); got != "0/2" {
		t.Fatalf("earliest after overflow: %q", got)
	}
	if got := b.Latest(); got != "0/4" {
		t.Fatalf("latest: %q", got)
	}
}

func TestBuffer_Replay_FromEmpty(t *testing.T) {
	b := NewBuffer(3)
	out, err := b.Replay("")
	if err != nil || len(out) != 0 {
		t.Fatalf("empty buffer + empty after: out=%v err=%v", out, err)
	}
	out, err = b.Replay("0/5")
	if err != ErrTruncated {
		t.Fatalf("empty buffer + specific after must be truncated: out=%v err=%v", out, err)
	}
}

func TestBuffer_Replay_AfterCovered(t *testing.T) {
	b := NewBuffer(5)
	for _, l := range []string{"0/1", "0/2", "0/3", "0/4"} {
		b.Push(mkev(l))
	}

	out, err := b.Replay("0/2")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, e := range out {
		got = append(got, string(e.LSN))
	}
	want := []string{"0/3", "0/4"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestBuffer_Replay_AfterEqualLatest(t *testing.T) {
	b := NewBuffer(5)
	b.Push(mkev("0/1"))
	b.Push(mkev("0/2"))
	out, err := b.Replay("0/2")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no new events, got %v", out)
	}
}

func TestBuffer_Replay_AfterTruncated(t *testing.T) {
	b := NewBuffer(2)
	b.Push(mkev("0/1"))
	b.Push(mkev("0/2"))
	b.Push(mkev("0/3")) // pushes out 0/1
	if got := b.Earliest(); got != "0/2" {
		t.Fatalf("earliest: %q", got)
	}
	_, err := b.Replay("0/1")
	if err != ErrTruncated {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
	// Empty after == start, must NOT be truncated even though earliest is 0/2.
	out, err := b.Replay("")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || string(out[0].LSN) != "0/2" || string(out[1].LSN) != "0/3" {
		t.Fatalf("empty after should return all retained: %v", out)
	}
}

func TestBuffer_Replay_AfterEqualEarliest_NotTruncated(t *testing.T) {
	b := NewBuffer(3)
	b.Push(mkev("0/5"))
	b.Push(mkev("0/6"))
	b.Push(mkev("0/7"))
	out, err := b.Replay("0/5")
	if err != nil {
		t.Fatalf("expected ok when after == earliest: %v", err)
	}
	if len(out) != 2 || string(out[0].LSN) != "0/6" {
		t.Fatalf("got %v", out)
	}
}

func TestBuffer_ZeroCapacity(t *testing.T) {
	b := NewBuffer(0)
	b.Push(mkev("0/1")) // no-op
	if b.Size() != 0 {
		t.Fatalf("zero-cap buffer should not store")
	}
	if _, err := b.Replay(""); err != ErrTruncated {
		t.Fatalf("zero-cap replay must return ErrTruncated, got %v", err)
	}
}
