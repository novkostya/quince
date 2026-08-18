package pushsvc

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// THE HAPPY PATH IS A CLEAN NIL, and it is worth asserting on its own because every other test in
// this file is about a failure being visible. A working delivery must report nothing at all — the
// runner logs whatever comes back, so a spurious error here would put a warning in the log of every
// install that is working correctly.
func TestDeliverDecisionReportsNothingWhenEveryDeviceTookIt(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, _ := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())

	if err := s.DeliverDecision(context.Background(), decision()); err != nil {
		t.Fatalf("a delivery every device accepted reported an error: %v", err)
	}
	if staged.got == nil {
		t.Errorf("nothing reached the push service")
	}
}

// A FAILURE IS REPORTED, WITH A COUNT. `Deliver` never returns early on a per-device failure, so
// without this its outcomes would be dropped on the floor at exactly the one caller that has a
// logger — "the push service refused every device" would be indistinguishable from a clean send.
func TestDeliverDecisionReportsHowManyDevicesMissedIt(t *testing.T) {
	staged := &stagedPush{status: http.StatusInternalServerError}
	srv := staged.server(t)
	s, _ := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())

	err := s.DeliverDecision(context.Background(), decision())
	if err == nil {
		t.Fatal("a push service answering 500 to every device reported success")
	}
	if !strings.Contains(err.Error(), "1 of 1") {
		t.Errorf("the error does not say how many missed it: %v", err)
	}
}

// AN ENDPOINT MUST NOT REACH THE ERROR, because the error reaches a log line and an endpoint is
// capability-grade: anyone holding one can push to that phone. `send.go` redacts, and this asserts
// the redaction survives the wrapping this method adds.
func TestDeliverDecisionNeverNamesAnEndpoint(t *testing.T) {
	staged := &stagedPush{status: http.StatusInternalServerError}
	srv := staged.server(t)
	s, _ := senderWith(t, srv.URL+"/push/secret-token-abc")
	s = s.WithHTTPClient(srv.Client())

	err := s.DeliverDecision(context.Background(), decision())
	if err == nil {
		t.Fatal("no error to inspect")
	}
	if strings.Contains(err.Error(), "secret-token-abc") {
		t.Errorf("the delivery error carries the endpoint path: %v", err)
	}
}

// EXPIRY IS NOT FAILURE, and collapsing them would make an ordinary uninstall look like an outage in
// the logs forever. A 410 means that phone unsubscribed: the row is marked, the settings surface
// names it, and nothing about this daemon is wrong.
func TestDeliverDecisionTreatsAnExpiredSubscriptionAsOrdinary(t *testing.T) {
	staged := &stagedPush{status: http.StatusGone}
	srv := staged.server(t)
	s, _ := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())

	if err := s.DeliverDecision(context.Background(), decision()); err != nil {
		t.Fatalf("a device that unsubscribed was reported as a delivery failure: %v", err)
	}
}

// NOBODY SUBSCRIBED IS NOT AN ERROR. A fresh install notifies nothing, and the runner would otherwise
// log a warning on every evaluation until the first phone is added.
func TestDeliverDecisionWithNoSubscriptionsIsNotAnError(t *testing.T) {
	s, _ := svc(t)
	if _, err := s.VAPIDPublicKey(); err != nil {
		t.Fatalf("key: %v", err)
	}
	if err := s.DeliverDecision(context.Background(), decision()); err != nil {
		t.Fatalf("a decision with nobody to send it to errored: %v", err)
	}
}
