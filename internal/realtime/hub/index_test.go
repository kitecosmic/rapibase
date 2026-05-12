package hub

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/rapibase/rapibase/internal/realtime/bus"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// ---- channelIndex unit tests --------------------------------------

func TestChannelIndex_LookupExactAndWildcard(t *testing.T) {
	i := newChannelIndex()
	a, b, c := &Channel{name: "a"}, &Channel{name: "b"}, &Channel{name: "c"}

	i.set(a, []tableKey{{schema: "public", table: "messages"}}, false)
	i.set(b, []tableKey{{schema: "public", table: "users"}}, false)
	i.set(c, nil, true) // wildcard only

	got := i.lookup("public", "messages")
	if !containsChan(got, a) || !containsChan(got, c) {
		t.Fatalf("messages lookup should include a and c: %v", got)
	}
	if containsChan(got, b) {
		t.Fatalf("messages lookup should not include b: %v", got)
	}

	got = i.lookup("public", "users")
	if !containsChan(got, b) || !containsChan(got, c) {
		t.Fatalf("users lookup should include b and c: %v", got)
	}

	got = i.lookup("public", "unknown_table")
	// Only the wildcard subscriber should be reached.
	if len(got) != 1 || got[0] != c {
		t.Fatalf("unknown table should yield only the wildcard: %v", got)
	}
}

func TestChannelIndex_SetReplacesPreviousEntries(t *testing.T) {
	i := newChannelIndex()
	a := &Channel{name: "a"}
	i.set(a, []tableKey{{schema: "p", table: "t1"}, {schema: "p", table: "t2"}}, false)

	// Replace with only t2 (no wildcard) — t1 must drop out.
	i.set(a, []tableKey{{schema: "p", table: "t2"}}, false)

	if got := i.lookup("p", "t1"); containsChan(got, a) {
		t.Fatalf("t1 should no longer hit a: %v", got)
	}
	if got := i.lookup("p", "t2"); !containsChan(got, a) {
		t.Fatalf("t2 should still hit a: %v", got)
	}
	if got := i.lookup("p", "elsewhere"); containsChan(got, a) {
		t.Fatalf("non-wildcard a should not hit unrelated tables: %v", got)
	}

	// Now enable wildcard — every lookup must include a.
	i.set(a, nil, true)
	if got := i.lookup("p", "anything"); !containsChan(got, a) {
		t.Fatalf("wildcard should hit any lookup: %v", got)
	}
	if got := i.lookup("p", "t2"); containsChan(got, a) {
		// Old t2 entry must have been cleaned up — but a should still
		// appear via wildcard, not via byTable.
		t.Logf("a present in t2 lookup (expected — wildcard): %v", got)
	}
}

func TestChannelIndex_RemoveCleansAllEntries(t *testing.T) {
	i := newChannelIndex()
	a := &Channel{name: "a"}
	i.set(a, []tableKey{{schema: "p", table: "t1"}}, true)
	i.remove(a)

	if got := i.lookup("p", "t1"); len(got) != 0 {
		t.Fatalf("expected empty after remove, got %v", got)
	}
	if got := i.lookup("p", "anything"); len(got) != 0 {
		t.Fatalf("wildcard not cleared after remove, got %v", got)
	}
	if i.size() != 0 {
		t.Fatalf("size = %d, want 0", i.size())
	}
}

func TestChannelIndex_NoDuplicateWhenChannelHasBothExactAndWildcard(t *testing.T) {
	i := newChannelIndex()
	a := &Channel{name: "a"}
	i.set(a, []tableKey{{schema: "p", table: "t"}}, true)
	got := i.lookup("p", "t")
	if len(got) != 1 || got[0] != a {
		t.Fatalf("expected exactly one entry for a, got %v", got)
	}
}

// ---- end-to-end: index is maintained correctly by hub -------------

func TestHub_IndexIsMaintainedByAttachDetach(t *testing.T) {
	b := bus.NewLocal(8)
	defer b.Close()
	h := New(Config{Shards: 2, SubscriberQueueSize: 8, ResumeBufferSize: 8}, b)

	sub := NewSubscriber("s1", "anon", "", 8)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{
			{Event: "*", Schema: "public", Table: "messages"},
			{Event: "*", Schema: "public", Table: "users"},
		},
	}
	if err := h.Attach(context.Background(), sub, "room", cfg); err != nil {
		t.Fatal(err)
	}

	// The index should now route messages and users to this channel.
	if got := h.tableIndex.lookup("public", "messages"); len(got) != 1 {
		t.Fatalf("messages lookup = %v", got)
	}
	if got := h.tableIndex.lookup("public", "users"); len(got) != 1 {
		t.Fatalf("users lookup = %v", got)
	}
	// And not other tables.
	if got := h.tableIndex.lookup("public", "products"); len(got) != 0 {
		t.Fatalf("products lookup should be empty: %v", got)
	}

	h.Detach(sub, "room")
	if got := h.tableIndex.lookup("public", "messages"); len(got) != 0 {
		t.Fatalf("messages lookup after detach: %v", got)
	}
	if h.ChannelCount() != 0 {
		t.Fatalf("channel should be garbage-collected")
	}
}

