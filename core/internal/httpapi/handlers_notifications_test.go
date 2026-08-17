package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// fakeNotifications is the port, staged. A fake rather than the real service because these tests are
// about STATUS CODES AND SHAPE — `pushsvc` has its own suite for the key's lifecycle, and driving a
// real SQLite store here would test that twice and this once.
type fakeNotifications struct {
	key         string
	keyErr      error
	subs        []wire.PushSubscription
	subErr      error
	newID       string
	subsErr     error
	gone        bool
	delErr      error
	testResults []wire.PushDeliveryResult
	testErr     error
}

func (f *fakeNotifications) VAPIDPublicKey() (string, error) { return f.key, f.keyErr }
func (f *fakeNotifications) Subscriptions() ([]wire.PushSubscription, error) {
	return f.subs, f.subsErr
}
func (f *fakeNotifications) Subscribe(_, _, _, _ string) (string, error) { return f.newID, f.subErr }
func (f *fakeNotifications) Unsubscribe(string) (bool, error)            { return f.gone, f.delErr }
func (f *fakeNotifications) SendTest(context.Context) ([]wire.PushDeliveryResult, error) {
	return f.testResults, f.testErr
}

// THE SURFACE IS BEHIND EVERY GUARD, AND `authExempt` DOES NOT GROW.
//
// The exempt set is fifteen routes and each exists because it is reachable BEFORE a session can —
// obtaining a credential, or explaining why you cannot get one. A subscription belongs to somebody
// already logged in and the list is a capability inventory, so none of these qualify. Asserted
// rather than assumed, because `authExempt` is a literal switch and adding a line to it is one edit.
func TestTheNotificationRoutesAreNotPreAuth(t *testing.T) {
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/api/notifications"},
		{http.MethodPost, "/api/notifications/subscriptions"},
		{http.MethodDelete, "/api/notifications/subscriptions/abc"},
	} {
		r := httptest.NewRequest(rt.method, rt.path, nil)
		if authExempt(r) {
			t.Errorf("%s %s is pre-auth; a subscription list would be readable without a session",
				rt.method, rt.path)
		}
		if csrfExempt(r) && rt.method != http.MethodGet {
			t.Errorf("%s %s is exempt from CSRF while mutating", rt.method, rt.path)
		}
	}
}

// A DIVERGENT DB SAYS WHAT TO DO. `ErrVAPIDKeyMissing` means subscriptions exist without a signing
// key — the app DB was partially restored — and the remedy is in the error text. Collapsing it into
// a bare "internal error" would discard the one sentence the operator needs, which is the
// troubleshooting rule's whole point.
func TestAMissingVAPIDKeyIsReportedWithItsRemedy(t *testing.T) {
	d := depsWithNotifications(t, &fakeNotifications{keyErr: store.ErrVAPIDKeyMissing})
	rec := httptest.NewRecorder()
	d.handleNotificationsGet()(rec, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "vapid_key_missing") {
		t.Errorf("the error code is generic, so a client cannot distinguish this from any failure:\n%s", body)
	}
	if !strings.Contains(body, "re-subscribe") {
		t.Errorf("the remedy did not survive into the response:\n%s", body)
	}
}

