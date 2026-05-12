package transport

import (
	"strings"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// negotiateSubprotocol selects the first supported subprotocol from the
// comma-separated value of Sec-WebSocket-Protocol. Returns the codec to
// use and the canonical subprotocol string to echo back. When no
// supported subprotocol is offered, returns nil.
func negotiateSubprotocol(headerValue string) protocol.Codec {
	if headerValue == "" {
		return nil
	}
	offered := strings.Split(headerValue, ",")
	// Prefer msgpack first.
	for _, want := range []string{protocol.SubprotocolMsgpack, protocol.SubprotocolJSON} {
		for _, o := range offered {
			if strings.EqualFold(strings.TrimSpace(o), want) {
				if c := protocol.SelectCodec(want); c != nil {
					return c
				}
			}
		}
	}
	return nil
}

// AuthValidator validates the (apikey, token) pair presented during the
// WebSocket handshake. Implementations live in the auth package; the
// transport accepts the interface to stay decoupled.
type AuthValidator interface {
	// Validate returns the role and user id derived from the inputs.
	// An empty token is allowed and yields the anonymous role; an
	// invalid token returns ErrUnauthorized.
	Validate(apiKey, token string) (role, userID string, err error)
}

// ErrUnauthorized is returned by AuthValidator implementations for
// invalid credentials. Mapped to protocol.CloseUnauthorized when the
// handshake fails.
var ErrUnauthorized = protocol.NewError(protocol.ErrUnauthorized, "invalid credentials")
