import { ChevronLeft } from "lucide-react";
import { BackLink } from "@/components/BackLink";
import { RelativeTime } from "@/components/RelativeTime";
import { Badge } from "@/components/ui/badge";
import { SectionHeading } from "@/components/ui/section-heading";
import { Button } from "@/components/ui/button";
import { isIOS, pushSupport } from "@/lib/pwa";
import {
  useNotifications,
  useSendTest,
  useSubscribe,
  useThisDevice,
  useUnsubscribe,
} from "@/lib/notifications";

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
    // A CHILD OF THE AUTHED SHELL, LAID OUT LIKE ONE. This page was built with the pre-shell
    // onboarding layout — `min-h-dvh`, its own safe-area padding, its own background and its own
    // `mx-auto max-w-2xl` — and then routed INSIDE the shell, which supplies all four already. The
    // result was a full-page layout nested in a full-page layout: doubled horizontal inset, a large
    // dead gap above the title, and a column that did not line up with any other settings page.
    // Operator-reported 2026-08-18 against the staging stand.
    //
    // The router knew and the component did not — `router.tsx` says "INSIDE the authed shell, unlike
    // the `/onboarding/*` routes" three lines from the comment here that claimed to follow them.
    // `SettingsAuthPage` is the pattern this now matches, because it is the other settings sub-page.
    <section>
      {/* A WAY BACK, for the reason SettingsAuthPage gives and one more: on a Home Screen web app
          there is no browser chrome, so the back gesture is all a phone user has and nothing on
          screen promises it. This page is reachable only from Settings, so it must return there. */}
      <BackLink
        to="/settings"
        className="-ml-1 inline-flex items-center gap-1 text-sm text-muted transition-colors hover:text-fg"
      >
        <ChevronLeft size={16} strokeWidth={1.75} />
        Settings
      </BackLink>
      <h1 className="mt-2 text-xl font-semibold tracking-tight">
        Notifications
      </h1>
      <p className="mt-1 text-sm text-muted">
        quince can tell you when a device is due for a backup, and when one
        needs you.
      </p>

      <div className="mt-6 max-w-xl space-y-8">
        {support === "unsupported_platform" && (
          <section>
            <SectionHeading className="flex items-center gap-2">
              Notifications are unavailable on this browser
              <Badge tone="warn">Unavailable</Badge>
            </SectionHeading>
            <div className="mt-3 space-y-3 text-sm text-muted">
              {/* LOCKDOWN MODE IS NAMED AS LIKELY, NOT ASSERTED (quince#510, spec D7 and G7).
                  WebKit disables service workers and the Push API declaratively in Lockdown Mode on
                  any certificate, and service workers have shipped since iOS 11.3 — so on a current
                  iOS their absence is the signature. Detection still cannot PROVE it, and a screen
                  that states an unproven cause is a state-honesty failure.

                  THERE IS NO WORKAROUND AND THE COPY SAYS SO. Offering one would send a person to
                  fiddle with settings that cannot help. */}
              {ios ? (
                <p>
                  This is most likely Lockdown Mode, which turns off web
                  notifications for every website. quince cannot work around it.
                </p>
              ) : (
                <p>
                  This browser does not support web notifications. quince cannot
                  work around it.
                </p>
              )}
              <p>
                Open quince to see which devices are due — the Devices list
                shows how long it has been since each one was backed up.
              </p>
            </div>
          </section>
        )}

        {support === "unsupported_ios_version" && (
          <section>
            <SectionHeading className="flex items-center gap-2">
              This iPhone or iPad needs a newer iOS
              <Badge tone="warn">Unavailable</Badge>
            </SectionHeading>
            <div className="mt-3 space-y-3 text-sm text-muted">
              {/* NOT the Lockdown Mode copy, and that is the point of this state existing. Lockdown
                  Mode removes service workers as well, so having one rules it out — naming it here
                  would send someone to check a setting that is not the cause. */}
              <p>
                quince is on your Home Screen, but notifications for web apps
                need iOS 16.4 or later.
              </p>
              <p>
                Open quince to see which devices are due — the Devices list
                shows how long it has been since each one was backed up.
              </p>
            </div>
          </section>
        )}

        {support === "needs_install" && (
          <section>
            <SectionHeading className="flex items-center gap-2">
              Add quince to your Home Screen
              <Badge tone="accent">Step 1</Badge>
            </SectionHeading>
            <div className="mt-3 space-y-3 text-sm text-muted">
              {/* THE LITERAL GESTURE, WITH THE GLYPH NAMED. "Install" is not a word that appears
                  anywhere in iOS, so an instruction using it sends a person looking for a button
                  that does not exist.

                  NO NON-iOS BRANCH, AND ITS ABSENCE IS THE FIX RATHER THAN AN OMISSION.
                  `needs_install` now means *installing would help*, which is true only on iOS —
                  everywhere else push works in an ordinary tab, so a browser without it is not one
                  installing can rescue. This card previously carried an "install from your address
                  bar" line that was reachable ONLY when it was wrong. */}
              <p>
                On iPhone and iPad, notifications only work once quince has been
                added to your Home Screen.
              </p>
              <ol className="list-decimal space-y-1 pl-5">
                <li>
                  Tap the Share button — the square with an arrow pointing up,
                  in Safari&rsquo;s toolbar.
                </li>
                <li>
                  Scroll down and tap <strong>Add to Home Screen</strong>.
                </li>
                <li>
                  Open quince from your Home Screen and come back to this page.
                </li>
              </ol>
            </div>
          </section>
        )}

        {support === "supported" && <NotificationsControls />}
      </div>
    </section>
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
  // THIS DEVICE, NOT ANY DEVICE. `live.length > 0` was answering "is somebody subscribed" and
  // rendering it as "you are" — see `useThisDevice` for what that cost on a second device.
  //
  // WITH THE OTHER HOOKS, ABOVE EVERY EARLY RETURN. Placed where it is used, it sat below the
  // `permission === "denied"` branch, and a hook called conditionally changes order between renders.
  const thisDevice = useThisDevice();
  const permission =
    typeof Notification === "undefined" ? "default" : Notification.permission;

  // PERMISSION DENIED IS TERMINAL FROM HERE, and saying so is the honest thing. The platform will
  // not re-prompt after a denial, so a button would do nothing — the remedy is outside quince.
  if (permission === "denied") {
    return (
      <section>
        <SectionHeading className="flex items-center gap-2">
          Notifications are blocked for quince
          <Badge tone="warn">Blocked</Badge>
        </SectionHeading>
        <div className="mt-3 space-y-3 text-sm text-muted">
          <p>
            You turned notifications off for quince. quince cannot ask again —
            turn them back on in iOS Settings → Notifications → quince, then
            come back to this page.
          </p>
        </div>
      </section>
    );
  }

  const live = (q.data?.subscriptions ?? []).filter((s) => s.state === "live");
  const expired = (q.data?.subscriptions ?? []).filter(
    (s) => s.state !== "live",
  );

  // THIS DEVICE, NOT ANY DEVICE. `live.length > 0` was answering "is somebody subscribed" and
  // rendering it as "you are" — see `useThisDevice` for what that cost on a second device.

  return (
    <div className="space-y-8">
      <section>
        <SectionHeading className="flex items-center gap-2">
          This device
          {thisDevice.on ? (
            <Badge tone="ok">On</Badge>
          ) : (
            <Badge tone="neutral">Off</Badge>
          )}
        </SectionHeading>
        <div className="mt-3 space-y-3 text-sm text-muted">
          {thisDevice.on ? (
            <p>
              quince will notify this device when a backup is due or needs you.
            </p>
          ) : (
            <p>
              Turn on notifications to hear when a device is due for a backup,
              or needs you.
            </p>
          )}
          {/* THE PROMPT MUST HANG OFF A REAL TAP — a platform requirement, not a preference, which
              is why this is a button and not something the page does on mount. */}
          {!thisDevice.on && (
            <Button
              onClick={() =>
                q.data && subscribe.mutate(q.data.vapid_public_key)
              }
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
          {thisDevice.on && (
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
                      {r.state === "sent" && (
                        <span>Sent to {r.label}. Check its lock screen.</span>
                      )}
                      {r.state === "expired" && (
                        <span className="text-warn">
                          {r.label} is no longer reachable — turn notifications
                          on again on that device.
                        </span>
                      )}
                      {r.state === "error" && (
                        <span className="text-danger">
                          {r.label} did not receive it.
                          {/* THE REASON, NOT A REASSURANCE. The server already redacts this — it is
                              built through `push.RedactEndpoint`, so it carries an origin at most —
                              and it is the ONLY diagnosis that exists anywhere: `SendTest` returns
                              it to the caller and logs nothing. Rendering "try again in a moment"
                              over the top of it threw away the one fact a person could act on, and
                              made a permanent misconfiguration look like a transient blip. Found on
                              the first real hardware send, 2026-08-18. */}
                          {r.error ? (
                            <> {r.error}</>
                          ) : (
                            <> Try again in a moment.</>
                          )}
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              )}
              {sendTest.isError && (
                <p className="text-danger">
                  quince could not send the test. Try again in a moment.
                </p>
              )}
            </div>
          )}
        </div>
      </section>

      {/* AN EXPIRED SUBSCRIPTION IS SHOWN, NOT HIDDEN (spec D8). A device that quietly stopped
          receiving is the failure whose first symptom would otherwise be a missed backup, and the
          remedy — re-enable on THAT device — needs the device named. */}
      {expired.length > 0 && (
        <section>
          <SectionHeading className="flex items-center gap-2">
            A device stopped receiving
            <Badge tone="warn">Needs attention</Badge>
          </SectionHeading>
          <div className="mt-3 space-y-3 text-sm text-muted">
            <p>
              These devices are no longer reachable. Open quince on each one and
              turn notifications on again.
            </p>
            <ul className="mt-3 flex flex-col gap-2">
              {expired.map((s) => (
                <li
                  key={s.id}
                  className="flex flex-wrap items-center justify-between gap-2 rounded-card border border-line bg-bg px-3 py-2"
                >
                  <div className="min-w-0">
                    <div className="truncate text-sm font-medium">
                      {s.label}
                    </div>
                    <div className="text-xs text-muted">
                      {s.expired_at ? (
                        <>
                          {"stopped receiving "}
                          <RelativeTime iso={s.expired_at} />
                        </>
                      ) : (
                        "no longer reachable"
                      )}
                    </div>
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => unsubscribe.mutate(s.id)}
                    disabled={unsubscribe.isPending}
                  >
                    Remove
                  </Button>
                </li>
              ))}
            </ul>
          </div>
        </section>
      )}

      {live.length > 0 && (
        <section>
          <SectionHeading>Devices receiving notifications</SectionHeading>
          {/* THE SAME ROW AS A PASSKEY, because it is the same kind of thing: a credential-backed
              registration the user can revoke, one per line, with the name at full contrast and the
              detail beneath it. Operator-reported 2026-08-18 — this list was plain muted text with a
              ghost button, so the device name read fainter than the sentence above it and the
              control barely looked like one. */}
          <ul className="mt-3 flex flex-col gap-2">
            {live.map((s) => (
              <li
                key={s.id}
                className="flex flex-wrap items-center justify-between gap-2 rounded-card border border-line bg-bg px-3 py-2"
              >
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">{s.label}</div>
                  {/* THE CURRENT DEVICE IS MARKED, and it sits in the metadata line rather than
                      beside the name — two Macs produce one identical label, and "Turn off" against
                      the wrong row is a destructive misclick. */}
                  <div className="text-xs text-muted">
                    {s.id === thisDevice.id ? "this device" : "added "}
                    {s.id === thisDevice.id ? null : (
                      <RelativeTime iso={s.created_at} />
                    )}
                    {s.last_sent_at ? (
                      <>
                        {" · last notified "}
                        <RelativeTime iso={s.last_sent_at} />
                      </>
                    ) : (
                      // NOTHING SENT YET IS A FACT WORTH SHOWING, exactly as "never used" is for a
                      // passkey: a device quince has never reached is the one to be suspicious of.
                      " · nothing sent yet"
                    )}
                  </div>
                </div>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => unsubscribe.mutate(s.id)}
                  disabled={unsubscribe.isPending}
                >
                  Turn off
                </Button>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}
