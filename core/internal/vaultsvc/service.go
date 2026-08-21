// Package vaultsvc joins the vault seam to storage and serves httpapi's VaultBrowse.
//
// IT EXISTS SO THAT NEITHER SIDE HAS TO KNOW THE OTHER. `httpapi` holds a consumer-defined
// interface and imports no vault subsystem — the pattern DeviceReader, MuxerControl and
// VersionAdmin already follow — and `vault` knows nothing about versions, storages or HTTP.
// The join is one small package rather than a dependency either way.
//
// EVERY METHOD ANSWERS A CONTRACTS §4 CODE RATHER THAN AN ERROR TO CLASSIFY. The seam already
// owns that classification (`vault.Code`); re-deriving it in the handler would put the
// taxonomy in two places, and the second one drifts.
package vaultsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/wire"
)

// Versions is the storage surface this needs: one version by id. Consumer-defined, so this
// package imports no storage subsystem either (*storage.Manager satisfies it).
type Versions interface {
	Version(id string) (wire.Version, bool, error)
}

// Service is httpapi's VaultBrowse over a real registry.
type Service struct {
	versions Versions
	registry *vault.Registry
	log      *slog.Logger

	// open builds a Vault for a version. A field so a test can substitute one without a
	// backup on disk, and so the encrypted/unencrypted selection has ONE home (see openFor).
	open func(v wire.Version, scratchDir string) (vault.Vault, error)
}

// New builds the service. scratchRoot is wiped — see vault.NewRegistry.
func New(versions Versions, scratchRoot string, ttl time.Duration, log *slog.Logger) (*Service, error) {
	reg, err := vault.NewRegistry(scratchRoot, ttl)
	if err != nil {
		return nil, err
	}
	return &Service{versions: versions, registry: reg, log: log, open: openFor}, nil
}

// Registry exposes the registry so the daemon can sweep expired sessions and tear them all
// down at shutdown. Nothing else should reach past this package for it.
func (s *Service) Registry() *vault.Registry { return s.registry }

// openFor chooses the implementation for a version.
//
// THE UNENCRYPTED CASE IS REFUSED, WITH THE REASON, AND THAT IS TEMPORARY AND TRACKED. quince
// permits unencrypted versions (`Version.encrypted`, `backup.require_encryption: false`) and
// the qn.8 spec's D7 rules that they should be browsable by a second, passwordless
// implementation of the same interface. That implementation is slice 4 and is blocked on
// `ios-backup-crypt` exporting its file-record decoder (ios-backup-crypt#8): `Size` and
// `MTime` live in an NSKeyedArchiver blob whose decoding is NOT a decryption step, and there
// is no public path to it.
//
// SO THIS REFUSES RATHER THAN SERVING A VERSION WITH TWO FIELDS MISSING. A browse row without
// a size is not a file browser, and inventing one would be the silent degradation the hard
// rules forbid. The message names the reason and the remedy, per the troubleshooting rule —
// it does not say "unsupported".
func openFor(v wire.Version, scratchDir string) (vault.Vault, error) {
	if !v.Encrypted {
		return nil, fmt.Errorf("%w: this version is not encrypted, and quince cannot browse "+
			"unencrypted backups yet — it is tracked at ios-backup-crypt#8. Encrypted versions "+
			"of this device browse normally", errUnencryptedUnsupported)
	}
	return vault.OpenEncrypted(v.BrowseRoot, scratchDir)
}

// errUnencryptedUnsupported is this package's own, not the seam's: the seam has no opinion
// about which implementations exist.
var errUnencryptedUnsupported = errors.New("vaultsvc: unencrypted versions are not browsable yet")

