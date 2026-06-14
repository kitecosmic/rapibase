package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Token audiences keep app-user tokens and dashboard-admin tokens in
// separate trust domains. Both are signed with the same secret, but a
// token minted for one audience is rejected when presented to the
// other. This is what stops a self-service app signup (AudienceApp,
// role "user") from ever being accepted by the admin dashboard
// (AudienceDashboard, role "admin").
const (
	AudienceApp       = "rapibase:app"
	AudienceDashboard = "rapibase:dashboard"
)

type Claims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

type JWTManager struct {
	secret    []byte
	jwtExpiry time.Duration
}

func NewJWTManager(secret string) *JWTManager {
	return &JWTManager{
		secret:    []byte(secret),
		jwtExpiry: time.Hour, // default 1 hour
	}
}

func NewJWTManagerWithExpiry(secret string, jwtExpiry time.Duration) *JWTManager {
	return &JWTManager{
		secret:    []byte(secret),
		jwtExpiry: jwtExpiry,
	}
}

// GenerateToken mints a signed JWT scoped to the given audience
// (AudienceApp for third-party app users, AudienceDashboard for the
// admin panel). The audience is enforced on the validation side so the
// two token families cannot be used interchangeably.
func (m *JWTManager) GenerateToken(userID string, email, role, audience string) (string, error) {
	claims := &Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(m.jwtExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "rapibase",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// GetExpiry returns the JWT expiry duration
func (m *JWTManager) GetExpiry() time.Duration {
	return m.jwtExpiry
}

// ValidateToken verifies the signature and expiry of a JWT and returns
// its claims. When expectedAudience is non-empty, the token's "aud"
// claim must contain it or validation fails — this is what keeps app
// tokens out of the dashboard and dashboard tokens out of the app API.
// Pass "" only where the audience genuinely does not matter.
func (m *JWTManager) ValidateToken(tokenString, expectedAudience string) (*Claims, error) {
	opts := []jwt.ParserOption{}
	if expectedAudience != "" {
		opts = append(opts, jwt.WithAudience(expectedAudience))
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secret, nil
	}, opts...)

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}
