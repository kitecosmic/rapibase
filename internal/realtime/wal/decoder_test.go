package wal

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pglogrepl"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// Helpers --------------------------------------------------------------

// rel makes a RelationMessage with the given (relid, schema, table)
// and a fixed set of columns. Columns are flagged as key.
func rel(relid uint32, schema, table string, cols ...colSpec) *pglogrepl.RelationMessage {
	r := &pglogrepl.RelationMessage{
		RelationID:      relid,
		Namespace:       schema,
		RelationName:    table,
		ReplicaIdentity: 'd',
		ColumnNum:       uint16(len(cols)),
	}
	for _, c := range cols {
		r.Columns = append(r.Columns, &pglogrepl.RelationMessageColumn{
			Flags:    c.flags,
			Name:     c.name,
			DataType: c.oid,
		})
	}
	return r
}

type colSpec struct {
	name  string
	oid   uint32
	flags uint8
}

// tup builds a TupleData from string values. Use "" with DataType set
// to 'n' (via tupCol) for nulls.
func tup(cols ...*pglogrepl.TupleDataColumn) *pglogrepl.TupleData {
	return &pglogrepl.TupleData{
		ColumnNum: uint16(len(cols)),
		Columns:   cols,
	}
}

func tupCol(text string) *pglogrepl.TupleDataColumn {
	return &pglogrepl.TupleDataColumn{
		DataType: 't',
		Length:   uint32(len(text)),
		Data:     []byte(text),
	}
}

func nullCol() *pglogrepl.TupleDataColumn {
	return &pglogrepl.TupleDataColumn{DataType: 'n'}
}

func unchangedCol() *pglogrepl.TupleDataColumn {
	return &pglogrepl.TupleDataColumn{DataType: 'u'}
}

// drive feeds messages through processMessage and collects events.
func drive(t *testing.T, d *pgoutputDecoder, msgs ...pglogrepl.Message) []Event {
	t.Helper()
	var out []Event
	for _, m := range msgs {
		evs, err := d.processMessage(m)
		if err != nil {
			t.Fatalf("processMessage %T: %v", m, err)
		}
		out = append(out, evs...)
	}
	return out
}

func newDecoder() *pgoutputDecoder {
	return &pgoutputDecoder{relations: map[uint32]*pglogrepl.RelationMessage{}}
}

// Tests ---------------------------------------------------------------

func TestDecoder_InsertEmitsEventWithTxnContext(t *testing.T) {
	d := newDecoder()
	r := rel(1, "public", "messages",
		colSpec{name: "id", oid: oidInt8, flags: 1},
		colSpec{name: "text", oid: 25}, // text
	)
	commitTS := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)

	events := drive(t, d,
		r,
		&pglogrepl.BeginMessage{FinalLSN: 0x16B3F40, CommitTime: commitTS, Xid: 1},
		&pglogrepl.InsertMessage{RelationID: 1, Tuple: tup(tupCol("42"), tupCol("hola"))},
		&pglogrepl.CommitMessage{},
	)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Type != protocol.EventInsert {
		t.Fatalf("type: %v", e.Type)
	}
	if e.Schema != "public" || e.Table != "messages" {
		t.Fatalf("relation: %s.%s", e.Schema, e.Table)
	}
	if !e.CommitTS.Equal(commitTS) {
		t.Fatalf("commit ts: %v want %v", e.CommitTS, commitTS)
	}
	if string(e.LSN) != "0/16B3F40" {
		t.Fatalf("lsn: %q", e.LSN)
	}
	if e.New["id"] != int64(42) {
		t.Fatalf("id should be int64(42): %v (%T)", e.New["id"], e.New["id"])
	}
	if e.New["text"] != "hola" {
		t.Fatalf("text: %v", e.New["text"])
	}
}

func TestDecoder_DMLOutsideTxn_Errors(t *testing.T) {
	d := newDecoder()
	d.relations[1] = rel(1, "public", "t", colSpec{name: "id", oid: oidInt4})
	_, err := d.processMessage(&pglogrepl.InsertMessage{RelationID: 1, Tuple: tup(tupCol("1"))})
	if !errors.Is(err, errOutOfTxn) {
		t.Fatalf("expected errOutOfTxn, got %v", err)
	}
}

