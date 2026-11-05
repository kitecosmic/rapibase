// Envío nativo a APNs (iOS) y FCM v1 (Android): implementaciones estándar
// sobre la stdlib + golang-jwt, con caché de tokens de autenticación.
//
//   - APNs: JWT ES256 firmado con la Auth Key (.p8) del Apple Developer
//     account, HTTP/2 contra api.push.apple.com (Go negocia h2 solo).
//   - FCM: OAuth2 de service account (JWT RS256 → token endpoint de Google)
//     contra la API HTTP v1 de Firebase Cloud Messaging.
//
// Los errores de credenciales salen con el detalle del proveedor para que
// configurar sea depurable; los tokens de dispositivo dados de baja
// (Unregistered/UNREGISTERED) eliminan la suscripción, igual que web push.
package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// normalizePEM: al pegar la clave desde un JSON de service account (o un
// .p8 copiado), los saltos de línea llegan como "\n" literales.
func normalizePEM(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), `\n`, "\n")
}

// --- caché de tokens (APNs JWT / FCM access token) -------------------------

type cachedToken struct {
	token   string
	expires time.Time
}

var (
	tokenMu    sync.Mutex
	tokenCache = map[string]cachedToken{}
)

func getCachedToken(key string) (string, bool) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	t, ok := tokenCache[key]
	if !ok || time.Now().After(t.expires) {
		return "", false
	}
	return t.token, true
}

func setCachedToken(key, token string, ttl time.Duration) {
	tokenMu.Lock()
	tokenCache[key] = cachedToken{token: token, expires: time.Now().Add(ttl)}
	tokenMu.Unlock()
}

// --- APNs -------------------------------------------------------------------

// sendAPNS sends an Apple Push Notification via the provider API.
func (s *Sender) sendAPNS(ctx context.Context, sub PushSubscription, msg PushMessage) error {
	config, err := s.store.GetPushConfig(ctx, PlatformIOS)
	if err != nil || !config.Enabled {
		return fmt.Errorf("APNs not configured")
	}
	var c APNSConfig
	b, _ := json.Marshal(config.Config)
	_ = json.Unmarshal(b, &c)
	if c.KeyID == "" || c.TeamID == "" || c.BundleID == "" || c.PrivateKey == "" {
		return fmt.Errorf("APNs config incomplete (key_id, team_id, bundle_id and private_key are required)")
	}

	token, err := s.apnsJWT(c)
	if err != nil {
		return fmt.Errorf("APNs auth: %w", err)
	}

	host := "https://api.sandbox.push.apple.com"
	if c.Production {
		host = "https://api.push.apple.com"
	}

	aps := map[string]interface{}{
		"alert": map[string]string{"title": msg.Title, "body": msg.Body},
		"sound": "default",
	}
	if o := msg.IOS; o != nil {
		if o.Sound != "" {
			aps["sound"] = o.Sound
		}
		if o.Badge != nil {
			aps["badge"] = *o.Badge
		}
		if o.ThreadID != "" {
			aps["thread-id"] = o.ThreadID
		}
		if o.Category != "" {
			aps["category"] = o.Category
		}
	}
	payload := map[string]interface{}{"aps": aps}
	for k, v := range msg.Data {
		if k != "aps" {
			payload[k] = v
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", host+"/3/device/"+sub.Token, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+token)
	req.Header.Set("apns-topic", c.BundleID)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("content-type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	// 410 = token dado de baja; 400 BadDeviceToken también es terminal
	if resp.StatusCode == 410 || strings.Contains(string(respBody), "BadDeviceToken") {
		_ = s.store.DeleteSubscription(ctx, sub.ID)
		return errUnregistered{"APNs device token unregistered: " + strings.TrimSpace(string(respBody))}
	}
	return fmt.Errorf("APNs failed: %d - %s", resp.StatusCode, string(respBody))
}

// apnsJWT devuelve el JWT ES256 del proveedor, cacheado ~45 min (Apple
// exige entre 20 y 60).
func (s *Sender) apnsJWT(c APNSConfig) (string, error) {
	cacheKey := "apns:" + c.TeamID + ":" + c.KeyID
	if t, ok := getCachedToken(cacheKey); ok {
		return t, nil
	}

	block, _ := pem.Decode([]byte(normalizePEM(c.PrivateKey)))
	if block == nil {
		return "", fmt.Errorf("private_key is not valid PEM (paste the full .p8 contents)")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parsing .p8 key: %w", err)
	}
	ecKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf(".p8 key is not an ECDSA key")
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": c.TeamID,
		"iat": time.Now().Unix(),
	})
	tok.Header["kid"] = c.KeyID
	signed, err := tok.SignedString(ecKey)
	if err != nil {
		return "", err
	}
	setCachedToken(cacheKey, signed, 45*time.Minute)
	return signed, nil
}

// --- FCM (HTTP v1) ----------------------------------------------------------

// sendFCM sends a Firebase Cloud Messaging notification via the v1 API.
func (s *Sender) sendFCM(ctx context.Context, sub PushSubscription, msg PushMessage) error {
	config, err := s.store.GetPushConfig(ctx, PlatformAndroid)
	if err != nil || !config.Enabled {
		return fmt.Errorf("FCM not configured")
	}
	var c FCMConfig
	b, _ := json.Marshal(config.Config)
	_ = json.Unmarshal(b, &c)
	if c.ProjectID == "" || c.ClientEmail == "" || c.PrivateKey == "" {
		return fmt.Errorf("FCM config incomplete (project_id, client_email and private_key are required)")
	}

	accessToken, err := s.fcmAccessToken(ctx, c)
	if err != nil {
		return fmt.Errorf("FCM auth: %w", err)
	}

	body, err := json.Marshal(map[string]interface{}{"message": fcmMessage(sub.Token, msg)})
	if err != nil {
		return err
	}

	endpoint := "https://fcm.googleapis.com/v1/projects/" + c.ProjectID + "/messages:send"
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == 404 || strings.Contains(string(respBody), "UNREGISTERED") {
		_ = s.store.DeleteSubscription(ctx, sub.ID)
		return errUnregistered{"FCM device token unregistered: " + strings.TrimSpace(string(respBody))}
	}
	return fmt.Errorf("FCM failed: %d - %s", resp.StatusCode, string(respBody))
}

// fcmData convierte data al map[string]string que exige FCM v1: los
// strings van tal cual y el resto (números, bool, objetos) como JSON.
func fcmData(in map[string]interface{}) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
			out[k] = ""
		default:
			b, err := json.Marshal(t)
			if err != nil {
				out[k] = fmt.Sprint(t)
			} else {
				out[k] = string(b)
			}
		}
	}
	return out
}

