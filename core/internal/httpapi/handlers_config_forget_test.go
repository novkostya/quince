package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/config"
)

// seedStorages puts a known declaration in place through the public PUT, so these tests exercise
// the same document a client would have sent rather than reaching into the service.
func seedStorages(t *testing.T, srv *httptest.Server, c *http.Client, storageJSON string) {
	t.Helper()
	body := `{"backup":{"preferred_transport":"usb","require_encryption":true},` +
		`"storage":` + storageJSON + `,` +
		`"devices":{"manage_muxer":true,"usbmuxd_socket":"/var/run/usbmuxd","netmuxd_addr":"127.0.0.1:27015"},` +
		`"sessions":{"ttl_minutes":30},"automation":{"staleness_days":3,"reminder_cooldown_hours":24},` +
		`"ui":{"theme":"system"}}`
	req := newReq(t, http.MethodPut, srv.URL+"/api/config", body)
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	if code := doStatus(t, c, req); code != http.StatusOK {
		t.Fatalf("seeding the config = %d, want 200", code)
	}
}

const twoStorages = `[{"name":"pool","path":"/backups","default":true,"backend":"auto",` +
	`"zfs":{"parent_dataset":"","mode":"exec","hook_cmd":"","seed":"auto"},` +
	`"retention":{"keep_recent":10,"keep_daily":30,"keep_weekly":12}},` +
	`{"name":"shuttle","path":"/mnt/shuttle","default":false,"backend":"auto",` +
	`"zfs":{"parent_dataset":"","mode":"exec","hook_cmd":"","seed":"auto"},` +
	`"retention":{"keep_recent":10,"keep_daily":30,"keep_weekly":12}}]`

func deleteStorage(t *testing.T, srv *httptest.Server, c *http.Client, name string) (int, []byte) {
	t.Helper()
	req := newReq(t, http.MethodDelete, srv.URL+"/api/config/storage/"+name, "")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

// The happy path AT THE BOUNDARY: 200, the config-endpoint body, and the restart SURFACED.
//
// The warning is the half worth pinning. Gap B ruled the restart is never silent, and the only
// thing standing between a 200 and a card that lingers with no explanation is this notice riding
// the `warnings` channel the UI already renders.
func TestForgetStorageReturnsTheConfigAndSurfacesTheRestart(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	code, body := deleteStorage(t, srv, c, "shuttle")
	if code != http.StatusOK {
		t.Fatalf("DELETE a non-default storage = %d, want 200: %s", code, body)
	}

	var got struct {
		Config   config.Config    `json:"config"`
		Warnings []config.Warning `json:"warnings"`
		Source   config.Source    `json:"source"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("response is not the config shape: %v: %s", err, body)
	}

	if got.Config.Storage == nil || len(*got.Config.Storage) != 1 {
		t.Fatalf("the body must carry the NEW config; got %+v", got.Config.Storage)
	}
	if (*got.Config.Storage)[0].Name != "pool" {
		t.Errorf("the survivor must be pool, got %q", (*got.Config.Storage)[0].Name)
	}
	if got.Source.Path == "" {
		t.Error("the body must carry `source`, like every other config endpoint")
	}

	var found bool
	for _, w := range got.Warnings {
		if strings.Contains(w.Message, "shuttle") && strings.Contains(strings.ToLower(w.Message), "restart") {
			found = true
		}
	}
	if !found {
		t.Errorf("the restart must be SURFACED, never silent — no warning names the storage and "+
			"the restart; got %+v", got.Warnings)
	}
}

// 422, and the body is the same {errors:[{path,message}]} shape a PUT refusal returns — so a
// client that already renders config errors renders this one without learning a second shape.
func TestForgetStorageRefusesTheDefaultAtTheBoundary(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	code, body := deleteStorage(t, srv, c, "pool")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("DELETE the default storage = %d, want 422: %s", code, body)
	}

	var got struct {
		Errors []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("a refusal must use the config-error shape: %v: %s", err, body)
	}
	if len(got.Errors) != 1 || got.Errors[0].Path != "storage" {
		t.Fatalf("want one error at storage:, got %+v", got.Errors)
	}
	if !strings.Contains(got.Errors[0].Message, "pool") {
		t.Errorf("the refusal must name the storage, got %q", got.Errors[0].Message)
	}

	// AND IT CHANGED NOTHING. A 422 that had already written the file would leave the user's
	// config edited by a request that reported failure.
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
		t.Errorf("a refused Forget must leave both storages declared, got %+v", after.Config.Storage)
	}
}

func TestForgetStorageUnknownNameIs404(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	if code, body := deleteStorage(t, srv, c, "nosuch"); code != http.StatusNotFound {
		t.Fatalf("DELETE an undeclared storage = %d, want 404: %s", code, body)
	}
}

// The single-storage case reaches the SAME rule, and this is the one a real user meets: one disk,
// one card, one Forget button. It must not be a 500 or a silent success that leaves a daemon
// which refuses to start at the next restart (qn.6c G7).
func TestForgetStorageRefusesTheOnlyStorage(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, `[{"name":"pool","path":"/backups","default":true,"backend":"auto",`+
		`"zfs":{"parent_dataset":"","mode":"exec","hook_cmd":"","seed":"auto"},`+
		`"retention":{"keep_recent":10,"keep_daily":30,"keep_weekly":12}}]`)

	code, body := deleteStorage(t, srv, c, "pool")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("DELETE the only storage = %d, want 422: %s", code, body)
	}
	if !strings.Contains(string(body), "only storage") {
		t.Errorf("the refusal must explain that it is the only storage rather than offering a "+
			"remedy that assumes a second one; got %s", body)
	}
}

// A state-changing endpoint is behind CSRF like every other one. Cheap to assert, and the cost of
// it being wrong is a cross-site request that edits someone's storage declaration.
func TestForgetStorageRequiresCSRF(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	req := newReq(t, http.MethodDelete, srv.URL+"/api/config/storage/shuttle", "")
	if code := doStatus(t, c, req); code != http.StatusForbidden {
		t.Fatalf("DELETE without CSRF = %d, want 403", code)
	}
}
