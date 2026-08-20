package conformance

import (
	"context"
	"io"

	"github.com/novkostya/quince/core/internal/vault"
)

// Mutation names one deliberate defect to inject into a correct Vault.
type Mutation int

const (
	// MutateDropLastEntry makes a full walk return one entry short — the shape a broken
	// cursor produces, and the one a suite that only counted pages would miss.
	MutateDropLastEntry Mutation = iota
	// MutateDirectoryIsNotFound collapses not_a_file into not_found: an entry that exists
	// and has no content answers as though it did not exist. Every message stays true.
	MutateDirectoryIsNotFound
	// MutateSilentClamp applies the limit clamp without disclosing it.
	MutateSilentClamp
	// MutateStaleAfterClose keeps serving reads after Close, so a locked vault is not one.
	MutateStaleAfterClose
)

func (m Mutation) String() string {
	switch m {
	case MutateDropLastEntry:
		return "a full walk drops its last entry"
	case MutateDirectoryIsNotFound:
		return "a directory answers not_found instead of not_a_file"
	case MutateSilentClamp:
		return "the limit clamp is applied but not disclosed"
	case MutateStaleAfterClose:
		return "reads still work after Close"
	default:
		return "unknown"
	}
}

// AllMutations is what RunMutantMustFail sweeps.
var AllMutations = []Mutation{
	MutateDropLastEntry,
	MutateDirectoryIsNotFound,
	MutateSilentClamp,
	MutateStaleAfterClose,
}

// RunMutantMustFail is the suite's own control. It wraps a correct Vault in each deliberate
// defect and asserts the suite REJECTS it.
//
// AN ALL-PASS FROM A SUITE NOBODY HAS SEEN FAIL PROVES THAT THE SUITE RAN, NOT THAT THE
// IMPLEMENTATION IS RIGHT. Every mutation here is a defect whose every individual message
// stays true — a walk one entry short, a directory reported as missing, a clamp that
// silently holds — which is the class a count-the-assertions suite waves through.
//
// It reports through `recorder` rather than the caller's T, so a suite failure is an
// observation instead of a test failure. That is what the T interface in reporter.go buys.
func RunMutantMustFail(t T, f Fixture) {
	t.Helper()
	for _, m := range AllMutations {
		mutated := f
		mutated.Open = func(inner T) vault.Vault { return mutate(f.Open(inner), m) }

		r := &recorder{}
		Run(r, mutated)
		if !r.failed() {
			t.Errorf("the conformance suite PASSED an implementation where %s — it is not an instrument", m)
		}
	}
}

// RunMutantDetail reports which checks caught which mutation. Not part of the gate: it is
// for a reader asking what the suite is actually sensitive to, which is a different and
// useful question from whether it passes.
func RunMutantDetail(f Fixture) map[Mutation][]string {
	out := make(map[Mutation][]string, len(AllMutations))
	for _, m := range AllMutations {
		mutated := f
		mutated.Open = func(inner T) vault.Vault { return mutate(f.Open(inner), m) }
		r := &recorder{}
		Run(r, mutated)
		out[m] = r.failures
	}
	return out
}

func mutate(v vault.Vault, m Mutation) vault.Vault { return &mutant{Vault: v, how: m} }

type mutant struct {
	vault.Vault
	how    Mutation
	closed bool
}

func (m *mutant) List(ctx context.Context, q vault.Query) (vault.Page, error) {
	if m.how == MutateStaleAfterClose && m.closed {
		// The defect: a closed vault keeps answering. Delegate to the real one, which is
		// itself closed — so instead fabricate a plausible empty page.
		return vault.Page{}, nil
	}
	page, err := m.Vault.List(ctx, q)
	if err != nil {
		return page, err
	}
	switch m.how {
	case MutateDropLastEntry:
		if page.NextCursor == "" && len(page.Entries) > 0 {
			page.Entries = page.Entries[:len(page.Entries)-1]
		}
	case MutateSilentClamp:
		page.EffectiveLimit = 0
	}
	return page, nil
}

func (m *mutant) Open(ctx context.Context, fileID string) (io.ReadCloser, error) {
	rc, err := m.Vault.Open(ctx, fileID)
	if m.how == MutateDirectoryIsNotFound && err != nil {
		if code := vault.Code(err); code == "not_a_file" {
			return nil, vault.ErrFileNotFound
		}
	}
	return rc, err
}

func (m *mutant) Close() error {
	m.closed = true
	return m.Vault.Close()
}
