// Package vault reads one committed backup version — the swappable seam of stack D4.
//
// THE SEAM IS THE INTERFACE, NOT A PROCESS. contracts.md §4 specifies a stdio JSON-RPC
// protocol and says in as many words that the core "talks to a vault.Vault Go interface;
// any implementation of it, in-process or over this RPC, must pass the golden conformance
// suite". This package is that interface. Whether an implementation runs in this address
// space or in a child is a deployment choice decided by a measurement (qn.8 spec D10),
// not a property of the seam — which is why nothing here mentions a process, a pipe or a
// frame.
//
// READS STREAM, and that is the one shape decision the rest follows from (spec D1).
// contracts.md §4's `materialize {file_id} → {handle, rel_path, size}` exists because a
// process boundary cannot carry an open file: the vault decrypts into scratch, the core
// reads the path, streams it, unlinks. Put that on THIS interface and an in-process
// implementation pays decrypt → scratch → read → unlink for a boundary it does not have —
// double I/O on the slowest disks quince targets. So the method is Open, returning an
// io.ReadCloser, and an RPC implementation calls `materialize` behind it and returns a
// reader whose Close unlinks. The cost is paid by the implementation that needs it and by
// nothing else.
//
// KEYS LIVE BETWEEN Unlock AND Close, and nowhere else. The password is never persisted
// (contracts.md §1), never reaches argv, env or a log line, and a Vault that has been
// Closed answers ErrLocked to everything.
package vault

import (
	"context"
	"errors"
	"io"
	"sort"
	"time"
)

// Vault reads one committed version. Implementations are NOT required to be safe for
// concurrent use; the session registry serializes access to each one (see session.go).
type Vault interface {
	// Unlock derives the keys and opens the index. It is idempotent: a second call on an
	// unlocked Vault is a no-op returning the same Info.
	//
	// An implementation for a version that needs no password ignores the argument, and the
	// caller is told which case it is by Info.Encrypted rather than by the password being
	// accepted — a field that silently validates nothing is worse than no field.
	Unlock(ctx context.Context, password string) (Info, error)

	// List returns one page of entries in a stable total order. See Query and Page.
	List(ctx context.Context, q Query) (Page, error)

	// Stat returns one entry by file id.
	Stat(ctx context.Context, fileID string) (FileEntry, error)

	// Open returns the decrypted content of one file. The caller MUST Close the reader:
	// for an RPC implementation that is what unlinks the materialized scratch copy.
	//
	// CANCELLATION IS Close's JOB. ctx bounds the OPENING of the file; the read that follows
	// ends when the file ends or the reader is Closed. Cancelling ctx afterwards does not stop
	// a decrypt already in flight — the parameter would otherwise suggest it does.
	//
	// A read may end in ErrIncompleteFile AFTER delivering every byte the backup holds —
	// see that error. Directories and symlinks answer ErrNotAFile, never ErrFileNotFound:
	// the entry exists, and "there is no such file" is a different remedy.
	Open(ctx context.Context, fileID string) (io.ReadCloser, error)

	// VerifyCanary decrypts one small file to prove the keys and the blob store agree —
	// the basis for a version's content_verified_at (design §4). It reports the reason
	// when no eligible entry exists rather than passing vacuously.
	VerifyCanary(ctx context.Context) error

	// Aggregate returns per-domain file counts and sizes for the WHOLE version, in ONE
	// PASS. It is the only method here that is not paginated, and that is the point of it.
	//
	// PAGINATION IS THE BROWSER'S CONTRACT, NOT AN AGGREGATE'S. Walking List to build a
	// whole-version total re-pays a per-page cost for every page — measured on a real backup
	// at 9.4 s to 2 m 05 s against 1.1 s for a single pass, for the same answer (qn.9 spec
	// D4, fact 8). The two implementations are slow for two DIFFERENT reasons there
	// (quince#1444), which is the argument for a method that avoids the shape rather than a
	// fix in each.
	//
	// It needs no order and no cursor, so an implementation is free to answer it however is
	// cheapest — the conformance suite gates the ANSWER, not the route.
	Aggregate(ctx context.Context) (Totals, error)

	// Close locks the vault: keys are dropped and any decrypted index is removed. It is
	// idempotent, and every other method answers ErrLocked afterwards.
	Close() error
}

// Info summarizes an unlocked version. It is what contracts.md §4's `initialize` reply
// carries, minus `protocol_version` — a wire fact of the RPC implementation, not of the
// seam.
type Info struct {
	DeviceName string
	IOSVersion string
	FileCount  int64

	// ManifestSHA256 is taken over the ON-DISK Manifest.db — for an encrypted version that
	// is the ciphertext. It therefore identifies the VERSION rather than the decryption,
	// and is computable before Unlock, which is what makes it usable as a cache key
	// (contracts.md §5) without holding a password.
	ManifestSHA256 string

	// Encrypted reports which implementation answered. quince permits unencrypted versions
	// and badges them incomplete (contracts.md §2), so this is a real distinction and not a
	// constant — and it is how a caller knows the password it supplied was not consulted.
	Encrypted bool
}

