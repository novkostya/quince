import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DocLink } from "@/components/DocLink";
import { InsecureTransportBanner } from "@/components/InsecureTransportBanner";
import { PlainHTTPConfirm } from "@/features/onboarding/PlainHTTPConfirm";
import { ProxyProbe } from "@/features/onboarding/ProxyProbe";
import { useOnboardingHTTPS } from "@/lib/onboarding";
import { useAuthStatus } from "@/lib/auth";
import type { OnboardingHTTPS } from "@/lib/types";

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
  // `Complete` offers no TIERS, and that is G1: an already-secure origin meets zero friction and is
  // never asked to confirm what quince can see for itself. It does offer a way OUT of the page,
  // which is neither a choice about transport nor friction: `showTiers` being false here means this
  // card is the whole page, and a page saying "Nothing to do" needs somewhere to go.
  const showTiers = q.isError || (q.data !== undefined && !q.data.complete);

  return (
    // `min-h-dvh` is correct here for the same reason it is on `PasswordForm`, which carries the
    // full reasoning (quince#659): `dvh` equals the visible area in BOTH toolbar states — the unit
    // that does NOT is `lvh` — and this route is a sibling of `AppLayout` rather than a child, so
    // the document scrolls. A transient lag during the toolbar animation costs a brief scroll, not
    // reachability. Do not "unify" this with the authed shell's rule without reading that comment.
    <div className="min-h-dvh bg-bg pb-16 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(2.5rem,env(safe-area-inset-top))] text-fg">
      {/* `max-w-4xl`, AND IT IS THE OTHER TWO STEPS THAT MOVED TO MATCH IT — Operator direction
          2026-08-13. The first attempt at making the three consistent narrowed THIS page instead,
          on the argument that 36rem is the better measure for prose. That was the wrong direction,
          and the storage step is why: it renders the whole `quince-zfs-helper` script, whose lines
          run past 90 characters, and at 36rem the script was clipped mid-line. A width chosen for
          prose is not a width that can hold a shell script, and the flow has to fit its widest
          step rather than its most readable one.

          The heading is `text-xl` — AuthPage's own rule is that a PAGE's heading is `text-xl` and a
          CARD's is `text-base`, and this is a page. That half of the alignment stands. */}
      <div className="mx-auto w-full max-w-4xl">
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
          <Incomplete firstRun={firstRun} code={q.data.unencrypted_code} />
        )}

        {showTiers ? <Tiers firstRun={firstRun} tlsUnusable={q.data?.tls_unusable_code} /> : null}
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
      {/* "NOTHING TO DO" IS TRUE OF THE TRANSPORT AND FALSE OF EVERYTHING ELSE. `showTiers` is false
          once the check is complete, so without this the card is the whole page — and a reader can be
          looking at it with no password set, having arrived from the proxy tier's own link or a
          bookmark.

          NEITHER THE DESTINATION NOR THE LABEL NAMES A STEP. If the plain-HTTP banner is ever made
          actionable by linking HERE, the reader arriving is SIGNED IN and fixing their transport —
          for them the next thing is the app, not a password, and a card promising one would be wrong
          for exactly that audience. `/` resolves through `RequireAuth` on the LIVE auth state, so
          this page states no order of its own. Two redirects, one truth.

          RENDERED IN EVERY STATE, which is a reading of quince#908 §2 rather than an exception to
          it. That ruling keeps this page INSTRUCTIONAL for a returning reader — it is about the
          page's character, not about whether a reader can leave. A neutral way back is navigation,
          not a decision, and the audience that needs it most is the signed-in one above. */}
      <p className="mt-4">
        <Button asChild>
          <Link to="/">Continue to quince</Link>
        </Button>
      </p>
    </>
  );
}

// WHAT QUINCE ACTUALLY SAW — quince#940 §2 + quince#939 §7. All four causes rendered as **Not
// encrypted** and nothing else until the server gained `unencrypted_code`, and two of them sent the
// user to a remedy that was not merely vaguer but WRONG.
//
// `CLAUDE.md`: *a diagnostic that collapses distinguishable causes is a defect even when every word
// of it is true.* "Not encrypted" is accurate in all four rows and useless in all four.
//
// THE WORDING RULE IS §6's — say what quince OBSERVED, never invent the cause. Each branch below
// states the evidence and then the remedy that follows from it, and the one branch that is an
// INFERENCE says so in its own sentence rather than in a comment.
function WhatQuinceSaw({ code }: { code: OnboardingHTTPS["unencrypted_code"] }) {
  switch (code) {
    case "proxy_reports_plain":
      // THE CRUEL ONE, and the reason this whole field exists. The proxy is behaving perfectly and
      // reporting the truth; the old copy told the operator it was broken. Nothing below points at
      // quince, and the tiers are the wrong answer here — which is why this says so outright.
      return (
        <Note tone="warn">
          <strong>Your proxy is reaching quince correctly, and says the browser reached IT over
          plain HTTP.</strong>{" "}
          quince is not the thing to change: the connection is unencrypted between your browser and
          the proxy, so it is the proxy's own listener that needs HTTPS. quince is only repeating
          what it was told.
        </Note>
      );
    case "proxy_untrusted":
      return (
        <Note tone="warn">
          <strong>A proxy told quince this connection was HTTPS, and quince did not believe it.</strong>{" "}
          <code className="font-mono">QUINCE_TRUSTED_PROXIES</code> is set, and the address this
          request arrived from is not in it — so the header was ignored on purpose. Add that proxy's
          address to the list, or unset it to trust any sender.
        </Note>
      );
    case "proxy_not_forwarding_scheme":
      // A HINT, AND IT IS WORDED AS ONE (quince#939 §7). It is inferred from `X-Forwarded-For` being
      // present, and nginx does not set that by default either — so a confident sentence here would
      // tell some correctly-configured operators their proxy is broken. "Looks like" is doing real
      // work; do not tighten it.
      return (
        <Note tone="warn">
          <strong>It looks like something is in front of quince that is not passing the scheme
          along.</strong>{" "}
          quince saw forwarding headers but no{" "}
          <code className="font-mono">X-Forwarded-Proto</code>, so it cannot tell whether the traffic
          reached your proxy over HTTPS. For nginx that is one line in the{" "}
          <code className="font-mono">location</code> block:{" "}
          <code className="font-mono">proxy_set_header X-Forwarded-Proto $scheme;</code> — Caddy and
          Traefik set it for you.
        </Note>
      );
    // `no_proxy_seen` RENDERS NOTHING, and that is the right answer rather than a missing one.
    // quince saw no evidence of any proxy, so the remedy IS the tier list directly below — repeating
    // it here would be a second copy of the same advice, and stating "there is no proxy" as a fact
    // would be the same over-claim §7 refuses for the row above.
    default:
      return null;
  }
}

