package protocol

import (
	"bytes"
	"fmt"

	"github.com/vmihailenco/msgpack/v5"
)

// MsgpackCodec encodes frames using vmihailenco/msgpack/v5. It is the
// default codec advertised in the welcome frame because of its smaller
// payloads and faster decode in browsers and mobile clients.
//
// The codec configures the underlying encoder to use string map keys so
// the wire is interoperable with JavaScript / Swift / Kotlin msgpack
// implementations that do not understand integer-keyed maps.
type MsgpackCodec struct{}

// NewMsgpackCodec returns the singleton msgpack codec.
func NewMsgpackCodec() *MsgpackCodec { return msgpackSingleton }

var msgpackSingleton = &MsgpackCodec{}

// Subprotocol implements Codec.
func (*MsgpackCodec) Subprotocol() string { return SubprotocolMsgpack }

// Encode implements Codec.
func (*MsgpackCodec) Encode(f Frame) ([]byte, error) {
	var buf bytes.Buffer
	enc := msgpack.NewEncoder(&buf)
	enc.SetSortMapKeys(false)
	enc.UseCompactInts(true)
	enc.UseCompactFloats(true)
	enc.SetOmitEmpty(true)
	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("msgpack codec: encode %s: %w", f.Type, err)
	}
	return buf.Bytes(), nil
}

// Decode implements Codec.
func (*MsgpackCodec) Decode(data []byte) (Frame, error) {
	if len(data) == 0 {
		return Frame{}, fmt.Errorf("msgpack codec: empty frame")
	}
	var f Frame
	dec := msgpack.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&f); err != nil {
		return Frame{}, fmt.Errorf("msgpack codec: decode: %w", err)
	}
	if f.Type == "" {
		return Frame{}, fmt.Errorf("msgpack codec: missing type")
	}
	return f, nil
}

func init() { RegisterCodec(msgpackSingleton) }
