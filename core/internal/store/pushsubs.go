package store

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// Web Push subscription storage and the VAPID signing key (qn.12, Operator ruling quince#1128).
//
// EVERY FIELD OF A SUBSCRIPTION IS CAPABILITY-GRADE — anyone holding the endpoint and the two keys
// can push to that phone — so nothing here logs one, and `push.RedactEndpoint` is what any caller
// prints. That is a rule about the callers; this file simply never has a log line.

// VAPIDKeySetting is where the signing key lives, in `settings`, beside the argon2id admin hash.
//
// ONE ARTIFACT WITH THE SUBSCRIPTIONS, WHICH IS THE RULING'S REASON rather than a filing choice: the
// state that must never occur is *subscriptions exist, signing key does not*, and putting both in
// the app DB makes it unrepresentable. They are lost together or not at all.
const VAPIDKeySetting = "push.vapid_private_key"

// PushSubscription is one device's Web Push registration.
type PushSubscription struct {
	ID       string
	Endpoint string
	P256DH   string
	Auth     string
	Label    string
	// Origin is the address the subscribing browser reached quince by — the scheme and authority a
	// notification's `navigate` URL must be absolute against. Empty for rows created before
	// migration 0012, which is a refusal at send time and never a guess.
	Origin     string
	CreatedAt  time.Time
	ExpiredAt  *time.Time // non-nil once the push service answered 410/404
	LastSentAt *time.Time
}

// Live reports whether this subscription can still receive.
func (p PushSubscription) Live() bool { return p.ExpiredAt == nil }

// ErrSubscriptionExists is returned when the endpoint is already registered.
//
// AN ENDPOINT IS THE IDENTITY, not the row id: a browser that re-subscribes after clearing its
// storage produces the same endpoint, and two rows for one endpoint would mean two pushes to one
// phone for every notification.
var ErrSubscriptionExists = errors.New("store: subscription already exists")

