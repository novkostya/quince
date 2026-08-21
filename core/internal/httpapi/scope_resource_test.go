package httpapi

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/wire"
)

// EVERY scopedOwnDevice ROUTE HAS A RESOLVER OR A STATED REASON. The construction-time assertion
// already panics; this is the readable form, and it also catches the case the panic cannot — a
// route listed in BOTH tables, where the resolver would be silently ignored.
func TestEveryScopedRouteResolvesOrFailsClosed(t *testing.T) {
	for pattern, class := range routeScope {
		if class != scopedOwnDevice {
			continue
		}
		_, resolvable := resourceDevice[pattern]
		_, deferred := unresolvableToday[pattern]
		switch {
		case resolvable && deferred:
			t.Errorf("%s is in BOTH resourceDevice and unresolvableToday — the resolver would never run", pattern)
		case !resolvable && !deferred:
			t.Errorf("%s is scopedOwnDevice with no resolver and no stated reason — it would be "+
				"permitted with nothing compared", pattern)
		}
	}
}

// THE FOURTH SHAPE, and the reason it gets its own test: `POST /api/jobs` names its device in the
// BODY, so a guard that only read paths would pass a backup-start through unchecked.
func TestTheJobsCreateDeviceComesFromTheBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(`{"udid":"DEV-A","transport":"usb"}`))
	udid, ok := fromBody(Deps{}, r)
	if !ok || udid != "DEV-A" {
		t.Fatalf("got %q ok=%v — want DEV-A from the body", udid, ok)
	}
}

// AND THE BODY MUST SURVIVE BEING READ. This is the one resolver that mutates the request, so if it
// consumed the stream the handler downstream would see an empty body — every backup start would
// fail, and only for a scoped holder, which is the hardest possible case to notice.
func TestTheBodyResolverLeavesTheBodyReadable(t *testing.T) {
	const payload = `{"udid":"DEV-A","transport":"usb"}`
	r := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(payload))
	if _, ok := fromBody(Deps{}, r); !ok {
		t.Fatal("resolver did not read the device")
	}
	rest, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("body unreadable after the resolver: %v", err)
	}
	if string(rest) != payload {
		t.Fatalf("body was consumed or altered: got %q want %q", rest, payload)
	}
}

// A body that does not parse is NOT a scope failure to distinguish — the guard reports no device and
// refuses, and the admin (who skips the guard) still gets the handler's 400.
func TestAnUnparseableBodyResolvesToNoDevice(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(`not json`))
	if udid, ok := fromBody(Deps{}, r); ok {
		t.Fatalf("garbage parsed to a device: %q", udid)
	}
}

func TestThePathResolverReadsTheUDID(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/devices/DEV-A", nil)
	r.SetPathValue("udid", "DEV-A")
	if udid, ok := fromPath(Deps{}, r); !ok || udid != "DEV-A" {
		t.Fatalf("got %q ok=%v", udid, ok)
	}
}

type fakeJobs struct {
	job wire.Job
	ok  bool
}

func (f fakeJobs) Jobs(string, string, int) ([]wire.Job, string) { return nil, "" }
func (f fakeJobs) Job(string) (wire.Job, bool)                   { return f.job, f.ok }
func (f fakeJobs) JobLog(string) (string, bool)                  { return "", false }

func TestTheJobResolverFollowsTheJobToItsDevice(t *testing.T) {
	d := Deps{Jobs: fakeJobs{job: wire.Job{UDID: "DEV-A"}, ok: true}}
	r := httptest.NewRequest("GET", "/api/jobs/j1", nil)
	r.SetPathValue("id", "j1")
	if udid, ok := fromJob(d, r); !ok || udid != "DEV-A" {
		t.Fatalf("got %q ok=%v", udid, ok)
	}
}

// AN UNKNOWN JOB RESOLVES TO NO DEVICE, which makes the guard refuse rather than 404.
//
// Deliberate: answering 404 for an id that does not exist and 403 for one that does would let a
// scoped holder enumerate job ids by reading the difference. The admin skips the guard, so they
// still get the handler's real 404.
func TestAnUnknownJobResolvesToNoDevice(t *testing.T) {
	d := Deps{Jobs: fakeJobs{ok: false}}
	r := httptest.NewRequest("GET", "/api/jobs/nope", nil)
	r.SetPathValue("id", "nope")
	if _, ok := fromJob(d, r); ok {
		t.Fatal("an unknown job resolved to a device")
	}
}

// THE FOUR VAULT ROUTES ARE NOW RESOLVED, not failing closed (slice 8b-2). Kept as a test
// rather than deleted, because the inverse is what regressed: a route slipping back into
// `unresolvableToday` would be refused for its rightful holder and nothing else would say so.
func TestTheVaultRoutesResolveTheirDevice(t *testing.T) {
	for _, p := range []string{
		"POST /api/versions/{id}/unlock",
		"POST /api/sessions/{id}/lock",
		"GET /api/sessions/{id}/browse",
		"GET /api/sessions/{id}/file/{file_id}",
	} {
		if _, ok := resourceDevice[p]; !ok {
			t.Errorf("%s has no resolver — a scoped holder cannot reach their own version", p)
		}
		if _, deferred := unresolvableToday[p]; deferred {
			t.Errorf("%s is still recorded as unresolvable", p)
		}
	}
	if len(unresolvableToday) != 0 {
		t.Fatalf("unresolvableToday should be empty: %v", unresolvableToday)
	}
}
