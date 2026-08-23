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
	"io"
	"log/slog"
	"sync"
	"time"

	parsermessages "github.com/novkostya/ios-backup-parser/messages"

	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/vault/capability"
	"github.com/novkostya/quince/core/internal/vault/messages"
	"github.com/novkostya/quince/core/internal/vault/parserfs"
	"github.com/novkostya/quince/core/internal/vault/preunlock"
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

	// capCache holds each session's capability report. Keyed by session id and dropped by
	// the closer AttachDerived registers, so it cannot outlive the session it describes.
	capMu    sync.Mutex
	capCache map[string][]wire.DomainCapability

	// msgReaders holds each session's Messages reader, so the 18-second projection build
	// happens once per session rather than once per request. Same lifetime discipline as
	// capCache: AttachDerived closes it and drops the entry when the session ends by any
	// route, so nothing derived from decrypted content outlives the lock.
	msgMu      sync.Mutex
	msgReaders map[string]*messages.Reader

	// preCache memoises the PRE-UNLOCK plist read, keyed by version id (spec D2c:
	// "read once per version and memoised, not per request"). Info.plist is XML and
	// costs 10-99 ms scaling with the app count, and it sits on the path of the thing
	// users came for.
	//
	// IT CACHES THE FILE READ AND NOTHING ELSE. The registry facts — kind, created_at,
	// udid — are composed fresh on every request, so a row that changes is never served
	// from here. What is cached is immutable by a hard rule: a committed version is
	// never mutated, so its plists cannot change under the entry.
	//
	// NOT SESSION-SCOPED, AND DELIBERATELY. D6 requires the CAPABILITY report to die
	// with its session because it describes unlocked content. Nothing here needed a
	// password, so there is no lock for it to outlive — story 6 governs what a lock
	// tears down, and this tier is never behind one.
	preMu    sync.Mutex
	preCache map[string]preunlock.Tier
}

// New builds the service. scratchRoot is wiped — see vault.NewRegistry.
func New(versions Versions, scratchRoot string, ttl time.Duration, log *slog.Logger) (*Service, error) {
	reg, err := vault.NewRegistry(scratchRoot, ttl)
	if err != nil {
		return nil, err
	}
	return &Service{versions: versions, registry: reg, log: log, open: openFor,
		capCache: map[string][]wire.DomainCapability{}, preCache: map[string]preunlock.Tier{},
		msgReaders: map[string]*messages.Reader{}}, nil
}

// Registry exposes the registry so the daemon can sweep expired sessions and tear them all
// down at shutdown. Nothing else should reach past this package for it.
func (s *Service) Registry() *vault.Registry { return s.registry }

// openFor chooses the implementation for a version, on the same flag design §4 already
// branches structural verify on: `Manifest.plist`'s `IsEncrypted`, surfaced as
// `Version.encrypted`.
//
// BOTH BRANCHES SERVE. This refused the unencrypted case with a reason until slice 4, because
// `Size` and `MTime` live in an NSKeyedArchiver record with no public decoder and a browse row
// without a size is not a file browser. `ios-backup-crypt#8` exported the decoder — it is
// format knowledge rather than a decryption step — and the passwordless implementation is
// gated by the same golden conformance suite as the encrypted one, so a version is browsable
// on either branch or on neither.
//
// THE SCRATCH DIRECTORY IS USED BY ONE BRANCH ONLY, and that asymmetry is real rather than an
// oversight: the encrypted implementation needs somewhere to decrypt `Manifest.db` to, and the
// unencrypted one opens a plain SQLite file in place and writes nothing at all.
func openFor(v wire.Version, scratchDir string) (vault.Vault, error) {
	if !v.Encrypted {
		return vault.OpenUnencrypted(v.BrowseRoot)
	}
	return vault.OpenEncrypted(v.BrowseRoot, scratchDir)
}

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
		page = toWirePage(p, s.registry.IncompleteIn(sessionID), s.registry.OverlongIn(sessionID))
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
	return s.registry.WatchIncomplete(sessionID, fileID, rc), toWireEntry(entry, false, false), "", ""
}

