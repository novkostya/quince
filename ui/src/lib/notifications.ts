import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import type { NotificationsResponse, NotificationsTestResponse } from "./types";
import { pushSupport } from "./pwa";

// The Web Push subscription client (qn.12, contracts §1).
//
// EVERY CAPABILITY STAYS IN THE BROWSER. The endpoint and keys a subscription carries go to quince
// once, on create, and never come back — the list the server returns is labels, states and
// timestamps. So nothing in this module holds anything that could push to a device.

export const notificationsKey = ["notifications"] as const;

export function useNotifications() {
  return useQuery({
    queryKey: notificationsKey,
    queryFn: () => api.get<NotificationsResponse>("/api/notifications"),
    // ONLY WHERE PUSH COULD WORK. `GET /api/notifications` GENERATES the VAPID keypair on first
    // read, so calling it from a browser that can never subscribe would mint a key for an install
    // that has no use for one. Harmless but untidy, and the query is pointless there anyway.
    enabled: pushSupport() === "supported",
  });
}

// urlBase64ToUint8Array converts the server's `applicationServerKey` for `pushManager.subscribe`.
//
// IT HAS TO EXIST, AND THAT IS THE WEB PUSH API'S FAULT RATHER THAN A CHOICE. `subscribe` takes a
// BufferSource, the key travels as base64url, and `atob` speaks standard base64 — so the two
// alphabet substitutions and the padding are mandatory. Getting this wrong yields
// `InvalidCharacterError` at subscribe time, which names neither the field nor the cause.
function urlBase64ToUint8Array(base64url: string): Uint8Array {
  const padded = base64url.padEnd(base64url.length + ((4 - (base64url.length % 4)) % 4), "=");
  const raw = atob(padded.replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

// deviceLabel is what the settings list calls this device.
//
// COARSE ON PURPOSE. It has to be recognisable in a list of two or three, and it must never be a
// UDID or a verbatim User-Agent: the first is Operator-private and the second is a fingerprint
// nobody asked to store. Platform plus browser family is enough to tell "my phone" from "the iPad".
function deviceLabel(): string {
  const ua = navigator.userAgent;
  const platform = /iPhone/.test(ua) ? "iPhone" : /iPad/.test(ua) ? "iPad" : /Android/.test(ua) ? "Android" : "This device";
  const browser = /CriOS|Chrome/.test(ua) ? "Chrome" : /Firefox/.test(ua) ? "Firefox" : "Safari";
  return `${platform} · ${browser}`;
}

// subscribe asks the platform for permission and registers the result with quince.
//
// THE PERMISSION PROMPT MUST HANG OFF A REAL TAP — WebKit requires it, and a `useEffect` that
// subscribed on mount would be refused by the platform with nothing rendered to say so. That is why
// this is a mutation a button calls and not something a query does on its own.
export function useSubscribe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vapidPublicKey: string) => {
      const permission = await Notification.requestPermission();
      if (permission !== "granted") {
        // A REFUSAL IS NOT AN ERROR TO SWALLOW. The caller renders the reason, because "denied" and
        // "dismissed" have different remedies and only the user can act on either.
        throw new Error(permission === "denied" ? "permission_denied" : "permission_dismissed");
      }
      const reg = await navigator.serviceWorker.ready;
      const sub = await reg.pushManager.subscribe({
        // REQUIRED BY EVERY BROWSER THAT MATTERS, and not merely conventional: a subscription that
        // can deliver silently is one Chrome refuses to create and iOS would use to wake the app
        // without telling anybody. quince always shows a notification, so this costs nothing.
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(vapidPublicKey),
      });
      const json = sub.toJSON();
      return api.post<{ id: string }>("/api/notifications/subscriptions", {
        endpoint: sub.endpoint,
        keys: { p256dh: json.keys?.p256dh ?? "", auth: json.keys?.auth ?? "" },
        label: deviceLabel(),
      });
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: notificationsKey }),
  });
}

// useUnsubscribe removes a subscription from quince AND from the browser.
//
// BOTH SIDES, OR THE DEVICE KEEPS A LIVE SUBSCRIPTION THE SERVER HAS FORGOTTEN — which is not a leak
// (nothing can push to it without the keys quince just dropped) but does mean the browser reports
// itself subscribed while the settings list says otherwise, and the user cannot re-enable because
// `subscribe` returns the existing registration.
export function useUnsubscribe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      await api.del<void>(`/api/notifications/subscriptions/${encodeURIComponent(id)}`);
      // BEST EFFORT, AND AFTER the server call. If the browser half fails the row is still gone
      // server-side, which is the half that decides whether anything is sent.
      try {
        const reg = await navigator.serviceWorker.ready;
        const sub = await reg.pushManager.getSubscription();
        await sub?.unsubscribe();
      } catch {
        /* The platform's own bookkeeping; nothing quince can do about it and nothing it breaks. */
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: notificationsKey }),
  });
}

// useSendTest asks quince to push one notification to every live subscription, right now.
//
// IT IS WHAT MAKES THE FEATURE INSTALLABLE BY A PERSON. Without it the only proof that
// notifications work is to wait for a device to go stale — three days by default — and the only
// diagnosis available on failure is "nothing arrived", which does not distinguish a declined
// permission from a dead subscription from a push service that is down.
//
// IT INVALIDATES THE LIST, because a test is also a probe: a 410 marks that subscription expired
// server-side, so the device list is stale the moment this returns. That is the whole reason the
// endpoint reports per-device state rather than a boolean.
export function useSendTest() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<NotificationsTestResponse>("/api/notifications/test", {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: notificationsKey }),
  });
}
