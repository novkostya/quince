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

// quince#818 piece B — POST /api/storages/zfs/key.

func zfsKeyReq(t *testing.T, srv *httptest.Server, c *http.Client) *http.Request {
	t.Helper()
	req := newReq(t, http.MethodPost, srv.URL+"/api/storages/zfs/key", "")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	return req
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
