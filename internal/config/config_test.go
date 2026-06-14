package config

import (
	"context"
	"strings"
	"testing"
)

type fakeStore struct{ m map[string]string }

func (s *fakeStore) GetSecret(_ context.Context, k string) (string, bool, error) {
	v, ok := s.m[k]
	return v, ok, nil
}
func (s *fakeStore) SetSecret(_ context.Context, k, v string) error {
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[k] = v
	return nil
}

// Weak operator-provided secrets must abort startup, and the error must
// list EVERY offending variable at once (not just the first).
func TestResolveSecrets_RejectsWeak_ListsAll(t *testing.T) {
	c := &Config{
		JWTSecret: "change-this-secret-in-production",
		AnonKey:   "rapibase-anon-key-2024-public-access",
		// ServiceKey blank — would generate, but the two above must fail first.
	}
	err := c.ResolveSecrets(context.Background(), &fakeStore{})
	if err == nil {
		t.Fatal("expected startup to be refused for weak secrets")
	}
	msg := err.Error()
	if !strings.Contains(msg, "JWT_SECRET") || !strings.Contains(msg, "ANON_KEY") {
		t.Fatalf("error must list both weak vars, got: %s", msg)
	}
	// Print the exact message an operator would see in the logs.
	t.Logf("\n--- exact boot error shown in `docker compose logs` ---\nSecret configuration error: %s\n", msg)
}

// Blank secrets are generated automatically (zero-config first boot).
func TestResolveSecrets_BlankGenerates(t *testing.T) {
	c := &Config{}
	if err := c.ResolveSecrets(context.Background(), &fakeStore{}); err != nil {
		t.Fatalf("blank secrets should auto-generate, got: %v", err)
	}
	if len(c.JWTSecret) < 32 || c.AnonKey == "" || c.ServiceKey == "" {
		t.Fatalf("expected generated secrets, got jwt(len)=%d anon=%q service=%q", len(c.JWTSecret), c.AnonKey, c.ServiceKey)
	}
}
