package config

import (
	"os"
	"path/filepath"
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
	// DRIVEN THROUGH THE REAL Load, which is what quince#544 made possible. This test used to
	// `Parse`, then MIRROR the warning Load composes — `Warning{Message: "invalid YAML: " + …}` —
	// and hand that to CheckStorages. So it asserted against its own copy of a string contract:
	// rewording Load's warning would have broken detection in production and left this green, which
	// is the brittleness quince#544 calls the sharper half. A real file through the real Load cannot
	// drift from Load.
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	l := Load(path)
	if l.OK {
		t.Fatal("the old nested shape must fail to parse against the flattened schema")
	}
	if l.Failure == nil || l.Failure.Kind != LoadUnparsable {
		t.Fatalf("Load must report a typed unparsable cause, got %+v", l.Failure)
	}
	cfg := l.Config

	req := CheckStorages(cfg, nil, l.Failure)
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
	// A document that PARSED and merely carries warnings has no LoadFailure, so it cannot read as a
	// parse failure — which is now structural rather than a property of the warning's wording. This
	// case used to be expressed by passing `[]Warning{{Message: "unknown config key"}}` and trusting
	// that it did not happen to start with the magic prefix (quince#544).
	absent := CheckStorages(Default(), nil, nil)
	if absent.Malformed || absent.Unreadable || !absent.Missing {
		t.Errorf("a parsed document with no failure must read as Missing: %+v", absent)
	}
	empty := CheckStorages(withStorages(), nil, nil)
	if empty.Malformed || empty.Unreadable || !empty.Empty {
		t.Errorf("an empty list is still Empty: %+v", empty)
	}
}

// quince#544 — A READ FAILURE IS NOT AN ABSENT KEY, and this is the branch quince#508 did not walk.
//
// It is arguably the more misleading of the two: a parse failure at least means quince READ the
// file, where this can mean quince never saw the operator's config at all — and the old message
// sent them to edit a file the daemon cannot open. A `/data` bind that did not mount produces
// exactly this.
// THE FIXTURE IS A DIRECTORY AT THE CONFIG PATH, AND THAT IS THE REPORTED SHAPE RATHER THAN A
// CONVENIENCE. A bind mount whose source does not exist makes the container runtime create a
// DIRECTORY at the target, so `/data/config.yml` becomes a directory — `Stat` succeeds, `ReadFile`
// fails with EISDIR, and the operator gets "you have no storage" about a config they wrote.
//
// It also runs where a chmod fixture cannot. The first version of this test chmod'ed the file to
// 0000 and had to skip as root — which is how the gates run, so it would have gated NOTHING in CI
// while looking green. Mode bits do not stop root; EISDIR stops everyone.
func TestAReadFailureIsNotReportedAsAnAbsentKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("stage a directory at the config path: %v", err)
	}

	l := Load(path)
	if l.OK {
		t.Fatal("a config that cannot be read must not load as OK")
	}
	if l.Failure == nil || l.Failure.Kind != LoadUnreadable {
		t.Fatalf("Load must report a typed unreadable cause, got %+v", l.Failure)
	}

	req := CheckStorages(l.Config, nil, l.Failure)
	if !req.Unreadable {
		t.Fatal("a read failure must be reported as unreadable")
	}
	if req.Missing {
		t.Error("Missing must be false — quince never saw the file, so it cannot say the key is absent")
	}
	if req.OK() {
		t.Error("a config that could not be read must not be allowed to serve")
	}

	var sb strings.Builder
	err := req.Explain(&sb, "/data/config.yml")
	out := sb.String()
	if strings.Contains(out, "no `storage:` key") {
		t.Errorf("the refusal still claims the key is absent:\n%s", out)
	}
	// AND IT MUST NOT OFFER THE REMEDY BLOCK. "Add this to config.yml, then start again" is advice
	// quince cannot honour when it cannot read the file, and it is what the old fall-through gave.
	if strings.Contains(out, "Add this to") {
		t.Errorf("the refusal tells the operator to edit a file quince cannot read:\n%s", out)
	}
	for _, want := range []string{
		"could not be READ",       // what actually happened
		"permission",              // the class of cause
		"bind that did not mount", // the plausible container mistake
	} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal missing %q:\n%s", want, out)
		}
	}
	if err == nil || strings.Contains(err.Error(), "no storage declared") {
		t.Errorf("the short error repeats the false claim: %v", err)
	}
}

// THE UNREADABLE BRANCH IS REACHED BY A TYPE, NOT BY A FIXTURE, so the assertion above still has a
// counterpart on a box where the chmod cannot bite — which since the gates run as root is every box
// this project actually gates on. Same claim, driven from the cause rather than the disk.
func TestAnUnreadableCauseRefusesAndDoesNotClaimTheKeyIsAbsent(t *testing.T) {
	req := CheckStorages(Default(), nil, &LoadFailure{
		Kind:   LoadUnreadable,
		Detail: "open /data/config.yml: permission denied",
	})
	if !req.Unreadable || req.Missing || req.OK() {
		t.Fatalf("an unreadable cause must refuse and must not read as Missing: %+v", req)
	}
	var sb strings.Builder
	err := req.Explain(&sb, "/data/config.yml")
	out := sb.String()
	if !strings.Contains(out, "permission denied") {
		t.Errorf("the OS's own sentence is lost:\n%s", out)
	}
	if strings.Contains(out, "no `storage:` key") || strings.Contains(out, "Add this to") {
		t.Errorf("the refusal gives absent-key advice for a file quince could not read:\n%s", out)
	}
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("the short error does not name the cause: %v", err)
	}
}

// AND THE TWO CAUSES DO NOT COLLAPSE INTO EACH OTHER. Both refuse, so `OK()` cannot tell them
// apart — the whole point is that the operator is told which one happened.
func TestUnreadableAndMalformedAreDistinguishable(t *testing.T) {
	unread := CheckStorages(Default(), nil, &LoadFailure{Kind: LoadUnreadable, Detail: "i/o error"})
	unparse := CheckStorages(Default(), nil, &LoadFailure{Kind: LoadUnparsable, Detail: "line 3: bad"})

	if unread.Malformed {
		t.Error("a read failure must not report as malformed — quince never got to the parser")
	}
	if unparse.Unreadable {
		t.Error("a parse failure must not report as unreadable — the bytes were read fine")
	}
	var a, b strings.Builder
	_ = unread.Explain(&a, "/data/config.yml")
	_ = unparse.Explain(&b, "/data/config.yml")
	if !strings.Contains(a.String(), "could not be READ") || !strings.Contains(b.String(), "could not be parsed") {
		t.Errorf("the two refusals do not say which one happened:\n--- unreadable ---\n%s\n--- malformed ---\n%s", a.String(), b.String())
	}
}
