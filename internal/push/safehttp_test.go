package push

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidateWebPushEndpoint(t *testing.T) {
	ok := []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/xyz",
		"https://web.push.apple.com/QGc",
	}
	for _, e := range ok {
		if err := ValidateWebPushEndpoint(e); err != nil {
			t.Errorf("%s: %v", e, err)
		}
	}
	bad := []string{
		"http://fcm.googleapis.com/x",
		"https://127.0.0.1/x",
		"https://10.0.0.5/x",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/x",
		"https://localhost/x",
		"https://rapi-app-storage:8888/buckets/x",
		"ftp://example.com/x",
		"not a url",
	}
	for _, e := range bad {
		if err := ValidateWebPushEndpoint(e); err == nil {
			t.Errorf("%s accepted", e)
		}
	}
}

func TestPublicOnlyClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	if _, err := newPublicOnlyClient(5 * time.Second).Do(req); err == nil {
		t.Fatal("connected to a loopback address")
	}
}
