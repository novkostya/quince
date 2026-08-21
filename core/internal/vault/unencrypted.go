package vault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	iosbackup "github.com/novkostya/ios-backup-crypt"
	"howett.net/plist"
	_ "modernc.org/sqlite" // register the cgo-free "sqlite" driver
)

// unencrypted is the passwordless Vault over a backup nobody encrypted (spec D7).
//
// WHAT IT SHARES WITH THE ENCRYPTED IMPLEMENTATION IS THE RECORD FORMAT, AND NOTHING ELSE.
// An unencrypted backup's Manifest.db is plain SQLite and its blobs are plaintext, so there
// is no keybag, no key derivation and no decryption here. What is NOT different is the
// `Files` table's `file` column: the same NSKeyedArchiver MBFile record, carrying Size and
// LastModified, which are not columns and cannot be read without decoding it.
//
// So this borrows exactly one thing from the crypto library — `DecodeFileRecord`, exported
// for this purpose (novkostya/ios-backup-crypt#8) — rather than reimplementing an Apple
// format. Two decoders can disagree about a file's size, and the encrypted path saying 12
// where this one says something else, for the identical record, is the failure that argues
// against a second copy.
//
// MEASURED before it was built, on a real unencrypted iPad backup: 101,018 records, every
// one decoding through that function, and the recorded size matching the on-disk plaintext
// length on 93,925 of 94,020 ordinary files. The remainder disagree in both directions and
// are a property of iOS backups rather than of this code — see Open, and quince#1379.
type unencrypted struct {
	dir string

	// mu guards the fields below, for the same reason the encrypted implementation holds
	// one: the registry serializes per session, so this is not for concurrency the seam
	// expects — it is so a caller that gets the serialization wrong sees a consistent Vault
	// rather than a torn one.
	mu     sync.Mutex
	db     *sql.DB
	info   Info
	closed bool
}

// OpenUnencrypted prepares a Vault for an unencrypted version at dir. Nothing is read until
// Unlock.
//
// IT TAKES NO SCRATCH DIRECTORY, and that absence is the point rather than an oversight. The
// encrypted implementation needs one because the library decrypts Manifest.db to a file; here
// the index is already plain SQLite and is opened in place, read-only. Nothing is written
// anywhere, so there is nothing for a lock to wipe and nothing a crash can leave behind.
func OpenUnencrypted(dir string) (Vault, error) {
	return &unencrypted{dir: dir}, nil
}

