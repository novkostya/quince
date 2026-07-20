package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/wire"
)

// stubJobControl is a JobControl whose outcomes the test fixes; it records the last call so a test
// can prove the request fields reach the subsystem.
type stubJobControl struct {
	startJob     wire.Job
	startStatus  int
	startReason  string
	cancelJob    wire.Job
	cancelStatus int
	cancelReason string

	recUDID, recTransport, recRetryOf, recCancelID string
}

func (s *stubJobControl) StartBackup(udid, transport, retryOf string) (wire.Job, int, string) {
	s.recUDID, s.recTransport, s.recRetryOf = udid, transport, retryOf
	return s.startJob, s.startStatus, s.startReason
}

func (s *stubJobControl) CancelJob(id string) (wire.Job, int, string) {
	s.recCancelID = id
	return s.cancelJob, s.cancelStatus, s.cancelReason
}

func jobsServer(t *testing.T, jc JobControl) (*httptest.Server, *http.Client) {
	t.Helper()
	deps := testDeps(t)
	if jc != nil {
		deps.JobControl = jc
	}
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	return srv, authedClient(t, srv)
}

func TestJobCreateAccepted(t *testing.T) {
	stub := &stubJobControl{startJob: wire.Job{ID: "01JOB", State: "queued"}, startStatus: http.StatusAccepted}
	srv, c := jobsServer(t, stub)
	resp := postCSRF(t, c, srv, "/api/jobs", `{"udid":"DEV-1","transport":"usb"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	var j wire.Job
	_ = json.NewDecoder(resp.Body).Decode(&j)
	if j.ID != "01JOB" || j.State != "queued" {
		t.Fatalf("job = %+v", j)
	}
	if stub.recUDID != "DEV-1" || stub.recTransport != "usb" || stub.recRetryOf != "" {
		t.Fatalf("recorded udid=%q transport=%q retry=%q", stub.recUDID, stub.recTransport, stub.recRetryOf)
	}
}

func TestJobCreateRetryOfPassed(t *testing.T) {
	stub := &stubJobControl{startJob: wire.Job{ID: "01JOB2"}, startStatus: http.StatusAccepted}
	srv, c := jobsServer(t, stub)
	resp := postCSRF(t, c, srv, "/api/jobs", `{"udid":"DEV-1","transport":"wifi","retry_of":"01OLD"}`)
	defer func() { _ = resp.Body.Close() }()
	if stub.recRetryOf != "01OLD" || stub.recTransport != "wifi" {
		t.Fatalf("retry_of=%q transport=%q", stub.recRetryOf, stub.recTransport)
	}
}

// transport "auto" reaches the engine (which resolves it); when the engine refuses — e.g.
// auto-when-absent (design §4/(bp)) — the 422 maps to the error envelope. The handler is
// transport-agnostic: it passes "auto" through and maps whatever status comes back.
func TestJobCreateAutoResolvedByEngine(t *testing.T) {
	stub := &stubJobControl{startStatus: http.StatusUnprocessableEntity,
		startReason: "device is not currently connected — connect it over USB or Wi-Fi, or choose a transport"}
	srv, c := jobsServer(t, stub)
	resp := postCSRF(t, c, srv, "/api/jobs", `{"udid":"DEV-1","transport":"auto"}`)
	defer func() { _ = resp.Body.Close() }()
	if stub.recTransport != "auto" {
		t.Fatalf("handler passed transport=%q, want auto reaching the engine", stub.recTransport)
	}
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	var body wire.APIError
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Error.Code != "invalid_request" {
		t.Fatalf("error code = %q", body.Error.Code)
	}
}

func TestJobCreateConflict(t *testing.T) {
	stub := &stubJobControl{startStatus: http.StatusConflict, startReason: "a backup is already running for this device"}
	srv, c := jobsServer(t, stub)
	resp := postCSRF(t, c, srv, "/api/jobs", `{"udid":"DEV-1","transport":"usb"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestJobCancelAccepted(t *testing.T) {
	stub := &stubJobControl{cancelJob: wire.Job{ID: "01JOB", State: "cancelled"}, cancelStatus: http.StatusAccepted}
	srv, c := jobsServer(t, stub)
	resp := postCSRF(t, c, srv, "/api/jobs/01JOB/cancel", "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if stub.recCancelID != "01JOB" {
		t.Fatalf("cancel id = %q", stub.recCancelID)
	}
}

// The default (no engine wired, e.g. --demo) refuses the command surface with 503, never a
// fabricated job.
func TestJobCommandsUnavailableByDefault(t *testing.T) {
	srv, c := jobsServer(t, nil)
	resp := postCSRF(t, c, srv, "/api/jobs", `{"udid":"DEV-1","transport":"usb"}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
