package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// quince#722 — `SetDefaultStorage` at the package boundary. The route's own behaviour is asserted
// in `httpapi`, beside the forget it mirrors; what lives here is what the boundary cannot reach.

// A SERVICE WITH NO `storage:` KEY AT ALL answers "no such storage", not a panic and not a 200.
//
// UNREACHABLE THROUGH THE ROUTE, which is exactly why it is tested here. `RequireStorage` and the
// setup guard both stand between a zero-storage install and this endpoint, so the handler tests
// cannot construct the state — but a fresh install genuinely has no `storage:` key (it is a
// pointer so that absent differs from empty, `schema.go`), and a nil deref on the first line of a
// mutation is not a thing to leave to two guards in another package.
func TestSetDefaultOnAConfigWithNoStorageKeyAnswersNoSuchStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	svc := NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil)))

	outcome, errs, _, err := svc.SetDefaultStorage("anything")
	if err != nil {
		t.Fatalf("a missing storage key is an answer, not a failure: %v", err)
	}
	if outcome != SetDefaultNoSuchStorage {
		t.Errorf("outcome = %v, want SetDefaultNoSuchStorage", outcome)
	}
	if len(errs) != 0 {
		t.Errorf("404 is a fact about the request, not a refusal with a remedy: %+v", errs)
	}
}

// THE FILE ON DISK CARRIES THE MOVE, and the order of its entries is untouched.
//
// Asserted against the YAML rather than the in-memory snapshot because that file is a documented
// hand-editable surface (D12): the ruling's promise is that re-designation becomes ONE edit — move
// the flag — and the only way to show the product agrees is to read what it wrote.
func TestSetDefaultRewritesTheFileWithoutReorderingIt(t *testing.T) {
	svc, path := forgetSvc(t,
		StorageEntry{Name: "pool", Path: "/backups", Default: true},
		StorageEntry{Name: "shuttle", Path: "/mnt/shuttle"},
	)

	outcome, errs, _, err := svc.SetDefaultStorage("shuttle")
	if err != nil || outcome != SetDefaultDone {
		t.Fatalf("SetDefaultStorage = %v errs=%+v err=%v, want SetDefaultDone", outcome, errs, err)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // the path is this test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	var onDisk struct {
		Storage []StorageEntry `yaml:"storage"`
	}
	if err := yaml.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("the written config is not parseable: %v\n%s", err, raw)
	}
	if len(onDisk.Storage) != 2 {
		t.Fatalf("both storages must survive; got %+v", onDisk.Storage)
	}
	if onDisk.Storage[0].Name != "pool" || onDisk.Storage[1].Name != "shuttle" {
		t.Errorf("file order must be untouched — want [pool shuttle], got %v",
			storageNames(Config{Storage: &onDisk.Storage}))
	}
	if onDisk.Storage[0].Default || !onDisk.Storage[1].Default {
		t.Errorf("the flag must sit on shuttle alone; got pool=%v shuttle=%v",
			onDisk.Storage[0].Default, onDisk.Storage[1].Default)
	}
}

// THE SNAPSHOT OTHER READERS HOLD IS NOT MUTATED BEHIND THEM.
//
// `Current()` hands back a Config whose `Storage` pointer aliases the live one, so a splice that
// wrote through it would rewrite a caller's view — and would do it BEFORE the write, leaving the
// process serving a declaration that is on no disk if the write then failed. `ForgetStorage` copies
// for the same reason; this asserts the copy rather than trusting the comment.
func TestSetDefaultDoesNotMutateASnapshotTakenBeforeIt(t *testing.T) {
	svc, _ := forgetSvc(t,
		StorageEntry{Name: "pool", Path: "/backups", Default: true},
		StorageEntry{Name: "shuttle", Path: "/mnt/shuttle"},
	)

	before := svc.Current()
	if before.Storage == nil {
		t.Fatal("precondition: the seeded config must declare storages")
	}
	if _, _, _, err := svc.SetDefaultStorage("shuttle"); err != nil {
		t.Fatal(err)
	}

	if !(*before.Storage)[0].Default {
		t.Error("a snapshot taken before the call must still say pool is default — the splice must " +
			"build a new slice rather than writing through the aliased pointer")
	}
	if (*before.Storage)[1].Default {
		t.Error("the pre-call snapshot must not have gained shuttle's flag")
	}
}

// THE WRITE LOG NAMES THIS DOOR, not `PUT /api/config` (quince#967's rule, applied to a new route).
// A narrow write attributed to the full-document replace is the exact misattribution that constant
// set exists to prevent.
func TestSetDefaultIsAttributedToItsOwnRoute(t *testing.T) {
	if !strings.Contains(SourceSetDefaultStorage, "/default") {
		t.Errorf("SourceSetDefaultStorage = %q, want the route it names", SourceSetDefaultStorage)
	}
	for _, other := range []string{SourcePutConfig, SourceAddStorage, SourceForgetStorage} {
		if SourceSetDefaultStorage == other {
			t.Errorf("this door must be distinguishable from %q", other)
		}
	}
}