func TestDecoder_UnknownRelation_Errors(t *testing.T) {
	d := newDecoder()
	drive(t, d, &pglogrepl.BeginMessage{FinalLSN: 1})
	_, err := d.processMessage(&pglogrepl.InsertMessage{RelationID: 99, Tuple: tup(tupCol("1"))})
	if err == nil {
		t.Fatalf("expected error for unknown relation")
	}
}

func TestDecoder_Update_WithOldAndNew(t *testing.T) {
	d := newDecoder()
	r := rel(1, "public", "users",
		colSpec{name: "id", oid: oidInt4, flags: 1},
		colSpec{name: "name", oid: 25},
	)
	events := drive(t, d,
		r,
		&pglogrepl.BeginMessage{FinalLSN: 0x20},
		&pglogrepl.UpdateMessage{
			RelationID:   1,
			OldTupleType: 'O',
			OldTuple:     tup(tupCol("7"), tupCol("old")),
			NewTuple:     tup(tupCol("7"), tupCol("new")),
		},
		&pglogrepl.CommitMessage{},
	)
	if len(events) != 1 || events[0].Type != protocol.EventUpdate {
		t.Fatalf("expected single update, got %+v", events)
	}
	if events[0].Old["name"] != "old" || events[0].New["name"] != "new" {
		t.Fatalf("old/new mismatch: %+v", events[0])
	}
}

func TestDecoder_Update_UnchangedToastOmitted(t *testing.T) {
	d := newDecoder()
	r := rel(1, "public", "docs",
		colSpec{name: "id", oid: oidInt4, flags: 1},
		colSpec{name: "body", oid: 25},
	)
	events := drive(t, d,
		r,
		&pglogrepl.BeginMessage{FinalLSN: 0x30},
		&pglogrepl.UpdateMessage{
			RelationID:   1,
			OldTupleType: 'K',
			OldTuple:     tup(tupCol("7"), unchangedCol()),
			NewTuple:     tup(tupCol("7"), unchangedCol()),
		},
		&pglogrepl.CommitMessage{},
	)
	if len(events) != 1 {
		t.Fatal("expected 1 event")
	}
	if _, present := events[0].New["body"]; present {
		t.Fatalf("unchanged TOAST should be omitted, got %v", events[0].New["body"])
	}
	if events[0].New["id"] != int64(7) {
		t.Fatalf("id: %v", events[0].New["id"])
	}
}

func TestDecoder_Delete(t *testing.T) {
	d := newDecoder()
	r := rel(1, "public", "t", colSpec{name: "id", oid: oidInt4, flags: 1})
	events := drive(t, d,
		r,
		&pglogrepl.BeginMessage{FinalLSN: 0x40},
		&pglogrepl.DeleteMessage{
			RelationID:   1,
			OldTupleType: 'K',
			OldTuple:     tup(tupCol("99")),
		},
		&pglogrepl.CommitMessage{},
	)
	if len(events) != 1 || events[0].Type != protocol.EventDelete {
		t.Fatalf("expected delete, got %+v", events)
	}
	if events[0].Old["id"] != int64(99) {
		t.Fatalf("old id: %v", events[0].Old["id"])
	}
	if events[0].New != nil {
		t.Fatalf("delete should not have New: %v", events[0].New)
	}
}

func TestDecoder_NullValue(t *testing.T) {
	d := newDecoder()
	r := rel(1, "public", "t",
		colSpec{name: "id", oid: oidInt4, flags: 1},
		colSpec{name: "deleted_at", oid: oidTimestamptz},
	)
	events := drive(t, d,
		r,
		&pglogrepl.BeginMessage{FinalLSN: 1},
		&pglogrepl.InsertMessage{RelationID: 1, Tuple: tup(tupCol("1"), nullCol())},
		&pglogrepl.CommitMessage{},
	)
	if events[0].New["deleted_at"] != nil {
		t.Fatalf("null should be nil, got %v", events[0].New["deleted_at"])
	}
}

