package db

import (
	"crypto/rand"
	"math/big"
)

// passwordAlphabet is intentionally restricted to letters and digits — the
// characters that are safe to paste into any terminal, psql prompt or
// connection string without quoting or escaping. It excludes quotes, spaces,
// backslashes and every shell metacharacter, so a generated password never
// "breaks" in another terminal session.
const passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// GeneratePassword returns a cryptographically-random password of length n
// built only from terminal-safe characters (see passwordAlphabet). n <= 0
// defaults to 32. It returns an error only if the system CSPRNG fails.
func GeneratePassword(n int) (string, error) {
	if n <= 0 {
		n = 32
	}
	max := big.NewInt(int64(len(passwordAlphabet)))
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = passwordAlphabet[idx.Int64()]
	}
	return string(b), nil
}
