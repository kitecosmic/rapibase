package bus

import (
	"context"
	"errors"

	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// errNotImplemented signals that the NATS-backed bus is not yet wired.
// Distinct from ErrClosed so callers can tell apart "bus is shut down"
// from "this implementation is incomplete".
var errNotImplemented = errors.New("bus.nats: not yet implemented")

// NATSConfig parameterises the NATS-backed Bus. The NATS client itself
// is not imported in this scaffold so the contracts compile without
// adding a dependency before implementation.
type NATSConfig struct {
	// URL is the NATS server URL (e.g. nats://localhost:4222) or empty
	// to start an embedded server in-process (recommended for small
	// deployments).
	URL string

	// Subject is the subject events are published to. Defaults to
	// "rapibase.realtime.events" when zero.
	Subject string

	// QueueGroup, when non-empty, applies a NATS queue group so events
	// are load-balanced across subscribers in the group. The hub does
	// not use queue groups: every node must see every event so it can
	// fan-out to its own local subscribers.
	QueueGroup string
}

// NATS is the multi-node Bus implementation. It forwards events
// published by the leader node to every other rapibase instance.
type NATS struct {
	cfg NATSConfig
}

// NewNATS constructs a NATS bus. Connection is deferred until the
// caller starts publishing or subscribing.
func NewNATS(cfg NATSConfig) *NATS {
	if cfg.Subject == "" {
		cfg.Subject = "rapibase.realtime.events"
	}
	return &NATS{cfg: cfg}
}

// Publish implements Bus.
func (n *NATS) Publish(ctx context.Context, ev wal.Event) error {
	_ = ctx
	_ = ev
	return errNotImplemented
}

// Subscribe implements Bus.
func (n *NATS) Subscribe(ctx context.Context) (<-chan wal.Event, func(), error) {
	_ = ctx
	return nil, func() {}, errNotImplemented
}

// Close implements Bus.
func (n *NATS) Close() error {
	return errNotImplemented
}