// AN EMPTY LIST IS `[]`, NOT `null`. A client that has to distinguish "no subscriptions" from "the
// field is missing" is a client written around a wire quirk.
func TestAnEmptySubscriptionListRendersAsAnArray(t *testing.T) {
	d := depsWithNotifications(t, &fakeNotifications{key: "BPub"})
	rec := httptest.NewRecorder()
	d.handleNotificationsGet()(rec, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"subscriptions":[]`) {
		t.Errorf("an empty list did not render as []:\n%s", rec.Body.String())
	}
	var got wire.NotificationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.VAPIDPublicKey != "BPub" {
		t.Errorf("vapid_public_key = %q; a browser cannot subscribe without it", got.VAPIDPublicKey)
	}
}

// A MALFORMED SUBSCRIPTION IS A 422 THAT NAMES THE PROBLEM, not a 400 and not a 500. It is the
// caller's input being wrong, and `pushsvc`'s messages name the field without echoing the value —
// which is what makes returning them safe as well as useful.
func TestAMalformedSubscriptionIsA422(t *testing.T) {
	d := depsWithNotifications(t, &fakeNotifications{
		subErr: errors.New("push: auth secret is 3 octets, want 16"),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/subscriptions",
		strings.NewReader(`{"endpoint":"https://p.example/a","keys":{"p256dh":"x","auth":"y"}}`))
	d.handleNotificationsSubscribe()(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "want 16") {
		t.Errorf("the refusal does not say what was wrong:\n%s", rec.Body.String())
	}
}

// A BAD BODY IS A 400 AND A BAD SUBSCRIPTION IS A 422, and the two must not collapse: one is "I
// cannot read your request", the other is "I read it and it is not a usable subscription".
func TestAnUnreadableBodyIsA400(t *testing.T) {
	d := depsWithNotifications(t, &fakeNotifications{newID: "sub-A"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/subscriptions",
		strings.NewReader(`{not json`))
	d.handleNotificationsSubscribe()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestASubscriptionIsCreatedWithItsID(t *testing.T) {
	d := depsWithNotifications(t, &fakeNotifications{newID: "sub-A"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/subscriptions",
		strings.NewReader(`{"endpoint":"https://p.example/a","keys":{"p256dh":"x","auth":"y"},"label":"iPhone"}`))
	d.handleNotificationsSubscribe()(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "sub-A") {
		t.Errorf("the id did not come back, so the client cannot address the row it just made:\n%s", rec.Body.String())
	}
}

// 404 ON AN UNKNOWN ID rather than a silent 204. "It is gone either way" is true of the outcome and
// false of the question asked — and a UI that cannot tell a removal from a stale row it was still
// showing will leave that row on the screen.
func TestRemovingAnUnknownSubscriptionIsA404(t *testing.T) {
	d := depsWithNotifications(t, &fakeNotifications{gone: false})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/notifications/subscriptions/nope", nil)
	req.SetPathValue("id", "nope")
	d.handleNotificationsUnsubscribe()(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	d2 := depsWithNotifications(t, &fakeNotifications{gone: true})
	req = httptest.NewRequest(http.MethodDelete, "/api/notifications/subscriptions/sub-A", nil)
	req.SetPathValue("id", "sub-A")
	d2.handleNotificationsUnsubscribe()(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("a successful removal returned %d, want 204", rec.Code)
	}
}

// A BUILD WITH NO PUSH WIRED SERVES NO ROUTE, rather than a route that panics on nil. `--demo` and
// every test router that does not wire this are the callers that depend on it.
func TestTheRoutesAreAbsentWhenNotificationsAreNotWired(t *testing.T) {
	srv := NewRouter(testDeps(t))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))
	// Whatever the unwired answer is, it must not be a panic and must not be a 200 carrying a key.
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "vapid_public_key") {
		t.Errorf("a build with no notifications wired served a key")
	}
}

// depsWithNotifications is `testDeps` plus a staged notification port, so these tests exercise the
// same Deps a real server builds rather than a hand-made one that could diverge from it.
func depsWithNotifications(t *testing.T, n NotificationReader) Deps {
	t.Helper()
	d := testDeps(t)
	d.Notifications = n
	return d
}

// THE TEST ENDPOINT ANSWERS 202 WITH PER-DEVICE OUTCOMES. Partial success is the normal case — one
// phone live, one gone — so a single status could only be true of one of them.
func TestTheTestEndpointReportsEveryDevice(t *testing.T) {
	d := depsWithNotifications(t, &fakeNotifications{testResults: []wire.PushDeliveryResult{
		{Label: "iPhone", State: "sent"},
		{Label: "old iPad", State: "expired"},
	}})
	rec := httptest.NewRecorder()
	d.handleNotificationsTest()(rec, httptest.NewRequest(http.MethodPost, "/api/notifications/test", nil))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"iPhone", `"state":"sent"`, "old iPad", `"state":"expired"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the response is missing %q:\n%s", want, body)
		}
	}
}

// NOBODY SUBSCRIBED IS `[]` AND STILL A 202. It is a true answer the screen must be able to render,
// and an error would make it look like something broke.
func TestTheTestEndpointWithNoSubscriptionsIsStillAccepted(t *testing.T) {
	d := depsWithNotifications(t, &fakeNotifications{})
	rec := httptest.NewRecorder()
	d.handleNotificationsTest()(rec, httptest.NewRequest(http.MethodPost, "/api/notifications/test", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"results":[]`) {
		t.Errorf("an empty result did not render as []:\n%s", rec.Body.String())
	}
}

// AND IT IS BEHIND EVERY GUARD, like its three siblings.
func TestTheTestEndpointIsNotPreAuth(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/notifications/test", nil)
	if authExempt(r) {
		t.Errorf("POST /api/notifications/test is pre-auth — anyone reaching the port could make quince send")
	}
	if csrfExempt(r) {
		t.Errorf("POST /api/notifications/test is CSRF-exempt while mutating")
	}
}
