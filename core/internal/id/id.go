// Package id centralizes identifier generation. Wire objects (jobs, versions, ops, audit
// rows) use ULIDs — sortable-by-time, and the "01J…" form contracts.md specifies. Session
// and CSRF values use full 256-bit random tokens instead: a session identifier must be
// unguessable, and a ULID exposes its timestamp and carries only 80 random bits.
package id

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	mu      sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0) // not concurrency-safe → guarded by mu
)

// New returns a fresh ULID string (e.g. "01J...") for a wire object.
func New() string {
	mu.Lock()
	defer mu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}

// Token returns a URL-safe base64 string of nbytes of cryptographic randomness — for
// session cookie values and CSRF tokens.
func Token(nbytes int) string {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never returns an error on supported platforms; panic is correct
		// here because a broken CSPRNG must not silently yield a weak token.
		panic("id: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
