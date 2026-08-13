package httpapi

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// quince#912 — the host-key ceremony's two endpoints.

func hostKeyLine(t *testing.T, addr string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return knownhosts.Line([]string{knownhosts.Normalize(addr)}, k)
}

func postJSON(t *testing.T, srv *httptest.Server, c *http.Client, path, body string) *http.Response {
	t.Helper()
	req := newReq(t, http.MethodPost, srv.URL+path, body)
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// AN UNREACHABLE HOST IS A 200 CARRYING THE REASON, not an error status — the same rule `probe` and
// `probe/hook` follow. A host that is not up yet is the ANSWER to "what key does it offer", and the
// form renders it beside the field the operator is looking at.
//
// `.invalid` is reserved and never resolves (RFC 2606), so this is fast and cannot accidentally
// reach a real machine.
func TestZFSHostKeyScanAnswersForAnUnreachableHost(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	resp := postJSON(t, srv, c, "/api/storages/zfs/hostkey", `{"ssh_host":"nothing.invalid"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an unreachable host is an answer", resp.StatusCode)
	}

	var got wire.StorageZFSHostKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Found || got.HostKey != nil {
		t.Errorf("found=%v host_key=%+v, want neither", got.Found, got.HostKey)
	}
	if got.Reason == "" {
		t.Error("no reason — the screen has nothing to show the operator")
	}
}

// A MALFORMED QUESTION IS A 422 NAMING THE FIELD, so the form can put the message where the user is
// looking.
func TestZFSHostKeyScanRefusesAnEmptyHost(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	resp := postJSON(t, srv, c, "/api/storages/zfs/hostkey", `{"ssh_host":""}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	var got struct {
		Errors []struct {
			Path string `json:"path"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Errors) != 1 || got.Errors[0].Path != "ssh_host" {
		t.Errorf("errors = %+v, want one naming ssh_host", got.Errors)
	}
}

// TRUST RECORDS THE LINE IT IS GIVEN, and reports the file so the screen can name it.
func TestZFSHostKeyTrustRecordsTheConfirmedLine(t *testing.T) {
	deps := testDeps(t)
	path := filepath.Join(t.TempDir(), "known_hosts")
	deps.ZFSKnownHostsPath = path
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	line := hostKeyLine(t, "nas.example:22")
	body, err := json.Marshal(wire.StorageZFSHostKeyTrustRequest{Line: line})
	if err != nil {
		t.Fatal(err)
	}

	resp := postJSON(t, srv, c, "/api/storages/zfs/hostkey/trust", string(body))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got wire.StorageZFSHostKeyTrustResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Trusted || got.Path != path {
		t.Errorf("trusted=%v path=%q, want true and %q", got.Trusted, got.Path, path)
	}

	on, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("nothing was written: %v", err)
	}
	if string(on) != line+"\n" {
		t.Errorf("the file does not hold exactly the confirmed line.\nwant: %s\ngot:  %s", line, on)
	}
}

// A CHANGED HOST KEY IS A 422 AND THE OLD ENTRY SURVIVES.
//
// This is the edge the whole ceremony exists for: overwriting silently would make one button trust
// an impersonator exactly as readily as the real host, which is what `StrictHostKeyChecking=yes` was
// chosen to prevent. quince cannot tell a rebuilt host from an attack and must not decide.
func TestZFSHostKeyTrustRefusesAChangedKey(t *testing.T) {
	deps := testDeps(t)
	path := filepath.Join(t.TempDir(), "known_hosts")
	deps.ZFSKnownHostsPath = path
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	first := hostKeyLine(t, "nas.example:22")
	second := hostKeyLine(t, "nas.example:22")
	for _, l := range []string{first} {
		b, _ := json.Marshal(wire.StorageZFSHostKeyTrustRequest{Line: l})
		r := postJSON(t, srv, c, "/api/storages/zfs/hostkey/trust", string(b))
		if r.StatusCode != http.StatusOK {
			t.Fatalf("seeding the first key: status %d", r.StatusCode)
		}
		_ = r.Body.Close()
	}

	b, _ := json.Marshal(wire.StorageZFSHostKeyTrustRequest{Line: second})
	resp := postJSON(t, srv, c, "/api/storages/zfs/hostkey/trust", string(b))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — a changed key must not be written", resp.StatusCode)
	}

	on, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != first+"\n" {
		t.Errorf("the refusal did not leave the original entry alone.\ngot: %s", on)
	}
}

// BOTH ENDPOINTS ARE REACHABLE ON A STORAGELESS INSTALL, which is the only state they matter in:
// this is the LAST step of the first-run zfs form. quince#898 was the same defect one endpoint over,
// and the demo server every e2e drives always HAS a storage, so nothing else would catch it.
func TestZFSHostKeyEndpointsSurviveSetupMode(t *testing.T) {
	deps := storagelessDeps(t, true)
	deps.ZFSKnownHostsPath = filepath.Join(t.TempDir(), "known_hosts")
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	scan := postJSON(t, srv, c, "/api/storages/zfs/hostkey", `{"ssh_host":"nothing.invalid"}`)
	if scan.StatusCode == http.StatusServiceUnavailable {
		t.Error("the scan was refused BY THE SETUP GUARD — the operator cannot trust a key they " +
			"cannot be shown")
	}
	_ = scan.Body.Close()

	b, _ := json.Marshal(wire.StorageZFSHostKeyTrustRequest{Line: hostKeyLine(t, "nas.example:22")})
	trust := postJSON(t, srv, c, "/api/storages/zfs/hostkey/trust", string(b))
	if trust.StatusCode == http.StatusServiceUnavailable {
		t.Error("trust was refused BY THE SETUP GUARD — this is the last step of the first-run form")
	}
	_ = trust.Body.Close()
}
