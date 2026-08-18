import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { isIOS, pushSupport } from "@/lib/pwa";
import { useNotifications, useSendTest, useSubscribe, useUnsubscribe } from "@/lib/notifications";

// The install step for notifications (qn.12, spec D1).
//
// DETECT → INSTRUCT → CONFIRM, IN THAT ORDER AND NO OTHER. Web Push on iOS requires the web app to
// have been added to the Home Screen, and the permission prompt must hang off a real tap. So this
// page never offers a control the platform will refuse: a button that does nothing is the *no silent
// caps* failure in its most literal form.
//
// THE ENABLE CONTROL IS NOT HERE YET, and its absence is deliberate rather than unfinished. This
// slice ships the precondition and the worker; the subscribe control and the five-cause status
// surface arrive with the API that backs them. A control rendered now would either lie about being
// wired or need a "coming soon" label, and neither belongs on a screen.
export function NotificationsInstallPage() {
  const support = pushSupport();
  const ios = isIOS();

  return (
    // `min-h-dvh` and the safe-area padding follow the other pre-shell pages; `OnboardingHTTPSPage`
    // carries the full reasoning for `dvh` over `lvh`.
    <div className="min-h-dvh overflow-x-clip bg-bg pb-16 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(2.5rem,env(safe-area-inset-top))] text-fg">
      <div className="mx-auto max-w-2xl space-y-6">
        <header className="space-y-2">
          <h1 className="text-2xl font-semibold">Notifications</h1>
          <p className="text-fg-muted">
            quince can tell you when a device is due for a backup, and when one needs you.
          </p>
        </header>

        {support === "unsupported_platform" && (
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                Notifications are unavailable on this browser
                <Badge tone="warn">Unavailable</Badge>
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3 text-fg-muted">
              {/* LOCKDOWN MODE IS NAMED AS LIKELY, NOT ASSERTED (quince#510, spec D7 and G7).
                  WebKit disables service workers and the Push API declaratively in Lockdown Mode on
                  any certificate, and service workers have shipped since iOS 11.3 — so on a current
                  iOS their absence is the signature. Detection still cannot PROVE it, and a screen
                  that states an unproven cause is a state-honesty failure.

                  THERE IS NO WORKAROUND AND THE COPY SAYS SO. Offering one would send a person to
                  fiddle with settings that cannot help. */}
              {ios ? (
                <p>
                  This is most likely Lockdown Mode, which turns off web notifications for every
                  website. quince cannot work around it.
                </p>
              ) : (
                <p>This browser does not support web notifications. quince cannot work around it.</p>
              )}
              <p>
                Open quince to see which devices are due — the Devices list shows how long it has
                been since each one was backed up.
              </p>
            </CardContent>
          </Card>
        )}

        {support === "unsupported_ios_version" && (
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                This iPhone or iPad needs a newer iOS
                <Badge tone="warn">Unavailable</Badge>
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3 text-fg-muted">
              {/* NOT the Lockdown Mode copy, and that is the point of this state existing. Lockdown
                  Mode removes service workers as well, so having one rules it out — naming it here
                  would send someone to check a setting that is not the cause. */}
              <p>
                quince is on your Home Screen, but notifications for web apps need iOS 16.4 or later.
              </p>
              <p>
                Open quince to see which devices are due — the Devices list shows how long it has
                been since each one was backed up.
              </p>
            </CardContent>
          </Card>
        )}

        {support === "needs_install" && (
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                Add quince to your Home Screen
                <Badge tone="accent">Step 1</Badge>
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3 text-fg-muted">
              {/* THE LITERAL GESTURE, WITH THE GLYPH NAMED. "Install" is not a word that appears
                  anywhere in iOS, so an instruction using it sends a person looking for a button
                  that does not exist.

                  NO NON-iOS BRANCH, AND ITS ABSENCE IS THE FIX RATHER THAN AN OMISSION.
                  `needs_install` now means *installing would help*, which is true only on iOS —
                  everywhere else push works in an ordinary tab, so a browser without it is not one
                  installing can rescue. This card previously carried an "install from your address
                  bar" line that was reachable ONLY when it was wrong. */}
              <p>
                On iPhone and iPad, notifications only work once quince has been added to your Home
                Screen.
              </p>
              <ol className="list-decimal space-y-1 pl-5">
                <li>
                  Tap the Share button — the square with an arrow pointing up, in Safari&rsquo;s
                  toolbar.
                </li>
                <li>
                  Scroll down and tap <strong>Add to Home Screen</strong>.
                </li>
                <li>Open quince from your Home Screen and come back to this page.</li>
              </ol>
            </CardContent>
          </Card>
        )}

        {support === "supported" && <NotificationsControls />}
      </div>
    </div>
  );
}

