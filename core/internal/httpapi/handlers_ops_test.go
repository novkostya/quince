package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// stubOps is a DeviceOps whose outcomes the test fixes; it records the last encryption call so
// a test can prove the password body reaches the subsystem (story 5 proves it goes no further).
type stubOps struct {
	pairOpID, pairReason string
	pairStatus           int
	valPaired            bool
	valStatus            int
	valReason            string
	encOpID, encReason   string
	encStatus            int
	theOp                wire.Op
	opOK                 bool

	recAction, recPW, recOld, recNew string
}

func (s *stubOps) Pair(context.Context, string) (string, int, string) {
	return s.pairOpID, s.pairStatus, s.pairReason
}
func (s *stubOps) Validate(context.Context, string) (bool, int, string) {
	return s.valPaired, s.valStatus, s.valReason
}
func (s *stubOps) Encryption(_ context.Context, _, action, pw, old, nw string) (string, int, string) {
	s.recAction, s.recPW, s.recOld, s.recNew = action, pw, old, nw
	return s.encOpID, s.encStatus, s.encReason
}
func (s *stubOps) Op(string) (wire.Op, bool) { return s.theOp, s.opOK }

func opsServer(t *testing.T, ops DeviceOps) (*httptest.Server, *http.Client) {
	t.Helper()
	deps := testDeps(t)
	deps.Ops = ops
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	return srv, authedClient(t, srv)
}

func postCSRF(t *testing.T, c *http.Client, srv *httptest.Server, path, body string) *http.Response {
	t.Helper()
	req := newReq(t, http.MethodPost, srv.URL+path, body)
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// stubReset is a WorkingReset whose outcome the test fixes.
type stubReset struct {
	status int
	reason string
}

func (s stubReset) ResetWorking(string) (int, string) { return s.status, s.reason }

// TestResetWorkingHandler covers POST /api/devices/{udid}/reset-working (qn.5b): the handler maps
// the control's status, and a nil control (no engine wired) defaults to 503 via the router guard.
func TestResetWorkingHandler(t *testing.T) {
	cases := []struct {
		name    string
		control WorkingReset // nil → exercise the router's UnavailableWorkingReset default
		want    int
	}{
		{"accepted", stubReset{status: http.StatusAccepted, reason: "reset"}, http.StatusAccepted},
		{"running", stubReset{status: http.StatusConflict, reason: "a backup is running"}, http.StatusConflict},
		{"unknown", stubReset{status: http.StatusNotFound, reason: "unknown device"}, http.StatusNotFound},
		{"no engine (nil → 503)", nil, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps(t)
			deps.WorkingReset = tc.control
			srv := httptest.NewServer(NewRouter(deps))
			t.Cleanup(srv.Close)
			c := authedClient(t, srv)
			resp := postCSRF(t, c, srv, "/api/devices/DEV-1/reset-working", "")
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.want {
				t.Fatalf("reset-working status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestPairAccepted(t *testing.T) {
	srv, c := opsServer(t, &stubOps{pairOpID: "01OP", pairStatus: http.StatusAccepted})
	resp := postCSRF(t, c, srv, "/api/devices/DEV-1/pair", "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("pair status = %d, want 202", resp.StatusCode)
	}
	var body struct {
		OpID string `json:"op_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.OpID != "01OP" {
		t.Fatalf("op_id = %q", body.OpID)
	}
}

func TestPairNeedsUSBIs409(t *testing.T) {
	srv, c := opsServer(t, &stubOps{pairStatus: http.StatusConflict, pairReason: "pairing needs a USB connection"})
	resp := postCSRF(t, c, srv, "/api/devices/DEV-1/pair", "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("pair-not-usb status = %d, want 409", resp.StatusCode)
	}
}

func TestPairCSRFGuarded(t *testing.T) {
	srv, c := opsServer(t, &stubOps{pairStatus: http.StatusAccepted})
	// No CSRF header → 403 (proves the route is on the mutating chain).
	req := newReq(t, http.MethodPost, srv.URL+"/api/devices/DEV-1/pair", "")
	if code := doStatus(t, c, req); code != http.StatusForbidden {
		t.Fatalf("pair without CSRF = %d, want 403", code)
	}
}

func TestPairValidate(t *testing.T) {
	srv, c := opsServer(t, &stubOps{valPaired: true, valStatus: http.StatusOK})
	resp := postCSRF(t, c, srv, "/api/devices/DEV-1/pair/validate", "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("validate status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Paired bool `json:"paired"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if !body.Paired {
		t.Fatal("expected paired=true")
	}
}

func TestEncryptionAcceptedPassesBody(t *testing.T) {
	ops := &stubOps{encOpID: "01ENC", encStatus: http.StatusAccepted}
	srv, c := opsServer(t, ops)
	resp := postCSRF(t, c, srv, "/api/devices/DEV-1/encryption",
		`{"action":"enable","password":"in-body-only"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("encryption status = %d, want 202", resp.StatusCode)
	}
	// The password reached the subsystem via the body (its onward handling is story 5).
	if ops.recAction != "enable" || ops.recPW != "in-body-only" {
		t.Fatalf("subsystem got action=%q pw-empty=%v", ops.recAction, ops.recPW == "")
	}
}

func TestEncryptionInvalidIs422(t *testing.T) {
	srv, c := opsServer(t, &stubOps{encStatus: http.StatusUnprocessableEntity, encReason: "password is required"})
	resp := postCSRF(t, c, srv, "/api/devices/DEV-1/encryption", `{"action":"enable"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("encryption invalid status = %d, want 422", resp.StatusCode)
	}
}

func TestOpGet(t *testing.T) {
	op := wire.Op{ID: "01OP", UDID: "DEV-1", Kind: "pair", State: "waiting_for_user", Message: "Tap Trust…"}
	srv, c := opsServer(t, &stubOps{theOp: op, opOK: true})

	resp, err := c.Get(srv.URL + "/api/ops/01OP")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("op get status = %d, want 200", resp.StatusCode)
	}
	var got wire.Op
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.State != "waiting_for_user" || got.ID != "01OP" {
		t.Fatalf("op body = %+v", got)
	}
}

func TestOpGetUnknownIs404(t *testing.T) {
	srv, c := opsServer(t, &stubOps{opOK: false})
	resp, err := c.Get(srv.URL + "/api/ops/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown op status = %d, want 404", resp.StatusCode)
	}
}

// TestDeviceOpsUnavailableByDefault: with no Ops wired, actions refuse honestly (503).
func TestDeviceOpsUnavailableByDefault(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t))) // Ops nil → UnavailableDeviceOps
	defer srv.Close()
	c := authedClient(t, srv)
	resp := postCSRF(t, c, srv, "/api/devices/DEV-1/pair", "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("default pair status = %d, want 503", resp.StatusCode)
	}
}
