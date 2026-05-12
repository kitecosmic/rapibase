package wal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

// Config configures the replication pipeline.
type Config struct {
	// ConnString is a libpq-style connection string used to open the
	// replication connection. Must point to a database where the
	// configured publication exists and where the connecting role has
	// the REPLICATION attribute. The query string must include
	// `replication=database` so pgconn opens a replication connection
	// rather than a normal SQL one — Bootstrap injects this if missing.
	ConnString string

	// SlotName is the logical replication slot consumed by this
	// rapibase node. The slot must be permanent and pre-created
	// (Bootstrap creates it on first run).
	SlotName string

	// PublicationName is the Postgres publication describing the
	// tables to capture.
	PublicationName string

	// StatusInterval is how often the replicator sends a
	// StandbyStatusUpdate (with the latest applied LSN) so Postgres
	// can truncate the WAL. Defaults to 10s when zero.
	StatusInterval time.Duration

	// ProtoVersion is the pgoutput protocol version requested. 1 is
	// the default; 2 enables streaming of in-progress transactions
	// which this decoder does not currently support.
	ProtoVersion int
}

// EventSink receives decoded events from the replicator. Implementations
// must not block the calling goroutine for long — the replicator stops
// applying WAL while a sink is blocked.
type EventSink interface {
	OnEvent(context.Context, Event) error
}

// Replicator drives a single logical replication slot and feeds an
// EventSink. One Replicator instance is created per rapibase node; only
// the elected leader actually calls Run.
type Replicator struct {
	cfg   Config
	sink  EventSink
	dec   Decoder
	gauge LagGauge

	// appliedLSN is updated atomically after every successful sink
	// call. The status loop reads it without coordination.
	appliedLSN atomic.Uint64
	// serverEndLSN tracks the upstream's reported XLogPos so we can
	// compute the lag without an extra round trip.
	serverEndLSN atomic.Uint64
}

// LagGauge is the observability sink for replicator-level metrics.
// Implemented by the realtime root as an adapter over
// metrics.Recorder so this package stays metrics-agnostic.
type LagGauge interface {
	// SetLagBytes is called periodically with the difference between
	// the server's reported WAL end position and the LSN this
	// replicator has acknowledged. A non-decreasing value indicates
	// the consumer is falling behind.
	SetLagBytes(bytes uint64)
}

type nopGauge struct{}

func (nopGauge) SetLagBytes(uint64) {}

// NewReplicator constructs a replicator. The decoder defaults to a
// pgoutput decoder when nil is passed.
func NewReplicator(cfg Config, sink EventSink, dec Decoder) *Replicator {
	if cfg.StatusInterval == 0 {
		cfg.StatusInterval = 10 * time.Second
	}
	if cfg.ProtoVersion == 0 {
		cfg.ProtoVersion = 1
	}
	if dec == nil {
		dec = NewPgoutputDecoder()
	}
	return &Replicator{cfg: cfg, sink: sink, dec: dec, gauge: nopGauge{}}
}

// SetLagGauge installs an observability hook for WAL lag bytes.
func (r *Replicator) SetLagGauge(g LagGauge) {
	if g == nil {
		r.gauge = nopGauge{}
		return
	}
	r.gauge = g
}

