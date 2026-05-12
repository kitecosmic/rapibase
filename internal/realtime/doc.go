// Package realtime is the root of the rapibase realtime subsystem.
//
// It composes the smaller packages — protocol, filter, wal, bus, hub,
// presence, rpc, transport, metrics — into a runnable Service. Other
// parts of rapibase (the api/routes layer, cmd/rapibase) only depend
// on this root package; they never reach into the sub-packages
// directly.
//
// Architectural map (arrows are "depends on"):
//
//	cmd/rapibase ──► internal/realtime (this package)
//	                    │
//	   ┌────────────────┼────────────────┐
//	   ▼                ▼                ▼
//	transport         hub             wal
//	   │              │  │             │
//	   ▼              ▼  ▼             ▼
//	protocol      filter  bus       (pgoutput)
//	   ▲
//	   └─ everything that talks the wire
//
// The realtime.Service value wires concrete codecs into protocol,
// builds the hub with the supplied permission checker, starts the
// leader-elected replicator, runs the bus pump, and exposes a
// Router for transport to dispatch inbound frames against.
package realtime
