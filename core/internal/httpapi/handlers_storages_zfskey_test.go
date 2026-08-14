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
	"github.com/novkostya/quince/core/internal/wire"
)

// quince#818 piece B — POST /api/storages/zfs/key. quince#985 gave it a body: the dataset the key
// is confined to, which now rides in the forced command on the line this endpoint returns.

// testParent is the dataset these tests confine the key to. Any legal ZFS name will do; what the
// endpoint does with it is put it inside `command="…"`.
const testParent = "tank/backups"

func zfsKeyReqFor(t *testing.T, srv *httptest.Server, c *http.Client, parent string) *http.Request {
	t.Helper()
	body, err := json.Marshal(wire.StorageZFSKeyRequest{ParentDataset: parent})
	if err != nil {
		t.Fatal(err)
	}
	req := newReq(t, http.MethodPost, srv.URL+"/api/storages/zfs/key", string(body))
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	return req
}

func zfsKeyReq(t *testing.T, srv *httptest.Server, c *http.Client) *http.Request {
	t.Helper()
	return zfsKeyReqFor(t, srv, c, testParent)
}

// THE HAPPY PATH: quince makes a key and hands back what the operator must paste. `created` is on
// the wire because the form has to say *made you one* rather than *found yours*.
func TestZFSKeyEndpointGeneratesAndReturnsThePasteableLine(t *testing.T) {
	deps := testDeps(t)
	dir := t.TempDir()
	deps.ZFSKeyPath = filepath.Join(dir, "keys", "zfs")
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	resp, err := c.Do(zfsKeyReq(t, srv, c))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var out wire.StorageZFSKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.Key.Created {
		t.Fatalf("created = false for a key that did not exist")
	}
	if !strings.HasPrefix(out.Key.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("public_key = %q", out.Key.PublicKey)
	}
	// THE FORCED COMMAND IS THE POINT. A naked key pasted into authorized_keys is an unconstrained
	// shell login on the operator's storage host.
	if !strings.HasPrefix(out.Key.AuthorizedKeys, `command="`) {
		t.Fatalf("authorized_keys does not lead with the forced command: %q", out.Key.AuthorizedKeys)
	}
	// AND SINCE quince#985 IT CARRIES THE DATASET, inside the same quotes. That word is the whole of
	// this key's confinement — the helper script it points at is identical on every install — so a
	// line served without it bounds the key to nothing.
	if !strings.Contains(out.Key.AuthorizedKeys, ` `+testParent+`"`) {
		t.Fatalf("authorized_keys does not confine the key to %q: %q", testParent, out.Key.AuthorizedKeys)
	}
	if out.Key.Path != deps.ZFSKeyPath {
		t.Fatalf("path = %q, want %q", out.Key.Path, deps.ZFSKeyPath)
	}
}

