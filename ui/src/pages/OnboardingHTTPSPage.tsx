import type { ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DocLink } from "@/components/DocLink";
import { InsecureTransportBanner } from "@/components/InsecureTransportBanner";
import { useOnboardingHTTPS } from "@/lib/onboarding";
import { useAuthStatus } from "@/lib/auth";

// The onboarding HTTPS check (qn.6f). Reachable with no session and with no password in
// existence — see the route in `routes/router.tsx` and rung-ruled decision 6.
//
// EVERY VISITOR SEES THE SAME PAGE. Its only two inputs are the connection the visitor
// themselves opened and the static prose below, so there is nothing here an unauthenticated
// caller should not have. What it must NEVER grow is device, storage, version or job data —
// an edit needing any of those means the pre-auth exemption is wrong for that edit.
export function OnboardingHTTPSPage() {
  const q = useOnboardingHTTPS();
  // FIRST RUN IS A DIFFERENT AUDIENCE, NOT A DIFFERENT PAGE (quince#908 §2). Since quince#923 a
  // first-run visitor is SENT here rather than arriving by choice, so this page is the whole of
  // what they can see — and what they need is a decision, where a returning user reading the same
  // URL wants reference material.
  //
  // `needs_setup` AND NOTHING ELSE, deliberately: a loading or errored status renders the
  // instructional page, which is the version that is correct for everybody. The failure direction
  // matches `useInsecureOrigin`'s — the safe answer is the one that changes nothing.
  const auth = useAuthStatus();
  const firstRun = auth.data?.state === "needs_setup";

  // THE TIERS RENDER FOR BOTH `not complete` AND `check failed`, and they live here rather than
  // inside a branch for that reason. They are static prose: correct whether or not the server
  // answered, which is exactly why the error copy promises them.
  //
  // They were nested inside the not-complete branch in the first version, so a failed check said
  // "the setup options below are still correct" above nothing at all — to the one audience this
  // page has, somebody whose deployment is already half-broken (review on quince#559).
  //
  // `Complete` still offers nothing. That is G1 and it stays: an already-secure origin meets zero
  // friction and is never asked to confirm what quince can see for itself.
  const showTiers = q.isError || (q.data !== undefined && !q.data.complete);

  return (
    // `min-h-dvh` is correct here for the same reason it is on `PasswordForm`, which carries the
    // full reasoning (quince#659): `dvh` equals the visible area in BOTH toolbar states — the unit
    // that does NOT is `lvh` — and this route is a sibling of `AppLayout` rather than a child, so
    // the document scrolls. A transient lag during the toolbar animation costs a brief scroll, not
    // reachability. Do not "unify" this with the authed shell's rule without reading that comment.
    <div className="min-h-dvh bg-bg pb-16 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(2.5rem,env(safe-area-inset-top))] text-fg">
      {/* `max-w-2xl`, AND IT IS THE OTHER TWO STEPS THAT MOVED TO MATCH IT — Operator direction
          2026-08-13. The first attempt at making the three consistent narrowed THIS page instead,
          on the argument that 36rem is the better measure for prose. That was the wrong direction,
          and the storage step is why: it renders the whole `quince-zfs-helper` script, whose lines
          run past 90 characters, and at 36rem the script was clipped mid-line. A width chosen for
          prose is not a width that can hold a shell script, and the flow has to fit its widest
          step rather than its most readable one.

          The heading is `text-xl` — AuthPage's own rule is that a PAGE's heading is `text-xl` and a
          CARD's is `text-base`, and this is a page. That half of the alignment stands. */}
      <div className="mx-auto w-full max-w-2xl">
        {/* THE THIRD SURFACE. This route sits outside every guard and outside `AuthPage`, so
            neither of the other two placements reaches it — and it is the page a user lands on
            precisely when transport is what they are dealing with. Above the wordmark, as on
            `AuthPage`, so the two first-run screens agree. */}
        <InsecureTransportBanner />
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <h1 className="mt-4 text-xl font-semibold tracking-tight">Reaching quince securely</h1>

        {q.isPending ? (
          <p className="mt-1 text-sm text-muted">Checking this connection…</p>
        ) : q.isError ? (
          <CheckFailed />
        ) : q.data.complete ? (
          <Complete detected={q.data.detected} />
        ) : (
          <Incomplete firstRun={firstRun} />
        )}

        {showTiers ? <Tiers firstRun={firstRun} /> : null}
      </div>
    </div>
  );
}

