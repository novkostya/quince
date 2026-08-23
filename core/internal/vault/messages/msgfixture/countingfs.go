package msgfixture

import (
	"sync"

	backup "github.com/novkostya/ios-backup-parser"
)

// CountingFS wraps a backup.FS and records every key it is asked to materialize.
//
// IT EXISTS TO CHECK A SAFETY PROPERTY THAT IS OTHERWISE ONLY DOCUMENTED. qn.10 D2b holds the
// vault only long enough to materialize the Messages database, and runs the ~16 s projection
// scan OUTSIDE the session lock. That is safe because parserfs memoises: the scan's own
// Materialize hits the memo and never reaches the vault.
//
// THE MEMO ONLY HELPS FOR THE KEY THAT WAS PRE-MATERIALIZED. A call for any OTHER
// (domain, relativePath) misses, reaches the vault outside registry.With, and races whatever
// else is using that session — and a race NEED NOT PRODUCE AN ERROR, so a test that merely
// runs the build and sees no failure proves nothing. What has to be asserted is the key set.
//
// Today `messages` makes one such call and the parser makes one more, both for the same file.
// A later slice that materializes something else during the scan — an attachment, a sidecar
// under a different key — silently re-opens the race. This is what notices (architect,
// quince#1498).
type CountingFS struct {
	Inner backup.FS

	mu   sync.Mutex
	keys []Key
}

// Key is one materialize request.
type Key struct {
	Domain       string
	RelativePath string
}

// NewCountingFS wraps fsys.
func NewCountingFS(fsys backup.FS) *CountingFS { return &CountingFS{Inner: fsys} }

func (c *CountingFS) Materialize(domain, relativePath string) (string, error) {
	c.mu.Lock()
	c.keys = append(c.keys, Key{Domain: domain, RelativePath: relativePath})
	c.mu.Unlock()
	return c.Inner.Materialize(domain, relativePath)
}

func (c *CountingFS) Exists(domain, relativePath string) (bool, error) {
	return c.Inner.Exists(domain, relativePath)
}

// ReadDir delegates when the inner FS implements backup.ReadDirFS, so wrapping does not
// silently remove a capability a domain type-asserts for (`reminders` does). A wrapper that
// changed what the host can do would make the test environment differ from production in a
// way nothing announces.
func (c *CountingFS) ReadDir(domain, relativeDir string) ([]string, error) {
	rd, ok := c.Inner.(backup.ReadDirFS)
	if !ok {
		return nil, backup.ErrUnavailable
	}
	return rd.ReadDir(domain, relativeDir)
}

// Keys returns every materialize request in order.
func (c *CountingFS) Keys() []Key {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Key(nil), c.keys...)
}

// Reset forgets what has been recorded, so a caller can measure one phase in isolation —
// which is the whole point here: what matters is what the SCAN asks for, after the
// pre-materialize under the lock has already happened.
func (c *CountingFS) Reset() {
	c.mu.Lock()
	c.keys = nil
	c.mu.Unlock()
}

// OffKey returns every recorded key that is not want. Empty means the scan asked for nothing
// but the file that was already materialized, which is the property D2b's safety rests on.
func (c *CountingFS) OffKey(want Key) []Key {
	var out []Key
	for _, k := range c.Keys() {
		if k != want {
			out = append(out, k)
		}
	}
	return out
}