// OpenFileByPath is OpenFile addressed by (domain, relativePath) — qn.10 slice 5.
//
// SAME HANDLER, SAME STREAM, SAME GUARDS; only the addressing differs. D6's "no new
// file-serving surface" forbids a second way to stream bytes, not a second way to name a file
// the existing path then serves — ruled at quince#1483. Nothing leaves quince through code
// that is not OpenFile's.
//
// OpenStreamByPath, NOT a resolve-then-OpenStream: two acquisitions with a gap between would
// let a lock or a TTL sweep end the session between resolving the id and streaming it.
func (s *Service) OpenFileByPath(sessionID, domain, relativePath string) (io.ReadCloser, wire.FileEntry, string, string) {
	rc, entry, err := s.registry.OpenStreamByPath(context.Background(), sessionID, domain, relativePath)
	if err != nil {
		return nil, wire.FileEntry{}, codeFor(err), err.Error()
	}
	// The incompleteness watch is keyed by FILE ID, and the entry carries the one that was
	// resolved — so a short read marks the same entry a browse listing would show.
	return s.registry.WatchIncomplete(sessionID, entry.FileID, rc), toWireEntry(entry, false, false), "", ""
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

func toWirePage(p vault.Page, incomplete, overlong map[string]bool) wire.BrowsePage {
	out := wire.BrowsePage{
		Entries:        make([]wire.FileEntry, 0, len(p.Entries)),
		NextCursor:     p.NextCursor,
		EffectiveLimit: p.EffectiveLimit,
	}
	for _, e := range p.Entries {
		out.Entries = append(out.Entries, toWireEntry(e, incomplete[e.FileID], overlong[e.FileID]))
	}
	return out
}

func toWireEntry(e vault.FileEntry, incomplete, overlong bool) wire.FileEntry {
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
		Overlong:     overlong,
	}
}

// SessionVersion reports which version a session was opened on (qn.13 slice 8b-2).
//
// THE AUTHORIZATION SEAM'S HALF OF session → version → device. The scope guard has a session id and
// needs the device; `VersionReader.Version` covers the second hop, and this is the first.
//
// IT ANSWERS FROM THE REGISTRY, NOT FROM A CACHE, so a session that has been locked or has expired
// reports `false` — which the guard turns into a refusal rather than into access under a session
// that no longer exists.
func (s *Service) SessionVersion(sessionID string) (string, bool) {
	sess, ok := s.registry.Get(sessionID)
	if !ok {
		return "", false
	}
	return sess.VersionID, true
}

// overviewCapabilities is what THIS adapter can do — the envelope's frozen `capabilities`,
// which is NOT the per-domain report. `domain_totals` is the aggregate; `capability_report`
// is the fact that the `domains` key is populated at all.
var overviewCapabilities = []string{"domain_totals", "capability_report"}

// overviewAdapterVersion identifies this surface's shape, per the envelope.
const overviewAdapterVersion = "overview.v1"

// Overview serves GET /api/sessions/{id}/overview.
//
// ONE PASS FOR THE TOTALS, and that is the whole reason the seam has Aggregate: assembling
// these by walking the paginated browse costs 9.4 s to 2 m 05 s on a real backup against
// 1.1 s for a single pass (qn.9 D4). Nothing here pages the manifest.
//
// THE CAPABILITY REPORT IS BUILT AT MOST ONCE PER SESSION and is attached to the session so
// the registry's single teardown drops it — the ruling is that it must not survive a lock,
// and attaching it makes that true by construction rather than by remembering to invalidate.
func (s *Service) Overview(sessionID string, q wire.BrowseQuery) (wire.Overview, string, string) {
	var out wire.Overview
	err := s.registry.With(sessionID, func(v vault.Vault) error {
		totals, err := v.Aggregate(context.Background())
		if err != nil {
			return err
		}
		out = toWireOverview(totals, q)
		return nil
	})
	if err != nil {
		return wire.Overview{}, codeFor(err), err.Error()
	}

	// The report is assembled OUTSIDE the With above, deliberately: it materializes domain
	// databases through the same session, and nesting a second With would deadlock on the
	// entry's busy lock rather than serialize.
	if rep := s.capabilityReport(sessionID); rep != nil {
		out.Domains = rep
	}
	return out, "", ""
}