// fcmMessage arma message de la API HTTP v1. token acepta el registration
// token clásico o el Firebase Installation ID: FCM usa el mismo campo.
func fcmMessage(token string, msg PushMessage) map[string]interface{} {
	notification := map[string]string{"title": msg.Title, "body": msg.Body}
	m := map[string]interface{}{"token": token, "notification": notification}
	if len(msg.Data) > 0 {
		m["data"] = fcmData(msg.Data)
	}
	if o := msg.Android; o != nil {
		if o.Image != "" {
			notification["image"] = o.Image
		}
		android := map[string]interface{}{}
		switch strings.ToLower(o.Priority) {
		case "high":
			android["priority"] = "HIGH"
		case "normal":
			android["priority"] = "NORMAL"
		}
		if o.TTLSeconds > 0 {
			android["ttl"] = fmt.Sprintf("%ds", o.TTLSeconds)
		}
		an := map[string]string{}
		for k, v := range map[string]string{
			"channel_id": o.ChannelID, "icon": o.Icon, "color": o.Color,
			"sound": o.Sound, "tag": o.Tag, "click_action": o.ClickAction,
		} {
			if v != "" {
				an[k] = v
			}
		}
		if len(an) > 0 {
			android["notification"] = an
		}
		if len(android) > 0 {
			m["android"] = android
		}
	}
	return m
}

// fcmAccessToken intercambia un JWT RS256 del service account por un
// access token de Google, cacheado hasta ~5 min antes de su expiración.
func (s *Sender) fcmAccessToken(ctx context.Context, c FCMConfig) (string, error) {
	cacheKey := "fcm:" + c.ClientEmail
	if t, ok := getCachedToken(cacheKey); ok {
		return t, nil
	}

	rsaKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(normalizePEM(c.PrivateKey)))
	if err != nil {
		return "", fmt.Errorf("parsing service account private_key: %w", err)
	}

	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   c.ClientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if c.PrivateKeyID != "" {
		tok.Header["kid"] = c.PrivateKeyID
	}
	assertion, err := tok.SignedString(rsaKey)
	if err != nil {
		return "", err
	}

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google token endpoint: %d - %s", resp.StatusCode, string(respBody))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("google token endpoint: unexpected response")
	}
	ttl := time.Duration(out.ExpiresIn)*time.Second - 5*time.Minute
	if ttl < time.Minute {
		ttl = time.Minute
	}
	setCachedToken(cacheKey, out.AccessToken, ttl)
	return out.AccessToken, nil
}