func TestHub_FanoutOnlyDeliversToMatchingChannels(t *testing.T) {
	b := bus.NewLocal(16)
	defer b.Close()
	h := New(Config{Shards: 4, SubscriberQueueSize: 16, ResumeBufferSize: 16}, b)

	// 3 channels: one watches messages, one watches users, one wildcard.
	subA := NewSubscriber("a", "anon", "", 16)
	defer subA.Close()
	subB := NewSubscriber("b", "anon", "", 16)
	defer subB.Close()
	subW := NewSubscriber("w", "anon", "", 16)
	defer subW.Close()

	if err := h.Attach(context.Background(), subA, "chan_a", &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{Event: "*", Schema: "public", Table: "messages"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.Attach(context.Background(), subB, "chan_b", &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{Event: "*", Schema: "public", Table: "users"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.Attach(context.Background(), subW, "chan_w", &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{Event: "*"}}, // table empty -> wildcard
	}); err != nil {
		t.Fatal(err)
	}

	// Publish a "messages" event. subA + subW should receive; subB
	// should not.
	h.PublishLocal(wal.Event{
		LSN: "0/1", Type: protocol.EventInsert,
		Schema: "public", Table: "messages",
		New: wal.Row{"id": 1},
	})

	if len(subA.Outbound()) != 1 {
		t.Fatalf("subA queue len = %d, want 1", len(subA.Outbound()))
	}
	if len(subW.Outbound()) != 1 {
		t.Fatalf("wildcard subW queue len = %d, want 1", len(subW.Outbound()))
	}
	if len(subB.Outbound()) != 0 {
		t.Fatalf("subB should not have received: queue len = %d", len(subB.Outbound()))
	}
}

func TestHub_IndexHandlesSetAuthRevoke(t *testing.T) {
	b := bus.NewLocal(8)
	defer b.Close()
	checker := denyChecker{deny: map[string]map[string]bool{"anon": {"secret": true}}}
	h := New(Config{Shards: 2, SubscriberQueueSize: 8, Permissions: checker}, b)

	sub := NewSubscriber("s1", "user", "7", 8)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{
			Event: "*", Schema: "public", Table: "users",
			Filter: map[string]any{"column": "secret", "op": "eq", "value": "x"},
		}},
	}
	if err := h.Attach(context.Background(), sub, "room", cfg); err != nil {
		t.Fatal(err)
	}
	if got := h.tableIndex.lookup("public", "users"); len(got) != 1 {
		t.Fatalf("expected entry pre-revoke, got %v", got)
	}

	sub.SetRole("anon", "7")
	revoked := h.ReValidateSubscriber(sub)
	if len(revoked) != 1 || revoked[0] != "room" {
		t.Fatalf("revoked: %v", revoked)
	}

	// Index should be empty now — the only channel was detached.
	if got := h.tableIndex.lookup("public", "users"); len(got) != 0 {
		t.Fatalf("index should be empty after revoke, got %v", got)
	}
}

func containsChan(s []*Channel, target *Channel) bool {
	for _, c := range s {
		if c == target {
			return true
		}
	}
	return false
}

// ---- benchmark: speedup vs the old linear scan --------------------

// BenchmarkFanout_ManyChannels_SingleTable measures fan-out cost when
// most channels do NOT match the event. With the inverted index, time
// should be roughly constant in the number of unrelated channels.
func BenchmarkFanout_ManyChannels_SingleTable(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("channels=%d", n), func(b *testing.B) {
			bus := bus.NewLocal(64)
			defer bus.Close()
			h := New(Config{Shards: 16, SubscriberQueueSize: 64, ResumeBufferSize: 4}, bus)

			// All channels listen to different tables; only one matches.
			subs := make([]*Subscriber, n)
			for i := 0; i < n; i++ {
				sub := NewSubscriber("s"+strconv.Itoa(i), "anon", "", 64)
				subs[i] = sub
				table := "t_" + strconv.Itoa(i)
				if i == 0 {
					table = "target" // The one we'll publish to.
				}
				_ = h.Attach(context.Background(), sub, "ch_"+strconv.Itoa(i),
					&protocol.SubscribeConfig{
						PostgresChanges: []protocol.PostgresChangesConfig{
							{Event: "*", Schema: "public", Table: table},
						},
					})
			}

			ev := wal.Event{
				LSN: "0/1", Type: protocol.EventInsert,
				Schema: "public", Table: "target",
				New: wal.Row{"id": 1},
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.fanout(ev)
				// Drain the subscriber so we don't hit the queue limit.
				select {
				case <-subs[0].Outbound():
				default:
				}
			}
			b.StopTimer()
			for _, s := range subs {
				s.Close()
			}
		})
	}
}
