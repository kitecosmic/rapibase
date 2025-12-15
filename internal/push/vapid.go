package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"math/big"
)

// GenerateVAPIDKeys generates a new VAPID key pair
func GenerateVAPIDKeys() (*WebPushConfig, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	publicKeyBytes := elliptic.Marshal(elliptic.P256(), privateKey.PublicKey.X, privateKey.PublicKey.Y)
	privateKeyBytes := privateKey.D.Bytes()

	// Pad private key to 32 bytes if needed
	if len(privateKeyBytes) < 32 {
		padded := make([]byte, 32)
		copy(padded[32-len(privateKeyBytes):], privateKeyBytes)
		privateKeyBytes = padded
	}

	return &WebPushConfig{
		VAPIDPublicKey:  base64.RawURLEncoding.EncodeToString(publicKeyBytes),
		VAPIDPrivateKey: base64.RawURLEncoding.EncodeToString(privateKeyBytes),
		Subject:         "mailto:admin@rapibase.local",
	}, nil
}

// DecodeVAPIDKeys decodes base64url encoded VAPID keys
func DecodeVAPIDKeys(publicKey, privateKey string) (*ecdsa.PrivateKey, error) {
	pubBytes, err := base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil {
		return nil, err
	}

	privBytes, err := base64.RawURLEncoding.DecodeString(privateKey)
	if err != nil {
		return nil, err
	}

	x, y := elliptic.Unmarshal(elliptic.P256(), pubBytes)
	if x == nil {
		return nil, err
	}

	priv := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     x,
			Y:     y,
		},
		D: new(big.Int).SetBytes(privBytes),
	}

	return priv, nil
}
