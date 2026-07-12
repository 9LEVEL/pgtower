package db

import "testing"

// TestScramSecretVector pins scramSecret against an independent reference vector
// computed with Python's hashlib/hmac (pbkdf2_hmac + HMAC-SHA-256), the same
// algorithm PostgreSQL's scram_build_secret uses. A wrong result here means a
// stored verifier the server would reject.
func TestScramSecretVector(t *testing.T) {
	const want = "SCRAM-SHA-256$4096:MDEyMzQ1Njc4OWFiY2RlZg==$" +
		"nQpbZ77WudtqufPwikHXGRt6g2QJ4zns8bZLw273DRM=:" +
		"jn2amWP1q1h+jgjy0YTO14S6/F02SV7taipOeB7ef20="

	got, err := scramSecret("pencil", []byte("0123456789abcdef"), 4096)
	if err != nil {
		t.Fatalf("scramSecret: %v", err)
	}
	if got != want {
		t.Errorf("scramSecret mismatch:\n got %q\nwant %q", got, want)
	}
}
