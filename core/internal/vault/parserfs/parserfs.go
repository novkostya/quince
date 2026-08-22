// Package parserfs implements ios-backup-parser's backup.FS over a vault session.
//
// The parser reads an already-decrypted backup through an accessor it calls FS —
// Materialize, which must hand back a real filesystem path, and Exists. Its built-in DirFS
// works over a reconstructed <root>/<Domain>/<relativePath> tree, which quince does not
// have and will not build: quince has a vault session that decrypts one file at a time and
// wipes its scratch on lock (design §7). So quince writes the implementation.
//
// THE NEVER-MUTATE RULE IS WHY Materialize COPIES AT ALL. The parser opens returned paths
// with ordinary SQLite semantics, and opening a database with a live -wal replays or
// checkpoints it — a write. The FS contract therefore requires a PRIVATE copy, and quince
// has an independent reason to want one: the committed version is immutable and the
// unencrypted implementation opens it `immutable=1` precisely so SQLite cannot drop a
// sidecar beside it (qn.9 spec fact 13).
//
// EVERYTHING IS WRITTEN INSIDE THE SESSION'S OWN SCRATCH and nowhere else, so the teardown
// qn.8 already owns — one path for lock, TTL and shutdown alike — removes it without
// learning about this package.
package parserfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	backup "github.com/novkostya/ios-backup-parser"

	"github.com/novkostya/quince/core/internal/vault"
)

// sidecarSuffixes are the SQLite companions Materialize copies alongside a database when the
// backup holds them. Copying the database WITHOUT them would show a stale or corrupt view —
// the -wal holds committed pages the main file does not yet have — and the parser's own FS
// doc requires they come along.
var sidecarSuffixes = []string{"-wal", "-shm", "-journal"}

// FS reads one unlocked version through a vault.Vault, materializing into a scratch
// directory the caller owns.
//
// It is NOT safe for concurrent use by multiple goroutines, matching the seam it sits on:
// vault.Vault makes no concurrency promise and the session registry serializes access. The
// mutex here guards this type's own bookkeeping, not the vault.
type FS struct {
	v       vault.Vault
	scratch string

	mu   sync.Mutex
	seq  int
	made map[string]string // domain\x00relativePath → materialized path
}

// New builds an FS over an UNLOCKED vault, writing into scratchDir, which must already
// exist and belong to this session.
func New(v vault.Vault, scratchDir string) (*FS, error) {
	if v == nil {
		return nil, errors.New("parserfs: nil vault")
	}
	st, err := os.Stat(scratchDir)
	if err != nil {
		return nil, fmt.Errorf("parserfs: scratch dir: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("parserfs: scratch path %s is not a directory", scratchDir)
	}
	return &FS{v: v, scratch: scratchDir, made: map[string]string{}}, nil
}

// Compile-time proof that this satisfies both the required and the optional contract. The
// second is not decoration: `reminders` type-asserts for ReadDirFS, and a host that does not
// implement it is served BEST-EFFORT — the domain would under-report through its capability
// report rather than fail, which is the silent-degradation shape quince refuses.
var (
	_ backup.FS        = (*FS)(nil)
	_ backup.ReadDirFS = (*FS)(nil)
)

// Exists reports whether the backup holds domain/relativePath.
func (f *FS) Exists(domain, relativePath string) (bool, error) {
	entry, err := f.lookup(domain, relativePath)
	if err != nil {
		return false, err
	}
	return entry != nil, nil
}

// Materialize decrypts domain/relativePath into the session scratch and returns the copy,
// bringing any sidecars with it.
//
// It is MEMOISED per (domain, relativePath): the parser may materialize the same database
// more than once across domains, and decrypting a multi-gigabyte file twice for one session
// is a cost with no answer attached. The memo lives as long as the FS, which lives as long
// as the session.
func (f *FS) Materialize(domain, relativePath string) (string, error) {
	f.mu.Lock()
	if p, ok := f.made[memoKey(domain, relativePath)]; ok {
		f.mu.Unlock()
		return p, nil
	}
	f.seq++
	dir := filepath.Join(f.scratch, fmt.Sprintf("m%03d", f.seq))
	f.mu.Unlock()

	entry, err := f.lookup(domain, relativePath)
	if err != nil {
		return "", err
	}
	if entry == nil {
		return "", fmt.Errorf("parserfs: %s/%s: %w", domain, relativePath, fs.ErrNotExist)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("parserfs: %w", err)
	}

	target := filepath.Join(dir, filepath.Base(relativePath))
	if err := f.copyOut(entry.FileID, target); err != nil {
		return "", err
	}

	// SIDECARS ARE BEST-EFFORT BY ABSENCE, NOT BY FAILURE. A backup that holds no -wal is
	// the ordinary case; one whose -wal will not decrypt is a real error and is returned.
	for _, suffix := range sidecarSuffixes {
		side, err := f.lookup(domain, relativePath+suffix)
		if err != nil {
			return "", err
		}
		if side == nil {
			continue
		}
		if err := f.copyOut(side.FileID, target+suffix); err != nil {
			return "", err
		}
	}

	f.mu.Lock()
	f.made[memoKey(domain, relativePath)] = target
	f.mu.Unlock()
	return target, nil
}

// ReadDir returns the names of the entries directly inside domain/relativeDir.
//
// FINAL PATH ELEMENT ONLY, and direct children only — the parser joins a returned name onto
// relativeDir, so returning a deeper path would produce one that addresses nothing. The
// manifest is flat, so "directly inside" is computed rather than looked up.
func (f *FS) ReadDir(domain, relativeDir string) ([]string, error) {
	prefix := strings.TrimSuffix(relativeDir, "/") + "/"
	seen := map[string]bool{}
	var names []string
	found := false

	err := f.walk(domain, prefix, func(e vault.FileEntry) {
		found = true
		rest := strings.TrimPrefix(e.RelativePath, prefix)
		if rest == "" {
			return
		}
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			rest = rest[:i] // a directory, named by its first segment
		}
		if !seen[rest] {
			seen[rest] = true
			names = append(names, rest)
		}
	})
	if err != nil {
		return nil, err
	}
	if !found {
		// The contract asks for an error wrapping fs.ErrNotExist for an absent directory,
		// which is distinct from an empty one — and a flat manifest cannot hold an empty
		// directory, so "no rows under this prefix" IS absence.
		return nil, fmt.Errorf("parserfs: %s/%s: %w", domain, relativeDir, fs.ErrNotExist)
	}
	return names, nil
}