func TestDecoder_Truncate(t *testing.T) {
	d := newDecoder()
	r1 := rel(1, "public", "a", colSpec{name: "id", oid: oidInt4})
	r2 := rel(2, "public", "b", colSpec{name: "id", oid: oidInt4})
	events := drive(t, d,
		r1, r2,
		&pglogrepl.BeginMessage{FinalLSN: 0x50},
		&pglogrepl.TruncateMessage{RelationNum: 2, RelationIDs: []uint32{1, 2}},
		&pglogrepl.CommitMessage{},
	)
	if len(events) != 2 {
		t.Fatalf("expected 2 truncate events, got %d", len(events))
	}
	if events[0].Type != protocol.EventTruncate || events[1].Type != protocol.EventTruncate {
		t.Fatalf("event types: %+v", events)
	}
	if events[0].Table != "a" || events[1].Table != "b" {
		t.Fatalf("tables: %+v", events)
	}
}

func TestDecoder_Reset_ClearsRelationsAndTxn(t *testing.T) {
	d := newDecoder()
	d.relations[1] = rel(1, "p", "t", colSpec{name: "id", oid: oidInt4})
	d.inTxn = true
	d.txnLSN = "0/1"
	d.Reset()
	if len(d.relations) != 0 || d.inTxn || d.txnLSN != "" {
		t.Fatalf("reset did not clear state: %+v", d)
	}
}

func TestParseValue_TypeCoercion(t *testing.T) {
	cases := []struct {
		name string
		oid  uint32
		in   string
		want any
	}{
		{"bool t", oidBool, "t", true},
		{"bool f", oidBool, "f", false},
		{"int4", oidInt4, "42", int64(42)},
		{"int8 large", oidInt8, "9999999999", int64(9999999999)},
		{"float8", oidFloat8, "3.14", 3.14},
		{"numeric", oidNumeric, "1.5", 1.5},
		{"text fallback", 25, "hola", "hola"},
		{"json object", oidJSON, `{"a":1}`, map[string]any{"a": float64(1)}},
		{"jsonb array", oidJSONB, `[1,2]`, []any{float64(1), float64(2)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseValue(c.oid, []byte(c.in))
			if !deepEq(got, c.want) {
				t.Fatalf("got %v (%T) want %v (%T)", got, got, c.want, c.want)
			}
		})
	}
}

func TestParseValue_Timestamps(t *testing.T) {
	got := parseValue(oidTimestamptz, []byte("2026-05-11 12:34:56+00"))
	tt, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tt.Year() != 2026 || tt.Hour() != 12 {
		t.Fatalf("parsed time wrong: %v", tt)
	}
}

func TestParseValue_BytesHex(t *testing.T) {
	got := parseValue(oidBytea, []byte("\\xdeadbeef"))
	b, ok := got.([]byte)
	if !ok {
		t.Fatalf("bytea should decode to []byte, got %T", got)
	}
	want := []byte{0xde, 0xad, 0xbe, 0xef}
	if len(b) != len(want) {
		t.Fatalf("len mismatch: %v", b)
	}
	for i := range want {
		if b[i] != want[i] {
			t.Fatalf("byte[%d] = %x want %x", i, b[i], want[i])
		}
	}
}

func TestDecoder_UnknownMessageType_NotError(t *testing.T) {
	d := newDecoder()
	_, err := d.processMessage(&pglogrepl.OriginMessage{})
	if err != nil {
		t.Fatalf("OriginMessage should be ignored: %v", err)
	}
}

// deepEq is a tiny structural comparator just for parseValue cases.
func deepEq(a, b any) bool {
	switch ax := a.(type) {
	case map[string]any:
		bx, ok := b.(map[string]any)
		if !ok || len(ax) != len(bx) {
			return false
		}
		for k, v := range ax {
			if !deepEq(v, bx[k]) {
				return false
			}
		}
		return true
	case []any:
		bx, ok := b.([]any)
		if !ok || len(ax) != len(bx) {
			return false
		}
		for i := range ax {
			if !deepEq(ax[i], bx[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}
