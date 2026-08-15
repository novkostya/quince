package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/wire"
)

// qn.6e PR 5 — POST /api/config/storage, Forget's mirror.
//
// config.AddStorage's own rules are gated in internal/config, INCLUDING the splice-keeps-siblings
// claim, which is asserted there on the file itself. What is proven here is the seam: the route
// exists and is reachable, a refusal is a 422 in the shared shape carrying the CALLER'S field, a
// success returns the config-endpoint body, and the route is behind the auth guard.

// oneStorage is seedStorages' single-entry counterpart to twoStorages, carrying the same fully
// specified shape a client sends.
const oneStorage = `[{"name":"one","path":"/backups-a","default":true,"backend":"reflink",` +
	`"zfs":{"parent_dataset":"","mode":"hook","hook_cmd":"","ssh_user":"","ssh_host":"","ssh_port":0,"ssh_key":"","seed":"auto"},` +
	`"retention":{"keep_recent":10,"keep_daily":30,"keep_weekly":12}}]`

func addStorage(t *testing.T, srv *httptest.Server, c *http.Client, body string) (int, []byte) {
	t.Helper()
	req := newReq(t, http.MethodPost, srv.URL+"/api/config/storage", body)
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

func storageNames(t *testing.T, srv *httptest.Server, c *http.Client) []string {
	t.Helper()
	req := newReq(t, http.MethodGet, srv.URL+"/api/config", "")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Config struct {
			Storage []struct {
				Name    string `json:"name"`
				Backend string `json:"backend"`
			} `json:"storage"`
		} `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(out.Config.Storage))
	for _, s := range out.Config.Storage {
		names = append(names, s.Name)
	}
	return names
}

func TestAddStorageEndpointAddsAndReturnsTheConfigBody(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, oneStorage)

	code, body := addStorage(t, srv, c, `{"name":"second","path":"/backups-b","backend":"reflink"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", code, body)
	}

	// THE CONFIG-ENDPOINT BODY, not a 201 with the entry: this is a config mutation, and the client
	// re-renders from the same payload GET, PUT and DELETE hand it.
	var out struct {
		Config   map[string]any `json:"config"`
		Warnings []any          `json:"warnings"`
		Source   map[string]any `json:"source"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding the 200: %v — %s", err, body)
	}
	if out.Config == nil || out.Source == nil {
		t.Fatalf("want the {config, warnings, source} body, got: %s", body)
	}

	got := storageNames(t, srv, c)
	if len(got) != 2 || got[0] != "one" || got[1] != "second" {
		t.Fatalf("storages after the add = %v, want [one second]", got)
	}
}

// A REFUSAL NAMES THE CALLER'S FIELD, not an index in the merged list. A client adding one entry
// cannot map `storage[3].path` back to the box the user typed into.
func TestAddStorageEndpointRefusesAtTheCallersField(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, oneStorage)

	for _, tc := range []struct{ name, body, wantPath string }{
		{"duplicate name", `{"name":"one","path":"/backups-b","backend":"copy"}`, "name"},
		{"duplicate path", `{"name":"two","path":"/backups-a","backend":"copy"}`, "path"},
		{"relative path", `{"name":"two","path":"rel/dir","backend":"copy"}`, "path"},
		{"no backend", `{"name":"two","path":"/backups-b"}`, "backend"},
		{"claims default", `{"name":"two","path":"/backups-b","backend":"copy","default":true}`, "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := addStorage(t, srv, c, tc.body)
			if code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", code, body)
			}
			var out struct {
				Errors []wire.ConfigError `json:"errors"`
			}
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Errors) == 0 || out.Errors[0].Path != tc.wantPath {
				t.Fatalf("422 must name the caller's field; got %+v, want %q", out.Errors, tc.wantPath)
			}
		})
	}

	// AND NOTHING WAS ADDED by any of them. Asserted after the whole table rather than per case,
	// because the failure this catches is cumulative: a refusal that still writes.
	if got := storageNames(t, srv, c); len(got) != 1 {
		t.Fatalf("a REFUSED add still changed the config: %v", got)
	}
}

// Behind the auth guard, like every other config mutation.
func TestAddStorageEndpointIsNotAuthExempt(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	req := newReq(t, http.MethodPost, srv.URL+"/api/config/storage",
		`{"name":"two","path":"/backups-b","backend":"copy"}`)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("an UNAUTHENTICATED add got 200 — this writes config and must never be auth-exempt")
	}
}

// quince#1038 — ADD IS WHAT COMMITS THE KEY, and the fingerprint is what makes that safe.
//
// The two-parents-two-keys property quince#989 established is unchanged; what moved is WHEN it
// becomes true. Asking about a dataset no longer writes anything, so both storages are shown the one
// pending key while they are being filled in, and each gets its own only when it is added.
func TestAddingAZFSStorageCommitsThePendingKey(t *testing.T) {
	deps := testDeps(t)
	deps.ZFSKeyDir = t.TempDir()
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	shown := decodeZFSKeyFor(t, c, srv, "tank/one")
	if !shown.Pending {
		t.Fatal("the key was not pending before the storage was added")
	}

	entry := `{"name":"one","path":"/backups-a","backend":"zfs","zfs":{"parent_dataset":"tank/one",` +
		`"mode":"hook","ssh_user":"quince","ssh_host":"nas","seed":"auto"},` +
		`"zfs_key_fingerprint":"` + shown.Fingerprint + `"}`
	if code, body := addStorage(t, srv, c, entry); code != http.StatusOK {
		t.Fatalf("add = %d, want 200: %s", code, body)
	}

	// THE KEY IS NOW UNDER `zfs-*` — which is the whole property: that directory names exactly the
	// storages quince committed to.
	landed := config.ZFSKeyPathIn(deps.ZFSKeyDir, "tank/one")
	if _, err := os.Stat(landed); err != nil {
		t.Fatalf("the key did not land at %s: %v", landed, err)
	}
	if _, err := os.Stat(filepath.Join(deps.ZFSKeyDir, storage.PendingKeyName)); !os.IsNotExist(err) {
		t.Error("the pending key survived the add — it is a rename, so nothing is left behind")
	}

	// AND THE NEXT DATASET GETS A FRESH PENDING KEY, so two committed storages end up with two keys.
	next := decodeZFSKeyFor(t, c, srv, "tank/two")
	if next.Fingerprint == shown.Fingerprint {
		t.Error("the second storage was shown the key the first one just committed")
	}
	// The one that was committed answers as committed, not pending.
	first := decodeZFSKeyFor(t, c, srv, "tank/one")
	if first.Pending || first.Created || first.Fingerprint != shown.Fingerprint {
		t.Errorf("after the add: pending=%v created=%v same-key=%v",
			first.Pending, first.Created, first.Fingerprint == shown.Fingerprint)
	}
}

// THE TWO-TAB REFUSAL, at the endpoint: a 422 naming a field, not a silent second key.
//
// Tab A and tab B are both shown the pending key for different datasets, and both operators paste
// their line on the host. Tab A adds; the key moves. Tab B adds — and quince must say so, because
// the line tab B pasted is for a key it no longer holds.
func TestAddingAZFSStorageRefusesAStaleKeyFingerprint(t *testing.T) {
	deps := testDeps(t)
	deps.ZFSKeyDir = t.TempDir()
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	shown := decodeZFSKeyFor(t, c, srv, "tank/one") // what both tabs were shown

	entryA := `{"name":"one","path":"/backups-a","backend":"zfs","zfs":{"parent_dataset":"tank/one",` +
		`"mode":"hook","ssh_user":"quince","ssh_host":"nas","seed":"auto"},` +
		`"zfs_key_fingerprint":"` + shown.Fingerprint + `"}`
	if code, body := addStorage(t, srv, c, entryA); code != http.StatusOK {
		t.Fatalf("tab A's add = %d, want 200: %s", code, body)
	}

	entryB := `{"name":"two","path":"/backups-b","backend":"zfs","zfs":{"parent_dataset":"tank/two",` +
		`"mode":"hook","ssh_user":"quince","ssh_host":"nas","seed":"auto"},` +
		`"zfs_key_fingerprint":"` + shown.Fingerprint + `"}`
	code, body := addStorage(t, srv, c, entryB)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("tab B's add = %d, want 422 — otherwise quince makes a second key and the line tab B "+
			"pasted authenticates nothing: %s", code, body)
	}
	// THE SENTENCE HAS TO BE ACTIONABLE. The remedy is *read the key again and paste the new line*,
	// and a refusal that only says "conflict" leaves the operator with a storage they cannot add.
	for _, want := range []string{"not the one quince holds", "paste"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the refusal does not carry %q: %s", want, body)
		}
	}
	if _, err := os.Stat(config.ZFSKeyPathIn(deps.ZFSKeyDir, "tank/two")); err == nil {
		t.Error("a key was written for tab B's dataset despite the refusal")
	}
}

// AN EXPLICIT `ssh_key` IS THE OPERATOR'S OWN, AND NOTHING HERE TOUCHES IT. It is the escape hatch
// for a key already deployed, so an add carrying one must not need a fingerprint and must not move
// the pending key.
func TestAddingAZFSStorageWithAnExplicitKeyCommitsNothing(t *testing.T) {
	deps := testDeps(t)
	deps.ZFSKeyDir = t.TempDir()
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	_ = decodeZFSKeyFor(t, c, srv, "tank/one") // a pending key exists

	entry := `{"name":"one","path":"/backups-a","backend":"zfs","zfs":{"parent_dataset":"tank/one",` +
		`"mode":"hook","ssh_user":"quince","ssh_host":"nas","ssh_key":"/data/keys/mine","seed":"auto"}}`
	if code, body := addStorage(t, srv, c, entry); code != http.StatusOK {
		t.Fatalf("add = %d, want 200 — an explicit ssh_key needs no fingerprint: %s", code, body)
	}
	if _, err := os.Stat(config.ZFSKeyPathIn(deps.ZFSKeyDir, "tank/one")); err == nil {
		t.Error("quince committed a key for a storage that brought its own")
	}
	if _, err := os.Stat(filepath.Join(deps.ZFSKeyDir, storage.PendingKeyName)); err != nil {
		t.Error("the pending key was consumed by a storage that brought its own key")
	}
}
