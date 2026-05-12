package hub

import "sync"

// tableKey is the (schema, table) pair the fan-out path indexes on.
// Both fields must be non-empty to live in the byTable map;
// wildcards (empty schema or empty table) go into a separate set.
type tableKey struct {
	schema string
	table  string
}

// channelIndex maintains an inverted index from (schema, table) to
// the set of channels with at least one pgStream targeting that
// relation. Without this index, Hub.fanout would have to invoke every
// channel for every event; with it, fan-out is O(matches) instead of
// O(channels).
//
// Wildcards (pgStream.schema == "" or pgStream.table == "") cannot be
// keyed exactly, so they live in a separate `wildcard` set that
// participates in every lookup. This is the right trade-off because
// wildcard subscriptions are intentionally broad — they pay the cost
// they implicitly accept.
//
// The index keeps a per-channel cache so set() and remove() are O(N)
// in the number of distinct tables the channel listens for, not O(N)
// in the total index size. Mutations and lookups serialize on a
// single RWMutex; the lock is held only long enough to copy the
// matching channels into a slice — the fan-out itself runs lock-free
// over that snapshot.
type channelIndex struct {
	mu         sync.RWMutex
	byTable    map[tableKey]map[*Channel]struct{}
	wildcard   map[*Channel]struct{}
	perChannel map[*Channel]channelIndexEntry
}

type channelIndexEntry struct {
	tables   []tableKey
	wildcard bool
}

func newChannelIndex() *channelIndex {
	return &channelIndex{
		byTable:    make(map[tableKey]map[*Channel]struct{}),
		wildcard:   make(map[*Channel]struct{}),
		perChannel: make(map[*Channel]channelIndexEntry),
	}
}

// set replaces the index entries for a channel with the supplied
// (tables, wildcard) state. Callers compute this from the union of
// every subscriber's pgStreams, then call set once per Attach/Detach
// to keep the index in sync.
//
// Repeated calls with the same arguments are a no-op (cheap).
func (i *channelIndex) set(c *Channel, tables []tableKey, wildcard bool) {
	i.mu.Lock()
	defer i.mu.Unlock()

	prev := i.perChannel[c]

	// Remove byTable entries that are no longer present.
	newTables := make(map[tableKey]struct{}, len(tables))
	for _, k := range tables {
		newTables[k] = struct{}{}
	}
	for _, k := range prev.tables {
		if _, kept := newTables[k]; kept {
			continue
		}
		if set, ok := i.byTable[k]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(i.byTable, k)
			}
		}
	}

	// Add byTable entries that are new.
	prevTables := make(map[tableKey]struct{}, len(prev.tables))
	for _, k := range prev.tables {
		prevTables[k] = struct{}{}
	}
	for _, k := range tables {
		if _, existed := prevTables[k]; existed {
			continue
		}
		set, ok := i.byTable[k]
		if !ok {
			set = make(map[*Channel]struct{})
			i.byTable[k] = set
		}
		set[c] = struct{}{}
	}

	// Wildcard transitions.
	switch {
	case wildcard && !prev.wildcard:
		i.wildcard[c] = struct{}{}
	case !wildcard && prev.wildcard:
		delete(i.wildcard, c)
	}

	// Cache the new state. Defensive copy of tables so callers can
	// reuse the slice safely.
	cached := make([]tableKey, len(tables))
	copy(cached, tables)
	i.perChannel[c] = channelIndexEntry{tables: cached, wildcard: wildcard}
}

// remove drops every entry for a channel. Called when a channel is
// garbage-collected from its shard.
func (i *channelIndex) remove(c *Channel) {
	i.mu.Lock()
	defer i.mu.Unlock()
	prev := i.perChannel[c]
	for _, k := range prev.tables {
		if set, ok := i.byTable[k]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(i.byTable, k)
			}
		}
	}
	if prev.wildcard {
		delete(i.wildcard, c)
	}
	delete(i.perChannel, c)
}

// lookup returns every channel that has at least one pgStream
// matching (schema, table). The result includes channels whose
// streams are full or partial wildcards.
//
// The returned slice is a fresh allocation owned by the caller; it
// can be iterated without holding the index lock.
func (i *channelIndex) lookup(schema, table string) []*Channel {
	i.mu.RLock()
	defer i.mu.RUnlock()

	exact := i.byTable[tableKey{schema: schema, table: table}]
	out := make([]*Channel, 0, len(exact)+len(i.wildcard))
	for c := range exact {
		out = append(out, c)
	}
	for c := range i.wildcard {
		// Avoid duplicates when a channel has both an exact and a
		// wildcard stream.
		if _, dup := exact[c]; dup {
			continue
		}
		out = append(out, c)
	}
	return out
}

// size returns the number of (channel × table) entries in the byTable
// map plus the wildcard size. Used by tests.
func (i *channelIndex) size() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	total := len(i.wildcard)
	for _, set := range i.byTable {
		total += len(set)
	}
	return total
}
