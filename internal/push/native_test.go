package push

import (
	"encoding/json"
	"testing"
)

func TestFCMData(t *testing.T) {
	got := fcmData(map[string]interface{}{
		"s": "texto", "n": 3.5, "b": true, "o": map[string]interface{}{"id": 7}, "z": nil,
	})
	want := map[string]string{"s": "texto", "n": "3.5", "b": "true", "o": `{"id":7}`, "z": ""}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("data[%s] = %q, want %q", k, got[k], v)
		}
	}
}

func TestFCMMessageAndroidOptions(t *testing.T) {
	m := fcmMessage("tok", PushMessage{
		Title: "T", Body: "B",
		Android: &AndroidOptions{ChannelID: "pedidos", Priority: "high", Icon: "ic_stat", Sound: "default", TTLSeconds: 60, Image: "https://x/y.png"},
	})
	b, _ := json.Marshal(m)
	var out struct {
		Token        string            `json:"token"`
		Notification map[string]string `json:"notification"`
		Android      struct {
			Priority     string            `json:"priority"`
			TTL          string            `json:"ttl"`
			Notification map[string]string `json:"notification"`
		} `json:"android"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Token != "tok" || out.Notification["title"] != "T" || out.Notification["image"] != "https://x/y.png" {
		t.Errorf("message: %s", b)
	}
	if out.Android.Priority != "HIGH" || out.Android.TTL != "60s" {
		t.Errorf("android: %s", b)
	}
	if out.Android.Notification["channel_id"] != "pedidos" || out.Android.Notification["icon"] != "ic_stat" || out.Android.Notification["sound"] != "default" {
		t.Errorf("android.notification: %s", b)
	}

	plain, _ := json.Marshal(fcmMessage("tok", PushMessage{Title: "T"}))
	var p map[string]interface{}
	_ = json.Unmarshal(plain, &p)
	if _, ok := p["android"]; ok {
		t.Errorf("no android options should mean no android block: %s", plain)
	}
}

func TestIsAPNsToken(t *testing.T) {
	apns := "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	for tok, want := range map[string]bool{
		apns:                     true,
		apns + "ab":              true,  // longer hex tokens exist
		apns[:63]:                false, // odd / too short
		"cX9a:APA91bH-fcm_token": false, // FCM registration token
		"eKz1_3Yt4Qx2Z8bW7vLmNa": false, // Firebase Installation ID
		"":                       false,
	} {
		if got := IsAPNsToken(tok); got != want {
			t.Errorf("IsAPNsToken(%q) = %v, want %v", tok, got, want)
		}
	}
}
