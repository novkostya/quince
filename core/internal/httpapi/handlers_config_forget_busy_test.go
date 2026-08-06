package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/wire"
)

// qn.6g G5 — a forget is REFUSED while a backup is running on that storage (Operator ruling
// 2026-08-06, quince#577, option (b)).
//
// THE FIRST 422 ON THIS ENDPOINT ABOUT LIVENESS. Every other refusal here answers *is this a valid
// set of storages?*; this one answers *is quince busy?*. That is a new kind, written into contracts
// §1 as a decision, so these tests pin what a reader of that contract needs: the status and shape
// match the existing refusals, the message carries the remedy, and a refused forget writes nothing.
//
// `Manager.JobsOn`'s own guards — an empty storage id, deterministic ordering — are asserted in
// `internal/storage`, where the binding map lives. Here the claim is only about the HANDLER: that
// it joins name → id correctly and refuses on the answer.

// busyStorages is a StorageReader with a name → id declaration and an independent id → jobs map.
//
// THE TWO ARE SEPARATE ON PURPOSE. The route is keyed by NAME and the binding map by STORAGE_ID, so
// the handler has to join them; a fake that answered `JobsOn` for any argument would let a handler
// that skipped the join pass every test below.
type busyStorages struct {
	name    string
	id      string
	jobsByI map[string][]string
	askedID string // what JobsOn was actually called with
}

func (b *busyStorages) Storages(string) []wire.Storage {
	return []wire.Storage{{ID: b.id, Name: b.name, Path: "/backups", Reachable: true}}
}
func (b *busyStorages) Recheck(string) (wire.Storage, bool) { return wire.Storage{}, false }
func (b *busyStorages) JobsOn(storageID string) []string {
	b.askedID = storageID
	return b.jobsByI[storageID]
}

func busyServer(t *testing.T, s *busyStorages) (*httptest.Server, *http.Client) {
	t.Helper()
	d := testDeps(t)
	d.Storages = s
	srv := httptest.NewServer(NewRouter(d))
	t.Cleanup(srv.Close)
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)
	return srv, c
}

// The refusal itself: 422, the config-error shape, and the JOB NAMED.
//
// Naming the job is not cosmetic — it is the whole remedy. "Something is running" leaves a user
// with no way to find what, on a page that lists neither. The message has to carry the id they can
// look up and cancel.
func TestForgetIsRefusedWhileABackupRunsOnThatStorage(t *testing.T) {
	fake := &busyStorages{name: "shuttle", id: "01STORAGESHUTTLE",
		jobsByI: map[string][]string{"01STORAGESHUTTLE": {"01JOBRUNNING"}}}
	srv, c := busyServer(t, fake)

	code, body := deleteStorage(t, srv, c, "shuttle")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("DELETE a storage with a job on it = %d, want 422: %s", code, body)
	}

	var got struct {
		Errors []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("a liveness refusal must use the SAME config-error shape as every other refusal "+
			"on this endpoint, or a client renders one and not the other: %v: %s", err, body)
	}
	if len(got.Errors) != 1 || got.Errors[0].Path != "storage" {
		t.Fatalf("want one error at storage:, got %+v", got.Errors)
	}
	msg := got.Errors[0].Message
	for _, want := range []string{"shuttle", "01JOBRUNNING"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must name %q — a user who cannot find the job cannot clear the "+
				"condition; got %q", want, msg)
		}
	}
	if fake.askedID != "01STORAGESHUTTLE" {
		t.Errorf("JobsOn was asked about %q, want the storage's ID — the route is keyed by NAME and "+
			"the binding map by storage_id, so a handler that skipped the join asks about the wrong "+
			"thing and always finds nothing", fake.askedID)
	}
}