// THE PRIVATE HALF MUST NEVER REACH THE WIRE. Asserted against the actual bytes on disk rather than
// against a field list, so adding a field cannot quietly start publishing it.
func TestZFSKeyEndpointNeverSerialisesThePrivateHalf(t *testing.T) {
	deps := testDeps(t)
	deps.ZFSKeyPath = filepath.Join(t.TempDir(), "zfs")
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	resp, err := c.Do(zfsKeyReq(t, srv, c))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(rawBody)

	raw, err := os.ReadFile(deps.ZFSKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.NewReplacer("-----BEGIN OPENSSH PRIVATE KEY-----", "",
		"-----END OPENSSH PRIVATE KEY-----", "", "\n", "").Replace(string(raw))
	if len(secret) < 32 {
		t.Fatalf("could not isolate the private body; this test would pass vacuously")
	}
	if strings.Contains(body, secret) {
		t.Fatalf("the response carries the private key")
	}
}

// A SECOND PRESS FINDS THE FIRST KEY. The form's *"quince found your existing key"* rests on this,
// and so does not breaking a host whose authorized_keys already carries the old public half.
func TestZFSKeyEndpointIsIdempotentAndSaysSo(t *testing.T) {
	deps := testDeps(t)
	deps.ZFSKeyPath = filepath.Join(t.TempDir(), "zfs")
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	first := decodeZFSKey(t, c, srv)
	second := decodeZFSKey(t, c, srv)

	if !first.Created {
		t.Fatalf("the first press did not create")
	}
	if second.Created {
		t.Fatalf("created = true on the second press — the form would offer to overwrite a live key")
	}
	if second.PublicKey != first.PublicKey {
		t.Fatalf("the key changed between presses:\n%q\n%q", first.PublicKey, second.PublicKey)
	}
}

// NOT AUTH-EXEMPT. It writes into quince's own volume, so an anonymous caller must not reach it —
// the exempt set is five literal method+path strings and this is not one of them.
func TestZFSKeyEndpointIsNotAuthExempt(t *testing.T) {
	deps := testDeps(t)
	deps.ZFSKeyPath = filepath.Join(t.TempDir(), "zfs")
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	resp, err := srv.Client().Post(srv.URL+"/api/storages/zfs/key", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", resp.StatusCode)
	}
	if _, err := os.Stat(deps.ZFSKeyPath); err == nil {
		t.Fatalf("an anonymous request created a key")
	}
}

func decodeZFSKey(t *testing.T, c *http.Client, srv *httptest.Server) wire.StorageZFSKey {
	t.Helper()
	resp, err := c.Do(zfsKeyReq(t, srv, c))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out wire.StorageZFSKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Key
}

// A DATASET NAME THAT COULD BREAK THE LINE IS A 422, NOT A 500 AND NOT A RENDER — moved here from
// the helper endpoint by quince#985, because this is where the value is now interpolated.
//
// It lands inside `command="…"` in a file SSHD PARSES. A `"` closes the option value and what
// follows is read as further options, so `tank" no-pty="` is not a syntax error, it is a different
// set of restrictions on a key that still works. Refusing is right rather than escaping — every
// legal ZFS name already passes, so nothing valid is lost.
//
// AND NO KEY IS WRITTEN. A refusal that generated first would leave a keypair on disk whose public
// half nobody was ever shown, which is exactly the artifact `EnsureZFSKey` refuses to overwrite
// later.
func TestZFSKeyEndpointRefusesAnUnsafeParent(t *testing.T) {
	for _, bad := range []string{
		`tank" no-pty="`,
		`tank",pty,command="/bin/sh`,
		"tank/backups; id",
		"tank/backups $(id)",
		"",
	} {
		deps := testDeps(t)
		deps.ZFSKeyPath = filepath.Join(t.TempDir(), "zfs")
		srv := httptest.NewServer(NewRouter(deps))
		c := authedClient(t, srv)

		resp, err := c.Do(zfsKeyReqFor(t, srv, c, bad))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("parent_dataset=%q → %d, want 422", bad, resp.StatusCode)
		}
		_ = resp.Body.Close()
		if _, err := os.Stat(deps.ZFSKeyPath); err == nil {
			t.Errorf("parent_dataset=%q was refused AFTER writing a key", bad)
		}
		srv.Close()
	}
}

// THE REFUSAL NAMES THE FIELD THE OPERATOR FILLED IN, so the form can put the message where they are
// looking. A 422 whose path points at some other field sends them to re-check something correct —
// the defect quince#865 was reviewed for on this same form.
//
// THE MESSAGE ALSO NAMES THE MISTAKE, NOT JUST THE RULE. A filesystem path is what a user actually
// types here, because the field DIRECTLY ABOVE this one on the same form takes one — `/backups` — so
// carrying it down is the obvious move. "Must be a valid ZFS dataset name" is true, unarguable, and
// no use at all to somebody looking at `/backups` and seeing nothing wrong with it. Reported from a
// phone on a first-run stand, 2026-08-13, where it was the only thing between the operator and a
// working storage.
//
// ASSERTED PER FACT rather than as one substring of the whole sentence, so a reword that drops the
// path-vs-dataset distinction fails even if what remains still reads well.
func TestZFSKeyRefusalTellsThemItIsNotAPath(t *testing.T) {
	deps := testDeps(t)
	deps.ZFSKeyPath = filepath.Join(t.TempDir(), "zfs")
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	// The exact value a user carries down from the Path field above it.
	resp, err := c.Do(zfsKeyReqFor(t, srv, c, "/backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}

	var got struct {
		Errors []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Errors) != 1 || got.Errors[0].Path != "parent_dataset" {
		t.Fatalf("errors = %+v, want one naming parent_dataset", got.Errors)
	}

	for _, want := range []struct{ fact, why string }{
		{"no leading `/`", "the single thing wrong with the value they typed"},
		{"field above", "which field they took it from — both are on one screen"},
		{"rpool/quince", "an example, because the rule alone does not show the shape"},
	} {
		if !strings.Contains(got.Errors[0].Message, want.fact) {
			t.Errorf("the refusal does not carry %q — %s.\ngot: %s",
				want.fact, want.why, got.Errors[0].Message)
		}
	}
}
