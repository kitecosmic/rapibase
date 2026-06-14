package auth

import (
	"testing"
	"time"
)

// RFC 6238 test vectors (SHA1). The published vectors are 8-digit; our
// 6-digit codes are those mod 1e6 (i.e. the last 6 digits).
func TestTOTP_RFC6238Vectors(t *testing.T) {
	// base32 of the ASCII secret "12345678901234567890"
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		got, err := totpCode(secret, uint64(c.unix/totpPeriod))
		if err != nil {
			t.Fatalf("t=%d: %v", c.unix, err)
		}
		if got != c.code {
			t.Errorf("t=%d: got %s want %s", c.unix, got, c.code)
		}
	}
}

func TestValidateTOTP_AcceptsCurrentRejectsWrong(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, err := totpCode(secret, uint64(time.Now().Unix()/totpPeriod))
	if err != nil {
		t.Fatal(err)
	}
	if !ValidateTOTP(secret, code) {
		t.Fatal("the current code should validate")
	}
	if ValidateTOTP(secret, "123") {
		t.Fatal("a malformed code must be rejected")
	}
	// A code that is almost certainly wrong for this secret/time.
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if ValidateTOTP(secret, wrong) {
		t.Fatal("an incorrect code must be rejected")
	}
}
