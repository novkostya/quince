package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argonParams are the argon2id cost parameters. Defaults are OWASP-ish for a single-user
// LAN daemon; tests inject cheap params (this type is package-internal).
type argonParams struct {
	memory      uint32 // KiB
	iterations  uint32
	parallelism uint8
	saltLen     uint32
	keyLen      uint32
}

func defaultArgonParams() argonParams {
	return argonParams{memory: 64 * 1024, iterations: 3, parallelism: 2, saltLen: 16, keyLen: 32}
}

// hashPassword returns a PHC-encoded argon2id hash:
// $argon2id$v=19$m=65536,t=3,p=2$<b64 salt>$<b64 key>
func hashPassword(pw string, p argonParams) (string, error) {
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, p.iterations, p.memory, p.parallelism, p.keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.iterations, p.parallelism,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// verifyPassword re-derives the hash with the encoded params and compares in constant time.
func verifyPassword(pw, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, key]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("auth: malformed password hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, err
	}
	if version != argon2.Version {
		return false, fmt.Errorf("auth: unsupported argon2 version %d", version)
	}
	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &p.parallelism); err != nil {
		return false, err
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	// Guard against a corrupt/truncated hash with an empty key: keyLen 0 would make the
	// derived key empty too, and ConstantTimeCompare([], []) == 1 would accept ANY password
	// (fail-open). A well-formed hash always carries a 32-byte key.
	if len(want) == 0 {
		return false, errors.New("auth: password hash has an empty key")
	}
	got := argon2.IDKey([]byte(pw), salt, p.iterations, p.memory, p.parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
