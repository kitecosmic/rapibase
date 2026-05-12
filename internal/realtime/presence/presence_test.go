package presence

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

func TestTrack_FirstEntry_IsJoin(t *testing.T) {
	s := NewState()
	d := s.Track("user_7", "sess_a", map[string]any{"status": "online"})
	if d.Empty() {
		t.Fatalf("expected non-empty diff")
	}
	if len(d.Joins["user_7"]) != 1 || len(d.Leaves) != 0 || len(d.Updates) != 0 {
		t.Fatalf("expected single join, got %+v", d)
	}
	if got := d.Joins["user_7"][0].Ref; got != "sess_a" {
		t.Fatalf("ref mismatch: %q", got)
	}
	if s.MemberCount() != 1 || s.EntryCount() != 1 {
		t.Fatalf("counts: members=%d entries=%d", s.MemberCount(), s.EntryCount())
	}
}

func TestTrack_SameRef_IsUpdate_PreservesJoinedAt(t *testing.T) {
	s := NewState()
	d1 := s.Track("user_7", "sess_a", map[string]any{"status": "online"})
	originalJoined := d1.Joins["user_7"][0].JoinedAt

	time.Sleep(2 * time.Millisecond) // ensure time would change if not preserved

	d2 := s.Track("user_7", "sess_a", map[string]any{"status": "away"})
	if len(d2.Updates["user_7"]) != 1 || len(d2.Joins) != 0 {
		t.Fatalf("expected single update, got %+v", d2)
	}
	entry := d2.Updates["user_7"][0]
	if !entry.JoinedAt.Equal(originalJoined) {
		t.Fatalf("joined_at not preserved across update: %v -> %v", originalJoined, entry.JoinedAt)
	}
	state, _ := entry.State.(map[string]any)
	if state["status"] != "away" {
		t.Fatalf("state not updated: %+v", entry.State)
	}
	if s.EntryCount() != 1 {
		t.Fatalf("update should not change entry count")
	}
}

func TestTrack_DifferentRefSameKey_AddsSecondEntry(t *testing.T) {
	s := NewState()
	s.Track("user_7", "sess_a", "online")
	d := s.Track("user_7", "sess_b", "online")
	if len(d.Joins["user_7"]) != 1 || d.Joins["user_7"][0].Ref != "sess_b" {
		t.Fatalf("expected join for sess_b, got %+v", d)
	}
	if s.MemberCount() != 1 {
		t.Fatalf("members: %d", s.MemberCount())
	}
	if s.EntryCount() != 2 {
		t.Fatalf("entries: %d", s.EntryCount())
	}
}

func TestUntrack_RemovesEntry_EmitsLeave(t *testing.T) {
	s := NewState()
	s.Track("user_7", "sess_a", "online")
	s.Track("user_7", "sess_b", "online")
	d := s.Untrack("user_7", "sess_a")
	if len(d.Leaves["user_7"]) != 1 || d.Leaves["user_7"][0].Ref != "sess_a" {
		t.Fatalf("expected leave for sess_a, got %+v", d)
	}
	if s.MemberCount() != 1 || s.EntryCount() != 1 {
		t.Fatalf("counts after untrack: members=%d entries=%d", s.MemberCount(), s.EntryCount())
	}
}

func TestUntrack_LastEntry_RemovesKey(t *testing.T) {
	s := NewState()
	s.Track("user_7", "sess_a", "online")
	d := s.Untrack("user_7", "sess_a")
	if len(d.Leaves["user_7"]) != 1 {
		t.Fatalf("expected leave, got %+v", d)
	}
	if s.MemberCount() != 0 {
		t.Fatalf("key should be gone, members=%d", s.MemberCount())
	}
}

func TestUntrack_UnknownRef_EmptyDiff(t *testing.T) {
	s := NewState()
	s.Track("user_7", "sess_a", "online")
	if d := s.Untrack("user_7", "sess_zzz"); !d.Empty() {
		t.Fatalf("untrack of missing ref should be empty: %+v", d)
	}
	if d := s.Untrack("nobody", "sess_a"); !d.Empty() {
		t.Fatalf("untrack of missing key should be empty: %+v", d)
	}
}

func TestDropRef_RemovesAcrossEveryKey(t *testing.T) {
	s := NewState()
	s.Track("user_7", "sess_a", "online")
	s.Track("user_8", "sess_a", "online") // same ref, different key
	s.Track("user_8", "sess_b", "online")

	d := s.DropRef("sess_a")
	if len(d.Leaves) != 2 {
		t.Fatalf("expected leaves for 2 keys, got %+v", d.Leaves)
	}
	if s.MemberCount() != 1 || s.EntryCount() != 1 {
		t.Fatalf("counts after dropref: members=%d entries=%d", s.MemberCount(), s.EntryCount())
	}
	// user_8 still has sess_b
	snap := s.Snapshot()
	if len(snap["user_8"]) != 1 || snap["user_8"][0].Ref != "sess_b" {
		t.Fatalf("user_8 should retain sess_b: %+v", snap)
	}
}

func TestDropRef_NoMatch_EmptyDiff(t *testing.T) {
	s := NewState()
	s.Track("user_7", "sess_a", "online")
	if d := s.DropRef("not_here"); !d.Empty() {
		t.Fatalf("expected empty diff, got %+v", d)
	}
}

func TestSnapshot_IsDeepCopy(t *testing.T) {
	s := NewState()
	s.Track("user_7", "sess_a", "online")
	snap1 := s.Snapshot()
	// Mutating the snapshot should not affect later snapshots.
	snap1["user_7"] = append(snap1["user_7"], protocol.PresenceEntry{Ref: "fake"})
	snap1["new_key"] = nil

	snap2 := s.Snapshot()
	if len(snap2["user_7"]) != 1 {
		t.Fatalf("live state mutated by snapshot edit: %+v", snap2)
	}
	if _, present := snap2["new_key"]; present {
		t.Fatalf("live state gained spurious key")
	}
}

func TestDiff_ToFrame(t *testing.T) {
	d := Diff{
		Joins: map[string][]protocol.PresenceEntry{
			"user_7": {{Ref: "sess_a", State: "online"}},
		},
	}
	f := d.ToFrame("room")
	if f.Type != protocol.FramePresenceDiff || f.Channel != "room" {
		t.Fatalf("frame envelope wrong: %+v", f)
	}
	if !reflect.DeepEqual(f.Joins, d.Joins) {
		t.Fatalf("joins not propagated: %+v", f.Joins)
	}
}

func TestState_Concurrent_TrackAndDropRef(t *testing.T) {
	s := NewState()
	const refs = 32
	const updatesPerRef = 50

	var wg sync.WaitGroup
	for i := 0; i < refs; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ref := refOf(i)
			for j := 0; j < updatesPerRef; j++ {
				s.Track("k", ref, j)
			}
			s.Untrack("k", ref)
		}()
	}

	// Concurrent reader.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = s.Snapshot()
		}
	}()

	wg.Wait()
	if s.EntryCount() != 0 {
		t.Fatalf("expected fully drained, got %d entries", s.EntryCount())
	}
}

func refOf(i int) string {
	// Small helper to avoid pulling in strconv just for tests.
	const hex = "0123456789abcdef"
	return string([]byte{hex[(i>>4)&0xf], hex[i&0xf]})
}