// Run opens the replication connection and streams events to the sink
// until ctx is canceled or a fatal error occurs. Returns nil only when
// the context is canceled cleanly.
func (r *Replicator) Run(ctx context.Context) error {
	if r.cfg.SlotName == "" {
		return errors.New("wal: SlotName is required")
	}
	if r.cfg.PublicationName == "" {
		return errors.New("wal: PublicationName is required")
	}

	conn, err := pgconn.Connect(ctx, ensureReplicationParam(r.cfg.ConnString))
	if err != nil {
		return fmt.Errorf("wal: connect: %w", err)
	}
	defer conn.Close(ctx)

	sys, err := pglogrepl.IdentifySystem(ctx, conn)
	if err != nil {
		return fmt.Errorf("wal: identify_system: %w", err)
	}
	startLSN := sys.XLogPos
	r.appliedLSN.Store(uint64(startLSN))

	r.dec.Reset()
	if err := pglogrepl.StartReplication(ctx, conn, r.cfg.SlotName, startLSN, pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			fmt.Sprintf("proto_version '%d'", r.cfg.ProtoVersion),
			fmt.Sprintf("publication_names '%s'", r.cfg.PublicationName),
		},
	}); err != nil {
		return fmt.Errorf("wal: start_replication: %w", err)
	}

	nextStatus := time.Now().Add(r.cfg.StatusInterval)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if time.Now().After(nextStatus) {
			if err := r.sendStatusUpdate(conn); err != nil {
				return fmt.Errorf("wal: status update: %w", err)
			}
			nextStatus = time.Now().Add(r.cfg.StatusInterval)
		}

		recvCtx, cancel := context.WithDeadline(ctx, nextStatus)
		msg, err := conn.ReceiveMessage(recvCtx)
		cancel()
		if err != nil {
			if pgconn.Timeout(err) {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("wal: receive: %w", err)
		}

		copyData, ok := msg.(*pgproto3.CopyData)
		if !ok {
			// Backend status messages we don't care about.
			continue
		}
		if len(copyData.Data) == 0 {
			continue
		}

		switch copyData.Data[0] {
		case pglogrepl.PrimaryKeepaliveMessageByteID:
			pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(copyData.Data[1:])
			if err != nil {
				return fmt.Errorf("wal: parse keepalive: %w", err)
			}
			r.serverEndLSN.Store(uint64(pkm.ServerWALEnd))
			r.publishLag()
			if pkm.ReplyRequested {
				nextStatus = time.Now()
			}

		case pglogrepl.XLogDataByteID:
			xld, err := pglogrepl.ParseXLogData(copyData.Data[1:])
			if err != nil {
				return fmt.Errorf("wal: parse xlogdata: %w", err)
			}
			r.serverEndLSN.Store(uint64(xld.ServerWALEnd))
			events, err := r.dec.Decode(xld.WALData)
			if err != nil {
				return fmt.Errorf("wal: decode: %w", err)
			}
			for _, ev := range events {
				if err := r.sink.OnEvent(ctx, ev); err != nil {
					return fmt.Errorf("wal: sink: %w", err)
				}
			}
			// Advance LSN past the message we just consumed. The
			// status loop publishes this back to Postgres so the WAL
			// can be truncated.
			r.appliedLSN.Store(uint64(xld.WALStart) + uint64(len(xld.WALData)))
			r.publishLag()
		}
	}
}

// publishLag emits the WAL lag (bytes between upstream tip and our
// applied position) to the configured LagGauge. Called whenever a
// status frame or XLogData updates either side of the difference.
func (r *Replicator) publishLag() {
	end := r.serverEndLSN.Load()
	applied := r.appliedLSN.Load()
	if end <= applied {
		r.gauge.SetLagBytes(0)
		return
	}
	r.gauge.SetLagBytes(end - applied)
}

func (r *Replicator) sendStatusUpdate(conn *pgconn.PgConn) error {
	lsn := pglogrepl.LSN(r.appliedLSN.Load())
	return pglogrepl.SendStandbyStatusUpdate(context.Background(), conn, pglogrepl.StandbyStatusUpdate{
		WALWritePosition: lsn,
		WALFlushPosition: lsn,
		WALApplyPosition: lsn,
		ClientTime:       time.Now(),
	})
}

// CurrentLSN returns the highest LSN the replicator has successfully
// handed to the sink. Used by metrics and by the hub's resume buffer
// to compute lag.
func (r *Replicator) CurrentLSN() string {
	return pglogrepl.LSN(r.appliedLSN.Load()).String()
}

// ensureReplicationParam injects `replication=database` into the
// connection string if not already present. pgconn requires this for
// the replication protocol; leaving it implicit is a foot-gun.
func ensureReplicationParam(conn string) string {
	if strings.Contains(conn, "replication=") {
		return conn
	}
	sep := "?"
	if strings.Contains(conn, "?") {
		sep = "&"
	}
	return conn + sep + "replication=database"
}
