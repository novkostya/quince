package pushsvc

import "testing"

// URGENCY IS A DELIVERY CONTRACT, NOT A HINT. RFC 8030 §5.3 defines `low` as explicit permission for
// the push service to DELAY a message to conserve battery. So this table is the difference between a
// notification that arrives now and one that arrives when the phone next wakes up.
//
// `test` IS THE ROW THIS FILE EXISTS FOR. It fell through to `low`, and a test notification is the
// one message in the whole rung whose entire value is arriving immediately — somebody tapped a
// button and is watching the screen. Measured on an iPhone, 2026-08-18: the first notification
// quince ever delivered was slow enough that the Operator assumed the network was to blame.
func TestUrgencyMatchesWhoIsWaiting(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want string
		why  string
	}{
		{"test", "high", "somebody tapped a button and is looking at the screen"},
		{"action_required", "high", "the backup is blocked on the person being told"},
		{"backup_failed", "high", "somebody is waiting on a backup that did not finish"},
		{"backup_available", "low", "a reminder may wait for the phone to wake"},
		{"backup_overdue", "low", "still a reminder, just an older one"},
		{"backup_completed", "low", "nothing is waiting on this at all"},
	} {
		payload := []byte(`{"web_push":8030,"notification":{"title":"t","navigate":"https://q.example/","kind":"` + tc.kind + `"}}`)
		if got := urgencyFor(payload); got != tc.want {
			t.Errorf("kind %q → urgency %q, want %q — %s", tc.kind, got, tc.want, tc.why)
		}
	}
}

// THE TEST TITLE DOES NOT REPEAT THE APP NAME. iOS renders a Home Screen web app's notification as
// `<title> from <app name>`, so a title starting with "quince" reads "quince notifications are
// working from quince" on the lock screen. The attribution belongs to the platform and exists only
// on a device, which is why this is asserted here rather than left to be seen again.
func TestTheTestTitleDoesNotRepeatTheAppName(t *testing.T) {
	// The literal is read from where it is written rather than from a delivered payload: what is
	// under test is the copy, and a round trip would prove the transport instead.
	const title = "Notifications are working"
	if got := testTitle; got != title {
		t.Errorf("test title = %q, want %q", got, title)
	}
	if len(testTitle) > 0 && (testTitle[0] == 'q' || testTitle[0] == 'Q') {
		t.Errorf("the test title starts with the app's own name: iOS appends %q itself", "from quince")
	}
}