// Query is one page request. A zero Query is legal and asks for the first page of
// everything at the default limit.
type Query struct {
	Domain string // exact match; empty means any
	Prefix string // relative-path prefix; empty means any
	Cursor string // opaque; empty starts at the beginning. See Page.NextCursor.
	Limit  int    // 0 means DefaultLimit; anything above MaxLimit is clamped and DISCLOSED
}

// Page is one page of entries.
type Page struct {
	Entries []FileEntry

	// NextCursor is empty on the last page. It is opaque and only meaningful against the
	// same version: it encodes the last (Domain, RelativePath) returned, so a page is a
	// fresh query rather than a parked iterator — no server-side state survives between
	// requests, and an idle session holds nothing.
	NextCursor string

	// EffectiveLimit is set ONLY when it differs from the Limit that was asked for, so a
	// clamp is surfaced rather than silently applied ("no silent caps or fallbacks"). A
	// caller that gets fewer rows than it asked for can tell a clamp from a short last page.
	EffectiveLimit int
}

// Kind is what a manifest row is. It mirrors the iOS flag values rather than inventing an
// enum, because the mapping is the only place the two vocabularies meet.
type Kind string

const (
	KindFile    Kind = "file"
	KindDir     Kind = "dir"
	KindSymlink Kind = "symlink"
)

// FileEntry is one row of a version's file index — contracts.md §2's shape.
type FileEntry struct {
	FileID       string
	Domain       string
	RelativePath string
	Kind         Kind
	Size         int64
	MTime        time.Time
}

// Pagination bounds. The range is design §7's own batch range, reused rather than
// reinvented: it already governs how the vault is meant to walk a manifest.
const (
	DefaultLimit = 500
	MaxLimit     = 2000
)

// ClampLimit resolves a requested limit and reports whether it was changed, so every
// implementation clamps identically and the disclosure cannot be forgotten in one of them.
func ClampLimit(requested int) (effective int, clamped bool) {
	switch {
	case requested <= 0:
		return DefaultLimit, false
	case requested > MaxLimit:
		return MaxLimit, true
	default:
		return requested, false
	}
}

// The seam's errors. contracts.md §4 freezes a wire code per failure; Code maps these to
// it, so an RPC implementation and an in-process one cannot disagree about what a failure
// is called.
var (
	// ErrBadPassword is a password that does not unwrap the keys.
	ErrBadPassword = errors.New("vault: incorrect backup password")

	// ErrCorruptManifest is an index that cannot be read as one.
	ErrCorruptManifest = errors.New("vault: manifest is unreadable")

	// ErrFileNotFound is a file id absent from the index.
	ErrFileNotFound = errors.New("vault: no such file in this version")

	// ErrNotAFile is an entry that EXISTS and has no content — a directory or a symlink.
	// Distinct from ErrFileNotFound on purpose: answering "not found" for something the
	// browse listing just showed collapses two causes with different remedies, which the
	// troubleshooting rule names as a defect even when every word of it is true.
	ErrNotAFile = errors.New("vault: entry has no content (directory or symlink)")

	// ErrLocked is a method called before Unlock or after Close. In-process that is a
	// caller bug; over the RPC it is a real ordering condition on the wire, and a seam that
	// cannot express it would make the RPC implementation lie.
	ErrLocked = errors.New("vault: locked")

	// ErrIncompleteFile reports that a version holds FEWER bytes for a file than its index
	// records — a file that was being written while the backup ran.
	//
	// IT IS NOT A FAILED READ. Every recovered byte has already been delivered when this
	// is returned, so it is a fact about the version rather than about the operation, and a
	// retry cannot change it. Callers surface it; they do not treat it as an error to hide.
	ErrIncompleteFile = errors.New("vault: file is incomplete in this backup")

	// ErrOverlongFile is ErrIncompleteFile's MIRROR: the version holds MORE bytes for a file
	// than its index records. Measured on real backups at ~34–38 files per version, and the
	// same file by the same amount across a month of them, so it is a property of iOS backups
	// rather than a transfer accident (quince#1379).
	//
	// IT IS ALSO NOT A FAILED READ, for the same reason and with the opposite sign. Everything
	// the INDEX promises has been delivered when this is returned; what the caller does not get
	// is the extra data on disk, because `Content-Length` is the recorded size and making it
	// the on-disk size would destroy short-read detectability (contracts §1).
	//
	// IT EXISTS BECAUSE THE ALTERNATIVE WAS SILENCE. The unencrypted implementation bounds its
	// read to the record, so nothing overruns, so the HTTP layer sees a clean success and the
	// user receives a truncated file with every signal agreeing — a silent cap, measured on
	// hardware after qn.8 slice 4 shipped. The encrypted path is loud instead, by accident: it
	// overruns and net/http tears the response. Neither told anyone WHY, which is what this and
	// the `overlong` wire field are for.
	ErrOverlongFile = errors.New("vault: file is longer in this backup than its index records")

	// ErrNoCanary is a version with no entry small enough and readable enough to prove the
	// keys against. VerifyCanary reports it rather than passing on an empty search.
	ErrNoCanary = errors.New("vault: no file in this version is eligible as a canary")
)

