package config

import (
	"strings"
	"testing"
)

// These are qn.6c gate G7. They assert the REFUSAL specifically, following
// deploy/runner/preflight-test's posture: a suite that only proved the happy case would prove the
// least interesting thing about it. The failure this guards is a quince that comes up with nowhere
// to put backups and looks healthy, so "does it refuse" and "does it say what to do" are the two
// claims worth testing.

func withStorages(entries ...StorageEntry) Config {
	c := Default()
	e := entries
	c.Storage = ResolveStorages(&e)
	return c
}

func TestCheckStoragesRefusesWhenKeyAbsent(t *testing.T) {
	c := Default() // Default() leaves Storages nil — there is no default storage to have
	req := CheckStorages(c, nil, nil)
	if req.OK() {
		t.Fatal("a config with no storage: key must NOT be allowed to serve")
	}
	if !req.Missing || req.Empty {
		t.Errorf("absent key must read as Missing, not Empty: %+v", req)
	}
}

func TestCheckStoragesRefusesWhenListEmpty(t *testing.T) {
	req := CheckStorages(withStorages(), nil, nil)
	if req.OK() {
		t.Fatal("an empty storage: list must NOT be allowed to serve")
	}
	if req.Missing || !req.Empty {
		t.Errorf("present-but-empty must read as Empty, not Missing: %+v", req)
	}
}

// The absent/empty split is the reason Storages is a pointer. If it collapsed, the two cases would
// be one and the message could not tell a user who never declared a storage from one who declared
// none — different mistakes with the same remedy but different causes.
func TestCheckStoragesDistinguishesAbsentFromEmpty(t *testing.T) {
	absent := CheckStorages(Default(), nil, nil)
	empty := CheckStorages(withStorages(), nil, nil)
	if absent.Missing == empty.Missing {
		t.Fatalf("absent and empty must be distinguishable, both Missing=%v", absent.Missing)
	}
}

func TestCheckStoragesAllowsOneDeclaredStorage(t *testing.T) {
	req := CheckStorages(withStorages(StorageEntry{Name: "local", Path: "/backups", Default: true}), nil, nil)
	if !req.OK() {
		t.Fatalf("one declared storage must be allowed to serve: %+v", req)
	}
}

// The retired variable never CAUSES the refusal — it explains one. A box that upgraded into this
// needs to know why the thing that used to work stopped, and that sentence is the difference
// between a five-minute fix and an hour of confusion.
func TestCheckStoragesReportsRetiredEnvVarButDoesNotRefuseOnIt(t *testing.T) {
	ok := CheckStorages(
		withStorages(StorageEntry{Name: "local", Path: "/backups", Default: true}),
		[]string{"QUINCE_BACKUPS=/backups"},
		nil,
	)
	if !ok.OK() {
		t.Fatal("a retired env var must not by itself stop a correctly-declared config from serving")
	}

	bad := CheckStorages(Default(), []string{"QUINCE_BACKUPS=/srv/backups"}, nil)
	if !bad.LegacyEnv || bad.LegacyEnvValue != "/srv/backups" {
		t.Fatalf("the retired var and its value must be captured for the explanation: %+v", bad)
	}
}

// G7's second half: refusing is not enough — the message must name the key and print the remedy.
// preflight's rule is that an error message is a claim, so every check names what it OBSERVED and
// the exact thing to do about it.
func TestExplainNamesTheKeyAndPrintsTheRemedy(t *testing.T) {
	var sb strings.Builder
	err := CheckStorages(Default(), nil, nil).Explain(&sb, "/data/config.yml")
	if err == nil {
		t.Fatal("Explain must return a non-nil error so main() exits non-zero")
	}
	out := sb.String()
	for _, want := range []string{
		"storage:",          // the key
		"/data/config.yml",  // where to put it
		"- path:",           // the remedy, as YAML the user can paste
		"`name` defaults",   // and the short form the 2026-08-01 ruling made possible
		"REFUSING to start", // and why refusing beats starting
	} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal message must contain %q; got:\n%s", want, out)
		}
	}
}

