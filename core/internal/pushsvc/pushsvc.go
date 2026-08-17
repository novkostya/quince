// Package pushsvc joins the two halves qn.12 built separately: `push` knows the protocols and holds
// no state, `store` holds the state and knows no protocol. This is the seam, and it is where the
// VAPID key's lifecycle lives.
//
// IT SENDS NOTHING. Delivery — dialling a push service, `410` handling, retry — is its own slice,
// because it is the only part with network egress and it wants its own thinking about timeouts.
package pushsvc

import (
	"fmt"
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
}

// IDFunc mints a subscription id. Injected so tests are deterministic.
type IDFunc func() string

// Service owns the VAPID key's lifecycle and the subscription list.
type Service struct {
	store Store
	newID IDFunc
	now   func() time.Time
}

func New(s Store, newID IDFunc, now func() time.Time) *Service {
	return &Service{store: s, newID: newID, now: now}
}

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
			ID:        r.ID,
			Label:     r.Label,
			State:     "live",
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
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
func (s *Service) Subscribe(endpoint, p256dh, auth, label string) (string, error) {
	if endpoint == "" || p256dh == "" || auth == "" {
		return "", fmt.Errorf("pushsvc: subscription is missing endpoint or keys")
	}
	if _, _, err := push.ParseSubscriptionKeys(p256dh, auth); err != nil {
		return "", err
	}
	id := s.newID()
	err := s.store.AddPushSubscription(store.PushSubscription{
		ID: id, Endpoint: endpoint, P256DH: p256dh, Auth: auth,
		Label: label, CreatedAt: s.now(),
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// Unsubscribe removes one by id, reporting whether a row went.
func (s *Service) Unsubscribe(id string) (bool, error) { return s.store.DeletePushSubscription(id) }
