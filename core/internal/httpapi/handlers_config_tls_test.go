package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
)

// configDoc is the `config` object out of the GET/PUT envelope, kept as raw JSON so the
// round-trip below can put back BYTE FOR BYTE what it was given. Decoding into a typed
// struct would silently re-add any key the server sent and the client dropped, which is
// exactly the failure this test exists to catch.
func fetchConfigDoc(t *testing.T, c *http.Client, base string) json.RawMessage {
	t.Helper()
	resp, err := c.Get(base + "/api/config")
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/config = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode config envelope: %v", err)
	}
	return env.Config
}

func tlsOf(t *testing.T, doc json.RawMessage) (cert, key string) {
	t.Helper()
	var parsed struct {
		TLS *struct {
			CertFile string `json:"cert_file"`
			KeyFile  string `json:"key_file"`
		} `json:"tls"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("unmarshal config doc: %v", err)
	}
	if parsed.TLS == nil {
		t.Fatal("the config document has no `tls` key at all")
	}
	return parsed.TLS.CertFile, parsed.TLS.KeyFile
}

func putConfig(t *testing.T, c *http.Client, srv *httptest.Server, body string) *http.Response {
	t.Helper()
	req := newReq(t, http.MethodPut, srv.URL+"/api/config", body)
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/config: %v", err)
	}
	return resp
}

const storageJSON = `"storage":[{"name":"local","path":"/backups","default":true,"backend":"auto",` +
	`"zfs":{"parent_dataset":"","mode":"hook","hook_cmd":"","seed":"auto"},` +
	`"retention":{"keep_recent":10,"keep_daily":30,"keep_weekly":12}}],`

// This is interface fact 6 made executable, and it is the whole reason the TS `Config` type
// changes in the same PR as the Go struct.
//
// PUT is a full-document replace decoded into a ZERO-VALUED config.Config, so a key the
// client omits does not keep its default — it arrives as the Go zero value. For `tls` the
// zero value is two empty strings, which is TLS OFF. A client that fetches a document,
// rebuilds it without `tls`, and saves would therefore turn off HTTPS while appearing to
// change nothing.
//
// So: set it, fetch what the server says, put THAT BACK VERBATIM, and require it to survive.
func TestConfigRoundTripPreservesTLS(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	body := `{"backup":{"preferred_transport":"usb","require_encryption":true},` + storageJSON +
		`"devices":{"manage_muxer":true,"usbmuxd_socket":"/var/run/usbmuxd","netmuxd_addr":"127.0.0.1:27015"},` +
		`"tls":{"cert_file":"/certs/quince.pem","key_file":"/certs/quince.key"},` +
		`"sessions":{"ttl_minutes":30},"automation":{"staleness_days":3,"reminder_cooldown_hours":24},` +
		`"ui":{"theme":"system"}}`

	resp := putConfig(t, c, srv, body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT with tls = %d, want 200", resp.StatusCode)
	}

	doc := fetchConfigDoc(t, c, srv.URL)
	if cert, key := tlsOf(t, doc); cert != "/certs/quince.pem" || key != "/certs/quince.key" {
		t.Fatalf("after PUT, GET returned tls %q/%q — the values did not survive the save", cert, key)
	}

	// The round trip proper: put back exactly the bytes the server just handed us.
	resp2 := putConfig(t, c, srv, string(doc))
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("PUT of the fetched document = %d, want 200 — GET does not produce a valid PUT body", resp2.StatusCode)
	}

	if cert, key := tlsOf(t, fetchConfigDoc(t, c, srv.URL)); cert != "/certs/quince.pem" || key != "/certs/quince.key" {
		t.Fatalf("after the round trip, tls is %q/%q — a GET→PUT lost it", cert, key)
	}
}

// The failure mode above, demonstrated rather than described: a client that omits `tls`
// turns it off, and the server accepts that with a 200. This is NOT a bug to fix here — a
// full-document replace that honoured omissions could never clear a field — it is the
// reason the TS type must carry the key, and it is worth a test so nobody later "fixes" the
// round-trip by making omission mean "keep", which would quietly make `tls` unclearable.
func TestConfigPutOmittingTLSTurnsItOff(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	withTLS := `{"backup":{"preferred_transport":"usb","require_encryption":true},` + storageJSON +
		`"devices":{"manage_muxer":true,"usbmuxd_socket":"/var/run/usbmuxd","netmuxd_addr":"127.0.0.1:27015"},` +
		`"tls":{"cert_file":"/certs/quince.pem","key_file":"/certs/quince.key"},` +
		`"sessions":{"ttl_minutes":30},"automation":{"staleness_days":3,"reminder_cooldown_hours":24},` +
		`"ui":{"theme":"system"}}`
	resp := putConfig(t, c, srv, withTLS)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup PUT = %d, want 200", resp.StatusCode)
	}

	withoutTLS := `{"backup":{"preferred_transport":"usb","require_encryption":true},` + storageJSON +
		`"devices":{"manage_muxer":true,"usbmuxd_socket":"/var/run/usbmuxd","netmuxd_addr":"127.0.0.1:27015"},` +
		`"sessions":{"ttl_minutes":30},"automation":{"staleness_days":3,"reminder_cooldown_hours":24},` +
		`"ui":{"theme":"system"}}`
	resp2 := putConfig(t, c, srv, withoutTLS)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("PUT omitting tls = %d, want 200", resp2.StatusCode)
	}

	if cert, key := tlsOf(t, fetchConfigDoc(t, c, srv.URL)); cert != "" || key != "" {
		t.Fatalf("omitting tls left %q/%q; the zero-value semantics of a full replace changed", cert, key)
	}
}

// Half a pair is rejected at the API boundary with a 422 naming the missing key, in both
// directions. Without it the operator writes one line, restarts, and gets plain http with
// no complaint anywhere.
func TestConfigPutRejectsAHalfSetTLSPair(t *testing.T) {
	tests := []struct {
		name     string
		tlsJSON  string
		wantPath string
	}{
		{"cert without key", `"tls":{"cert_file":"/certs/quince.pem","key_file":""},`, "tls.key_file"},
		{"key without cert", `"tls":{"cert_file":"","key_file":"/certs/quince.key"},`, "tls.cert_file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(NewRouter(testDeps(t)))
			defer srv.Close()
			c := authedClient(t, srv)

			body := `{"backup":{"preferred_transport":"usb","require_encryption":true},` + storageJSON +
				`"devices":{"manage_muxer":true,"usbmuxd_socket":"/var/run/usbmuxd","netmuxd_addr":"127.0.0.1:27015"},` +
				tc.tlsJSON +
				`"sessions":{"ttl_minutes":30},"automation":{"staleness_days":3,"reminder_cooldown_hours":24},` +
				`"ui":{"theme":"system"}}`

			resp := putConfig(t, c, srv, body)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("PUT with %s = %d, want 422", tc.name, resp.StatusCode)
			}
			var env struct {
				Errors []struct {
					Path string `json:"path"`
				} `json:"errors"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				t.Fatalf("decode 422 body: %v", err)
			}
			var found bool
			for _, e := range env.Errors {
				if e.Path == tc.wantPath {
					found = true
				}
			}
			if !found {
				t.Fatalf("422 did not name %s; got %+v", tc.wantPath, env.Errors)
			}
		})
	}
}