// capabilityReport returns the session's report, building and attaching it on first call.
//
// A NIL RETURN IS NOT AN ERROR AND NOT AN EMPTY REPORT. It means quince could not build one
// for this session — the session ended underneath us, or its scratch is gone — and the wire
// field is then ABSENT rather than an empty array, which is what quince#1459's ruling asked
// be decided here rather than left to the marshaller.
func (s *Service) capabilityReport(sessionID string) []wire.DomainCapability {
	s.capMu.Lock()
	defer s.capMu.Unlock()
	if cached, ok := s.capCache[sessionID]; ok {
		return cached
	}

	var rows []wire.DomainCapability
	err := s.registry.With(sessionID, func(v vault.Vault) error {
		fsys, err := parserfs.New(v, s.registry.ScratchFor(sessionID))
		if err != nil {
			return err
		}
		cache := capability.NewCache(fsys)
		for _, d := range cache.Report().Domains {
			rows = append(rows, wire.DomainCapability{
				Domain:      d.Domain,
				State:       string(d.State),
				Schema:      d.Schema,
				Missing:     d.Missing,
				Fingerprint: d.Fingerprint,
			})
		}
		return nil
	})
	if err != nil {
		return nil
	}

	// ATTACH BEFORE CACHING. If the session has already ended, AttachDerived reports false
	// and the report is dropped rather than memoised — otherwise a lock would leave a report
	// about decrypted content reachable in this map.
	if !s.registry.AttachDerived(sessionID, closerFunc(func() error {
		s.capMu.Lock()
		delete(s.capCache, sessionID)
		s.capMu.Unlock()
		return nil
	})) {
		return nil
	}
	s.capCache[sessionID] = rows
	return rows
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// toWireOverview shapes the envelope. The page carries DOMAIN rows: naming the
// user-installed subset needs Info.plist, which arrives with the pre-unlock tier.
func toWireOverview(t vault.Totals, q wire.BrowseQuery) wire.Overview {
	limit, clamped := vault.ClampLimit(q.Limit)
	_ = clamped

	start := 0
	if q.Cursor != "" {
		// The cursor is the domain to resume AFTER, which needs no server-side state: the
		// aggregate is already ordered by domain, so a position in it is a name.
		for i, d := range t.Domains {
			if d.Domain == q.Cursor {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(t.Domains) {
		end = len(t.Domains)
	}

	page := wire.OverviewPage{Items: make([]wire.DomainSummary, 0, end-start)}
	for _, d := range t.Domains[start:end] {
		page.Items = append(page.Items, wire.DomainSummary{Domain: d.Domain, Files: d.Files, Bytes: d.Bytes})
	}
	if end < len(t.Domains) {
		page.NextCursor = t.Domains[end-1].Domain
	}

	return wire.Overview{
		Capabilities:   overviewCapabilities,
		AdapterVersion: overviewAdapterVersion,
		// Never null on the wire: a client iterating warnings should not have to distinguish
		// "none" from "field absent".
		Warnings:          []string{},
		UnsupportedReason: nil,
		Page:              page,
		Totals: wire.OverviewTotals{
			Files:       t.TotalFiles,
			Bytes:       t.TotalBytes,
			DomainCount: len(t.Domains),
		},
	}
}

// VersionOverview serves GET /api/versions/{id}/overview — qn.9's pre-unlock tier (D11).
//
// NO SESSION, NO PASSWORD, AND IT MUST STAY THAT WAY. The tier is defined by what an iOS
// backup yields without the backup password (D1), so this method takes a version id and
// nothing else. A password parameter here would be one nothing could honestly use.
func (s *Service) VersionOverview(versionID string) (wire.VersionOverview, string, string) {
	v, ok, err := s.versions.Version(versionID)
	if err != nil {
		s.log.Error("vault: reading version", "version", versionID, "error", err)
		return wire.VersionOverview{}, wire.VaultCodeIO, "could not read the version"
	}
	if !ok {
		return wire.VersionOverview{}, wire.VaultCodeNotFound, "no such version"
	}
	// The same cue Unlock uses rather than a second rule: storage empties browse_root for a
	// version whose content cannot be served, and reading plists out of a fallback path
	// would describe the previous version or a half-written one.
	if v.BrowseRoot == "" {
		return wire.VersionOverview{}, wire.VaultCodeNotFound,
			"this version has no browsable content on disk (its snapshot is missing)"
	}

	tier, err := s.preunlockTier(versionID, v.BrowseRoot)
	if err != nil {
		s.log.Error("vault: reading the pre-unlock tier", "version", versionID, "error", err)
		// A plist that exists and will not parse is a broken backup, and corrupt_manifest is
		// the honest code: the files opened. An ABSENT plist never reaches here — it reports
		// its own absence, which the wire types carry as Present false.
		return wire.VersionOverview{}, wire.VaultCodeCorruptManifest,
			"this version's plists could not be read: " + err.Error()
	}
	return toWireVersionOverview(v, tier), "", ""
}

// preunlockTier reads the three no-password plists, memoised per version (D2c).
func (s *Service) preunlockTier(versionID, dir string) (preunlock.Tier, error) {
	s.preMu.Lock()
	if t, ok := s.preCache[versionID]; ok {
		s.preMu.Unlock()
		return t, nil
	}
	s.preMu.Unlock()

	// READ OUTSIDE THE LOCK. Info.plist is up to 99 ms of XML parsing, and holding the mutex
	// across it would serialise every version's overview behind the slowest one. Two callers
	// racing the same cold version both read and both store the same bytes off an immutable
	// tree — a duplicated read is the cost, and it is cheaper than the contention.
	t, err := preunlock.Read(dir)
	if err != nil {
		return preunlock.Tier{}, err
	}

	s.preMu.Lock()
	defer s.preMu.Unlock()
	// BOUNDED, because this one is not swept by anything. The capability cache is dropped by
	// the closer AttachDerived registers; a version's plists have no such event, so without a
	// cap this map grows with every version ever viewed for the life of the process. Clearing
	// wholesale rather than evicting one entry keeps it to a bound with no bookkeeping: the
	// cost of being wrong is a re-read of a file, not a wrong answer.
	if len(s.preCache) >= preCacheMax {
		s.preCache = map[string]preunlock.Tier{}
	}
	s.preCache[versionID] = t
	return t, nil
}

// preCacheMax bounds the pre-unlock memo. A device holds tens of versions and a household
// holds a few devices, so this is far above any real working set and exists as a ceiling
// rather than as a tuning knob — which is why it is a constant and not a config key.
const preCacheMax = 512

func toWireVersionOverview(v wire.Version, t preunlock.Tier) wire.VersionOverview {
	out := wire.VersionOverview{
		VersionID: v.ID,
		UDID:      v.UDID,
		CreatedAt: v.CreatedAt,
		// FROM THE REGISTRY, NOT FROM Status.plist.IsFullBackup — which the lab proved lies
		// (finding #9(a), quince#1466). This is the seed-sentinel answer storage already
		// derives, and "unknown" is a real state for an adopted version rather than a gap.
		Kind: v.Kind,
		// NULL, ALWAYS, on this route. The Files table is inside Manifest.db, so no
		// passwordless read can count it; an explicit null says UNKNOWN where 0 would be a
		// lie about a good backup (story 7, G2).
		FileCount: nil,
		Device: wire.VersionDevice{
			Present:        t.ManifestPresent,
			Name:           t.Lockdown.DeviceName,
			IOSVersion:     t.Lockdown.ProductVersion,
			Class:          t.Lockdown.DeviceClass,
			ProductType:    t.Lockdown.ProductType,
			BuildVersion:   t.Lockdown.BuildVersion,
			SerialNumber:   t.Lockdown.SerialNumber,
			UniqueDeviceID: t.Lockdown.UniqueDeviceID,
		},
		Backup: wire.VersionBackup{
			Present:       t.Status.Present,
			State:         t.Status.BackupState,
			SnapshotState: t.Status.SnapshotState,
			Date:          rfc3339OrEmpty(t.Status.Date),
			UUID:          t.Status.UUID,
			FormatVersion: t.Status.Version,
		},
		Apps: wire.VersionApps{
			Present:        t.Extras.Present,
			BundleIDs:      t.Extras.InstalledApplications,
			DisplayName:    t.Extras.DisplayName,
			ITunesVersion:  t.Extras.ITunesVersion,
			LastBackupDate: rfc3339OrEmpty(t.Extras.LastBackupDate),
			Cellular: wire.VersionCellular{
				IMEI:        t.Extras.IMEI,
				ICCID:       t.Extras.ICCID,
				PhoneNumber: t.Extras.PhoneNumber,
			},
		},
	}
	// THIS VERSION's encryption state, off its own Manifest.plist, with the registry row as
	// the fallback for a backup that has no manifest to read. A device's history can hold an
	// unencrypted head above encrypted snapshots (spec D1), so neither source may be assumed
	// to describe the other.
	out.Encrypted = v.Encrypted
	if t.ManifestPresent {
		out.Encrypted = t.Encrypted
	}
	if out.Apps.BundleIDs == nil {
		out.Apps.BundleIDs = []string{}
	}
	return out
}

// rfc3339OrEmpty renders a plist timestamp, or empty for one the format did not carry.
//
// ZERO MEANS ABSENT, NOT 1970. The optional-timestamp fields in these plists arrive as the
// zero Time when the key is missing, and rendering that as an epoch date would put a
// confident wrong answer on a screen.
func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// MessagesChats serves the conversation list for an unlocked session — qn.10 slice 3.
func (s *Service) MessagesChats(sessionID string) (wire.MessagesChats, string, string) {
	r, code, msg := s.messagesReader(sessionID)
	if code != "" {
		return wire.MessagesChats{}, code, msg
	}

	chats, warnings, err := r.Chats(context.Background())
	if err != nil {
		// THE TWO UNSUPPORTED CAUSES STAY APART. A backup with no readable Messages
		// database and one whose schema has no conversation tables are different facts
		// with different screens; the second still has messages (qn.10 D7).
		switch {
		case errors.Is(err, messages.ErrUnsupported):
			return unsupportedChats("this backup has no Messages database quince can read"), "", ""
		case errors.Is(err, messages.ErrChatsUnavailable):
			return unsupportedChats("this backup stores messages without conversations, so they cannot be listed"), "", ""
		case errors.Is(err, vault.ErrNoSession):
			return wire.MessagesChats{}, "not_found", "session not found or expired"
		case errors.Is(err, vault.ErrSessionBusy):
			return wire.MessagesChats{}, "busy", "this session is serving another request"
		}
		return wire.MessagesChats{}, "io", "could not read this backup's conversations"
	}

	out := wire.MessagesChats{
		Capabilities:   []string{"threads", "attachments"},
		AdapterVersion: messagesAdapterVersion,
		// Never nil on the wire: a client iterating should not have to distinguish
		// "none" from "field absent".
		Warnings: append([]string{}, warnings...),
		Page:     wire.MessagesChatsPage{Items: []wire.MessagesChat{}},
	}
	for _, c := range chats {
		out.Page.Items = append(out.Page.Items, wire.MessagesChat{
			ID: c.ID, GUID: c.GUID, DisplayName: c.DisplayName,
			Identifier: c.Identifier, IsGroup: c.IsGroup, Participants: c.Participants,
		})
	}
	return out, "", ""
}

// messagesAdapterVersion names the reader, not an iOS version. The parser's own schema alias
// is a discovery-order ordinal for the same reason: a fingerprint's identity is the
// introspected structure, never a version claim.
const messagesAdapterVersion = "messages-quince.v1"

// unsupportedChats builds the envelope for a backup this adapter cannot serve. It is a 200
// carrying unsupported_reason rather than an error status, because the SESSION is fine and
// the surface has something true to render — which is the envelope's whole purpose.
func unsupportedChats(reason string) wire.MessagesChats {
	return wire.MessagesChats{
		Capabilities:      []string{},
		AdapterVersion:    messagesAdapterVersion,
		Warnings:          []string{},
		UnsupportedReason: &reason,
		Page:              wire.MessagesChatsPage{Items: []wire.MessagesChat{}},
	}
}

// messagesReader returns the session's reader, building and attaching it on first call.
//
// THE VAULT IS HELD ONLY LONG ENOUGH TO MATERIALIZE, NEVER FOR THE SCAN. registry.With is
// exclusive and non-blocking — TryLock, else ErrSessionBusy — so wrapping the whole build in
// it would refuse every other call on this session for the ~16 s it runs, including a browse,
// a file download, and the user's own second click.
//
// MEASURED, BOTH ARMS, ON A REAL BACKUP (quince#1483): build inside With → 808 concurrent
// calls, ALL refused; materialize inside With and scan outside → 531 succeeded, and the 56
// refusals are exactly the 1.1 s materialize window. The residue is unavoidable, because the
// vault must decrypt.
//
// SCANNING OUTSIDE THE LOCK IS SAFE BECAUSE parserfs MEMOISES, and that is a safety property
// rather than a speed one: if the memo missed, the scan's own Materialize would reach the
// vault outside With, concurrently with another request, and a race need not produce an
// error. Measured directly: a second Materialize of the same file is 1 µs against 806 ms and
// returns the identical path.
func (s *Service) messagesReader(sessionID string) (*messages.Reader, string, string) {
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	if r, ok := s.msgReaders[sessionID]; ok {
		return r, "", ""
	}

	var reader *messages.Reader
	err := s.registry.With(sessionID, func(v vault.Vault) error {
		scratch := s.registry.ScratchFor(sessionID)
		fsys, err := parserfs.New(v, scratch)
		if err != nil {
			return err
		}
		// Materialize HERE, under the lock, so the scan that follows needs no vault.
		//
		// A FAILURE IS NOT FATAL AT THIS LAYER and is deliberately ignored: a backup with
		// no Messages database is an unsupported_reason, not an error, and Chats is what
		// reports which of the two causes it is. The reader is constructed either way.
		_, _ = fsys.Materialize(parsermessages.Domain, parsermessages.RelativePath)
		reader, err = messages.New(fsys, scratch)
		return err
	})
	switch {
	case errors.Is(err, vault.ErrNoSession):
		return nil, "not_found", "session not found or expired"
	case errors.Is(err, vault.ErrSessionBusy):
		return nil, "busy", "this session is serving another request"
	case err != nil:
		return nil, "io", "could not open this backup's Messages domain"
	}

	// ATTACH BEFORE CACHING, as capabilityReport does: if the session has already ended,
	// AttachDerived reports false and the reader is closed rather than memoised, so nothing
	// built from decrypted content stays reachable in this map.
	if !s.registry.AttachDerived(sessionID, closerFunc(func() error {
		s.msgMu.Lock()
		if r, ok := s.msgReaders[sessionID]; ok {
			_ = r.Close()
			delete(s.msgReaders, sessionID)
		}
		s.msgMu.Unlock()
		return nil
	})) {
		_ = reader.Close()
		return nil, "not_found", "session not found or expired"
	}
	s.msgReaders[sessionID] = reader
	return reader, "", ""
}

// MessagesThread serves one page of a conversation — qn.10 slice 4.
//
// THIS IS THE ROUTE THAT PAYS FOR THE PROJECTION. Opening a conversation for the first time in
// a session builds it — ~18 s on a real backup (qn.10 D2) — and every page after that is a
// seek measured at 265 µs. The request BLOCKS for that build: writeTimeout is 120 s
// (cmd/quince/main.go), so it completes, and blocking is the honest shape for a synchronous
// REST surface.
//
// PROGRESS IS NOT REPORTED HERE, AND THAT IS DECLARED RATHER THAN OVERLOOKED. The reader takes
// an onProgress callback and this passes nil, because a synchronous JSON response has nowhere
// to put progress. D2 makes the report load-bearing at 18 s, so delivering it is slice 7's,
// over the WebSocket that already carries job progress — the callback is the seam it will
// attach to.
func (s *Service) MessagesThread(sessionID string, chatID int64, cursor string, limit int) (wire.MessagesThread, string, string) {
	r, code, msg := s.messagesReader(sessionID)
	if code != "" {
		return wire.MessagesThread{}, code, msg
	}

	page, err := r.Thread(context.Background(), chatID, cursor, limit, nil)
	if err != nil {
		switch {
		case errors.Is(err, messages.ErrUnsupported):
			return unsupportedThread("this backup has no Messages database quince can read"), "", ""
		case errors.Is(err, messages.ErrChatsUnavailable):
			return unsupportedThread("this backup stores messages without conversations, so a conversation cannot be opened"), "", ""
		case errors.Is(err, vault.ErrNoSession):
			return wire.MessagesThread{}, "not_found", "session not found or expired"
		case errors.Is(err, vault.ErrSessionBusy):
			return wire.MessagesThread{}, "busy", "this session is serving another request"
		}
		// A malformed cursor is the caller's fault and is said so, rather than being
		// reported as an unreadable backup. Two causes, two remedies.
		if errors.Is(err, messages.ErrBadCursor) {
			return wire.MessagesThread{}, "bad_request", "that page marker is not one quince issued"
		}
		return wire.MessagesThread{}, "io", "could not read this conversation"
	}

	out := wire.MessagesThread{
		Capabilities:   []string{"threads", "attachments"},
		AdapterVersion: messagesAdapterVersion,
		Warnings:       append([]string{}, page.Warnings...),
		Page: wire.MessagesThreadPage{
			Items:      make([]wire.MessagesMessage, 0, len(page.Messages)),
			NextCursor: page.NextCursor,
		},
	}
	if page.LimitClamped {
		// The clamp is DISCLOSED, exactly as browse discloses its own. A truncated page
		// that does not say so is a silent cap.
		out.Warnings = append(out.Warnings, "more messages were requested than quince serves in one page; the page was shortened")
	}
	for _, m := range page.Messages {
		out.Page.Items = append(out.Page.Items, toWireMessage(m))
	}
	return out, "", ""
}

// toWireMessage shapes one message for the wire. SHARED by the thread and the search routes,
// so a field that carries a distinction — BodyUnknown, Present — cannot be mapped correctly on
// one surface and dropped on the other.
func toWireMessage(m messages.Message) wire.MessagesMessage {
	wm := wire.MessagesMessage{
		ID: m.ID, GUID: m.GUID, Time: m.Time.UTC().Format(time.RFC3339),
		FromMe: m.FromMe, Handle: m.Handle, Body: m.Body, BodyUnknown: m.BodyUnknown,
		IsTapback: m.IsTapback, ReactsTo: m.ReactsTo,
		Edited: m.Edited, Retracted: m.Retracted, Balloon: m.Balloon,
	}
	for _, a := range m.Attachments {
		wm.Attachments = append(wm.Attachments, wire.MessagesAttachment{
			Domain: a.Domain, RelativePath: a.RelativePath, MIMEType: a.MIMEType,
			Name: a.Name, Bytes: a.Bytes, Sticker: a.IsSticker, Present: a.Present,
		})
	}
	return wm
}

func unsupportedThread(reason string) wire.MessagesThread {
	return wire.MessagesThread{
		Capabilities:      []string{},
		AdapterVersion:    messagesAdapterVersion,
		Warnings:          []string{},
		UnsupportedReason: &reason,
		Page:              wire.MessagesThreadPage{Items: []wire.MessagesMessage{}},
	}
}

// MessagesSearch serves a search over the session's messages — qn.10 slice 6.
func (s *Service) MessagesSearch(sessionID, term string, limit int) (wire.MessagesSearch, string, string) {
	r, code, msg := s.messagesReader(sessionID)
	if code != "" {
		return wire.MessagesSearch{}, code, msg
	}

	res, err := r.Search(context.Background(), term, limit, nil)
	if err != nil {
		switch {
		case errors.Is(err, messages.ErrEmptyQuery):
			return wire.MessagesSearch{}, "bad_request", "type something to search for"
		case errors.Is(err, messages.ErrBadQuery):
			return wire.MessagesSearch{}, "bad_request", "that search term could not be understood; try plain words"
		case errors.Is(err, messages.ErrUnsupported):
			return unsupportedSearch("this backup has no Messages database quince can read"), "", ""
		case errors.Is(err, messages.ErrChatsUnavailable):
			return unsupportedSearch("this backup stores messages without conversations, so results cannot be placed"), "", ""
		case errors.Is(err, vault.ErrNoSession):
			return wire.MessagesSearch{}, "not_found", "session not found or expired"
		case errors.Is(err, vault.ErrSessionBusy):
			return wire.MessagesSearch{}, "busy", "this session is serving another request"
		}
		return wire.MessagesSearch{}, "io", "could not search this backup's messages"
	}

	out := wire.MessagesSearch{
		AdapterVersion: messagesAdapterVersion,
		Warnings:       append([]string{}, res.Warnings...),
		Page:           wire.MessagesSearchPage{Items: []wire.MessagesSearchHit{}},
	}
	// THE CAPABILITY IS DERIVED FROM WHETHER AN INDEX EXISTS, not asserted. A session
	// without one advertises no "search", and the surface hides the box.
	out.Capabilities = []string{"threads", "attachments"}
	if res.Searchable {
		out.Capabilities = append(out.Capabilities, "search")
	}
	if res.LimitClamped {
		out.Warnings = append(out.Warnings, "more results were requested than quince returns at once; the list was shortened")
	}
	for _, h := range res.Hits {
		hit := wire.MessagesSearchHit{ChatIDs: h.ChatIDs}
		hit.MessagesMessage = toWireMessage(h.Message)
		out.Page.Items = append(out.Page.Items, hit)
	}
	return out, "", ""
}

func unsupportedSearch(reason string) wire.MessagesSearch {
	return wire.MessagesSearch{
		Capabilities:      []string{},
		AdapterVersion:    messagesAdapterVersion,
		Warnings:          []string{},
		UnsupportedReason: &reason,
		Page:              wire.MessagesSearchPage{Items: []wire.MessagesSearchHit{}},
	}
}