// Honest rather than reassuring: quince does not know, and says which half is missing.
function CheckFailed() {
  return (
    <>
      <div className="mt-3 flex items-center gap-2">
        <Badge tone="warn">Unknown</Badge>
      </div>
      <p className="mt-3 text-sm text-danger">
        Could not check this connection — quince did not answer. The setup options below do not
        depend on that answer and are still correct.
      </p>
    </>
  );
}

// DETECTION IS A STATE, NOT A BUTTON (G1). An already-secure origin sees a result and no
// options at all — the top tier meets zero friction and is never asked to confirm something
// quince can see for itself.
function Complete({ detected }: { detected: "tls" | "forwarded_proto" | "none" }) {
  const how =
    detected === "tls"
      ? "quince is serving this connection over HTTPS with your certificate."
      : "A proxy in front of quince terminated HTTPS and told quince so.";
  return (
    <>
      <div className="mt-3 flex items-center gap-2">
        <Badge tone="ok">Encrypted</Badge>
        <span className="text-sm text-muted">Nothing to do.</span>
      </div>
      <p className="mt-3 text-sm text-muted">{how}</p>
    </>
  );
}

function Incomplete({ firstRun }: { firstRun: boolean }) {
  return (
    <>
      <div className="mt-3 flex items-center gap-2">
        <Badge tone="warn">Not encrypted</Badge>
      </div>
      {/* The consequence first, in the user's terms. "Your browser discards the cookie" is
          the mechanism; "you cannot sign in from your phone" is what they came here about.

          FIRST RUN IS A STRICTLY WORSE FACT AND SAYS SO (quince#908). A returning user cannot
          sign in FROM ELSEWHERE and can still reach quince on localhost; a first-run user cannot
          finish setup AT ALL, because `refuseInsecureOrigin` refuses `POST /api/auth/setup`
          before it looks at the password. Telling them "signing in will not work" would
          understate it — they have no account to sign in to, and the sentence would read as a
          problem for later. */}
      {firstRun ? (
        <p className="mt-3 text-sm text-muted">
          This connection is plain HTTP, so quince cannot finish setting up over it — a password
          set here would be discarded by the browser along with the session it earns, so quince
          refuses rather than let you set one that will not work. Nothing is wrong with quince or
          with your network. Pick how you want to reach it.
        </p>
      ) : (
        <p className="mt-3 text-sm text-muted">
          This connection is plain HTTP, so signing in from a phone or another computer will not
          work — the browser discards the session cookie and returns you to the login page without
          an error. Some features are also unavailable on an unencrypted connection, whatever you
          do about signing in. Pick one of these.
        </p>
      )}
    </>
  );
}

