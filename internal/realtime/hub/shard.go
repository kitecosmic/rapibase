package hub

import (
	"hash/fnv"
	"sync"
)

// shard owns a disjoint subset of channels. Subscribers, presence state
// and the resume buffer for each channel live entirely inside one
// shard, which means every operation on a single channel only acquires
// that shard's mutex. With N CPUs and N shards, fan-out on disjoint
// channels scales linearly.
type shard struct {
	mu       sync.RWMutex
	channels map[string]*Channel
	cfg      Config
}

func newShard(cfg Config) *shard {
	return &shard{channels: make(map[string]*Channel), cfg: cfg}
}

// shardFor returns the shard owning the given channel name.
func (h *Hub) shardFor(channel string) *shard {
	if len(h.shards) == 0 {
		return nil
	}
	hh := fnv.New32a()
	_, _ = hh.Write([]byte(channel))
	return h.shards[int(hh.Sum32())%len(h.shards)]
}

// getOrCreate returns the channel object inside the shard, creating it
// on first use. Safe under contention.
func (s *shard) getOrCreate(name string) *Channel {
	s.mu.RLock()
	c, ok := s.channels[name]
	s.mu.RUnlock()
	if ok {
		return c
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok = s.channels[name]; ok {
		return c
	}
	c = newChannel(name, s.cfg)
	s.channels[name] = c
	return c
}

// get returns the channel if present; nil otherwise.
func (s *shard) get(name string) *Channel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.channels[name]
}

// removeIfEmpty deletes the channel from the shard iff it has zero
// subscribers. Returns true if the channel was actually removed.
// Called by the hub after Detach so abandoned channels do not leak.
func (s *shard) removeIfEmpty(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.channels[name]
	if !ok {
		return false
	}
	if c.SubscriberCount() > 0 {
		return false
	}
	delete(s.channels, name)
	return true
}

// forEach iterates over every channel in the shard, invoking fn with a
// snapshot taken under the shard lock. fn is called outside the lock
// so a slow callback does not block other shard operations.
func (s *shard) forEach(fn func(*Channel)) {
	s.mu.RLock()
	snap := make([]*Channel, 0, len(s.channels))
	for _, c := range s.channels {
		snap = append(snap, c)
	}
	s.mu.RUnlock()
	for _, c := range snap {
		fn(c)
	}
}

// channelCount returns the number of channels in this shard. Used by
// metrics and tests.
func (s *shard) channelCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.channels)
}
