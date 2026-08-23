package msgfixture

import (
	"context"
	"io"
	"sync"

	"github.com/novkostya/quince/core/internal/vault"
)

// CountingVault wraps a vault.Vault and counts the calls that actually REACH it.
//
// IT SITS ONE LAYER BELOW CountingFS, AND THAT IS THE WHOLE POINT. CountingFS records what the
// scan ASKS the filesystem for; this records what the filesystem could not answer from memory
// and had to go to the vault for. Those are different questions, and quince#1483 is what
// happens when the first is mistaken for the second: `Materialize` was memoised so its calls
// never reached the vault, `Exists` was not, and a guard that counted only `Materialize` read
// as though it proved the scan touched nothing.
//
// qn.10 D2b runs the projection scan OUTSIDE the session lock. `vault.Vault` makes no
// concurrency promise — the registry serialises access — so the property that has to hold is
// "the scan reaches the vault ZERO times", and only a counter at this seam can say so.
type CountingVault struct {
	Inner vault.Vault

	mu    sync.Mutex
	calls []string
}

// NewCountingVault wraps v.
func NewCountingVault(v vault.Vault) *CountingVault { return &CountingVault{Inner: v} }

func (c *CountingVault) record(method string) {
	c.mu.Lock()
	c.calls = append(c.calls, method)
	c.mu.Unlock()
}

// Calls returns every method that reached the vault, in order.
func (c *CountingVault) Calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

// Reset forgets what has been recorded, so a caller can measure one phase alone.
func (c *CountingVault) Reset() {
	c.mu.Lock()
	c.calls = nil
	c.mu.Unlock()
}

func (c *CountingVault) Unlock(ctx context.Context, password string) (vault.Info, error) {
	c.record("Unlock")
	return c.Inner.Unlock(ctx, password)
}

func (c *CountingVault) List(ctx context.Context, q vault.Query) (vault.Page, error) {
	c.record("List")
	return c.Inner.List(ctx, q)
}

func (c *CountingVault) Stat(ctx context.Context, fileID string) (vault.FileEntry, error) {
	c.record("Stat")
	return c.Inner.Stat(ctx, fileID)
}

func (c *CountingVault) Open(ctx context.Context, fileID string) (io.ReadCloser, error) {
	c.record("Open")
	return c.Inner.Open(ctx, fileID)
}

func (c *CountingVault) VerifyCanary(ctx context.Context) error {
	c.record("VerifyCanary")
	return c.Inner.VerifyCanary(ctx)
}

func (c *CountingVault) Aggregate(ctx context.Context) (vault.Totals, error) {
	c.record("Aggregate")
	return c.Inner.Aggregate(ctx)
}

func (c *CountingVault) Close() error { return c.Inner.Close() }