// A user who had QUINCE_BACKUPS set should be told that is why, and the suggested path should be
// the one they were already using rather than a generic default they have to correct.
func TestExplainEchoesTheRetiredVarAndSuggestsItsPath(t *testing.T) {
	var sb strings.Builder
	_ = CheckStorages(Default(), []string{"QUINCE_BACKUPS=/srv/backups"}, nil).Explain(&sb, "/data/config.yml")
	out := sb.String()
	if !strings.Contains(out, "NO LONGER READ") {
		t.Errorf("must say the variable is no longer read; got:\n%s", out)
	}
	if !strings.Contains(out, "path: /srv/backups") {
		t.Errorf("must suggest the path the user was already using; got:\n%s", out)
	}
}

func TestExplainWritesNothingWhenSatisfied(t *testing.T) {
	var sb strings.Builder
	err := CheckStorages(withStorages(StorageEntry{Name: "local", Path: "/backups", Default: true}), nil, nil).
		Explain(&sb, "/data/config.yml")
	if err != nil || sb.String() != "" {
		t.Errorf("a satisfied requirement must be silent, got err=%v out=%q", err, sb.String())
	}
}

// A PARSE FAILURE IS NOT AN ABSENT KEY (quince#508). Measured against the staging stand's real
// pre-flatten config while rehearsing quince#506's upgrade: the file plainly has a `storage:` key,
// in the OLD shape, and the refusal said there was none.
//
// The cause is nil doing double duty. `Parse` returns `Default()` on a YAML error, `Default()`
// leaves `Storage` nil, and nil is also what a genuinely undeclared file produces — so by the time
// the message was composed the two were indistinguishable.
//
// It matters because this IS the upgrade path: the people who meet it are exactly those who
// upgraded before editing, and it told them to add a key they can see.
func TestAParseFailureIsNotReportedAsAnAbsentKey(t *testing.T) {
	// The exact shape every pre-quince#473 deployment has.
	raw := "storage:\n    storages:\n        - name: local\n          path: /backups\n          default: true\n    backend: zfs\n"
	cfg, _, warnings, perr := Parse([]byte(raw))
	if perr == nil {
		t.Fatal("the old nested shape must fail to parse against the flattened schema")
	}
	// Load composes this warning; mirror it rather than reaching into Load.
	warnings = append(warnings, Warning{Path: "", Message: "invalid YAML: " + perr.Error()})

	req := CheckStorages(cfg, nil, warnings)
	if !req.Malformed {
		t.Fatal("a parse failure must be reported as malformed")
	}
	if req.Missing {
		t.Error("Missing must be false — the key is present, it is the SHAPE that is wrong")
	}
	if req.OK() {
		t.Error("a config that did not parse must not be allowed to serve")
	}
	if !strings.Contains(req.MalformedDetail, "cannot unmarshal") {
		t.Errorf("the parser's own sentence is lost: %q", req.MalformedDetail)
	}

	var sb strings.Builder
	err := req.Explain(&sb, "/data/config.yml")
	out := sb.String()
	if strings.Contains(out, "no `storage:` key") {
		t.Errorf("the refusal still claims the key is absent:\n%s", out)
	}
	for _, want := range []string{
		"could not be parsed", // what actually happened
		"cannot unmarshal",    // the parser's line and type, not thrown away
		"CHANGED SHAPE",       // why a file that used to work stopped
		"upgrading.md",        // where the before/after is
	} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal missing %q:\n%s", want, out)
		}
	}
	if err == nil || strings.Contains(err.Error(), "no storage declared") {
		t.Errorf("the short error repeats the false claim: %v", err)
	}
}

// And the two states it must NOT swallow: a genuinely absent key and a genuinely empty list still
// say what they always said. A third state that ate the other two would be a worse bug.
func TestMalformedDoesNotSwallowAbsentOrEmpty(t *testing.T) {
	absent := CheckStorages(Default(), nil, []Warning{{Path: "x", Message: "unknown config key"}})
	if absent.Malformed || !absent.Missing {
		t.Errorf("an unrelated warning must not read as a parse failure: %+v", absent)
	}
	empty := CheckStorages(withStorages(), nil, nil)
	if empty.Malformed || !empty.Empty {
		t.Errorf("an empty list is still Empty: %+v", empty)
	}
}
