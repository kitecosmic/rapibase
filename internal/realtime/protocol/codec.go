package protocol

// Codec serializes and deserializes Frame values to and from the bytes
// that travel on the WebSocket. Two implementations are expected:
// msgpackCodec (default) and jsonCodec (debug / minimal clients).
//
// Codec implementations must be safe for concurrent use because a single
// codec instance is shared across all connections that negotiated the same
// encoding.
type Codec interface {
	// Encode marshals a Frame into wire bytes.
	Encode(Frame) ([]byte, error)

	// Decode unmarshals wire bytes into a Frame.
	Decode([]byte) (Frame, error)

	// Subprotocol returns the WebSocket subprotocol token negotiated for
	// this codec (e.g. SubprotocolMsgpack).
	Subprotocol() string
}

// SelectCodec returns the codec corresponding to the negotiated
// Sec-WebSocket-Protocol token, or nil if the token is not supported.
// Implementations are wired by NewMsgpackCodec / NewJSONCodec at startup.
func SelectCodec(subprotocol string) Codec {
	// Wired by realtime root package during initialization to avoid an
	// import cycle with the concrete codec implementations.
	return registeredCodecs[subprotocol]
}

// RegisterCodec is called by codec implementations during init to make
// themselves available to SelectCodec. The protocol package does not import
// codec implementations directly; this keeps the dependency graph clean
// when codecs require third-party libraries (msgpack).
func RegisterCodec(c Codec) {
	if registeredCodecs == nil {
		registeredCodecs = make(map[string]Codec)
	}
	registeredCodecs[c.Subprotocol()] = c
}

var registeredCodecs map[string]Codec
