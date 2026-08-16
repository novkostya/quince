package demo

import "testing"

// quince#1036 — `default` IS READ FROM THE DECLARATION, so re-designation is observable on a demo.
//
// THE DEFECT THIS GUARDS, and why it is worth a test rather than a comment. `Storages()` returned
// `Default` as a literal — `internal` true, `shuttle` false, forever. So on a demo:
//
//	POST /api/config/storage/shuttle/default   → 200
//	GET  /api/config    → internal default=false   shuttle default=true    ← the write landed
//	GET  /api/storages  → internal default=true    shuttle default=false   ← unchanged, forever
//
// The UI reads `storage.default` from the second, so pressing **Make default** succeeded, wrote the
// config, and moved nothing on screen. Indistinguishable from a no-op, on the one surface a reviewer
// clicks — and **an e2e asserting that re-designation moves the badge would have been green whatever
// the daemon did**, because the demo's answer was a constant. That is the same shape quince#661 cost
// this package once already, one field over, and the provider's own comment records it.

// A bare provider — nobody wired a config in — keeps the fixture's answer. This is the fallback the
// nil check exists for, and every other test in this package constructs exactly this.
func TestABareProviderStillDefaultsToInternal(t *testing.T) {
	p := seededProvider()
	got := map[string]bool{}
	for _, s := range p.Storages("") {
		got[s.Name] = s.Default
	}
	if !got["internal"] || got["shuttle"] {
		t.Errorf("a provider with no config wired in must keep the fixture's default: %+v", got)
	}
}

// THE CLAIM. With a declaration that names the shuttle, the storage LIST says so — which is the
// thing that was false.
func TestTheStorageListFollowsTheDeclaredDefault(t *testing.T) {
	p := seededProvider()
	p.SetDefaultStorageName(func() string { return "shuttle" })

	got := map[string]bool{}
	for _, s := range p.Storages("") {
		got[s.Name] = s.Default
	}
	if got["internal"] || !got["shuttle"] {
		t.Errorf("the list did not follow the declaration: %+v — this is quince#1036 returning, and "+
			"it presents as `Make default` succeeding while the badge does not move", got)
	}
}

// EXACTLY ONE DEFAULT, IN BOTH DIRECTIONS. Computing the flag per entry from one name is what makes
// this structural, and asserting it here is what stops a later edit reintroducing two literals that
// can disagree — two storages both claiming `default` is a state the daemon cannot produce.
func TestExactlyOneStorageIsEverDefault(t *testing.T) {
	for _, name := range []string{"internal", "shuttle"} {
		p := seededProvider()
		p.SetDefaultStorageName(func() string { return name })
		n := 0
		for _, s := range p.Storages("") {
			if s.Default {
				n++
			}
		}
		if n != 1 {
			t.Errorf("declaring %q default produced %d defaults, want exactly 1", name, n)
		}
	}
}

// AND THE OMITTED-STORAGE RESOLUTION FOLLOWS IT TOO. `defaultStorageID` is where a backup with no
// `storage_id` lands, and it reads `Storages()` — so this comes free, and asserting it is what says
// the badge and the destination cannot disagree. A demo where the card says `shuttle` and the job
// goes to `internal` would be a new lie in place of the old one.
func TestAnOmittedStorageResolvesToTheDeclaredDefault(t *testing.T) {
	p := seededProvider()
	if got := p.defaultStorageID(); got != demoStorageInternal {
		t.Fatalf("the fixture default is not internal: %q", got)
	}
	p.SetDefaultStorageName(func() string { return "shuttle" })
	if got := p.defaultStorageID(); got != demoStorageShuttle {
		t.Errorf("an omitted storage_id resolved to %q, want the shuttle — the card and the "+
			"destination disagree, which is worse than the badge never moving", got)
	}
}

// A NAME NOBODY DECLARED FALLS BACK RATHER THAN LEAVING NO DEFAULT. An empty answer means "the
// config declares none", which the daemon refuses long before this — but a provider that returned
// two non-defaults would make `defaultStorageID` fall through to its own literal while the LIST
// showed no default at all, and the two would disagree.
func TestAnUnknownDeclaredNameLeavesTheFixtureDefaultStanding(t *testing.T) {
	p := seededProvider()
	p.SetDefaultStorageName(func() string { return "" })
	n := 0
	for _, s := range p.Storages("") {
		if s.Default {
			n++
		}
	}
	if n != 1 {
		t.Errorf("an empty declared name produced %d defaults, want the fixture's 1", n)
	}
}