// The five-cause status surface (qn.12, spec D6).
//
// quince#1124 item 4 names this as quince#940's defect in waiting: *"notifications are off" has at
// least five causes and quince can tell them apart.* Collapsing them into one true, useless sentence
// is what leaves somebody toggling a setting that was never the problem. Each state below renders its
// OWN remedy, and the remedies are genuinely different — tap a button, open iOS Settings, edit a
// config key, re-subscribe on this device.
function NotificationsControls() {
  const q = useNotifications();
  const subscribe = useSubscribe();
  const unsubscribe = useUnsubscribe();
  const sendTest = useSendTest();
  const permission = typeof Notification === "undefined" ? "default" : Notification.permission;

  // PERMISSION DENIED IS TERMINAL FROM HERE, and saying so is the honest thing. The platform will
  // not re-prompt after a denial, so a button would do nothing — the remedy is outside quince.
  if (permission === "denied") {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            Notifications are blocked for quince
            <Badge tone="warn">Blocked</Badge>
          </CardTitle>
        </CardHeader>
        <CardContent className="text-fg-muted">
          <p>
            You turned notifications off for quince. quince cannot ask again — turn them back on in
            iOS Settings → Notifications → quince, then come back to this page.
          </p>
        </CardContent>
      </Card>
    );
  }

  const live = (q.data?.subscriptions ?? []).filter((s) => s.state === "live");
  const expired = (q.data?.subscriptions ?? []).filter((s) => s.state !== "live");

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            This device
            {live.length > 0 ? <Badge tone="ok">On</Badge> : <Badge tone="neutral">Off</Badge>}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 text-fg-muted">
          {live.length > 0 ? (
            <p>quince will notify this device when a backup is due or needs you.</p>
          ) : (
            <p>Turn on notifications to hear when a device is due for a backup, or needs you.</p>
          )}
          {/* THE PROMPT MUST HANG OFF A REAL TAP — a platform requirement, not a preference, which
              is why this is a button and not something the page does on mount. */}
          {live.length === 0 && (
            <Button
              onClick={() => q.data && subscribe.mutate(q.data.vapid_public_key)}
              disabled={!q.data || subscribe.isPending}
            >
              {subscribe.isPending ? "Turning on…" : "Turn on notifications"}
            </Button>
          )}
          {subscribe.isError && (
            <p className="text-danger">
              {String(subscribe.error).includes("permission_denied")
                ? "You declined. Turn notifications on in iOS Settings → Notifications → quince."
                : "That did not work. Try again, and if it keeps failing check that quince was opened from your Home Screen."}
            </p>
          )}
          {/* SEND TEST IS THE ONLY WAY TO PROVE THIS WORKS WITHOUT WAITING DAYS. The next real
              notification is whenever a device next goes stale — three days by default — so without
              this the setup flow ends on "we think that worked". It is placed on the same card as
              the switch, because "turn it on" and "check it arrived" are one task. */}
          {live.length > 0 && (
            <div className="space-y-2">
              <Button
                variant="outline"
                onClick={() => sendTest.mutate()}
                disabled={sendTest.isPending}
              >
                {sendTest.isPending ? "Sending…" : "Send a test notification"}
              </Button>
              {/* PER DEVICE, NEVER A SINGLE VERDICT. With two phones subscribed, "sent" would be a
                  lie about the one that failed, and the remedy differs per state. */}
              {sendTest.isSuccess && (
                <ul className="space-y-1 text-sm">
                  {sendTest.data.results.length === 0 && (
                    <li>No devices are subscribed, so nothing was sent.</li>
                  )}
                  {sendTest.data.results.map((r) => (
                    <li key={r.label + r.state}>
                      {r.state === "sent" && <span>Sent to {r.label}. Check its lock screen.</span>}
                      {r.state === "expired" && (
                        <span className="text-warn">
                          {r.label} is no longer reachable — turn notifications on again on that
                          device.
                        </span>
                      )}
                      {r.state === "error" && (
                        <span className="text-danger">
                          {r.label} did not receive it. Try again in a moment.
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              )}
              {sendTest.isError && (
                <p className="text-danger">quince could not send the test. Try again in a moment.</p>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      {/* AN EXPIRED SUBSCRIPTION IS SHOWN, NOT HIDDEN (spec D8). A device that quietly stopped
          receiving is the failure whose first symptom would otherwise be a missed backup, and the
          remedy — re-enable on THAT device — needs the device named. */}
      {expired.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              A device stopped receiving
              <Badge tone="warn">Needs attention</Badge>
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-fg-muted">
            <p>
              These devices are no longer reachable. Open quince on each one and turn notifications
              on again.
            </p>
            <ul className="space-y-1">
              {expired.map((s) => (
                <li key={s.id} className="flex items-center justify-between gap-3">
                  <span>{s.label}</span>
                  <Button variant="ghost" onClick={() => unsubscribe.mutate(s.id)}>
                    Remove
                  </Button>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}

      {live.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Devices receiving notifications</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="space-y-1 text-fg-muted">
              {live.map((s) => (
                <li key={s.id} className="flex items-center justify-between gap-3">
                  <span>{s.label}</span>
                  <Button variant="ghost" onClick={() => unsubscribe.mutate(s.id)}>
                    Turn off
                  </Button>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
