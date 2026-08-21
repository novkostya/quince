// Package pushsvc joins the two halves qn.12 built separately: `push` knows the protocols and holds
// no state, `store` holds the state and knows no protocol. This is the seam, and it is where the
// VAPID key's lifecycle lives.
//
// IT SENDS NOTHING. Delivery — dialling a push service, `410` handling, retry — is its own slice,
// because it is the only part with network egress and it wants its own thinking about timeouts.
package pushsvc

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/novkostya/quince/core/internal/push"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// Store is the storage this service needs. An interface so the service can be tested against a fake
// and so the dependency points one way.
type Store interface {
	VAPIDPrivateKey() ([]byte, bool, error)
	SetVAPIDPrivateKey([]byte) error
	PushSubscriptions() ([]store.PushSubscription, error)
	AddPushSubscription(store.PushSubscription) error
	DeletePushSubscription(id string) (bool, error)

	// DeviceNotificationsEnabled answers the per-device mute for ONE owner (qn.13 slice 10b).
	//
	// The send loop needs it because the mute moved here from the decision point: with two
	// principals there is no single answer to *should this go out*, and deciding upstream
	// produced no decision at all, so a scoped holder lost reminders about their own phone
	// because the admin muted it (Operator, 2026-08-21).
	DeviceNotificationsEnabled(udid, owner string) (bool, error)
}

// IDFunc mints a subscription id. Injected so tests are deterministic.
type IDFunc func() string

// Service owns the VAPID key's lifecycle and the subscription list.
type Service struct {
	// log surfaces degraded modes that are not delivery outcomes. NIL MEANS slog.Default() and
	// deliberately NOT a discard handler: the one thing this field exists for is a preference read
	// that failed, and defaulting to silence would reintroduce exactly the swallowed error it was
	// added to fix (quince#1409 review).
	//
	// A LOGGER RATHER THAN A `Result`, because a Result is a per-subscription record and
	// `deliver.go` counts them: appending a second one for a row that was delivered made
	// `DeliverDecision` report "1 of 2 subscriptions did not receive this notification" for one
	// subscription that did receive it. Both numbers false, in the subsystem whose whole job is to
	// be honest about whether a notification arrived — worse than the silence it replaced, because
	// silence asserted nothing. `live.go:232` logs the same read for the same reason.
	log *slog.Logger
	// http is the client deliveries use. Nil means http.DefaultClient; a test injects one so the
	// send path can be driven against an httptest server without reaching the network.
	http  *http.Client
	store Store
	newID IDFunc
	now   func() time.Time
	// subject is RFC 8292's `sub` claim — a contact a push service may use to reach whoever operates
	// this quince when its deliveries misbehave.
	//
	// IT MUST BE ROUTABLE, AND THIS IS MEASURED RATHER THAN READ. The default was
	// `mailto:quince@localhost`, on the spec's reasoning that a mailbox nobody owns is more honest
	// than inventing an address the operator does not have. **Apple rejects it**: the first real
	// delivery this project ever attempted, on an iPhone on 2026-08-18, came back
	// `https://web.push.apple.com/<redacted> answered 403`. Apple requires a `mailto:` with a real
	// domain or an `https:` URI, and `localhost` is neither.
	//
	// So the honesty argument was right about the goal and wrong about the fact, and the fix keeps
	// the goal: **the project's own URL is a real contact that quince genuinely has.** It points at
	// the software whose deliveries are misbehaving, which is what a push service operator chasing a
	// misbehaving sender actually wants — and it invents nothing about the person running this
	// install. An operator with a mailbox they want used should set `notifications.contact`.
	subject string
}

// DefaultSubject is the RFC 8292 `sub` claim used when the operator has named no contact.
//
// THE PROJECT URL, BECAUSE IT IS THE ONLY REAL CONTACT QUINCE HAS. Apple refuses a `sub` that is not
// a routable `mailto:` or an `https:` URI — measured, 403, see `Service.subject` — so a placeholder
// is not an option, and inventing an address for the person running this install would be a worse
// lie than naming the software.
const DefaultSubject = "https://github.com/novkostya/quince"

// WithSubject sets the contact a push service may use, overriding DefaultSubject.
func (s *Service) WithSubject(subject string) *Service {
	if subject != "" {
		s.subject = subject
	}
	return s
}

func New(s Store, newID IDFunc, now func() time.Time) *Service {
	return &Service{store: s, newID: newID, now: now, subject: DefaultSubject}
}

// WithHTTPClient points deliveries at a specific client.
//
// FOR TESTS, AND NOT A CONFIGURATION SEAM. The delivery path has to be drivable against an
// `httptest` server, because the alternative is a suite that either reaches the real internet or
// never exercises the code that talks to it. Production passes nothing and gets `http.DefaultClient`;
// the per-request context is what bounds a delivery, so there is no client-level timeout to tune.
func (s *Service) WithHTTPClient(c *http.Client) *Service { s.http = c; return s }

