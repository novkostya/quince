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
// deviceLabel names this device for the settings list.
//
// IT MUST NAME A PLATFORM, NOT SAY "This device". The fallback used to be the literal string
// "This device", so a Mac subscribed as "This device · Safari" — which is meaningless in a list
// whose whole job is to tell devices apart, and actively wrong when read from a different device.
// Operator-reported 2026-08-18, with an iPhone and a Mac subscribed at once.
//
// COARSE ON PURPOSE (spec D8). This is stored server-side and rendered on a screen, so it is a
// platform and a browser and never a User-Agent verbatim — a UA string is a fingerprint, and this
// list is meant to be readable rather than precise.
function deviceLabel(): string {
  const ua = navigator.userAgent;
  const platform = /iPhone/.test(ua)
    ? "iPhone"
    : /iPad/.test(ua)
      ? "iPad"
      : /Android/.test(ua)
        ? "Android"
        : /Macintosh/.test(ua)
          ? "Mac"
          : /Windows/.test(ua)
            ? "Windows PC"
            : /Linux/.test(ua)
              ? "Linux"
              : "Browser";
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
      const created = await api.post<{ id: string }>("/api/notifications/subscriptions", {
        endpoint: sub.endpoint,
        keys: { p256dh: json.keys?.p256dh ?? "", auth: json.keys?.auth ?? "" },
        label: deviceLabel(),
      });
      // THE ID IS WHAT MAKES "this device" ANSWERABLE LATER. It is not a capability — the endpoint
      // and keys never leave this function — so the browser may keep it.
      rememberSubscription(created.id);
      return created;
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
      if (id === rememberedSubscription()) {
        // Only when it is OUR row. Turning off another device from this one must not make this
        // browser forget its own subscription — which would then render as Off while still live.
        forgetSubscription();
      }
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

// --- which subscription belongs to THIS browser ---
//
// THE PAGE USED TO ANSWER THIS WITH "IS ANY SUBSCRIPTION LIVE", AND THAT IS A LIE ON EVERY DEVICE
// BUT ONE. Operator-reported 2026-08-18: subscribe on an iPhone, then open quince on a Mac, and the
// Mac says *"This device — On"* about a device that has no subscription at all. Worse, it is a trap:
// the only control offered is "Turn off", so the way to enable the Mac was to turn OFF the iPhone —
// deleting a working subscription — and then turn on. Afterwards the iPhone claimed On, also falsely.
//
// The state-honesty rule is explicit that nothing claims more than was proven, and "some device
// somewhere is subscribed" was being rendered as "this one is".
//
// THE SERVER CANNOT ANSWER IT, BY DESIGN. An endpoint is capability-grade and never comes back from
// the API (spec D8), so there is nothing in the list to compare a browser's own subscription
// against. The id quince mints at subscribe time is the one non-capability handle both sides hold,
// so the browser keeps it.

const subscriptionIDKey = "quince.push.subscription-id";

// rememberSubscription records the id quince assigned to THIS browser's subscription.
//
// `localStorage` AND NOT A COOKIE OR THE DB: it must be per-browser-profile, survive a reload, and
// never travel with a request. It holds an opaque id and nothing that could push to anybody.
function rememberSubscription(id: string) {
  try {
    localStorage.setItem(subscriptionIDKey, id);
  } catch {
    /* Private browsing can refuse. The consequence is a page that says Off while the browser is
       subscribed, which is the SAFE direction to be wrong: it offers to turn on, and re-subscribing
       is idempotent server-side (the row is keyed on endpoint and revived, not duplicated). */
  }
}

function forgetSubscription() {
  try {
    localStorage.removeItem(subscriptionIDKey);
  } catch {
    /* Same as above; a stale id is checked against the server list before it is believed. */
  }
}

function rememberedSubscription(): string | null {
  try {
    return localStorage.getItem(subscriptionIDKey);
  } catch {
    return null;
  }
}

// useThisDevice answers whether THIS browser is subscribed, and which row is its own.
//
// BOTH HALVES ARE REQUIRED. The browser's own `pushManager` is the only authority on whether this
// device has a subscription; the server list is the only authority on whether quince still knows
// about it. A browser holding a registration quince deleted from another device is Off — it will
// receive nothing — and saying On there is the same lie in the other direction.
export function useThisDevice() {
  const q = useNotifications();
  const browser = useQuery({
    queryKey: ["push", "browser-subscription"],
    queryFn: async () => {
      if (!("serviceWorker" in navigator)) return false;
      const reg = await navigator.serviceWorker.ready;
      return (await reg.pushManager.getSubscription()) !== null;
    },
    enabled: pushSupport() === "supported",
  });

  const id = rememberedSubscription();
  const mine = (q.data?.subscriptions ?? []).find((s) => s.id === id && s.state === "live");
  return { on: Boolean(browser.data && mine), id: mine?.id ?? null };
}