// Code is the frozen wire code for an error, per contracts.md §4 as this rung amends it.
// An error the seam does not name is `io`: the honest answer for "something below us
// failed", and never a guess at a more specific cause.
//
// ErrIncompleteFile answers "" DELIBERATELY, and it is the reason this function has an
// explicit case for something that is not a failure. It reports a fact about the version
// after a successful read, so letting it fall through to `io` would report an I/O failure
// that did not happen, on a read that delivered every byte the backup holds.
//
// BUT "" IS NOT A SIGNAL — IT IS SILENCE, AND THE DIFFERENCE MATTERS DOWNSTREAM.
// `Code(nil)` is also "", so the ordinary caller shape `if code := Code(err); code != ""`
// takes the SUCCESS path and the incompleteness vanishes with no trace. That is still the
// better trade against `io`, which would report a failure that did not occur — but it
// means this function CANNOT be the thing that surfaces it, and quince's "no silent caps
// or fallbacks" rule requires that it be surfaced somewhere.
//
// So incompleteness travels as a FIELD, never as a code: contracts.md §4's amendment and
// the REST surface (slice 6) carry it explicitly, with a test that the surface fires. If a
// caller ever routes it through here alone, D8.1's promise silently never appears and every
// test still passes — which is why this paragraph exists rather than a one-line comment.
// (quince#1348 review.)
func Code(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrIncompleteFile), errors.Is(err, ErrOverlongFile):
		return ""
	case errors.Is(err, ErrBadPassword):
		return "bad_password"
	case errors.Is(err, ErrCorruptManifest):
		return "corrupt_manifest"
	case errors.Is(err, ErrFileNotFound), errors.Is(err, ErrNoCanary):
		return "not_found"
	case errors.Is(err, ErrNotAFile):
		return "not_a_file"
	case errors.Is(err, ErrLocked):
		return "locked"
	default:
		return "io"
	}
}

// DomainTotals is one domain's contribution to a version, as Aggregate reports it.
type DomainTotals struct {
	Domain string
	Files  int64
	Bytes  int64
}

// Totals is the whole-version aggregate: per-domain counts and sizes, plus the totals they
// sum to.
//
// THE TOTALS ARE CARRIED RATHER THAN LEFT TO THE CALLER TO ADD UP. A surface that shows some
// domains and a total must be able to prove the two agree; recomputing the sum at the
// surface would make a dropped domain invisible, which is what "no silent caps" forbids.
type Totals struct {
	Domains    []DomainTotals // ordered by domain
	TotalFiles int64
	TotalBytes int64
}

// totalsAccumulator is shared by both implementations so they cannot disagree about what
// the aggregate MEANS — ordering, and whether directories count.
//
// EVERY ROW COUNTS, INCLUDING DIRECTORIES AND SYMLINKS, and their size counts too. A domain
// summary that silently dropped them would not sum to the version's own file count, and
// FileCount (contracts §4's `initialize` reply) is a row count over the same table. Two
// numbers on one screen that disagree because one of them quietly filtered is exactly the
// defect the spec's D3 reconciliation requirement exists to prevent.
type totalsAccumulator struct {
	byDomain map[string]*DomainTotals
}

func newTotalsAccumulator() *totalsAccumulator {
	return &totalsAccumulator{byDomain: make(map[string]*DomainTotals)}
}

func (a *totalsAccumulator) add(domain string, size int64) {
	d := a.byDomain[domain]
	if d == nil {
		d = &DomainTotals{Domain: domain}
		a.byDomain[domain] = d
	}
	d.Files++
	d.Bytes += size
}

// totals returns the accumulated result, ordered by domain.
//
// SORTED, because an aggregate whose order depends on map iteration would make every
// consumer's output nondeterministic — including the conformance suite, which would then
// have to sort before comparing and would stop noticing if an implementation started
// dropping rows in a stable-looking way.
func (a *totalsAccumulator) totals() Totals {
	t := Totals{Domains: make([]DomainTotals, 0, len(a.byDomain))}
	for _, d := range a.byDomain {
		t.Domains = append(t.Domains, *d)
		t.TotalFiles += d.Files
		t.TotalBytes += d.Bytes
	}
	sort.Slice(t.Domains, func(i, j int) bool { return t.Domains[i].Domain < t.Domains[j].Domain })
	return t
}
