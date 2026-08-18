package pushsvc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/novkostya/quince/core/internal/notify"
	"github.com/novkostya/quince/core/internal/push"
	"github.com/novkostya/quince/core/internal/store"
)

// Delivery — the one part of qn.12 with network egress (spec D8).
//
// IT IS THE ONLY PLACE A SUBSCRIPTION'S ENDPOINT AND KEYS ARE READ FOR USE, which is why the
// redaction rule is enforced here rather than trusted: every error and every log line goes through
// `push.RedactEndpoint`, because an endpoint is a capability and errors are how one reaches a log
// without anybody deciding it should.

// Sender is the storage delivery needs, beyond what `Store` already covers.
type Sender interface {
	Store
	ExpirePushSubscription(endpoint string, at time.Time) error
	MarkPushSent(endpoint string, at time.Time) error
}

// deliveryTimeout bounds one POST to a push service.
//
// TEN SECONDS, AND IT IS A CEILING RATHER THAN AN EXPECTATION. A push service that has not answered
// in ten seconds is not going to; without a bound, one unreachable endpoint would hold a send loop
// open indefinitely and every other device would wait behind it. It is not configurable — a knob
// here would be a setting whose only correct value is "long enough", which is what this is.
const deliveryTimeout = 10 * time.Second

// Result reports what happened to one device, so a caller can surface it rather than infer it.
type Result struct {
	// Label is what the settings list calls this device. THE ENDPOINT IS NOT HERE — a Result is the
	// kind of thing that ends up in a log line.
	Label string
	Sent  bool
	// Expired is true when the push service said the subscription is gone (410/404). The row is
	// MARKED, not deleted, so the settings surface can name the device that stopped receiving.
	Expired bool
	// Err is a transport or protocol failure. It is NOT expiry: "the NAS was offline" and "the phone
	// is gone" are different facts and collapsing them would either hide a dead device or condemn a
	// live one.
	Err error
}

// Deliver sends one decision to every LIVE subscription.
//
// A DECISION GOES TO EVERY DEVICE, not to one. quince has no way to know which phone a person is
// holding, and the alternative — pick one — silently drops the notification when they are holding
// the other.
//
// IT NEVER RETURNS EARLY ON A FAILURE. One dead subscription must not stop the others: the whole
// point of the expiry machinery is that a phone can drop out without taking the feature with it.
func (s *Service) Deliver(ctx context.Context, sender Sender, d notify.Decision, subject string) ([]Result, error) {
	raw, ok, err := sender.VAPIDPrivateKey()
	if err != nil {
		return nil, err
	}
	if !ok {
		// NO KEY AND NO SUBSCRIPTIONS is a clean install with nothing to send to, which is not an
		// error. `VAPIDPrivateKey` already refuses the divergent case, so reaching here with ok=false
		// means there is genuinely nobody to notify.
		return nil, nil
	}
	key, err := push.VAPIDKeyFromBytes(raw)
	if err != nil {
		return nil, err
	}
	payload, err := push.MarshalPayload(push.Notification{
		Title: d.Title, Body: d.Body, Navigate: d.Navigate, Kind: string(d.Kind),
	})
	if err != nil {
		return nil, err
	}

	rows, err := sender.PushSubscriptions()
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, row := range rows {
		if !row.Live() {
			continue
		}
		out = append(out, s.deliverOne(ctx, sender, key, row, payload, subject))
	}
	return out, nil
}

