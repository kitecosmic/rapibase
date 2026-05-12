package wal

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pglogrepl"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// Decoder translates raw pgoutput messages into Event values. A
// separate type allows alternate decoders (test fixtures, wal2json) to
// be swapped in without touching the replicator.
//
// Decoders are stateful: they accumulate relation messages and the
// active transaction boundaries (Begin / Commit). They must not be
// shared across replicators.
type Decoder interface {
	// Decode consumes one logical replication message (the bytes a
	// pgoutput XLogData payload carries) and returns zero or more
	// Events, or a fatal error that the caller should propagate.
	Decode(message []byte) ([]Event, error)

	// Reset clears all accumulated state. Called by the replicator
	// when the upstream connection is re-established.
	Reset()
}

// NewPgoutputDecoder returns a Decoder that speaks pgoutput protocol
// version 1 (the default on modern Postgres). Streaming of in-progress
// transactions (protocol v2 STREAM_* messages) is intentionally not
// supported by this decoder: rapibase requests proto v1 from the
// server, which buffers each transaction until commit before sending
// it. That trades a bit of memory on Postgres for a vastly simpler
// state machine here.
func NewPgoutputDecoder() Decoder {
	return &pgoutputDecoder{
		relations: make(map[uint32]*pglogrepl.RelationMessage),
	}
}

type pgoutputDecoder struct {
	mu sync.Mutex

	relations map[uint32]*pglogrepl.RelationMessage

	// Active transaction context. Populated on Begin, used by every
	// DML in the transaction, cleared on Commit.
	inTxn  bool
	txnLSN protocol.LSN
	txnTS  time.Time
}

// Decode implements Decoder.
func (d *pgoutputDecoder) Decode(data []byte) ([]Event, error) {
	if len(data) == 0 {
		return nil, errEmpty
	}
	msg, err := pglogrepl.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("pgoutput parse: %w", err)
	}
	return d.processMessage(msg)
}

// Reset implements Decoder.
func (d *pgoutputDecoder) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.relations = make(map[uint32]*pglogrepl.RelationMessage)
	d.inTxn = false
	d.txnLSN = ""
	d.txnTS = time.Time{}
}

// processMessage is the message-level handler that exists separately
// from Decode so unit tests can drive the state machine without
// constructing pgoutput byte sequences.
func (d *pgoutputDecoder) processMessage(msg pglogrepl.Message) ([]Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch m := msg.(type) {
	case *pglogrepl.RelationMessage:
		d.relations[m.RelationID] = m
		return nil, nil

	case *pglogrepl.BeginMessage:
		d.inTxn = true
		d.txnLSN = protocol.LSN(m.FinalLSN.String())
		d.txnTS = m.CommitTime
		return nil, nil

	case *pglogrepl.CommitMessage:
		d.inTxn = false
		return nil, nil

	case *pglogrepl.InsertMessage:
		rel, ok := d.relations[m.RelationID]
		if !ok {
			return nil, fmt.Errorf("insert references unknown relation %d", m.RelationID)
		}
		if !d.inTxn {
			return nil, errOutOfTxn
		}
		return []Event{{
			LSN:      d.txnLSN,
			CommitTS: d.txnTS,
			Type:     protocol.EventInsert,
			Schema:   rel.Namespace,
			Table:    rel.RelationName,
			New:      tupleToRow(rel, m.Tuple),
		}}, nil

	case *pglogrepl.UpdateMessage:
		rel, ok := d.relations[m.RelationID]
		if !ok {
			return nil, fmt.Errorf("update references unknown relation %d", m.RelationID)
		}
		if !d.inTxn {
			return nil, errOutOfTxn
		}
		ev := Event{
			LSN:      d.txnLSN,
			CommitTS: d.txnTS,
			Type:     protocol.EventUpdate,
			Schema:   rel.Namespace,
			Table:    rel.RelationName,
			New:      tupleToRow(rel, m.NewTuple),
		}
		if m.OldTuple != nil {
			ev.Old = tupleToRow(rel, m.OldTuple)
		}
		return []Event{ev}, nil

	case *pglogrepl.DeleteMessage:
		rel, ok := d.relations[m.RelationID]
		if !ok {
			return nil, fmt.Errorf("delete references unknown relation %d", m.RelationID)
		}
		if !d.inTxn {
			return nil, errOutOfTxn
		}
		ev := Event{
			LSN:      d.txnLSN,
			CommitTS: d.txnTS,
			Type:     protocol.EventDelete,
			Schema:   rel.Namespace,
			Table:    rel.RelationName,
		}
		if m.OldTuple != nil {
			ev.Old = tupleToRow(rel, m.OldTuple)
		}
		return []Event{ev}, nil

	case *pglogrepl.TruncateMessage:
		if !d.inTxn {
			return nil, errOutOfTxn
		}
		out := make([]Event, 0, len(m.RelationIDs))
		for _, rid := range m.RelationIDs {
			rel, ok := d.relations[rid]
			if !ok {
				continue
			}
			out = append(out, Event{
				LSN:      d.txnLSN,
				CommitTS: d.txnTS,
				Type:     protocol.EventTruncate,
				Schema:   rel.Namespace,
				Table:    rel.RelationName,
			})
		}
		return out, nil

	default:
		// OriginMessage, TypeMessage, MessageMessage, etc. — we don't
		// surface them as Events but they are not errors.
		return nil, nil
	}
}

