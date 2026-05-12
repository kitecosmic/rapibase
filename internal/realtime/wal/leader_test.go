package wal

import (
	"sync"
	"testing"
)

// The full Leader.Run path requires a live Postgres for the advisory
// lock; that lives in an integration test. Here we exercise the
// transition logic in setLeader and the callback wiring, which is the
// only part of the type we can test without a database.

func TestLeader_OnChangeFiresOnlyOnTransition(t *testing.T) {
	var mu sync.Mutex
	calls := []bool{}
	l := NewLeader(LeaderConfig{LockKey: 1}, func(b bool) {
		mu.Lock()
		calls = append(calls, b)
		mu.Unlock()
	})

	l.setLeader(false) // no transition, initial state is false
	l.setLeader(true)  // transition → fires
	l.setLeader(true)  // no transition
	l.setLeader(true)  // no transition
	l.setLeader(false) // transition → fires
	l.setLeader(false) // no transition

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("expected 2 callback calls, got %d: %v", len(calls), calls)
	}
	if calls[0] != true || calls[1] != false {
		t.Fatalf("transition order wrong: %v", calls)
	}
}

func TestLeader_IsLeaderTracksState(t *testing.T) {
	l := NewLeader(LeaderConfig{LockKey: 1}, nil)
	if l.IsLeader() {
		t.Fatal("initial state should be false")
	}
	l.setLeader(true)
	if !l.IsLeader() {
		t.Fatal("expected leader=true after setLeader(true)")
	}
	l.setLeader(false)
	if l.IsLeader() {
		t.Fatal("expected leader=false after setLeader(false)")
	}
}

func TestLeader_NilCallbackIsSafe(t *testing.T) {
	l := NewLeader(LeaderConfig{LockKey: 1}, nil)
	// Must not panic.
	l.setLeader(true)
	l.setLeader(false)
}
