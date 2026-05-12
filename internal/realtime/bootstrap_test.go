package realtime

import (
	"strings"
	"testing"
)

func TestValidateIdentifier(t *testing.T) {
	cases := []struct {
		name    string
		wantErr string // substring of expected error, or "" for success
	}{
		{"", "empty"},
		{"rapibase", ""},
		{"rapibase_realtime", ""},
		{"_underscore_start", ""},
		{"r1", ""},
		{"1starts_with_digit", "starts with digit"},
		{"has space", "invalid character"},
		{"has-dash", "invalid character"},
		{"has\"quote", "invalid character"},
		{"has;semi", "invalid character"},
		{strings.Repeat("x", 64), "exceeds 63"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateIdentifier(c.name)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("expected error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestQuoteIdentifier(t *testing.T) {
	q, err := quoteIdentifier("rapibase_realtime")
	if err != nil {
		t.Fatal(err)
	}
	if q != `"rapibase_realtime"` {
		t.Fatalf("quoted: %q", q)
	}
	if _, err := quoteIdentifier("bad name"); err == nil {
		t.Fatal("expected error for invalid identifier")
	}
}
