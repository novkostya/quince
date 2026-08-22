// Package capability reports what a version's domains can and cannot serve.
//
// It exists because the parser answers "unsupported" by THROWING rather than by returning a
// row: `Open` fails with ErrUnsupportedSchema instead of handing back a Capability with
// Supported false, so a caller holding a Capability always sees Supported true. Naming what
// a backup cannot serve therefore means ATTEMPTING each domain and catching the refusal.
//
// LAZY, AND CACHED FOR THE SESSION — architect ruling, quince#1432. Seven database opens is
// not free, and paying it on every overview render would put a fixed cost on a screen whose
// common case is "which apps are in here". The cache has exactly the session's lifetime,
// because the report is a fact about ONE unlocked backup: nothing carries it across a lock.
//
// THREE STATES, NOT TWO. The ruling assumed a domain either works or is unsupported;
// measured, `Open` distinguishes a third — a database that is not in this backup at all,
// reported by wrapping fs.ErrNotExist. "You have no Notes data in this backup" and "quince
// cannot read your Notes database" have different remedies, and one of them is not a defect,
// so collapsing them is the actionable-troubleshooting rule's named failure.
package capability

import (
	"errors"
	"io/fs"
	"sort"
	"sync"

	backup "github.com/novkostya/ios-backup-parser"
	"github.com/novkostya/ios-backup-parser/calendar"
	"github.com/novkostya/ios-backup-parser/calls"
	"github.com/novkostya/ios-backup-parser/contacts"
	"github.com/novkostya/ios-backup-parser/messages"
	"github.com/novkostya/ios-backup-parser/notes"
)

// State is what quince can say about one domain in one backup.
type State string

const (
	// StateSupported: the domain opened and its schema was recognised. Missing may still
	// name fields this particular schema cannot provide.
	StateSupported State = "supported"

	// StateUnsupported: the database is HERE and quince cannot read its schema. Carries the
	// observed fingerprint, which is what a schema-support issue needs.
	StateUnsupported State = "unsupported_schema"

	// StateAbsent: the database is not in this backup. NOT a defect — a domain the user has
	// no data for looks exactly like this, and telling them quince failed would be false.
	StateAbsent State = "absent"

	// StateUnreadable: the database is HERE, and the failure was NOT a schema quince does
	// not recognise — an I/O error, a corrupt file, something that is not a plausible
	// candidate for schema support at all.
	//
	// IT IS ITS OWN STATE BECAUSE THE REMEDY DIFFERS. StateUnsupported invites a
	// schema-support issue and carries the fingerprint that issue needs; this one says the
	// backup is damaged and no amount of parser work will help. Folding it into
	// StateUnsupported — which is what this package did until the test that was supposed to
	// cover the branch turned out to reach it instead — is the collapsed diagnostic the
	// actionable-troubleshooting rule names, and it would send someone to file an issue
	// against a corrupt file.
	StateUnreadable State = "unreadable"
)

// Domain is one row of the report.
type Domain struct {
	Domain string
	State  State

	// Schema is the detected fingerprint's alias, set only when Supported.
	Schema string

	// Missing names record fields this backup's schema cannot provide. It is `no silent
	// caps` as a data structure: the domain enumerates what it cannot give rather than
	// returning empties.
	Missing []string

	// Fingerprint is the observed structure, set only when Unsupported. Report it when
	// filing a schema-support issue — it is the evidence, and without it "unsupported" is a
	// dead end for whoever has to add support.
	Fingerprint string
}

// Report is the whole per-version answer, ordered by domain name.
type Report struct {
	Domains []Domain
}

// prober opens one domain and reports its capability. One per domain package, because the
// parser exposes NO registry — no Domains(), no slice, no map — so the list of domains quince
// knows about is quince's, and a new library domain is a quince change rather than only a
// release.
type prober struct {
	name string
	open func(backup.FS) (backup.Capability, func() error, error)
}