// Unlock opens the index. THE PASSWORD IS IGNORED AND THAT IS REPORTED, not accepted: the
// caller learns which case it is from Info.Encrypted being false, never from a password
// having been "accepted". A field that silently validates nothing is worse than no field
// (spec D7), and the REST layer is what declines to offer one.
func (u *unencrypted) Unlock(ctx context.Context, _ string) (Info, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return Info{}, ErrLocked
	}
	if u.db != nil {
		return u.info, nil // idempotent, per the interface
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}

	// The same hash the encrypted path takes, over the same file, so `manifest_sha256`
	// identifies the VERSION on either backend rather than the decryption.
	sum, err := manifestSHA256(u.dir)
	if err != nil {
		return Info{}, err
	}

	// READ-ONLY AND IMMUTABLE. `mode=ro` alone still lets SQLite create `-wal`/`-shm`
	// alongside the database; `immutable=1` promises the file will not change and suppresses
	// that. It is the same open `storage.verifyPlainDB` already uses.
	//
	// THE BACKEND WHERE THIS EARNS ITS KEEP IS THE NAMESPACE FAMILY, NOT ZFS. For an
	// `is_latest` version on reflink/hardlink/copy, `browseRoot` returns `latestDir()`, and
	// `latest/` there IS the newest committed version's content — so a sidecar file would be
	// a write into a committed version, which the storage rules forbid outright. On zfs
	// browse reads a SNAPSHOT (`zfsSnapRoot`), and a version with no snapshot has no browse
	// root at all, so the same write would fail rather than corrupt anything. The guard is
	// right on every backend; only one of them is where it matters.
	dbPath := filepath.Join(u.dir, "Manifest.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&immutable=1")
	if err != nil {
		return Info{}, fmt.Errorf("%w: %w", ErrCorruptManifest, err)
	}

	var count int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM Files`).Scan(&count); err != nil {
		_ = db.Close()
		// A Manifest.db that will not answer this is not a manifest we can browse, and
		// `corrupt_manifest` is the honest code rather than `io`: the file opened.
		return Info{}, fmt.Errorf("%w: %w", ErrCorruptManifest, err)
	}

	name, version := u.deviceInfo()

	u.db = db
	u.info = Info{
		DeviceName:     name,
		IOSVersion:     version,
		FileCount:      count,
		ManifestSHA256: sum,
		Encrypted:      false,
	}
	return u.info, nil
}

// deviceInfo reads the device's name and iOS version out of Manifest.plist.
//
// BEST EFFORT, AND DELIBERATELY NOT AN ERROR. The encrypted path gets these from the
// library, which fails the unlock if the plist is unreadable — but there the plist carries
// the KEYBAG, so an unreadable one means the backup cannot be opened at all. Here it carries
// two display strings and nothing else, and refusing to browse 100,000 readable files
// because a label is missing would be the wrong trade. Empty strings are what the seam
// already documents for an unknown name.
func (u *unencrypted) deviceInfo() (name, version string) {
	var mp struct {
		Lockdown struct {
			DeviceName     string `plist:"DeviceName"`
			ProductVersion string `plist:"ProductVersion"`
		} `plist:"Lockdown"`
	}
	b, err := os.ReadFile(filepath.Join(u.dir, "Manifest.plist"))
	if err != nil {
		return "", ""
	}
	if _, err := plist.Unmarshal(b, &mp); err != nil {
		return "", ""
	}
	return mp.Lockdown.DeviceName, mp.Lockdown.ProductVersion
}

// List returns one page in the seam's stable (domain, relativePath) order.
//
// IT SEEKS RATHER THAN SKIPS, which is the one place this implementation is better than the
// encrypted one rather than merely different. That one re-walks every earlier page because
// the library exposes no seek, and says so; SQLite has `WHERE (domain, relativePath) > (?,?)`
// with an index behind it, so a later page costs what an earlier one does. The ORDER is
// identical either way — SQLite's default BINARY collation compares bytes, which is what Go
// string comparison does — so a cursor means the same thing on both and the conformance
// suite's paging checks apply unchanged.
func (u *unencrypted) List(ctx context.Context, q Query) (Page, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.db == nil || u.closed {
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

	query := `SELECT fileID, domain, relativePath, flags, file FROM Files WHERE 1=1`
	args := []any{}
	if q.Domain != "" {
		query += ` AND domain = ?`
		args = append(args, q.Domain)
	}
	if q.Prefix != "" {
		// LIKE would treat _ and % in a real path as wildcards. Comparing against the
		// prefix and its successor is exact and uses the same index.
		query += ` AND relativePath >= ?`
		args = append(args, q.Prefix)
		if end, ok := prefixEnd(q.Prefix); ok {
			query += ` AND relativePath < ?`
			args = append(args, end)
		}
	}
	if q.Cursor != "" {
		query += ` AND (domain > ? OR (domain = ? AND relativePath > ?))`
		args = append(args, after.Domain, after.Domain, after.Path)
	}
	// limit+1 so the page knows whether a NEXT one exists without a second query, and
	// without claiming a cursor for a page that ends exactly on the boundary.
	query += ` ORDER BY domain, relativePath LIMIT ?`
	args = append(args, limit+1)

	rows, err := u.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("%w: %w", ErrCorruptManifest, err)
	}
	defer func() { _ = rows.Close() }()

	var page Page
	var last FileEntry
	for rows.Next() {
		if len(page.Entries) == limit {
			page.NextCursor = encodeCursor(cursor{Domain: last.Domain, Path: last.RelativePath})
			break
		}
		entry, err := scanEntry(rows)
		if err != nil {
			return Page{}, err
		}
		page.Entries = append(page.Entries, entry)
		last = entry
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("%w: %w", ErrCorruptManifest, err)
	}
	if clamped {
		page.EffectiveLimit = limit
	}
	return page, nil
}

func (u *unencrypted) Stat(ctx context.Context, fileID string) (FileEntry, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.db == nil || u.closed {
		return FileEntry{}, ErrLocked
	}
	if err := ctx.Err(); err != nil {
		return FileEntry{}, err
	}
	row := u.db.QueryRowContext(ctx,
		`SELECT fileID, domain, relativePath, flags, file FROM Files WHERE fileID = ?`, fileID)
	entry, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FileEntry{}, fmt.Errorf("%w: %s", ErrFileNotFound, fileID)
	}
	if err != nil {
		return FileEntry{}, err
	}
	return entry, nil
}

// Open returns the file's plaintext bytes.
//
// THE RECORDED SIZE IS THE LENGTH, not the file's length on disk, and the two genuinely
// disagree on real backups (quince#1379: ~110 files in 94,000, in both directions, measured
// on four versions spanning a month). Bounding the read to the record keeps this
// implementation's answer identical to the encrypted one, which truncates its decrypt to the
// same number — a browse must not depend on which backend a version happens to live on.
//
// A SHORT FILE IS NOT A FAILED READ. Every byte the backup holds is delivered and then
// ErrIncompleteFile is returned, exactly as the library does, so the caller can surface it as
// a FIELD rather than as an error (contracts §4, spec D8.1).
func (u *unencrypted) Open(ctx context.Context, fileID string) (io.ReadCloser, error) {
	entry, err := u.Stat(ctx, fileID)
	if err != nil {
		return nil, err
	}
	// REFUSE BEFORE STREAMING. A directory has no content, and discovering that halfway
	// through a response body means the caller has already sent a 200.
	if entry.Kind != KindFile {
		return nil, ErrNotAFile
	}

	f, err := os.Open(u.blobPath(fileID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: no blob on disk for %s", ErrFileNotFound, fileID)
		}
		return nil, err
	}
	return &boundedFile{f: f, remaining: entry.Size}, nil
}

// blobPath is the on-disk address of a file's content: <dir>/<fileID[:2]>/<fileID>. The
// same layout the encrypted backend uses; only the contents differ.
func (u *unencrypted) blobPath(fileID string) string {
	if len(fileID) < 2 {
		return filepath.Join(u.dir, fileID)
	}
	return filepath.Join(u.dir, fileID[:2], fileID)
}

// boundedFile delivers exactly the recorded number of bytes, then reports a short backup as
// ErrIncompleteFile — after the last byte, never instead of it.
type boundedFile struct {
	f         *os.File
	remaining int64
}

func (b *boundedFile) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.f.Read(p)
	b.remaining -= int64(n)
	if errors.Is(err, io.EOF) && b.remaining > 0 {
		return n, fmt.Errorf("%w: %d byte(s) short", ErrIncompleteFile, b.remaining)
	}
	return n, err
}

func (b *boundedFile) Close() error { return b.f.Close() }

// VerifyCanary reads one small file to prove the index and the blob store agree.
//
// The claim is weaker here than on the encrypted backend and says so: there no keys are
// involved, so what this proves is that a record's blob EXISTS and is readable — not that
// anything decrypted. That is still the question `content_verified_at` asks of a version
// whose files were never encrypted (design §4).
func (u *unencrypted) VerifyCanary(ctx context.Context) error {
	const canaryMaxBytes = 64 << 10

	u.mu.Lock()
	if u.db == nil || u.closed {
		u.mu.Unlock()
		return ErrLocked
	}
	db := u.db
	u.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	// The first eligible entry in the seam's stable order — deterministic per backup, and
	// not a hardcoded path, because no relative path exists in every backup (spec D2.1).
	rows, err := db.QueryContext(ctx,
		`SELECT fileID, file FROM Files WHERE flags = ? ORDER BY domain, relativePath`, flagFile)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCorruptManifest, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var fileID string
		var blob []byte
		if err := rows.Scan(&fileID, &blob); err != nil {
			return fmt.Errorf("%w: %w", ErrCorruptManifest, err)
		}
		rec, err := iosbackup.DecodeFileRecord(blob)
		if err != nil || rec.Size <= 0 || rec.Size > canaryMaxBytes {
			continue
		}
		rc, err := u.Open(ctx, fileID)
		if err != nil {
			return err
		}
		// Discarded: the point is that the record and the blob store agree, not the bytes.
		// An incomplete canary still proves that, so it is NOT a failure here — the file
		// was there and readable, which is the question asked.
		_, err = io.Copy(io.Discard, rc)
		_ = rc.Close()
		if err != nil && !errors.Is(err, ErrIncompleteFile) {
			return err
		}
		return nil
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrCorruptManifest, err)
	}
	return fmt.Errorf("%w: no file under %d bytes with content", ErrNoCanary, canaryMaxBytes)
}

func (u *unencrypted) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return nil // idempotent, per the interface
	}
	u.closed = true
	if u.db == nil {
		return nil
	}
	db := u.db
	u.db = nil
	return db.Close()
}

// scanner is what Stat and List have in common: one row, either from a QueryRow or a Rows.
type scanner interface{ Scan(dest ...any) error }

// scanEntry reads one Files row into a FileEntry, decoding the record for the two fields
// that are not columns.
func scanEntry(s scanner) (FileEntry, error) {
	var (
		fileID, domain, relPath string
		flags                   int64
		blob                    []byte
	)
	if err := s.Scan(&fileID, &domain, &relPath, &flags, &blob); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FileEntry{}, err // Stat translates this; List cannot reach it
		}
		return FileEntry{}, fmt.Errorf("%w: %w", ErrCorruptManifest, err)
	}

	entry := FileEntry{
		FileID:       fileID,
		Domain:       domain,
		RelativePath: relPath,
		Kind:         kindForFlags(flags),
	}

	// A RECORD THAT WILL NOT DECODE DOES NOT LOSE THE ROW. The domain, path and kind are
	// columns and are still true; only Size and MTime are unavailable. Failing the whole
	// listing would make one corrupt record hide every file after it in the page, which is
	// a worse answer than a row whose size is unknown — and the library's own fixture
	// generator has a `BadRecord` mode precisely because this case is real.
	if rec, err := iosbackup.DecodeFileRecord(blob); err == nil {
		entry.Size = rec.Size
		entry.MTime = rec.MTime
	}
	return entry, nil
}

func kindForFlags(flags int64) Kind {
	switch flags {
	case flagDir:
		return KindDir
	case flagSymlink:
		return KindSymlink
	default:
		return KindFile
	}
}

// prefixEnd is the exclusive upper bound of a byte-prefix range: the prefix with its last
// byte incremented. The second return is false when no such bound exists — a prefix that is
// entirely 0xff — and the caller then omits the upper comparison rather than inventing one.
// No real relative path reaches that, which is exactly why it is returned rather than
// approximated: an unreachable case that silently narrows a query is worse than one that
// cannot.
func prefixEnd(prefix string) (string, bool) {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}
