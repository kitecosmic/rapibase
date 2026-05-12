// Package presence tracks which members are currently active in each
// realtime channel and produces sync / diff events for subscribers.
//
// Presence state lives entirely in process memory and is rebuilt from
// scratch on restart. In multi-node deployments each node owns the
// presence state of its locally-connected subscribers; cross-node
// awareness is achieved by replaying presence frames through the bus
// (deferred to a later phase — current contracts assume single-node
// authoritative presence per channel).
package presence

import (
	"sync"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// State holds the presence members of a single channel. Safe for
// concurrent use; every read takes a short RLock, every write takes
// the WLock.
//
// The map is keyed by an opaque "presence key" (typically the JWT
// user id). Each key may have multiple PresenceEntries — one per
// active session — so multiple tabs / devices of the same user are
// first-class.
type State struct {
	mu      sync.RWMutex
	members map[string][]protocol.PresenceEntry
}

// NewState constructs an empty presence state for one channel.
func NewState() *State {
	return &State{members: make(map[string][]protocol.PresenceEntry)}
}

// Track adds a new presence entry for (key, ref), or updates the
// existing one if (key, ref) was already tracked. Returns the diff to
// broadcast: a Join when the entry is new, an Update when an existing
// entry's state was replaced.
//
// JoinedAt is preserved across updates, so clients can compute how
// long a member has been present.
func (s *State) Track(key, ref string, state any) Diff {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := s.members[key]
	for i, e := range list {
		if e.Ref == ref {
			list[i] = protocol.PresenceEntry{
				Ref:      ref,
				JoinedAt: e.JoinedAt,
				State:    state,
			}
			s.members[key] = list
			return Diff{
				Updates: map[string][]protocol.PresenceEntry{
					key: {list[i]},
				},
			}
		}
	}
	entry := makeEntry(ref, state)
	s.members[key] = append(list, entry)
	return Diff{
		Joins: map[string][]protocol.PresenceEntry{
			key: {entry},
		},
	}
}

// Untrack removes the entry identified by (key, ref). If it was the
// last entry for that key, the key disappears from the map. Returns
// the diff to broadcast (Leaves) or an empty Diff if no matching
// entry existed.
func (s *State) Untrack(key, ref string) Diff {
	s.mu.Lock()
	defer s.mu.Unlock()

	list, ok := s.members[key]
	if !ok {
		return Diff{}
	}
	for i, e := range list {
		if e.Ref == ref {
			removed := e
			newList := append(list[:i:i], list[i+1:]...)
			if len(newList) == 0 {
				delete(s.members, key)
			} else {
				s.members[key] = newList
			}
			return Diff{
				Leaves: map[string][]protocol.PresenceEntry{
					key: {removed},
				},
			}
		}
	}
	return Diff{}
}

// Snapshot returns the current state as a presence_state payload. The
// hub calls this once per subscriber after a successful subscribe.
//
// The returned map is a deep copy: callers can mutate it freely without
// affecting the live state.
func (s *State) Snapshot() map[string][]protocol.PresenceEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]protocol.PresenceEntry, len(s.members))
	for k, v := range s.members {
		entries := make([]protocol.PresenceEntry, len(v))
		copy(entries, v)
		out[k] = entries
	}
	return out
}

// DropRef removes every presence entry whose ref equals the supplied
// session id, across every key on this channel. Used when a connection
// drops so its presences are cleaned up without the client having to
// untrack explicitly.
//
// Returns a Diff aggregating all leaves; empty if nothing was removed.
func (s *State) DropRef(ref string) Diff {
	s.mu.Lock()
	defer s.mu.Unlock()

	var leaves map[string][]protocol.PresenceEntry
	for key, list := range s.members {
		kept := list[:0:0] // new backing array so we don't mutate input
		var removed []protocol.PresenceEntry
		for _, e := range list {
			if e.Ref == ref {
				removed = append(removed, e)
			} else {
				kept = append(kept, e)
			}
		}
		if len(removed) == 0 {
			continue
		}
		if leaves == nil {
			leaves = make(map[string][]protocol.PresenceEntry)
		}
		leaves[key] = removed
		if len(kept) == 0 {
			delete(s.members, key)
		} else {
			s.members[key] = kept
		}
	}
	if leaves == nil {
		return Diff{}
	}
	return Diff{Leaves: leaves}
}

// MemberCount returns the number of distinct keys currently present.
// Used by tests and metrics.
func (s *State) MemberCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.members)
}

// EntryCount returns the total number of presence entries across every
// key (i.e. distinct sessions).
func (s *State) EntryCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, list := range s.members {
		total += len(list)
	}
	return total
}

// makeEntry constructs a fresh PresenceEntry stamped with the current
// time. Centralised so tests and the production path produce uniform
// entries.
func makeEntry(ref string, state any) protocol.PresenceEntry {
	return protocol.PresenceEntry{
		Ref:      ref,
		JoinedAt: time.Now().UTC(),
		State:    state,
	}
}
