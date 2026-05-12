package presence

import "github.com/rapibase/rapibase/internal/realtime/protocol"

// Diff is the incremental change emitted to subscribers after a Track,
// Untrack or DropRef call. Empty diffs (no joins, no leaves, no
// updates) should not produce a wire frame.
type Diff struct {
	Joins   map[string][]protocol.PresenceEntry
	Leaves  map[string][]protocol.PresenceEntry
	Updates map[string][]protocol.PresenceEntry
}

// Empty reports whether the diff carries no changes.
func (d Diff) Empty() bool {
	return len(d.Joins) == 0 && len(d.Leaves) == 0 && len(d.Updates) == 0
}

// ToFrame produces a presence_diff frame for the supplied channel,
// suitable to be enqueued on each subscriber's outbound queue.
func (d Diff) ToFrame(channel string) protocol.Frame {
	return protocol.Frame{
		Type:    protocol.FramePresenceDiff,
		Channel: channel,
		Joins:   d.Joins,
		Leaves:  d.Leaves,
		Updates: d.Updates,
	}
}
