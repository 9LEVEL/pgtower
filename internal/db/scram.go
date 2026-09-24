package db

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const (
	// SCRAMDefaultIterations is the PBKDF2 iteration count ("rounds") used when
	// none is configured. It sits above PostgreSQL's own default of 4096 (and
	// above NIST SP 800-63B's 10000 minimum) to make the stored verifier
	// costlier to brute-force, while staying cheap enough that the server's
	// per-login verification stays in the low-millisecond range. Override with
	// the PGTOWER_SCRAM_ITERATIONS environment variable.
	SCRAMDefaultIterations = 15000

	scramMinIterations = 4096    // RFC 5802 / PostgreSQL floor — never weaker.
	scramMaxIterations = 1000000 // guardrail against pathological per-login cost.
	scramSaltLen       = 16      // PostgreSQL default (SCRAM_DEFAULT_SALT_LEN, bytes).
)

// SCRAMSHA256Secret computes the SCRAM-SHA-256 verifier for a password in the
// exact textual form PostgreSQL stores in pg_authid.rolpassword:
//
//	SCRAM-SHA-256$<iterations>:<base64 salt>$<base64 StoredKey>:<base64 ServerKey>
//
// Passing this string to `ALTER ROLE ... PASSWORD` makes the server store it
// verbatim (it recognises the format) instead of hashing a plaintext. The
// usable password therefore never travels to — nor can be logged by — the
// server; only the irreversible verifier does.
//
// iterations is clamped to a safe range by ClampSCRAMIterations (pass 0 to use
// SCRAMDefaultIterations). It returns the verifier and the effective iteration
// count actually used, so callers can surface it.
//
// The password is assumed to be ASCII, for which SASLprep (RFC 4013) is the
// identity function. Generated passwords are letters and digits only, so this
// always holds here.
func SCRAMSHA256Secret(password string, iterations int) (secret string, rounds int, err error) {
	rounds = ClampSCRAMIterations(iterations)
	salt := make([]byte, scramSaltLen)
	if _, err = rand.Read(salt); err != nil {
		return "", 0, err
	}
	secret, err = scramSecret(password, salt, rounds)
	if err != nil {
		return "", 0, err
	}
	return secret, rounds, nil
}

// ClampSCRAMIterations maps a requested iteration count to the effective one:
// 0 (unset) -> SCRAMDefaultIterations, below the floor -> scramMinIterations,
// above the ceiling -> scramMaxIterations.
func ClampSCRAMIterations(n int) int {
	switch {
	case n <= 0:
		return SCRAMDefaultIterations
	case n < scramMinIterations:
		return scramMinIterations
	case n > scramMaxIterations:
		return scramMaxIterations
	default:
		return n
	}
}

// PasswordSecret encodes a plaintext for a PASSWORD clause (CREATE/ALTER ROLE).
// When the plaintext is printable ASCII — the range for which SASLprep (RFC
// 4013) is the identity, so a client-computed verifier matches what the server
// expects at login — it returns the SCRAM-SHA-256 verifier and hashed=true,
// keeping the plaintext off the wire and out of the server log. Otherwise
// (non-ASCII, or empty) it returns the plaintext unchanged with hashed=false,
// letting the server SASLprep and hash it correctly. Callers pass the result
// straight to BuildCreateRole/BuildAlterRolePassword; an empty plaintext yields
// "" so the PASSWORD clause is omitted.
func PasswordSecret(plaintext string, iterations int) (secret string, hashed bool, err error) {
	if plaintext == "" || !isASCIIPrintable(plaintext) {
		return plaintext, false, nil
	}
	s, _, err := SCRAMSHA256Secret(plaintext, iterations)
	if err != nil {
		return "", false, err
	}
	return s, true, nil
}

// isASCIIPrintable reports whether s is only printable ASCII (0x20–0x7E), the
// range over which SASLprep is a no-op (NFKC identity, no mapping, nothing
// prohibited) — so hashing client-side stays compatible with server-side login.
func isASCIIPrintable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// scramSecret builds the verifier for an explicit salt and iteration count.
// It is split out from SCRAMSHA256Secret so it can be unit-tested with a fixed
// salt (deterministic output).
func scramSecret(password string, salt []byte, iterations int) (string, error) {
	saltedPassword, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return "", err
	}
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))
	storedKey := sha256.Sum256(clientKey)
	serverKey := hmacSHA256(saltedPassword, []byte("Server Key"))

	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s",
		iterations, b64(salt), b64(storedKey[:]), b64(serverKey)), nil
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}