// tupleToRow converts a pgoutput TupleData into a Row, looking up the
// column names and OIDs from the relation. Unknown OIDs fall back to a
// string value (the raw text from Postgres).
func tupleToRow(rel *pglogrepl.RelationMessage, tup *pglogrepl.TupleData) Row {
	if tup == nil {
		return nil
	}
	row := make(Row, len(tup.Columns))
	for i, col := range tup.Columns {
		if i >= len(rel.Columns) {
			break
		}
		name := rel.Columns[i].Name
		switch col.DataType {
		case 'n':
			row[name] = nil
		case 'u':
			// Unchanged TOAST: no value sent. Omit the column rather
			// than set it to nil so the consumer can distinguish
			// "absent" from "explicit null".
			continue
		case 't':
			row[name] = parseValue(rel.Columns[i].DataType, col.Data)
		case 'b':
			// Binary format. rapibase requests text format; if a
			// binary value sneaks in (custom output_options) keep it
			// as raw bytes rather than misparsing.
			row[name] = append([]byte(nil), col.Data...)
		default:
			row[name] = col.Data
		}
	}
	return row
}

// parseValue interprets a text-format value from Postgres into the
// natural Go type the filter and codec layers expect. Unknown types
// fall back to string.
//
// OID constants come from PostgreSQL's pg_type catalog; the set below
// covers the columns realistically used in change-data subscriptions.
// New OIDs added here should preserve the principle: numbers as
// float64/int64, booleans as bool, times as time.Time, JSON as a
// generic any decoded tree.
func parseValue(oid uint32, data []byte) any {
	s := string(data)
	switch oid {
	case oidBool:
		return s == "t" || s == "true"
	case oidInt2, oidInt4, oidInt8, oidOid:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		return s
	case oidFloat4, oidFloat8, oidNumeric:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
		return s
	case oidTimestamp:
		// "2026-05-11 12:34:56.789"
		for _, layout := range []string{
			"2006-01-02 15:04:05.999999",
			"2006-01-02 15:04:05",
		} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC()
			}
		}
		return s
	case oidTimestamptz:
		// "2026-05-11 12:34:56.789+00"
		for _, layout := range []string{
			"2006-01-02 15:04:05.999999-07",
			"2006-01-02 15:04:05-07",
			time.RFC3339Nano,
			time.RFC3339,
		} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC()
			}
		}
		return s
	case oidDate:
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t.UTC()
		}
		return s
	case oidJSON, oidJSONB:
		var v any
		if err := json.Unmarshal(data, &v); err == nil {
			return v
		}
		return s
	case oidBytea:
		// Postgres hex format: "\\xDEADBEEF". Strip the prefix and
		// decode; on failure return the raw bytes.
		if len(data) >= 2 && data[0] == '\\' && data[1] == 'x' {
			out := make([]byte, (len(data)-2)/2)
			if _, err := hexDecode(out, data[2:]); err == nil {
				return out
			}
		}
		return append([]byte(nil), data...)
	}
	return s
}

// hexDecode is a small inline hex decoder so we avoid pulling in
// encoding/hex for this single use case. Returns the number of bytes
// written and an error if the input is malformed.
func hexDecode(dst, src []byte) (int, error) {
	if len(src)%2 != 0 {
		return 0, errors.New("hex: odd length")
	}
	for i := 0; i < len(src); i += 2 {
		hi, ok1 := fromHex(src[i])
		lo, ok2 := fromHex(src[i+1])
		if !ok1 || !ok2 {
			return 0, errors.New("hex: invalid byte")
		}
		dst[i/2] = hi<<4 | lo
	}
	return len(src) / 2, nil
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// PostgreSQL type OIDs (from pg_type.h). Names chosen to make the
// intent obvious at the call site.
const (
	oidBool        uint32 = 16
	oidBytea       uint32 = 17
	oidInt8        uint32 = 20
	oidInt2        uint32 = 21
	oidInt4        uint32 = 23
	oidOid         uint32 = 26
	oidJSON        uint32 = 114
	oidFloat4      uint32 = 700
	oidFloat8      uint32 = 701
	oidDate        uint32 = 1082
	oidTimestamp   uint32 = 1114
	oidTimestamptz uint32 = 1184
	oidNumeric     uint32 = 1700
	oidJSONB       uint32 = 3802
)

// errOutOfTxn signals that a DML message arrived without a preceding
// Begin. This is a protocol violation from Postgres and should be
// treated as fatal — the replicator restarts the slot.
var errOutOfTxn = errors.New("wal: DML outside transaction")

// errEmpty signals an empty input slice — should not happen in practice
// because XLogData always carries at least the type byte.
var errEmpty = errors.New("wal: empty message")
