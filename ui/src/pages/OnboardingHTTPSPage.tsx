import type { ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useOnboardingHTTPS } from "@/lib/onboarding";

// The onboarding HTTPS check (qn.6f). Reachable with no session and with no password in
// existence — see the route in `routes/router.tsx` and rung-ruled decision 6.
//
// EVERY VISITOR SEES THE SAME PAGE. Its only two inputs are the connection the visitor
// themselves opened and the static prose below, so there is nothing here an unauthenticated
// caller should not have. What it must NEVER grow is device, storage, version or job data —
// an edit needing any of those means the pre-auth exemption is wrong for that edit.
export function OnboardingHTTPSPage() {
  const q = useOnboardingHTTPS();

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
    <div className="min-h-dvh bg-bg pb-10 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(2.5rem,env(safe-area-inset-top))] text-fg">
      <div className="mx-auto w-full max-w-2xl">
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <h1 className="mt-4 text-base font-semibold">Reaching quince securely</h1>

        {q.isPending ? (
          <p className="mt-1 text-sm text-muted">Checking this connection…</p>
        ) : q.isError ? (
          <CheckFailed />
        ) : q.data.complete ? (
          <Complete detected={q.data.detected} />
        ) : (
          <Incomplete />
        )}

        {showTiers ? <Tiers /> : null}
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

function Incomplete() {
  return (
    <>
      <div className="mt-3 flex items-center gap-2">
        <Badge tone="warn">Not encrypted</Badge>
      </div>
      {/* The consequence first, in the user's terms. "Your browser discards the cookie" is
          the mechanism; "you cannot sign in from your phone" is what they came here about. */}
      <p className="mt-3 text-sm text-muted">
        This connection is plain HTTP, so signing in from a phone or another computer will not
        work — the browser discards the session cookie and returns you to the login page without
        an error. Some features are also unavailable on an unencrypted connection, whatever you
        do about signing in. Pick one of these.
      </p>
    </>
  );
}

// Tiers is the static half of the page: five options that do not depend on what the check
// returned. Rendered for a failed check as well as an unencrypted one.
function Tiers() {
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
              <p className="mt-2">
                quince needs no configuration for this: load the page over HTTPS and this check
                completes itself.
              </p>
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
              <p className="mt-2">
                quince serves both protocols on the same port, so this URL keeps working — it just
                becomes HTTPS. Renewals are picked up without a restart.
              </p>
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
              <p className="mt-2">
                It also rules out notifications. Browsers only allow web push on an encrypted
                origin, so quince will never be able to tell you that a backup is waiting for your
                passcode. That feature is not built yet — which is exactly why it is worth knowing
                now, while this is still a choice.
              </p>
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

// DocLink turns a repo path into something a reader can actually open. This page is read on a
// phone more than anywhere else in quince, and a phone cannot open `deploy/tls.md` — printing a
// filename at someone who is stuck is a dead end dressed as help.
//
// `blob/main` rather than a pinned commit or tag: the reader wants the CURRENT instructions for
// the quince they are running, and a pinned link rots into describing an older one. The cost is
// that a very old deployment may read newer docs, which is the better failure of the two.
//
// The first external link in the UI, so the styling is here rather than in a shared component —
// one instance is not a pattern. If a second page needs it, that is when it moves.
function DocLink({ path }: { path: string }) {
  return (
    <a
      className="text-accent underline underline-offset-2"
      href={`https://github.com/novkostya/quince/blob/main/${path}`}
      target="_blank"
      rel="noreferrer"
    >
      {path}
    </a>
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