// lookup finds one entry by exact relative path, or nil when the backup has none.
//
// IT FILTERS BY PREFIX AND THEN MATCHES EXACTLY. The seam exposes no lookup by path and the
// library exports no file-id derivation, so this is the only route — and the prefix filter
// is what keeps it from being a full walk: a specific path matches itself and its children,
// which is a handful of rows rather than a hundred thousand.
func (f *FS) lookup(domain, relativePath string) (*vault.FileEntry, error) {
	var found *vault.FileEntry
	err := f.walk(domain, relativePath, func(e vault.FileEntry) {
		if found == nil && e.RelativePath == relativePath {
			hit := e
			found = &hit
		}
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// walk pages the whole filtered set, calling fn per entry.
func (f *FS) walk(domain, prefix string, fn func(vault.FileEntry)) error {
	ctx := context.Background()
	cursor := ""
	for {
		page, err := f.v.List(ctx, vault.Query{Domain: domain, Prefix: prefix, Cursor: cursor})
		if err != nil {
			return fmt.Errorf("parserfs: list %s/%s: %w", domain, prefix, err)
		}
		for _, e := range page.Entries {
			fn(e)
		}
		if page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

// copyOut streams one file id out of the vault into path.
//
// STREAMED, not read into memory: a materialized database is the whole point of this package
// and some of them are large. The vault's Open decrypts as the caller reads, so this holds
// one buffer regardless of file size.
func (f *FS) copyOut(fileID, path string) error {
	rc, err := f.v.Open(context.Background(), fileID)
	if err != nil {
		return fmt.Errorf("parserfs: open %s: %w", fileID, err)
	}
	defer func() { _ = rc.Close() }()

	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("parserfs: %w", err)
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		// A SHORT FILE IS NOT A FAILED MATERIALIZE. The vault reports ErrIncompleteFile
		// AFTER delivering every byte the backup holds, so the copy is as complete as the
		// backup is; failing here would deny the parser a database it could still read.
		if !errors.Is(err, vault.ErrIncompleteFile) {
			return fmt.Errorf("parserfs: copy %s: %w", fileID, err)
		}
		return nil
	}
	return out.Close()
}

func memoKey(domain, relativePath string) string { return domain + "\x00" + relativePath }