// Note is the shared shape for the four branches above, so the wording is what differs between them
// and not the markup.
function Note({ tone, children }: { tone: "warn"; children: ReactNode }) {
  return (
    <div
      role="status"
      className={`mt-3 rounded-card border bg-card px-3 py-2 text-sm ${tone === "warn" ? "border-warn" : "border-line"}`}
    >
      {children}
    </div>
  );
}

function Incomplete({
  firstRun,
  code,
}: {
  firstRun: boolean;
  code: OnboardingHTTPS["unencrypted_code"];
}) {
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
      <WhatQuinceSaw code={code} />
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
// TLSFailure says WHAT KIND of failure quince met with the operator's own pair — never a path and
// never the loader's text, which are authenticated (Operator ruling 2026-08-14, quince#940 §1).
//
// Each sentence names a DIFFERENT next action, which is the whole point: "quince could not use your
// certificate" is one message for five situations with five fixes.
function TLSFailure({ code }: { code: NonNullable<OnboardingHTTPS["tls_unusable_code"]> }) {
  const what: Record<string, string> = {
    unreadable: "quince could not read the files those two keys point at. Check the paths, and that the container can read them — a read-only mount still has to be mounted.",
    malformed: "quince read the files, and they are not a certificate and key it can parse. A DER file renamed to .pem does this, as does a truncated download.",
    mismatched: "quince read both files, and the key does not belong to that certificate. They are usually issued as a pair; check you have not mixed two renewals.",
    not_yet_valid: "That certificate is not valid yet — its start date is in the future. If it was just issued, check this machine's clock.",
    expired: "That certificate has expired. Renew it; quince picks up a replacement without a restart.",
    unknown: "quince could not use that certificate and cannot tell why — the files read cleanly just now. The server log has the loader's own message.",
  };
  return (
    <div role="status" className="rounded-card border border-danger bg-bg px-3 py-2">
      <strong>quince tried your certificate and could not use it.</strong>{" "}
      {what[code] ?? what.unknown}
    </div>
  );
}

function Tiers({ firstRun, tlsUnusable }: { firstRun: boolean; tlsUnusable?: OnboardingHTTPS["tls_unusable_code"] }) {
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
              {firstRun ? (
                // THE PROBE REVERSES quince#908 §4 (Operator ruling 2026-08-14, quince#939). §4 gave
                // this card no affordance because the check "completes itself when the page loads
                // over HTTPS" — true of a proxy that forwards the scheme, false of the one most
                // people build. See `ProxyProbe` for the whole argument.
                <ProxyProbe />
              ) : (
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
              {/* THE INSTRUCTION IS REPLACED, NOT REPEATED, WHEN THE PAIR IS ALREADY BROKEN
                  (quince#940 §1, and the ruling requires it). This user HAS pointed those two keys
                  at a certificate — that is why quince has a failure to report — so telling them to
                  do it again is the defect the whole sweep is about. Two messages, one saying
                  "quince tried yours and could not use it" and one saying "point it at a
                  certificate", contradict each other on the same card. */}
              {tlsUnusable ? (
                <TLSFailure code={tlsUnusable} />
              ) : (
                <p>
                  Point <code className="font-mono">tls.cert_file</code> and{" "}
                  <code className="font-mono">tls.key_file</code> in{" "}
                  <code className="font-mono">config.yml</code> at a certificate and key, mounted
                  read-only. From <code className="font-mono">acme.sh</code>,{" "}
                  <code className="font-mono">tailscale cert</code>, or a wildcard you already have.
                </p>
              )}
              {/* THE ISSUE'S OWN WORKED EXAMPLE of the test above: "quince serves both protocols
                  on the same port" is how-to, and it goes. It is a good sentence and it answers a
                  question the chooser has not asked yet. */}
              {firstRun ? (
                // ITS OWN ROUTE (§4), because the flow is multi-step and stateful and an accordion
                // is application state with no URL. The link is the affordance; the page is where
                // both halves of the check live.
                <p className="mt-3">
                  <Link className="underline" to="/onboarding/https/certificate">
                    Check a certificate and key
                  </Link>
                </p>
              ) : (
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
                <>
                  <p className="mt-2">
                    It also rules out notifications for good: browsers only allow web push on an
                    encrypted origin, so quince could never tell you a backup is waiting for your
                    passcode.
                  </p>
                  {/* §4: one boolean and a warning — a CONFIRM, not a page. The escape hatch for
                      somebody who cannot otherwise finish setup at all. The route behind it is
                      `Configured()`-gated, so first-run-only here is presentation and the 409 is
                      the control. */}
                  <PlainHTTPConfirm />
                </>
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
