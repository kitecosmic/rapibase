// Package protocol defines the wire contract of the Rapibase Realtime service.
//
// This package is the Go mirror of docs/realtime/protocol.md. All frame types,
// codec interfaces and error codes used on the WebSocket connection are
// declared here. Both the server and any Go client implementation reference
// these types so the wire stays consistent.
//
// The package depends only on the standard library and must remain
// dependency-free so it can be vendored or extracted to a public module
// without dragging the rest of rapibase along.
package protocol

// Version is the current protocol version string. It is the suffix used in
// the WebSocket subprotocol negotiation header (without the encoding part).
const Version = "rapibase-realtime.v1"

// Subprotocol identifiers exchanged in the Sec-WebSocket-Protocol header.
const (
	SubprotocolMsgpack = "rapibase-realtime.v1+msgpack"
	SubprotocolJSON    = "rapibase-realtime.v1+json"
)