func (s *Service) Unlock(versionID, password string) (wire.Session, string, string) {
	v, ok, err := s.versions.Version(versionID)
	if err != nil {
		s.log.Error("vault: reading version", "version", versionID, "error", err)
		return wire.Session{}, wire.VaultCodeIO, "could not read the version"
	}
	if !ok {
		return wire.Session{}, wire.VaultCodeNotFound, "no such version"
	}
	// BROWSE_ROOT IS EMPTY FOR A VERSION WHOSE CONTENT CANNOT BE SERVED — storage's own cue,
	// used rather than reinvented (layout.go: "the caller's cue to surface the version as
	// UNBROWSABLE WITH A REASON"). On zfs that means a row with no snapshot; falling through
	// would hand a session the live tree, which is the previous version or a half-written one.
	if v.BrowseRoot == "" {
		return wire.Session{}, wire.VaultCodeNotFound,
			"this version has no browsable content on disk (its snapshot is missing)"
	}

	sessionID := id.New()
	sess, info, err := s.registry.Unlock(context.Background(), sessionID, versionID, password,
		func(scratchDir string) (vault.Vault, error) { return s.open(v, scratchDir) })
	if err != nil {
		if errors.Is(err, errUnencryptedUnsupported) {
			return wire.Session{}, wire.VaultCodeUnsupportedVersion, err.Error()
		}
		return wire.Session{}, vault.Code(err), err.Error()
	}
	s.log.Info("vault: session opened", "session", sess.ID, "version", versionID,
		"device", info.DeviceName, "files", info.FileCount)
	return toWireSession(sess), "", ""
}

func (s *Service) Lock(sessionID string) (string, string) {
	if err := s.registry.Lock(sessionID); err != nil {
		s.log.Error("vault: locking session", "session", sessionID, "error", err)
		return wire.VaultCodeIO, "could not lock the session"
	}
	return "", ""
}

func (s *Service) Browse(sessionID string, q wire.BrowseQuery) (wire.BrowsePage, string, string) {
	var page wire.BrowsePage
	err := s.registry.With(sessionID, func(v vault.Vault) error {
		p, err := v.List(context.Background(), vault.Query{
			Domain: q.Domain, Prefix: q.Prefix, Cursor: q.Cursor, Limit: q.Limit,
		})
		if err != nil {
			return err
		}
		page = toWirePage(p, s.registry.IncompleteIn(sessionID))
		return nil
	})
	if err != nil {
		return wire.BrowsePage{}, codeFor(err), err.Error()
	}
	return page, "", ""
}

func (s *Service) OpenFile(sessionID, fileID string) (io.ReadCloser, wire.FileEntry, string, string) {
	// OpenStream, NOT With — the stream outlives this call, and the registry holds the session
	// busy until the reader is Closed. Using With here would release the session the moment
	// this function returns and let a lock tear the download (quince#1365 review).
	rc, entry, err := s.registry.OpenStream(context.Background(), sessionID, fileID)
	if err != nil {
		return nil, wire.FileEntry{}, codeFor(err), err.Error()
	}
	// The reader records incompleteness on the session when the read comes up short, so the
	// browse listing can mark that entry afterwards (spec D8.1a). The HTTP layer sees only a
	// body that ends against a declared Content-Length, which is what makes it detectable.
	return s.registry.WatchIncomplete(sessionID, fileID, rc), toWireEntry(entry, false), "", ""
}

// codeFor maps a registry or seam error onto a contracts §4 code. Session-level errors are
// this package's to classify; everything else the seam already owns.
func codeFor(err error) string {
	switch {
	case errors.Is(err, vault.ErrNoSession):
		return wire.VaultCodeLocked
	case errors.Is(err, vault.ErrSessionBusy):
		return wire.VaultCodeBusy
	default:
		return vault.Code(err)
	}
}

func toWireSession(s vault.Session) wire.Session {
	return wire.Session{ID: s.ID, VersionID: s.VersionID, ExpiresAt: s.ExpiresAt.UTC().Format(time.RFC3339)}
}

func toWirePage(p vault.Page, incomplete map[string]bool) wire.BrowsePage {
	out := wire.BrowsePage{
		Entries:        make([]wire.FileEntry, 0, len(p.Entries)),
		NextCursor:     p.NextCursor,
		EffectiveLimit: p.EffectiveLimit,
	}
	for _, e := range p.Entries {
		out.Entries = append(out.Entries, toWireEntry(e, incomplete[e.FileID]))
	}
	return out
}

func toWireEntry(e vault.FileEntry, incomplete bool) wire.FileEntry {
	// MTime is OPTIONAL in the format — absent is ordinary, not corrupt — so a zero Time
	// becomes an empty string rather than 1970 (ios-backup-crypt v0.2.0).
	mtime := ""
	if !e.MTime.IsZero() {
		mtime = e.MTime.UTC().Format(time.RFC3339)
	}
	return wire.FileEntry{
		FileID:       e.FileID,
		Domain:       e.Domain,
		RelativePath: e.RelativePath,
		Kind:         string(e.Kind),
		Size:         e.Size,
		Mtime:        mtime,
		Incomplete:   incomplete,
	}
}