// AND IT WROTE NOTHING — the half a status code cannot show.
//
// Sharper here than on the other refusals on this endpoint: the applier is wired, so a refusal that
// had already written the file would have stopped serving the storage in the same breath as
// reporting that it would not.
func TestARefusedBusyForgetLeavesTheConfigUnchanged(t *testing.T) {
	fake := &busyStorages{name: "shuttle", id: "01STORAGESHUTTLE",
		jobsByI: map[string][]string{"01STORAGESHUTTLE": {"01JOBRUNNING"}}}
	srv, c := busyServer(t, fake)

	if code, body := deleteStorage(t, srv, c, "shuttle"); code != http.StatusUnprocessableEntity {
		t.Fatalf("DELETE = %d, want 422: %s", code, body)
	}

	req := newReq(t, http.MethodGet, srv.URL+"/api/config", "")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var after struct {
		Config config.Config `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&after); err != nil {
		t.Fatal(err)
	}
	if after.Config.Storage == nil || len(*after.Config.Storage) != 2 {
		t.Fatalf("a refused forget must leave both storages declared, got %+v", after.Config.Storage)
	}
}

// THE OTHER DIRECTION, and it decides whether this guard is usable at all: a job running on a
// DIFFERENT storage does not block this one.
//
// A check that refused whenever any job existed anywhere would pass both tests above and make the
// button permanently dead on a busy install. That mistake is staged here rather than described: the
// fake has a job, on another id.
func TestForgetSucceedsWhenTheRunningJobIsOnAnotherStorage(t *testing.T) {
	fake := &busyStorages{name: "shuttle", id: "01STORAGESHUTTLE",
		jobsByI: map[string][]string{"01STORAGEPOOL": {"01JOBRUNNING"}}}
	srv, c := busyServer(t, fake)

	if code, body := deleteStorage(t, srv, c, "shuttle"); code != http.StatusOK {
		t.Fatalf("DELETE a storage with no job of its own = %d, want 200 — the job is bound to "+
			"another storage and says nothing about this one: %s", code, body)
	}
}

// THE ORDER: A PERMANENT REFUSAL OUTRANKS A TRANSIENT ONE, and this is the test for the bug that
// shipped in the first version of this PR.
//
// `pool` is the default AND has a backup running on it — which is the ORDINARY state, not a corner:
// the default storage is where backups go. The first implementation ran the liveness check before
// the declaration check, so the user was told *"wait for it to finish, or cancel it"*. They wait an
// hour, retry, and are then told *"it is the default"* — a remedy that was never going to work.
//
// **Every Go gate passed on the wrong order.** `story8` caught it on the first CI run that
// dispatched, because `--demo` keeps a job running on `internal`, which is also its default. This
// test exists so the next reader does not need a browser and a running daemon to find it again.
func TestTheDefaultRefusalOutranksTheBusyRefusal(t *testing.T) {
	fake := &busyStorages{name: "pool", id: "01STORAGEPOOL",
		jobsByI: map[string][]string{"01STORAGEPOOL": {"01JOBRUNNING"}}}
	srv, c := busyServer(t, fake)

	code, body := deleteStorage(t, srv, c, "pool")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("DELETE the default storage = %d, want 422: %s", code, body)
	}

	var got struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(body, &got); err != nil || len(got.Errors) != 1 {
		t.Fatalf("want one config error: %v: %s", err, body)
	}
	msg := got.Errors[0].Message

	if !strings.Contains(msg, "is the default") {
		t.Errorf("a storage that is BOTH default and busy must be refused for being the DEFAULT — "+
			"the permanent reason. Got %q", msg)
	}
	if strings.Contains(msg, "wait for it to finish") {
		t.Errorf("the transient remedy must not be offered when a permanent refusal also applies: "+
			"waiting out the backup cannot make this forget succeed. Got %q", msg)
	}
}

// AND THE BUSY REFUSAL STILL FIRES when the declaration would otherwise allow the forget — the
// other half of the ordering, without which the reorder above could be implemented by deleting the
// liveness check entirely and every test would still pass.
//
// `TestForgetIsRefusedWhileABackupRunsOnThatStorage` covers this, on a NON-default storage. Named
// here so the pair is findable together rather than by luck.

// A storage the reader does not list at all still reaches `ForgetStorage`, which owns the 404.
//
// The liveness check must be a REFUSAL it can add, never a gate it can close: a name absent from
// the storage list has no id to join on, and answering 404 there would move the not-found decision
// out of the config service that holds the declaration and into a reader that holds the runtime.
func TestAStorageTheReaderDoesNotListStillReachesTheForgetPath(t *testing.T) {
	fake := &busyStorages{name: "nothing-like-it", id: "01STORAGEOTHER",
		jobsByI: map[string][]string{"01STORAGEOTHER": {"01JOBRUNNING"}}}
	srv, c := busyServer(t, fake)

	if code, body := deleteStorage(t, srv, c, "shuttle"); code != http.StatusOK {
		t.Fatalf("DELETE a declared storage the runtime does not list = %d, want 200 — the "+
			"declaration is what a forget edits: %s", code, body)
	}
	if code, body := deleteStorage(t, srv, c, "nosuch"); code != http.StatusNotFound {
		t.Fatalf("DELETE an undeclared name = %d, want 404 — the liveness check must not swallow "+
			"the not-found answer: %s", code, body)
	}
}
