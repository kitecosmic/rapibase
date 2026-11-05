package config

import "testing"

func TestKeyKind(t *testing.T) {
	c := &Config{ServiceKey: "svc-123", AnonKey: "anon-456"}
	for key, want := range map[string]string{"svc-123": "service", "anon-456": "anon", "": "", "svc-12": "", "svc-1234": ""} {
		if got := c.KeyKind(key); got != want {
			t.Errorf("KeyKind(%q) = %q, want %q", key, got, want)
		}
	}
	if KeyEquals("", "") {
		t.Error("an empty key must never match")
	}
}
