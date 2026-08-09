package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeReconciler is a ReconcileReporter whose answer the test controls.
type fakeReconciler struct{ on bool }

func (f *fakeReconciler) Reconciling() bool { return f.on }

// G2 — `GET /api/health` REPORTS `reconciling`, AND IT IS READ PER REQUEST.
//
// The per-request half is the half worth gating. A deploy check polls this endpoint precisely ACROSS
// the transition, so a value captured at wiring time would report the startup state forever — the
// field would exist, the JSON would look right, and it would answer the wrong question for the whole
// life of the process. That failure is invisible to a test that only ever asks once.
func TestHealthReportsReconcilingAndRereadsItEveryRequest(t *testing.T) {
	f := &fakeReconciler{on: true}
	deps := testDeps(t)
	deps.Reconcile = f
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	if got := getHealth(t, srv.URL); !got.Reconciling {
		t.Fatal("reconciling = false while a pass is running — a client cannot tell a short version " +
			"list from a complete one, which is what blocker 2 was ruled about")
	}

	f.on = false // the pass completes; nothing is re-wired
	if got := getHealth(t, srv.URL); got.Reconciling {
		t.Fatal("reconciling stayed true after the pass finished — the field is captured rather than " +
			"read live, so it answers the startup question forever")
	}
}

// A DAEMON WITH NO RUNNER REPORTS `false`, AND THAT IS THE TRUTH RATHER THAN A DEFAULT.
//
// `--demo`, the admin CLIs and every test router wire no runner. There is no asynchronous pass in
// those, so nothing is ever provisional — `false` is the honest answer, not a fallback standing in
// for an unknown. It also pins the nil check: a typed-nil reporter would panic here.
func TestHealthReportsNotReconcilingWhenNoRunnerIsWired(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()

	if got := getHealth(t, srv.URL); got.Reconciling {
		t.Fatal("reconciling = true with no runner wired")
	}
}

func getHealth(t *testing.T, base string) HealthResponse {
	t.Helper()
	resp, err := http.Get(base + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}