// AddPushSubscription records a new subscription.
//
// RE-SUBSCRIBING REVIVES AN EXPIRED ROW rather than refusing or duplicating. That is the documented
// way an `expired` row leaves the list (spec D8) — the same device coming back — and it is the case
// a user reaches by tapping Enable again after being told their subscription died.
func (s *Store) AddPushSubscription(p PushSubscription) error {
	res, err := s.db.Exec(
		`UPDATE push_subscriptions
		    SET p256dh = ?, auth = ?, label = ?, origin = ?, expired_at = NULL
		  WHERE endpoint = ?`,
		p.P256DH, p.Auth, p.Label, p.Origin, p.Endpoint)
	if err != nil {
		return fmt.Errorf("store: revive subscription: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return nil
	}
	_, err = s.db.Exec(
		`INSERT INTO push_subscriptions (id, endpoint, p256dh, auth, label, origin, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Endpoint, p.P256DH, p.Auth, p.Label, p.Origin, p.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("store: add subscription: %w", err)
	}
	return nil
}

// PushSubscriptions returns every subscription, live and expired, newest first.
//
// EXPIRED ROWS ARE RETURNED, and that is the feature. The status surface has to show a device that
// stopped receiving; a list that quietly omitted it would make the failure invisible, which is the
// silent-fallback this rung is written against.
func (s *Store) PushSubscriptions() ([]PushSubscription, error) {
	rows, err := s.db.Query(
		`SELECT id, endpoint, p256dh, auth, label, origin, created_at, expired_at, last_sent_at
		   FROM push_subscriptions ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []PushSubscription
	for rows.Next() {
		var p PushSubscription
		var created string
		var origin, expired, lastSent sql.NullString
		if err := rows.Scan(&p.ID, &p.Endpoint, &p.P256DH, &p.Auth, &p.Label, &origin, &created, &expired, &lastSent); err != nil {
			return nil, fmt.Errorf("store: scan subscription: %w", err)
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339, created)
		p.Origin = origin.String
		if expired.Valid {
			if t, err := time.Parse(time.RFC3339, expired.String); err == nil {
				p.ExpiredAt = &t
			}
		}
		if lastSent.Valid {
			if t, err := time.Parse(time.RFC3339, lastSent.String); err == nil {
				p.LastSentAt = &t
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ExpirePushSubscription marks a subscription dead after a 410/404.
//
// IT DOES NOT DELETE. Deleting is what makes a phone that quietly stopped receiving invisible, and
// the first symptom of that is a missed backup. The row stays, marked, until the device re-subscribes
// or the user removes it — so there is no time-based pruning and no cap to surface (spec D8).
func (s *Store) ExpirePushSubscription(endpoint string, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE push_subscriptions SET expired_at = ? WHERE endpoint = ? AND expired_at IS NULL`,
		at.UTC().Format(time.RFC3339), endpoint)
	if err != nil {
		return fmt.Errorf("store: expire subscription: %w", err)
	}
	return nil
}

// DeletePushSubscription removes one by id, reporting whether a row went.
func (s *Store) DeletePushSubscription(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("store: delete subscription: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MarkPushSent records a successful delivery.
func (s *Store) MarkPushSent(endpoint string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE push_subscriptions SET last_sent_at = ? WHERE endpoint = ?`,
		at.UTC().Format(time.RFC3339), endpoint)
	return err
}

// ErrVAPIDKeyMissing is returned when subscriptions exist and the signing key does not.
//
// THIS IS THE STATE THE RULING MAKES UNREACHABLE BY ORDINARY MEANS, so meeting it means the DB was
// tampered with or partially restored. quince holds the inputs to say so, and the ruling is explicit
// that it must: **never regenerate a keypair silently** here. A fresh key would leave every
// subscribed phone holding a subscription quince can no longer sign for, and quince would look
// healthy while nothing arrived.
var ErrVAPIDKeyMissing = errors.New(
	"store: subscriptions exist but the VAPID signing key does not — the app DB was partially " +
		"restored or altered. quince will NOT mint a new key, because that would silently invalidate " +
		"every subscription. Every device must re-subscribe")

// VAPIDPrivateKey returns the stored signing key.
//
// `ok=false` with no error means a clean install: no key AND no subscriptions, so the caller
// generates one. `ErrVAPIDKeyMissing` means the divergent state above — subscriptions without a key —
// which is a refusal rather than a prompt to generate.
//
// THERE IS NO ROTATION AND NOTHING SHOULD OFFER ONE (ruling, quince#1128). It is destructive by
// construction: the public half is baked into every subscription a phone has ever created, so
// "rotate" means "silently stop reaching every device". There is no operator need it serves, which
// is why the absence of a `RotateVAPIDKey` here is a decision rather than an omission.
func (s *Store) VAPIDPrivateKey() (key []byte, ok bool, err error) {
	v, found, err := s.GetSetting(VAPIDKeySetting)
	if err != nil {
		return nil, false, err
	}
	if found {
		return decodeVAPID(v)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM push_subscriptions`).Scan(&n); err != nil {
		return nil, false, fmt.Errorf("store: count subscriptions: %w", err)
	}
	if n > 0 {
		return nil, false, ErrVAPIDKeyMissing
	}
	return nil, false, nil
}

// SetVAPIDPrivateKey stores a newly generated key. It REFUSES to overwrite one.
//
// The guard is here rather than left to callers because "generate if absent" is the only correct
// use, and a second caller racing the first — two startups, a retried request — would otherwise
// replace a key that subscriptions already depend on.
//
// `SetSettingIfAbsent`, NOT `SetSetting`, AND THAT IS THE WHOLE GUARD. `SetSetting` is
// `ON CONFLICT DO UPDATE` — an upsert — so a read-then-write here would be check-then-act: two
// callers both see *not found*, both proceed, and the loser silently replaces the winner's key.
// That is precisely the outcome the sentence above claims to prevent, and the atomic form is what
// actually prevents it: `ON CONFLICT DO NOTHING`, decided by the database rather than by this
// process.
//
// The identical hazard and the identical remedy are one package over, at `auth/passkey.go` —
// *"two concurrent first registrations must not each mint a handle and have the second overwrite the
// first"*. A VAPID key has the worse loss profile of the two: overwriting it silently unsigns every
// subscription every phone has ever created (quince#1128).
func (s *Store) SetVAPIDPrivateKey(key []byte) error {
	inserted, err := s.SetSettingIfAbsent(VAPIDKeySetting, encodeVAPID(key))
	if err != nil {
		return err
	}
	if !inserted {
		return errors.New("store: a VAPID key already exists; replacing it would invalidate every subscription")
	}
	return nil
}

// encodeVAPID / decodeVAPID move the 32-octet scalar through a TEXT column.
//
// BASE64 RATHER THAN A BLOB COLUMN, so the row is greppable and dumpable with the same tools as the
// rest of `settings` — and so a truncated or corrupted value fails to DECODE rather than arriving as
// a short key that would produce signatures no push service accepts. The length check is what turns
// "somebody edited this row" into a refusal instead of notifications that silently never arrive.
func encodeVAPID(key []byte) string { return base64.RawURLEncoding.EncodeToString(key) }

func decodeVAPID(v string) ([]byte, bool, error) {
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, false, fmt.Errorf("store: the stored VAPID key is not base64url: %w", err)
	}
	if len(b) != 32 {
		return nil, false, fmt.Errorf("store: the stored VAPID key is %d octets, want 32", len(b))
	}
	return b, true, nil
}

// PushReminder returns when this device was last reminded, and whether it ever was.
func (s *Store) PushReminder(udid string) (time.Time, bool, error) {
	var at string
	err := s.db.QueryRow(`SELECT last_sent_at FROM push_reminders WHERE udid = ?`, udid).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := time.Parse(time.RFC3339, at)
	return t, err == nil, nil
}

// SetPushReminder records a reminder against the device's TRACK — not against a kind. Escalating
// from `backup_available` to `backup_overdue` writes the same row, which is what makes one lapse
// produce one push (spec D5).
func (s *Store) SetPushReminder(udid string, at time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO push_reminders (udid, last_sent_at) VALUES (?, ?)
		 ON CONFLICT(udid) DO UPDATE SET last_sent_at = excluded.last_sent_at`,
		udid, at.UTC().Format(time.RFC3339))
	return err
}

// ClearPushReminder forgets a device's place on the track, so the next lapse starts a fresh
// cooldown. Called after a successful backup: the thing being reminded about has happened.
func (s *Store) ClearPushReminder(udid string) error {
	_, err := s.db.Exec(`DELETE FROM push_reminders WHERE udid = ?`, udid)
	return err
}