// VAPIDPublicKey returns the `applicationServerKey` a browser needs to create a subscription,
// GENERATING THE KEYPAIR ON FIRST USE.
//
// THE RULING'S FIRST CONSTRAINT LIVES HERE (quince#1128): generation happens only when the store
// reports a clean install — no key AND no subscriptions. When subscriptions exist without a key the
// store returns `ErrVAPIDKeyMissing` and this propagates it rather than minting, because a fresh key
// would leave every subscribed phone holding a subscription quince can no longer sign for while
// quince looked healthy. The refusal is the honest outcome; the remedy it names is that every device
// must re-subscribe.
//
// THE SECOND CONSTRAINT IS THE ABSENCE OF A SIBLING. There is no `RotateVAPIDKey` and there must not
// be: rotation is destructive by construction, since the public half is baked into every
// subscription a phone has ever created.
//
// ONLY THE PUBLIC HALF LEAVES THIS PACKAGE. The private scalar is read, used, and never returned.
func (s *Service) VAPIDPublicKey() (string, error) {
	raw, ok, err := s.store.VAPIDPrivateKey()
	if err != nil {
		return "", err
	}
	if ok {
		k, err := push.VAPIDKeyFromBytes(raw)
		if err != nil {
			return "", err
		}
		return k.PublicKeyBase64(), nil
	}
	k, err := push.GenerateVAPIDKey()
	if err != nil {
		return "", err
	}
	if err := s.store.SetVAPIDPrivateKey(k.PrivateBytes()); err != nil {
		// The store REFUSES to overwrite, so this is either a real write failure or a race with
		// another startup that generated first. Either way the caller retries and reads the winner —
		// what must not happen is this path silently replacing a key, and the store is what prevents
		// it rather than a check here.
		return "", fmt.Errorf("pushsvc: store the generated VAPID key: %w", err)
	}
	return k.PublicKeyBase64(), nil
}

// Subscriptions is the list for the settings surface, live and expired.
//
// EXPIRED ROWS ARE INCLUDED AND MARKED. That is the point of keeping them: a device that stopped
// receiving has to be nameable, or the failure is invisible and its first symptom is a missed backup.
//
// THE ENDPOINT AND THE KEYS DO NOT LEAVE. They are capability-grade — anyone holding them can push
// to that phone — so the wire type carries a label, a state and timestamps, and nothing a caller
// could send with.
func (s *Service) Subscriptions() ([]wire.PushSubscription, error) {
	rows, err := s.store.PushSubscriptions()
	if err != nil {
		return nil, err
	}
	out := make([]wire.PushSubscription, 0, len(rows))
	for _, r := range rows {
		w := wire.PushSubscription{
			ID:          r.ID,
			Label:       r.Label,
			State:       "live",
			CreatedAt:   r.CreatedAt.UTC().Format(time.RFC3339),
			Fingerprint: push.EndpointFingerprint(r.Endpoint),
		}
		if r.ExpiredAt != nil {
			w.State = "expired"
			w.ExpiredAt = r.ExpiredAt.UTC().Format(time.RFC3339)
		}
		if r.LastSentAt != nil {
			w.LastSentAt = r.LastSentAt.UTC().Format(time.RFC3339)
		}
		out = append(out, w)
	}
	return out, nil
}

// Subscribe records a browser's subscription.
//
// THE FIELDS ARE VALIDATED BY DECODING THEM, not by inspection: `push.Encrypt` would refuse a
// malformed key later, at send time, where the failure is invisible from here. Refusing at the door
// turns "notifications silently never arrive" into a 422 on the request that caused it.
// `origin` is the address the subscribing browser reached quince by, which is what makes a
// notification's `navigate` URL absolute. Taken from the request rather than from the client's own
// claim: the browser sends `Origin` on this POST, the same header the session layer already trusts.
func (s *Service) Subscribe(endpoint, p256dh, auth, label, origin string) (string, error) {
	if endpoint == "" || p256dh == "" || auth == "" {
		return "", fmt.Errorf("pushsvc: subscription is missing endpoint or keys")
	}
	if _, _, err := push.ParseSubscriptionKeys(p256dh, auth); err != nil {
		return "", err
	}
	id := s.newID()
	err := s.store.AddPushSubscription(store.PushSubscription{
		ID: id, Endpoint: endpoint, P256DH: p256dh, Auth: auth,
		Label: label, Origin: origin, CreatedAt: s.now(),
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// Unsubscribe removes one by id, reporting whether a row went.
func (s *Service) Unsubscribe(id string) (bool, error) { return s.store.DeletePushSubscription(id) }

// WithLogger points this service's degraded-mode reporting at a logger.
//
// SAME BUILDER SHAPE AS WithHTTPClient, but not for the same reason: that one exists so tests can
// avoid the network, this one exists so the daemon's logger reaches a path that must not be silent.
func (s *Service) WithLogger(l *slog.Logger) *Service { s.log = l; return s }

// logger is nil-safe. See the field's comment for why the default is `slog.Default()` and not a
// discard handler.
func (s *Service) logger() *slog.Logger {
	if s.log == nil {
		return slog.Default()
	}
	return s.log
}
