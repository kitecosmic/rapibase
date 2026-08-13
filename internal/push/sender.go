package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Sender handles sending push notifications
type Sender struct {
	client *http.Client
	store  PushStore
}

// PushStore interface for database operations
type PushStore interface {
	GetPushConfig(ctx context.Context, platform string) (*PushConfig, error)
	GetSubscriptionsByUserID(ctx context.Context, userID string) ([]PushSubscription, error)
	GetSubscriptionsByUserIDs(ctx context.Context, userIDs []string) ([]PushSubscription, error)
	GetSubscriptionsByFilter(ctx context.Context, filter *UserFilter) ([]PushSubscription, error)
	GetAllSubscriptions(ctx context.Context, platform string) ([]PushSubscription, error)
	DeleteSubscription(ctx context.Context, id int64) error
}

// NewSender creates a new push notification sender
func NewSender(store PushStore) *Sender {
	return &Sender{
		client: &http.Client{Timeout: 30 * time.Second},
		store:  store,
	}
}

// SendToUser sends a notification to all devices of a user
func (s *Sender) SendToUser(ctx context.Context, userID string, msg PushMessage) error {
	subs, err := s.store.GetSubscriptionsByUserID(ctx, userID)
	if err != nil {
		return err
	}

	var lastErr error
	for _, sub := range subs {
		if err := s.sendToSubscription(ctx, sub, msg); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// Broadcast sends a notification to all subscribed devices
func (s *Sender) Broadcast(ctx context.Context, msg PushMessage) error {
	platforms := []string{PlatformWeb, PlatformIOS, PlatformAndroid}

	var lastErr error
	for _, platform := range platforms {
		subs, err := s.store.GetAllSubscriptions(ctx, platform)
		if err != nil {
			continue
		}

		for _, sub := range subs {
			if err := s.sendToSubscription(ctx, sub, msg); err != nil {
				lastErr = err
			}
		}
	}

	return lastErr
}

// SendToUsers sends a notification to multiple users
func (s *Sender) SendToUsers(ctx context.Context, userIDs []string, msg PushMessage) error {
	subs, err := s.store.GetSubscriptionsByUserIDs(ctx, userIDs)
	if err != nil {
		return err
	}

	var lastErr error
	for _, sub := range subs {
		if err := s.sendToSubscription(ctx, sub, msg); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// SendToFilter sends a notification to users matching filter conditions
func (s *Sender) SendToFilter(ctx context.Context, filter *UserFilter, msg PushMessage) error {
	subs, err := s.store.GetSubscriptionsByFilter(ctx, filter)
	if err != nil {
		return err
	}

	var lastErr error
	for _, sub := range subs {
		if err := s.sendToSubscription(ctx, sub, msg); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// sendToSubscription sends to a single subscription
func (s *Sender) sendToSubscription(ctx context.Context, sub PushSubscription, msg PushMessage) error {
	switch sub.Platform {
	case PlatformWeb:
		return s.sendWebPush(ctx, sub, msg)
	case PlatformIOS:
		return s.sendAPNS(ctx, sub, msg)
	case PlatformAndroid:
		return s.sendFCM(ctx, sub, msg)
	default:
		return fmt.Errorf("unknown platform: %s", sub.Platform)
	}
}

// sendWebPush sends a Web Push notification
func (s *Sender) sendWebPush(ctx context.Context, sub PushSubscription, msg PushMessage) error {
	config, err := s.store.GetPushConfig(ctx, PlatformWeb)
	if err != nil || !config.Enabled {
		return fmt.Errorf("web push not configured")
	}

	var webConfig WebPushConfig
	configBytes, _ := json.Marshal(config.Config)
	json.Unmarshal(configBytes, &webConfig)

	// Parse subscription
	var webSub WebPushSubscription
	webSub.Endpoint = sub.Endpoint
	if sub.Metadata != nil {
		if keys, ok := sub.Metadata["keys"].(map[string]interface{}); ok {
			if p256dh, ok := keys["p256dh"].(string); ok {
				webSub.Keys.P256dh = p256dh
			}
			if auth, ok := keys["auth"].(string); ok {
				webSub.Keys.Auth = auth
			}
		}
	}

	// Encrypt payload
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	encrypted, err := s.encryptWebPush(payload, webSub, webConfig)
	if err != nil {
		return err
	}

	// Create VAPID JWT
	vapidJWT, err := s.createVAPIDJWT(webSub.Endpoint, webConfig)
	if err != nil {
		return err
	}

	// Send request
	req, err := http.NewRequestWithContext(ctx, "POST", webSub.Endpoint, bytes.NewReader(encrypted))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Authorization", fmt.Sprintf("vapid t=%s, k=%s", vapidJWT, webConfig.VAPIDPublicKey))

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 410 || resp.StatusCode == 404 {
		// Subscription expired, delete it
		s.store.DeleteSubscription(ctx, sub.ID)
		return fmt.Errorf("subscription expired")
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("web push failed: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

// encryptWebPush encrypts the payload for Web Push
func (s *Sender) encryptWebPush(payload []byte, sub WebPushSubscription, config WebPushConfig) ([]byte, error) {
	// Decode subscriber's public key
	p256dhBytes, err := base64.RawURLEncoding.DecodeString(sub.Keys.P256dh)
	if err != nil {
		return nil, err
	}

	// Decode auth secret
	authSecret, err := base64.RawURLEncoding.DecodeString(sub.Keys.Auth)
	if err != nil {
		return nil, err
	}

	// Generate ephemeral key pair
	curve := ecdh.P256()
	localPrivate, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	localPublic := localPrivate.PublicKey()

	// Import subscriber's public key
	subscriberPublic, err := curve.NewPublicKey(p256dhBytes)
	if err != nil {
		return nil, err
	}

	// ECDH shared secret
	sharedSecret, err := localPrivate.ECDH(subscriberPublic)
	if err != nil {
		return nil, err
	}

	// Generate salt
	salt := make([]byte, 16)
	rand.Read(salt)

	// Derive keys using HKDF
	ikm := s.hkdfExtract(authSecret, sharedSecret)

	keyInfo := append([]byte("Content-Encoding: aes128gcm\x00"), p256dhBytes...)
	keyInfo = append(keyInfo, localPublic.Bytes()...)

	prk := s.hkdfExtract(salt, ikm)
	key := s.hkdfExpand(prk, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := s.hkdfExpand(prk, []byte("Content-Encoding: nonce\x00"), 12)

	// Encrypt with AES-GCM
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Add padding delimiter
	paddedPayload := append(payload, 0x02)

	ciphertext := gcm.Seal(nil, nonce, paddedPayload, nil)

	// Build aes128gcm header
	recordSize := uint32(len(ciphertext) + 16 + 1 + 65 + 5)
	header := make([]byte, 0, 86)
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(65)) // key length
	header = append(header, localPublic.Bytes()...)

	return append(header, ciphertext...), nil
}

// hkdfExtract performs HKDF-Extract
func (s *Sender) hkdfExtract(salt, ikm []byte) []byte {
	h := sha256.New
	if len(salt) == 0 {
		salt = make([]byte, h().Size())
	}
	mac := hmacSHA256(salt, ikm)
	return mac
}

// hkdfExpand performs HKDF-Expand
func (s *Sender) hkdfExpand(prk, info []byte, length int) []byte {
	h := sha256.New
	hashLen := h().Size()
	n := (length + hashLen - 1) / hashLen

	okm := make([]byte, 0, n*hashLen)
	prev := []byte{}

	for i := 1; i <= n; i++ {
		data := append(prev, info...)
		data = append(data, byte(i))
		prev = hmacSHA256(prk, data)
		okm = append(okm, prev...)
	}

	return okm[:length]
}

// createVAPIDJWT creates a VAPID JWT token
func (s *Sender) createVAPIDJWT(endpoint string, config WebPushConfig) (string, error) {
	privKey, err := DecodeVAPIDKeys(config.VAPIDPublicKey, config.VAPIDPrivateKey)
	if err != nil {
		return "", err
	}

	// Extract origin from endpoint
	origin := endpoint
	if len(endpoint) > 8 {
		for i := 8; i < len(endpoint); i++ {
			if endpoint[i] == '/' {
				origin = endpoint[:i]
				break
			}
		}
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"aud": origin,
		"exp": time.Now().Add(12 * time.Hour).Unix(),
		"sub": config.Subject,
	})

	return token.SignedString(privKey)
}

// sendAPNS y sendFCM viven en native.go (envío nativo real con caché de
// tokens de autenticación).

// hmacSHA256 helper
func hmacSHA256(key, data []byte) []byte {
	h := sha256.New()
	h.Write(key)
	keyHash := h.Sum(nil)

	ipad := make([]byte, 64)
	opad := make([]byte, 64)

	for i := 0; i < 64; i++ {
		if i < len(keyHash) {
			ipad[i] = keyHash[i] ^ 0x36
			opad[i] = keyHash[i] ^ 0x5c
		} else {
			ipad[i] = 0x36
			opad[i] = 0x5c
		}
	}

	inner := sha256.New()
	inner.Write(ipad)
	inner.Write(data)
	innerHash := inner.Sum(nil)

	outer := sha256.New()
	outer.Write(opad)
	outer.Write(innerHash)

	return outer.Sum(nil)
}
