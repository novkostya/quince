package httpapi

import (
	"errors"
	"net/http"

	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// The Web Push subscription surface (qn.12 spec D10, contracts §1).
//
// EVERY ROUTE IS BEHIND `authGuard` AND THE CSRF GUARD, AND NOTHING IS ADDED TO `authExempt`. The
// exempt set is fifteen routes and every one of them exists because it is reachable BEFORE a session
// can exist — obtaining a credential, or explaining why you cannot get one. Nothing here is: a
// subscription is a device belonging to somebody who is already logged in, and the list is a
// capability inventory.
//
// CATEGORY TOGGLES ARE NOT HERE, deliberately. They are config, written through `PUT /api/config`,
// which is what keeps them hand-editable and restart-free (D12). A fifth endpoint would make them
// UI-only state, which the config contract forbids.

// handleNotificationsGet answers what the settings surface needs to render.
//
// `vapid_public_key` IS PUBLIC BY CONSTRUCTION — it is the `applicationServerKey` every subscription
// must be created against, so a browser cannot subscribe without it. Serving it is not a disclosure;
// withholding it would simply make the feature unusable.
//
// IT GENERATES THE KEYPAIR ON FIRST READ, which is why this is the endpoint the UI calls before
// offering to subscribe. The generation rules — never regenerate silently, never rotate — live in
// `pushsvc` and are the Operator's ruling (quince#1128), not this handler's to reinterpret.
func (d Deps) handleNotificationsGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, err := d.Notifications.VAPIDPublicKey()
		if err != nil {
			// A DIVERGENT DB IS A 500 THAT SAYS WHAT TO DO. `ErrVAPIDKeyMissing` means subscriptions
			// exist without a signing key — the app DB was partially restored — and the remedy is in
			// the error text: every device must re-subscribe. Collapsing it to a bare "internal
			// error" would hide the one sentence the operator needs.
			if errors.Is(err, store.ErrVAPIDKeyMissing) {
				writeError(w, d.Log, http.StatusInternalServerError, "vapid_key_missing", err.Error())
				return
			}
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not read the notification key")
			return
		}
		subs, err := d.Notifications.Subscriptions()
		if err != nil {
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not list subscriptions")
			return
		}
		// A NON-NIL SLICE, so the field renders as `[]` rather than `null`. A client that has to
		// distinguish "no subscriptions" from "the field is missing" is a client written around a
		// wire quirk.
		if subs == nil {
			subs = []wire.PushSubscription{}
		}
		writeJSON(w, d.Log, http.StatusOK, wire.NotificationsResponse{
			VAPIDPublicKey: key,
			Subscriptions:  subs,
		})
	}
}

// handleNotificationsSubscribe records a browser's subscription. → 201 | 400 | 422 | 500.
func (d Deps) handleNotificationsSubscribe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Endpoint string `json:"endpoint"`
			Keys     struct {
				P256DH string `json:"p256dh"`
				Auth   string `json:"auth"`
			} `json:"keys"`
			Label string `json:"label"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
			return
		}
		id, err := d.Notifications.Subscribe(body.Endpoint, body.Keys.P256DH, body.Keys.Auth, body.Label)
		if err != nil {
			// A 422, NOT A 500, AND THE ERROR TEXT IS SAFE TO RETURN. `pushsvc` refuses a malformed
			// endpoint or key, which is the caller's input being wrong — and its messages name the
			// field and what was wrong with it without echoing the value. The alternative, a bare
			// "invalid subscription", leaves a browser bug undiagnosable.
			writeError(w, d.Log, http.StatusUnprocessableEntity, "invalid_subscription", err.Error())
			return
		}
		writeJSON(w, d.Log, http.StatusCreated, struct {
			ID string `json:"id"`
		}{ID: id})
	}
}

// handleNotificationsUnsubscribe removes one subscription. → 204 | 404 | 500.
//
// 404 ON AN UNKNOWN ID rather than a silent 204. "It is gone either way" is true of the outcome and
// false of the question asked, and a UI that cannot tell a successful removal from a stale row it
// was still showing will leave that row on the screen.
func (d Deps) handleNotificationsUnsubscribe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		gone, err := d.Notifications.Unsubscribe(id)
		if err != nil {
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not remove the subscription")
			return
		}
		if !gone {
			writeError(w, d.Log, http.StatusNotFound, "not_found", "no such subscription")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleNotificationsTest sends one notification to every live subscription. → 202 | 500.
//
// IT EXISTS BECAUSE "IS THIS WORKING?" IS OTHERWISE UNANSWERABLE without waiting for a device to go
// stale — three days by default (spec D10). It is what the rung's click-list uses and what a support
// conversation starts with.
//
// 202 WITH THE OUTCOMES IN THE BODY, not 200 and not a bare 204. Delivery is per-device and partial
// success is the NORMAL case — one phone live, one gone — so a single status could only be true of
// one of them. The body names each device by label; an endpoint never appears, because this response
// is the kind of thing that gets pasted into an issue.
func (d Deps) handleNotificationsTest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results, err := d.Notifications.SendTest(r.Context())
		if err != nil {
			writeError(w, d.Log, http.StatusInternalServerError, "internal",
				"could not send the test notification")
			return
		}
		if results == nil {
			// A NON-NIL SLICE so the field is `[]` rather than `null` — and `[]` is a real answer
			// here: it means there is nobody subscribed, which the screen must be able to say.
			results = []wire.PushDeliveryResult{}
		}
		writeJSON(w, d.Log, http.StatusAccepted, struct {
			Results []wire.PushDeliveryResult `json:"results"`
		}{Results: results})
	}
}
