package protocol

import (
	"testing"
	"time"
)

func TestMsgpackCodec_Subprotocol(t *testing.T) {
	if got := NewMsgpackCodec().Subprotocol(); got != SubprotocolMsgpack {
		t.Fatalf("expected %q, got %q", SubprotocolMsgpack, got)
	}
}

func TestMsgpackCodec_RoundTrip_Subscribe(t *testing.T) {
	in := Frame{
		Type:    FrameSubscribe,
		Ref:     "s1",
		Channel: "room:42",
		Config: &SubscribeConfig{
			PostgresChanges: []PostgresChangesConfig{{
				Event:  "INSERT",
				Schema: "public",
				Table:  "messages",
				Filter: map[string]any{
					"op": "and",
					"conditions": []any{
						map[string]any{"column": "room_id", "op": "eq", "value": int64(42)},
					},
				},
				Columns: []string{"id", "text"},
			}},
			Broadcast: &BroadcastConfig{Self: false, Ack: true},
			Presence:  &PresenceConfig{Key: "user_7"},
		},
	}
	c := NewMsgpackCodec()
	bs, err := c.Encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := c.Decode(bs)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Type != FrameSubscribe || out.Ref != "s1" || out.Channel != "room:42" {
		t.Fatalf("envelope mismatch: %+v", out)
	}
	if out.Config == nil || len(out.Config.PostgresChanges) != 1 {
		t.Fatalf("missing config: %+v", out.Config)
	}
	pc := out.Config.PostgresChanges[0]
	if pc.Event != "INSERT" || pc.Table != "messages" {
		t.Fatalf("postgres_changes mismatch: %+v", pc)
	}
	if out.Config.Broadcast == nil || !out.Config.Broadcast.Ack {
		t.Fatalf("broadcast config mismatch: %+v", out.Config.Broadcast)
	}
}

func TestMsgpackCodec_RoundTrip_PostgresChanges(t *testing.T) {
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	in := Frame{
		Type:     FramePostgresChanges,
		Channel:  "room:42",
		LSN:      "0/16B3F40",
		CommitTS: now,
		DBEvent:  EventInsert,
		Schema:   "public",
		Table:    "messages",
		New:      map[string]any{"id": int64(1), "text": "hola"},
		Columns:  []string{"id", "text"},
	}
	c := NewMsgpackCodec()
	bs, err := c.Encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := c.Decode(bs)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.LSN != in.LSN || out.DBEvent != EventInsert {
		t.Fatalf("event metadata mismatch: %+v", out)
	}
	if !out.CommitTS.Equal(in.CommitTS) {
		t.Fatalf("commit ts mismatch: %v != %v", out.CommitTS, in.CommitTS)
	}
}

func TestMsgpackCodec_OmitemptyZeroValues(t *testing.T) {
	// Use a small frame with several zero fields and verify the wire size
	// is reasonable. An exact size assertion would be brittle; instead,
	// roundtrip it and confirm zero fields stay zero on the other side.
	in := Frame{Type: FrameHeartbeatIn, Ref: "h1"}
	c := NewMsgpackCodec()
	bs, err := c.Encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := c.Decode(bs)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Channel != "" || out.Config != nil {
		t.Fatalf("zero fields leaked: %+v", out)
	}
}

func TestMsgpackCodec_Decode_Errors(t *testing.T) {
	c := NewMsgpackCodec()
	if _, err := c.Decode(nil); err == nil {
		t.Fatalf("expected error on nil")
	}
	if _, err := c.Decode([]byte{0xff}); err == nil {
		t.Fatalf("expected error on malformed bytes")
	}
}

func TestSelectCodec_Msgpack(t *testing.T) {
	if SelectCodec(SubprotocolMsgpack) == nil {
		t.Fatalf("msgpack codec not registered")
	}
}

// TestCrossCodec_BroadcastInterop simulates a client connected with
// msgpack broadcasting a payload that another client decodes via JSON.
// The server-side path is: msgpack.Decode -> hub holds Frame -> json.Encode.
// Both codecs must agree on the semantic content of opaque payloads.
func TestCrossCodec_BroadcastInterop(t *testing.T) {
	original := Frame{
		Type:    FrameBroadcastOut,
		Channel: "room:42",
		Event:   "typing",
		Payload: map[string]any{"user_id": int64(7), "name": "Joel"},
		From:    &FrameOrigin{SessionID: "abc", UserID: "7"},
	}

	mp := NewMsgpackCodec()
	js := NewJSONCodec()

	wire, err := mp.Encode(original)
	if err != nil {
		t.Fatalf("msgpack encode: %v", err)
	}
	decoded, err := mp.Decode(wire)
	if err != nil {
		t.Fatalf("msgpack decode: %v", err)
	}

	// Now the server fan-outs to a JSON client.
	jsWire, err := js.Encode(decoded)
	if err != nil {
		t.Fatalf("json encode: %v", err)
	}
	jsOut, err := js.Decode(jsWire)
	if err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if jsOut.Event != "typing" || jsOut.From == nil || jsOut.From.UserID != "7" {
		t.Fatalf("interop failed: %+v from=%+v", jsOut, jsOut.From)
	}
	payload, ok := jsOut.Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type: %T", jsOut.Payload)
	}
	if payload["name"] != "Joel" {
		t.Fatalf("payload mismatch: %+v", payload)
	}
}