func (s *Service) deliverOne(ctx context.Context, sender Sender, key *push.VAPIDKey,
	row store.PushSubscription, payload []byte, subject string) Result {
	res := Result{Label: row.Label}

	body, err := push.Encrypt(push.Subscription{
		Endpoint: row.Endpoint, P256DH: row.P256DH, Auth: row.Auth,
	}, payload, nil, nil)
	if err != nil {
		res.Err = err
		return res
	}
	auth, err := key.AuthorizationHeader(row.Endpoint, subject, s.now())
	if err != nil {
		res.Err = err
		return res
	}

	ctx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, row.Endpoint, bytes.NewReader(body))
	if err != nil {
		res.Err = fmt.Errorf("push: build request for %s: %w", push.RedactEndpoint(row.Endpoint), err)
		return res
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	// RFC 8030 §5.2 — how long the push service should hold the message for a device that is offline.
	// FOUR HOURS, because a backup reminder is about *now*: a phone that comes back tomorrow should
	// not be handed yesterday's "ready to back up", which would be a notification about a moment that
	// has passed. Not configurable, for the same reason the timeout is not.
	req.Header.Set("TTL", "14400")
	// `urgency` lets a push service defer delivery to save the device's battery — correct for a
	// reminder and wrong for a failure, which is why this is the only header that varies by kind.
	req.Header.Set("Urgency", urgencyFor(payload))

	resp, err := s.client().Do(req)
	if err != nil {
		res.Err = fmt.Errorf("push: deliver to %s: %w", push.RedactEndpoint(row.Endpoint), err)
		return res
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	// 404/410 — RFC 8030's "this subscription is gone". THE ONLY TWO STATUSES THAT MEAN A DEVICE IS
	// DEAD; everything else is a transport problem that may clear on its own.
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		res.Expired = true
		if err := sender.ExpirePushSubscription(row.Endpoint, s.now()); err != nil {
			res.Err = err
		}
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		res.Sent = true
		if err := sender.MarkPushSent(row.Endpoint, s.now()); err != nil {
			res.Err = err
		}
	default:
		// A 4xx that is not 404/410 is quince's own bug — a malformed VAPID token, a payload over the
		// limit — and a 5xx is the push service's. NEITHER EXPIRES THE SUBSCRIPTION: marking a live
		// phone dead because a CDN had a bad minute is the failure this branch exists to avoid.
		//
		// THE SERVICE'S OWN REASON IS INCLUDED, and its absence cost this project a diagnosis. The
		// first real delivery ever attempted came back `403` and nothing else, so *why* had to be
		// worked out from a spec argument and a web search — when Apple had answered
		// `{"reason":"BadJwtToken"}` in the body and quince threw it away. A status code names the
		// category; the body names the fault.
		//
		// SAFE TO CARRY: this is the push service's error document, not the request, so it holds
		// nothing of the subscription. Bounded anyway — an error string reaches logs and screens, and
		// an unbounded read of a remote body is how a diagnostic becomes a denial of service.
		res.Err = fmt.Errorf("push: %s answered %d%s",
			push.RedactEndpoint(row.Endpoint), resp.StatusCode, reasonOf(resp.Body))
	}
	return res
}

// urgencyFor picks RFC 8030 §5.3's urgency from the payload's kind.
//
// A REMINDER MAY WAIT; A FAILURE MAY NOT. `low` lets a push service batch delivery to save battery,
// which is right for "your phone is due for a backup" and wrong for "the backup did not finish" —
// the second is the one somebody is waiting on.
func urgencyFor(payload []byte) string {
	if bytes.Contains(payload, []byte(`"kind":"action_required"`)) ||
		bytes.Contains(payload, []byte(`"kind":"backup_failed"`)) {
		return "high"
	}
	return "low"
}

// client returns the HTTP client, defaulting to one with no shared timeout — the per-request context
// is what bounds a delivery, so a client-level timeout would be a second, invisible bound.
func (s *Service) client() *http.Client {
	if s.http != nil {
		return s.http
	}
	return http.DefaultClient
}

// reasonMax bounds how much of a push service's error document reaches an error string.
//
// 200 BYTES IS ENOUGH FOR EVERY REAL ONE. Apple answers `{"reason":"BadJwtToken"}`; Mozilla answers
// a short JSON object with `errno` and `message`. The bound is not about those — it is about the
// case where a proxy returns an HTML error page, which would otherwise put a whole document into a
// log line and onto a screen.
const reasonMax = 200

// reasonOf renders a push service's error body for an error string, or "" when there is nothing
// useful to say. The leading separator is included so the caller composes without a conditional.
func reasonOf(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, reasonMax))
	if err != nil || len(raw) == 0 {
		return ""
	}
	// COLLAPSED TO ONE LINE. A multi-line body inside a structured log value makes the record
	// unparseable, and this string is built to be read in exactly that position.
	clean := strings.Join(strings.Fields(string(raw)), " ")
	if clean == "" {
		return ""
	}
	return ": " + clean
}
