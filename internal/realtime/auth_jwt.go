package realtime

import (
	"errors"

	"github.com/rapibase/rapibase/internal/auth"
)

// Roles recognised by the realtime auth validator.
const (
	RoleAnon          = "anon"
	RoleAuthenticated = "authenticated"
	RoleService       = "service_role"
)

// JWTAuthValidator implements transport.AuthValidator by combining the
// project's api key model (anon vs service) with the JWT manager that
// already powers the REST API.
//
// Wire semantics:
//
//   - apikey == service_key: caller has full project access. A token
//     is optional; if provided and valid, the caller's user id is
//     captured so RPC handlers can identify the actor, but role
//     remains "service_role".
//   - apikey == anon_key + no token: anonymous, role "anon".
//   - apikey == anon_key + valid token: role and user id come from
//     the JWT claims.
//   - any other apikey: unauthorized.
type JWTAuthValidator struct {
	jwt        *auth.JWTManager
	anonKey    string
	serviceKey string
}

// NewJWTAuthValidator constructs a validator. All three arguments are
// required; pass empty values only in tests with a permissive stub.
func NewJWTAuthValidator(jwt *auth.JWTManager, anonKey, serviceKey string) *JWTAuthValidator {
	return &JWTAuthValidator{jwt: jwt, anonKey: anonKey, serviceKey: serviceKey}
}

// Validate implements transport.AuthValidator.
func (v *JWTAuthValidator) Validate(apiKey, token string) (string, string, error) {
	if apiKey == "" {
		return "", "", errors.New("api key required")
	}
	switch apiKey {
	case v.serviceKey:
		if token != "" {
			if claims, err := v.jwt.ValidateToken(token, auth.AudienceApp); err == nil {
				return RoleService, claims.UserID, nil
			}
			// An invalid token presented alongside the service key is
			// still treated as service access — the service key alone
			// already authorises full project access — but we drop
			// the userID so handlers cannot mistake it for an
			// authenticated user.
		}
		return RoleService, "", nil
	case v.anonKey:
		if token == "" {
			return RoleAnon, "", nil
		}
		claims, err := v.jwt.ValidateToken(token, auth.AudienceApp)
		if err != nil {
			return "", "", errors.New("invalid token")
		}
		role := claims.Role
		if role == "" {
			role = RoleAuthenticated
		}
		return role, claims.UserID, nil
	default:
		return "", "", errors.New("invalid api key")
	}
}
