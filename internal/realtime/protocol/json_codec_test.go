package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONCodec_Subprotocol(t *testing.T) {
	if got := NewJSONCodec().Subprotocol(); got != SubprotocolJSON {
		t.Fatalf("expected %q, got %q", SubprotocolJSON, got)
	}
}

func TestJSONCodec_RoundTrip_Subscribe(t *testing.T) {
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
						map[string]any{"column": "room_id", "op": "eq", "value": float64(42)},
					},
				},
				Columns: []string{"id", "text"},
			}},
			Broadcast: &BroadcastConfig{Self: false, Ack: true},
			Presence:  &PresenceConfig{Key: "user_7"},
		},
	}

	c := NewJSONCodec()
	bytes, err := c.Encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(string(bytes), `"old"`) {
		t.Fatalf("zero-value field leaked: %s", bytes)
	}

	out, err := c.Decode(bytes)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Type != FrameSubscribe || out.Ref != in.Ref || out.Channel != in.Channel {
		t.Fatalf("envelope mismatch: %+v", out)
	}
	if out.Config == nil || len(out.Config.PostgresChanges) != 1 {
		t.Fatalf("missing config: %+v", out.Config)
	}
	pc := out.Config.PostgresChanges[0]
	if pc.Event != "INSERT" || pc.Table != "messages" {
		t.Fatalf("postgres_changes mismatch: %+v", pc)
	}
}

func TestJSONCodec_RoundTrip_PostgresChanges(t *testing.T) {
	const nowStr = "2026-05-11T10:00:00Z"
	in := Frame{
		Type:     FramePostgresChanges,
		Channel:  "room:42",
		LSN:      "0/16B3F40",
		CommitTS: nowStr,
		DBEvent:  EventInsert,
		Schema:   "public",
		Table:    "messages",
		New:      map[string]any{"id": float64(1), "text": "hola"},
		Columns:  []string{"id", "text"},
	}
	c := NewJSONCodec()
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
	if out.CommitTS != nowStr {
		t.Fatalf("commit ts mismatch: %q != %q", out.CommitTS, nowStr)
	}
	row, ok := out.New.(map[string]any)
	if !ok {
		t.Fatalf("new not decoded as map: %T", out.New)
	}
	// UseNumber means numeric values come back as json.Number, not float64.
	if id, _ := row["id"].(json.Number); id != "1" {
		t.Fatalf("id mismatch: %v (%T)", row["id"], row["id"])
	}
	if row["text"] != "hola" {
		t.Fatalf("text mismatch: %v", row["text"])
	}
}

func TestJSONCodec_RoundTrip_Broadcast(t *testing.T) {
	in := Frame{
		Type:    FrameBroadcastOut,
		Channel: "room:42",
		Event:   "typing",
		Payload: map[string]any{"user_id": float64(7)},
		From:    &FrameOrigin{SessionID: "abc", UserID: "7"},
	}
	c := NewJSONCodec()
	bs, err := c.Encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := c.Decode(bs)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Event != "typing" || out.From == nil || out.From.UserID != "7" {
		t.Fatalf("broadcast mismatch: %+v from=%+v", out, out.From)
	}
}

func TestJSONCodec_OmitemptyZeroValues(t *testing.T) {
	bs, err := NewJSONCodec().Encode(Frame{Type: FrameHeartbeatIn, Ref: "h1"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	s := string(bs)
	if strings.Contains(s, `"channel"`) || strings.Contains(s, `"config"`) {
		t.Fatalf("zero-value leakage: %s", s)
	}
	if !strings.Contains(s, `"type":"heartbeat"`) {
		t.Fatalf("type field missing: %s", s)
	}
}

func TestJSONCodec_Decode_Errors(t *testing.T) {
	c := NewJSONCodec()
	tests := map[string][]byte{
		"empty":          []byte(""),
		"whitespace":     []byte("   "),
		"malformed":      []byte(`{"type":`),
		"no type":        []byte(`{}`),
		"trailing bytes": []byte(`{"type":"heartbeat"}{}`),
	}
	for name, in := range tests {
		if _, err := c.Decode(in); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
}

func TestSelectCodec_JSON(t *testing.T) {
	if SelectCodec(SubprotocolJSON) == nil {
		t.Fatalf("JSON codec not registered")
	}
	if SelectCodec("bogus") != nil {
		t.Fatalf("bogus subprotocol returned a codec")
	}
}