// probers is THE ENUMERATION, and it is deliberately in one place.
//
// FIVE, NOT SEVEN, and that is a release fact rather than a design choice: `reminders` and
// `safari` exist on ios-backup-parser's main and in NO tag, and v0.1.0 is the only thing
// core/go.mod can require (qn.9 spec fact 9b). They join here in the same change that bumps
// the dependency — and `reminders` additionally needs backup.ReadDirFS, which parserfs
// already implements and cannot yet assert.
//
// A DOMAIN QUINCE CANNOT REACH IS ABSENT FROM THIS REPORT, NOT REPORTED ABSENT. StateAbsent
// means "not in this backup", which is a fact about the user's data; "quince has no support
// compiled in" is a fact about quince, and reporting the second as the first would tell
// someone they have no Safari data when nobody looked.
var probers = []prober{
	{name: "calendar", open: func(f backup.FS) (backup.Capability, func() error, error) {
		r, err := calendar.Open(f)
		if err != nil {
			return backup.Capability{}, nil, err
		}
		return r.Capability(), r.Close, nil
	}},
	{name: "calls", open: func(f backup.FS) (backup.Capability, func() error, error) {
		r, err := calls.Open(f)
		if err != nil {
			return backup.Capability{}, nil, err
		}
		return r.Capability(), r.Close, nil
	}},
	{name: "contacts", open: func(f backup.FS) (backup.Capability, func() error, error) {
		r, err := contacts.Open(f)
		if err != nil {
			return backup.Capability{}, nil, err
		}
		return r.Capability(), r.Close, nil
	}},
	{name: "messages", open: func(f backup.FS) (backup.Capability, func() error, error) {
		r, err := messages.Open(f)
		if err != nil {
			return backup.Capability{}, nil, err
		}
		return r.Capability(), r.Close, nil
	}},
	{name: "notes", open: func(f backup.FS) (backup.Capability, func() error, error) {
		r, err := notes.Open(f)
		if err != nil {
			return backup.Capability{}, nil, err
		}
		return r.Capability(), r.Close, nil
	}},
}

// Cache computes a Report at most once and holds it for its own lifetime.
//
// ITS LIFETIME IS THE SESSION'S, which is the ruling: the report describes one unlocked
// backup, so it must not survive a lock. Hanging it on the session registry's entry rather
// than on anything version-keyed is what makes that true by construction instead of by
// remembering to invalidate.
type Cache struct {
	fsys backup.FS

	mu     sync.Mutex
	report *Report
}

// NewCache builds a cache over an FS. It probes nothing until Report is called.
func NewCache(fsys backup.FS) *Cache { return &Cache{fsys: fsys} }

// Report returns the capability report, computing it on first call.
//
// ERRORS ARE PER-DOMAIN AND NEVER FAIL THE WHOLE REPORT. One domain refusing is precisely
// the information this exists to carry; letting it abort the run would hide four working
// domains behind one that is not there.
func (c *Cache) Report() *Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.report != nil {
		return c.report
	}

	rep := &Report{Domains: make([]Domain, 0, len(probers))}
	for _, p := range probers {
		rep.Domains = append(rep.Domains, probe(p, c.fsys))
	}
	sort.Slice(rep.Domains, func(i, j int) bool { return rep.Domains[i].Domain < rep.Domains[j].Domain })
	c.report = rep
	return rep
}

// probe attempts one domain and classifies the outcome.
func probe(p prober, fsys backup.FS) Domain {
	d := Domain{Domain: p.name}
	cap, closeFn, err := p.open(fsys)
	if err == nil {
		if closeFn != nil {
			// CLOSED IMMEDIATELY. A capability report reads no records, so holding the
			// domain open would pin a materialized database for the session's whole life
			// to answer a question already answered.
			_ = closeFn()
		}
		d.State = StateSupported
		d.Schema = cap.Schema
		d.Missing = cap.Missing
		return d
	}

	// ORDER MATTERS: an unsupported-schema error is checked FIRST, because a domain whose
	// database is present but unreadable must not be reported as absent — that would tell a
	// user they have no data when in fact quince could not read what is there.
	var uns *backup.UnsupportedSchemaError
	if errors.As(err, &uns) {
		d.State = StateUnsupported
		d.Fingerprint = uns.Fingerprint
		return d
	}
	if errors.Is(err, fs.ErrNotExist) {
		d.State = StateAbsent
		return d
	}

	// ANYTHING ELSE IS ALSO NOT A CRASH, AND IT IS NOT AN UNRECOGNISED SCHEMA EITHER. An
	// I/O failure or a corrupt file reaching one domain is reported as unreadable rather
	// than propagated, because the alternative is a blank screen where four domains would
	// have rendered — and rather than as unsupported, because that would invite a
	// schema-support issue against a damaged backup.
	d.State = StateUnreadable
	return d
}

// Names returns the domains quince knows about, for tests and for the wire.
func Names() []string {
	out := make([]string, 0, len(probers))
	for _, p := range probers {
		out = append(out, p.name)
	}
	sort.Strings(out)
	return out
}