// Tiers is the static half of the page: five options that do not depend on what the check
// returned. Rendered for a failed check as well as an unencrypted one.
//
// TWO MODES, NOT A REDESIGN (quince#908 §2). Today’s bodies are good REFERENCE material and
// survive unchanged for a returning user; first run gets a CHOOSER. Building a variant rather
// than replacing the page is the cheaper half of that decision and the safer one — it avoids
// making the instructional version worse in order to make the actionable one better.
//
// THE TEST FOR WHAT STAYS IS ONE QUESTION: does this sentence help me DECIDE? Each card
// currently answers "is this right for me?" and "how do I do it?", and a chooser needs only
// the first. So `quince serves both protocols on the same port` moves out — it is how-to — while
// the plain-http card’s notification foreclosure STAYS in compressed form, because it names
// something you permanently give up and that is a decision, not a procedure.
//
// THE BADGES STAY IN BOTH MODES. They are the most useful thing on the page: they rank four
// options at a glance, which is exactly what somebody choosing needs.
// returned. Rendered for a failed check as well as an unencrypted one.
function Tiers({ firstRun }: { firstRun: boolean }) {
  return (
    <>
      <div className="mt-6 flex flex-col gap-4">
        <Tier
          title="Put something in front of quince"
          badge={<Badge tone="ok">Recommended</Badge>}
          body={
            <>
              <p>
                <code className="font-mono">tailscale serve</code> needs no certificate at all. A
                reverse proxy — Caddy, nginx, Traefik — works too, as long as it sets{" "}
                <code className="font-mono">X-Forwarded-Proto</code>.
              </p>
              {firstRun ? null : (
                <p className="mt-2">
                  quince needs no configuration for this: load the page over HTTPS and this check
                  completes itself.
                </p>
              )}
            </>
          }
          docs="deploy/tls.md"
        />

        <Tier
          title="Give quince your own certificate"
          badge={<Badge tone="accent">Also recommended</Badge>}
          body={
            <>
              <p>
                Point <code className="font-mono">tls.cert_file</code> and{" "}
                <code className="font-mono">tls.key_file</code> in{" "}
                <code className="font-mono">config.yml</code> at a certificate and key, mounted
                read-only. From <code className="font-mono">acme.sh</code>,{" "}
                <code className="font-mono">tailscale cert</code>, or a wildcard you already have.
              </p>
              {/* THE ISSUE'S OWN WORKED EXAMPLE of the test above: "quince serves both protocols
                  on the same port" is how-to, and it goes. It is a good sentence and it answers a
                  question the chooser has not asked yet. */}
              {firstRun ? null : (
                <p className="mt-2">
                  quince serves both protocols on the same port, so this URL keeps working — it
                  just becomes HTTPS. Renewals are picked up without a restart.
                </p>
              )}
            </>
          }
          docs="deploy/tls.md"
        />

        <Tier
          title="Plain HTTP on a network you trust"
          badge={<Badge tone="warn">Not recommended</Badge>}
          body={
            <>
              <p>
                Set <code className="font-mono">sessions.allow_insecure_transport: true</code> in{" "}
                <code className="font-mono">config.yml</code>. Sign-in starts working and quince
                keeps telling you it is on.
              </p>
              <p className="mt-2">
                The honest case for this is a VPN, where the tunnel is already encrypted. The cost
                is that your session cookie crosses the network in clear, so anyone who can read
                the path can sign in as you.
              </p>
              {/* THE SAME FORECLOSURE THE SELF-SIGNED ROW CARRIES, and it was missing here.
                  Browsers only register service workers on a secure origin, and plain http to a
                  LAN address is not one — so this option rules out push exactly as a
                  click-through certificate does. Said now, while it is a choice, rather than
                  discovered when the feature arrives and does not work. */}
              {/* COMPRESSED IN THE CHOOSER, NOT DROPPED. This paragraph names something you give
                  up PERMANENTLY, which is decision-relevant by the test above — the only card
                  content that survives the shortening on its own merits rather than by being
                  short. */}
              {firstRun ? (
                <p className="mt-2">
                  It also rules out notifications for good: browsers only allow web push on an
                  encrypted origin, so quince could never tell you a backup is waiting for your
                  passcode.
                </p>
              ) : (
                <p className="mt-2">
                  It also rules out notifications. Browsers only allow web push on an encrypted
                  origin, so quince will never be able to tell you that a backup is waiting for
                  your passcode. That feature is not built yet — which is exactly why it is worth
                  knowing now, while this is still a choice.
                </p>
              )}
            </>
          }
          docs="deploy/tls.md"
        />

        {/* TWO DISABLED ROWS, RENDERED AND LABELLED — story 2 says "not merely inert". A tier
            that is silently absent looks like an option nobody thought of; one that is present
            and labelled says the decision was made and why. */}
        <Tier
          title="A certificate quince makes itself"
          badge={<Badge tone="neutral">Not implemented</Badge>}
          disabled
          body={
            <p>
              A self-signed certificate makes browsers show a warning, and clicking through it
              permanently blocks the notifications quince will use to tell you a backup needs your
              passcode. Deliberately not offered.
            </p>
          }
        />

        <Tier
          title="An address quince manages for you"
          badge={<Badge tone="neutral">Not implemented</Badge>}
          disabled
          body={<p>A hostname and certificate obtained for you, with nothing to configure. Not built yet.</p>}
        />
      </div>
    </>
  );
}


function Tier({
  title,
  badge,
  body,
  docs,
  disabled,
}: {
  title: string;
  badge: ReactNode;
  body: ReactNode;
  docs?: string;
  disabled?: boolean;
}) {
  return (
    // aria-disabled rather than removing it from the tree: a screen reader should hear that the
    // option exists and is unavailable, which is the whole point of rendering it.
    <Card className={disabled ? "opacity-60" : undefined} data-testid={`tier-${title}`}>
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <CardTitle>{title}</CardTitle>
          {badge}
        </div>
      </CardHeader>
      <CardContent>
        <div className="text-sm text-muted" aria-disabled={disabled || undefined}>
          {body}
          {docs ? (
            <p className="mt-2 text-xs text-muted">
              See <DocLink path={docs} />.
            </p>
          ) : null}
        </div>
      </CardContent>
    </Card>
  );
}
