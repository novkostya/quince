package vault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	iosbackup "github.com/novkostya/ios-backup-crypt"
)

// encrypted is the in-process Vault over `ios-backup-crypt` — the implementation the qn.8
// spike's number chose (spec D10.5). It holds a `*iosbackup.Backup` and nothing else that
// outlives a session.
//
// IT PAYS NO SCRATCH COST FOR A READ, which is D1's whole argument in code: Open returns a
// reader that decrypts as the caller reads, because the library streams to an io.Writer.
// An RPC implementation of this same interface would call `materialize`, write the file to
// scratch and hand back a reader that unlinks on Close — decrypt, write, read, unlink for
// every byte. Nothing here does that, and nothing here is arranged so that it could be
// mistaken for having done it.
type encrypted struct {
	dir     string
	scratch string

	// mu guards the fields below. The session registry already serializes calls per
	// session, so this is not for concurrency the seam expects — it is so that a caller
	// that gets the serialization wrong sees a consistent Vault rather than a torn one.
	mu     sync.Mutex
	backup *iosbackup.Backup
	info   Info
	closed bool
}

// OpenEncrypted prepares a Vault for an encrypted version at dir, decrypting its index
// under scratchDir. Nothing is read and no key exists until Unlock.
//
// scratchDir is where the library writes the decrypted Manifest.db, and it is the session's
// own directory (see Registry.ScratchFor). That is what lets "nothing survives a lock"
// survive a CRASH: the registry wipes that tree on start as well as on teardown, which a
// deferred cleanup cannot do.
func OpenEncrypted(dir, scratchDir string) (Vault, error) {
	return &encrypted{dir: dir, scratch: scratchDir}, nil
}

func (e *encrypted) Unlock(ctx context.Context, password string) (Info, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return Info{}, ErrLocked
	}
	if e.backup != nil {
		return e.info, nil // idempotent, per the interface
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}

	// manifest_sha256 is taken over the ON-DISK Manifest.db — the ciphertext for an
	// encrypted version — so it identifies the VERSION rather than the decryption, and is
	// computable without a password. Taken BEFORE unlocking so a wrong password cannot
	// change it and so the cost is paid once.
	sum, err := manifestSHA256(e.dir)
	if err != nil {
		return Info{}, err
	}

	b, err := iosbackup.Open(e.dir, iosbackup.WithScratchDir(e.scratch))
	if err != nil {
		return Info{}, translate(err)
	}
	if err := b.Unlock(password); err != nil {
		_ = b.Close()
		return Info{}, translate(err)
	}

	di, err := b.DeviceInfo()
	if err != nil {
		_ = b.Close()
		return Info{}, translate(err)
	}

	e.backup = b
	e.info = Info{
		DeviceName:     di.DeviceName,
		IOSVersion:     di.ProductVersion,
		FileCount:      di.FileCount,
		ManifestSHA256: sum,
		Encrypted:      true,
	}
	return e.info, nil
}

func (e *encrypted) List(ctx context.Context, q Query) (Page, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.backup == nil || e.closed {
		return Page{}, ErrLocked
	}
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}

	after, err := decodeCursor(q.Cursor)
	if err != nil {
		return Page{}, fmt.Errorf("%w: %w", ErrFileNotFound, err)
	}
	limit, clamped := ClampLimit(q.Limit)

	var page Page
	// The library yields in (domain, relativePath) order — a stable TOTAL order — so the
	// cursor is a position in it and resuming means skipping forward to it. SKIPPING RATHER
	// THAN SEEKING is a deliberate, honest cost: the library exposes no seek, so a later
	// page re-walks the earlier ones. It is bounded by the manifest and it is O(offset), so
	// if deep paging ever matters the fix is a seek in the library, NOT a parked iterator
	// here — that would put a goroutine and an open statement behind every idle browser tab.
	for entry := range e.backup.List(q.Domain, q.Prefix) {
		if q.Cursor != "" && !after.before(entry.Domain, entry.RelativePath) {
			continue
		}
		page.Entries = append(page.Entries, fromLibrary(entry))
		if len(page.Entries) == limit {
			page.NextCursor = encodeCursor(cursor{Domain: entry.Domain, Path: entry.RelativePath})
			break
		}
	}
	if err := e.backup.Err(); err != nil {
		return Page{}, translate(err)
	}
	if clamped {
		page.EffectiveLimit = limit
	}
	return page, nil
}

func (e *encrypted) Stat(ctx context.Context, fileID string) (FileEntry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.backup == nil || e.closed {
		return FileEntry{}, ErrLocked
	}
	if err := ctx.Err(); err != nil {
		return FileEntry{}, err
	}
	entry, err := e.backup.Stat(fileID)
	if err != nil {
		return FileEntry{}, translate(err)
	}
	return fromLibrary(entry), nil
}

