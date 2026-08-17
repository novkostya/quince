// quince's service worker — PUSH AND NOTIFICATION CLICKS ONLY (qn.12, spec D2).
//
// THERE IS NO `fetch` HANDLER AND THAT IS A BOUNDARY, NOT A PHASE-1 SIMPLIFICATION. Caching the SPA
// buys little on a LAN and introduces stale-UI-after-upgrade — the defect class `webui.handlerFor`'s
// cache policy was written to prevent (index.html `no-cache`, hashed assets immutable). A worker
// that cached would defeat it from the other side, and the first symptom is a deploy that is
// invisible until the user clears their browser storage. Do not add one to "make it a real PWA".
//
// WHY THIS FILE EXISTS AT ALL, given Declarative Web Push needs no worker: declarative landed in
// iOS/iPadOS 18.4, and quince supports every iOS that can receive Web Push at all — 16.4 and up.
// Between those two versions a worker is the ONLY mechanism. On 18.4+ both are live and WebKit gives
// this handler first refusal, falling back to the payload's own `notification` object if it does not
// display one in time.

// The payload is the Declarative Web Push envelope: {"web_push": 8030, "notification": {...}}.
// This worker reads the SAME `notification` object the declarative path would, so there is no second
// payload shape to keep in sync with the server.
self.addEventListener("push", (event) => {
  // A push with no data is not something quince sends. Showing nothing is right — a generic
  // "something happened" notification is the one true, useless sentence this rung exists to avoid.
  if (!event.data) return;

  let payload;
  try {
    payload = event.data.json();
  } catch {
    // Malformed JSON means a sender that is not quince, or a truncated delivery. Silence beats a
    // notification quince cannot describe.
    return;
  }

  const n = payload && payload.notification;
  // A NON-EMPTY TITLE IS REQUIRED by the notification API and by Declarative Web Push; without one
  // the user agent drops the message and records nothing anywhere. The server refuses to build such
  // a payload, so reaching here means something else sent it.
  if (!n || !n.title) return;

  event.waitUntil(
    self.registration.showNotification(n.title, {
      body: n.body,
      icon: "/icon-192.png",
      badge: "/icon-192.png",
      // `data.navigate` is what the click handler below opens. Carried through rather than read from
      // the notification's own fields, because `showNotification` does not preserve unknown keys.
      data: { navigate: n.navigate },
      // A quince notification always says WHICH device, so two devices needing attention produce two
      // notifications rather than one replacing the other. Grouped by kind+navigate: a second
      // reminder for the SAME device does replace the first, which is the behaviour that keeps a
      // stale device from stacking up a week of identical rows.
      tag: (n.kind || "quince") + ":" + (n.navigate || ""),
    }),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const target = (event.notification.data && event.notification.data.navigate) || "/";

  // FOCUS AN OPEN WINDOW RATHER THAN OPENING A SECOND ONE. On a home-screen web app a second window
  // is a second copy of the app, and the user's session, scroll position and any in-flight job view
  // are in the first.
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((clients) => {
      for (const client of clients) {
        if ("focus" in client) {
          // `navigate` can reject when the client is not controlled by this worker; focusing is the
          // part that must happen, so it comes first and the navigation is best-effort.
          const focused = client.focus();
          if ("navigate" in client) {
            return Promise.resolve(focused).then(() => client.navigate(target).catch(() => {}));
          }
          return focused;
        }
      }
      return self.clients.openWindow(target);
    }),
  );
});
