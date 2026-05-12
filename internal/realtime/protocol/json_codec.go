package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// JSONCodec encodes frames using the standard library's encoding/json.
// It is the simplest codec and the one used by clients that cannot
// implement msgpack (browsers without a msgpack lib, curl-based debug
// sessions, etc.).
type JSONCodec struct{}

// NewJSONCodec returns a singleton JSON codec. The codec is stateless
// and safe for concurrent use.
func NewJSONCodec() *JSONCodec { return jsonSingleton }

var jsonSingleton = &JSONCodec{}

// Subprotocol implements Codec.
func (*JSONCodec) Subprotocol() string { return SubprotocolJSON }

// Encode implements Codec. The output never has a trailing newline; the
// WebSocket library is responsible for framing.
func (*JSONCodec) Encode(f Frame) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("json codec: encode %s: %w", f.Type, err)
	}
	// encoding/json appends a newline; trim it.
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return out, nil
}

// Decode implements Codec. Decode requires the input to be a single JSON
// object; extra trailing data is rejected.
func (*JSONCodec) Decode(data []byte) (Frame, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Frame{}, fmt.Errorf("json codec: empty frame")
	}
	var f Frame
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&f); err != nil {
		return Frame{}, fmt.Errorf("json codec: decode: %w", err)
	}
	if dec.More() {
		return Frame{}, fmt.Errorf("json codec: unexpected trailing data")
	}
	if f.Type == "" {
		return Frame{}, fmt.Errorf("json codec: missing type")
	}
	return f, nil
}

func init() { RegisterCodec(jsonSingleton) }