func (e *encrypted) Open(ctx context.Context, fileID string) (io.ReadCloser, error) {
	e.mu.Lock()
	if e.backup == nil || e.closed {
		e.mu.Unlock()
		return nil, ErrLocked
	}
	b := e.backup
	e.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// REFUSE BEFORE STREAMING, not during. A directory has no content, and discovering that
	// halfway through a response body means the caller has already sent a 200 — so the
	// entry is checked first and ErrNotAFile is returned before any byte moves.
	entry, err := b.Stat(fileID)
	if err != nil {
		return nil, translate(err)
	}
	if fromLibrary(entry).Kind != KindFile {
		return nil, ErrNotAFile
	}

	pr, pw := io.Pipe()
	go func() {
		// DecryptFile writes; the caller reads. The pipe applies back-pressure, so a slow
		// reader slows the decrypt rather than buffering the file — which is the property
		// the spike measured (7.9 MiB peak for a 128 MiB file, spec D10.5).
		_ = pw.CloseWithError(translate(b.DecryptFile(fileID, pw)))
	}()
	return pr, nil
}

func (e *encrypted) VerifyCanary(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.backup == nil || e.closed {
		return ErrLocked
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// The first entry with content and a size under the cap, in the library's stable order:
	// deterministic per backup, present in any backup with one readable file, and cheap.
	// Not a hardcoded path — no relative path exists in every backup, so a fixed one is a
	// silent failure on the backup that lacks it (spec D2.1).
	const canaryMaxBytes = 64 << 10
	var chosen string
	for entry := range e.backup.List("", "") {
		if entry.Flags == flagFile && entry.Size > 0 && entry.Size <= canaryMaxBytes {
			chosen = entry.FileID
			break
		}
	}
	if err := e.backup.Err(); err != nil {
		return translate(err)
	}
	if chosen == "" {
		return fmt.Errorf("%w: no file under %d bytes with content", ErrNoCanary, canaryMaxBytes)
	}

	// Decrypted to io.Discard: the point is that the keys and the blob store agree, not the
	// bytes. An incomplete canary still proves that, so it is NOT a failure here — the file
	// decrypted, which is the question asked.
	if err := e.backup.DecryptFile(chosen, io.Discard); err != nil && !errors.Is(err, iosbackup.ErrIncompleteFile) {
		return translate(err)
	}
	return nil
}

func (e *encrypted) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil // idempotent, per the interface
	}
	e.closed = true
	if e.backup == nil {
		return nil
	}
	b := e.backup
	e.backup = nil
	return b.Close()
}

// iOS manifest flag values. Named here because the mapping between the format's integers
// and the seam's Kind is the only place the two vocabularies meet.
const (
	flagFile    = 1
	flagDir     = 2
	flagSymlink = 4
)

func fromLibrary(e iosbackup.FileEntry) FileEntry {
	kind := KindFile
	switch e.Flags {
	case flagDir:
		kind = KindDir
	case flagSymlink:
		kind = KindSymlink
	}
	return FileEntry{
		FileID:       e.FileID,
		Domain:       e.Domain,
		RelativePath: e.RelativePath,
		Kind:         kind,
		Size:         e.Size,
		MTime:        e.MTime,
	}
}

// translate maps the library's errors onto the seam's, so that Code() answers the same wire
// code whichever implementation produced the failure. An error with no counterpart passes
// through: it becomes `io` at the wire, which is honest about "something below us failed"
// and never a guess.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, iosbackup.ErrFileNotFound):
		return fmt.Errorf("%w: %w", ErrFileNotFound, err)
	case errors.Is(err, iosbackup.ErrNotAFile):
		return fmt.Errorf("%w: %w", ErrNotAFile, err)
	case errors.Is(err, iosbackup.ErrLocked):
		return fmt.Errorf("%w: %w", ErrLocked, err)
	case errors.Is(err, iosbackup.ErrIncompleteFile):
		return fmt.Errorf("%w: %w", ErrIncompleteFile, err)
	case errors.Is(err, iosbackup.ErrNotEncrypted):
		// Reached only if this implementation is handed an unencrypted version, which is a
		// selection bug rather than a user-visible condition — the unencrypted path is a
		// separate implementation (spec D7). Named so the failure says which, not `io`.
		return fmt.Errorf("vault: this version is not encrypted; it needs the passwordless implementation: %w", err)
	default:
		// A wrong password surfaces from the library as an unwrap failure with no sentinel,
		// so it cannot be matched by errors.Is. Matching on message text would be worse:
		// it breaks silently on any upstream rewording, and it would misclassify a genuine
		// I/O failure as a bad password — telling a user to retype a password that was
		// right. The seam answers `io` and says what it does not know.
		return err
	}
}

// manifestSHA256 hashes the on-disk Manifest.db without holding it: the file is the whole
// index and can be tens of megabytes.
func manifestSHA256(dir string) (string, error) {
	f, err := os.Open(filepath.Join(dir, "Manifest.db"))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrCorruptManifest, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("%w: %w", ErrCorruptManifest, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
