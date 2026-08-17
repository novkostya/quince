import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { isIOS, pushSupport } from "@/lib/pwa";

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
                  that does not exist. */}
              {ios ? (
                <>
                  <p>
                    On iPhone and iPad, notifications only work once quince has been added to your
                    Home Screen.
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
                </>
              ) : (
                <p>
                  Install quince from your browser&rsquo;s address bar, then open it from your apps
                  and come back to this page.
                </p>
              )}
            </CardContent>
          </Card>
        )}

        {support === "supported" && (
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                This device can receive notifications
                <Badge tone="ok">Ready</Badge>
              </CardTitle>
            </CardHeader>
            <CardContent className="text-fg-muted">
              <p>
                quince is installed on this device and its browser supports notifications. Nothing
                else is needed here.
              </p>
            </CardContent>
          </Card>
        )}
      </div>
    </div>
  );
}
