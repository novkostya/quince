import { useState } from "react";
import { Button } from "@/components/ui/button";
import { probeTargetURL, runProbe, type ProbeOutcome } from "@/lib/probe";

// THE REVERSE-PROXY TIER'S AFFORDANCE (quince#939, Operator ruling 2026-08-14).
//
// IT REVERSES quince#908 §4, which gave this card none on the reasoning that the check *"completes
// itself when the page loads over HTTPS"*. True of a proxy that forwards the scheme, and false of the
// one most people will build: nginx does not set `X-Forwarded-Proto` by default and the widely-copied
// `proxy_pass` block omits it. That user has a working HTTPS site, sees **Not encrypted**, and has no
// way to tell it from a broken proxy. The tier carrying the Recommended badge was the one with the
// silent failure.
//
// FIRST RUN ONLY, WHICH KEEPS quince#908 §2 INTACT. That ruling made this page instructional for a
// returning user and actionable for a first-run one; quince#939 overturned §4's *no affordance*, not
// §2's split. Note the SERVER's gate is deliberately different — the probe endpoint is gated on the
// NONCE rather than on `needs_setup`, precisely so an admin configuring TLS from Settings long after
// first run can reuse it (contracts §1). The narrower rule here is this page's, not the endpoint's.
export function ProxyProbe() {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<ProbeOutcome | null>(null);

  const target = probeTargetURL(value);

  async function check() {
    if (!target) return;
    setBusy(true);
    setOutcome(null);
    try {
      setOutcome(await runProbe(target));
    } finally {
      setBusy(false);
    }
  }

  return (
    // A FORM, SO ENTER CHECKS. One field and one button is the exact shape a person types an
    // address into and presses Enter — and this one did nothing, on a page reached by somebody
    // whose deployment is already half-broken (quince#1066). A `div` costs that for free; the
    // implicit submission a form gives back is the keyboard behaviour every other field on the
    // web has.
    <form
      className="mt-3"
      onSubmit={(e) => {
        e.preventDefault();
        void check();
      }}
    >
      <label className="block text-sm" htmlFor="proxy-host">
        The address you will reach quince at
      </label>
      {/* IT STARTS EMPTY AND IS NOT PRE-FILLED FROM THE CURRENT ADDRESS (quince#908 §5). That is the
          name they are LEAVING — an IP or a `.local` — and no CA issues for either. Moving them off
          it is the point of the step. */}
      <input
        id="proxy-host"
        className="mt-1 w-full rounded-card border border-line bg-bg px-3 py-2 font-mono text-sm text-fg"
        placeholder="quince.example.com"
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        value={value}
        onChange={(e) => {
          setValue(e.target.value);
          // A STALE RESULT IS WORSE THAN NONE. Leaving the previous answer on screen while the field
          // says something else invites acting on a verdict about a different name.
          setOutcome(null);
        }}
      />
      <div className="mt-2 flex items-center gap-2">
        {/* `type="submit"` EXPLICITLY: `Button` defaults to `type="button"`, so the form above would
            have no submit control and Enter would go on doing nothing. */}
        <Button type="submit" disabled={!target || busy}>
          {busy ? "Checking…" : "Check this address"}
        </Button>
        {value.trim() !== "" && !target ? (
          <span className="text-xs text-muted">That does not look like an address.</span>
        ) : null}
      </div>
      {outcome ? <Outcome outcome={outcome} /> : null}
    </form>
  );
}

// EVERY BRANCH NAMES WHAT WAS OBSERVED AND WHAT TO DO ABOUT IT — the house style
// `ErrUnsupportedRPID` and `ErrLastCredential` already set, and the thing quince#940's sweep found
// missing on exactly this surface.
function Outcome({ outcome }: { outcome: ProbeOutcome }) {
  switch (outcome.kind) {
    case "ready":
      return (
        <Box tone="ok">
          <p>
            <strong>That works.</strong> quince answered at <Addr url={outcome.url} /> and your proxy
            told it the connection was encrypted.
          </p>
          {/* A LINK RATHER THAN AN AUTOMATIC NAVIGATION. The probe proves THIS browser can reach the
              name; it does not prove the certificate is trusted here, and a redirect into a warning
              interstitial would look like quince broke something. Let them take the step. */}
          <p className="mt-2">
            <a className="underline" href={`${outcome.url}/onboarding/https`}>
              Continue at {outcome.url}
            </a>
          </p>
        </Box>
      );

    case "no-forwarded-proto":
      return (
        <Box tone="warn">
          <p>
            <strong>Your proxy is working, and it is not telling quince.</strong> quince answered at{" "}
            <Addr url={outcome.url} /> over a connection it saw as plain HTTP, so it cannot know the
            traffic was encrypted on the way to your proxy.
          </p>
          <p className="mt-2">
            For nginx, add this inside the <code className="font-mono">location</code> block:
          </p>
          <pre className="mt-1 overflow-x-auto rounded-card border border-line bg-bg p-2 font-mono text-xs">
            proxy_set_header X-Forwarded-Proto $scheme;
          </pre>
          <p className="mt-2">Caddy and Traefik set it for you. Then check again.</p>
          {/* THE HONEST LIMIT, AND IT SURVIVES quince#940 §2 — READ THIS BEFORE "FIXING" IT.
              §2's ruling required the PR adding `unencrypted_code` to retire the copy saying quince
              cannot tell these apart, on the grounds that it stops being true. **It stops being true
              on the SAME-ORIGIN page and stays true HERE**, and the same ruling is why: door 2 puts
              the new field on `OnboardingHTTPS` and explicitly does NOT touch the probe body, which
              the CORS ruling froze at `{nonce, detected}`.

              This card renders the answer from a CROSS-ORIGIN probe of a DIFFERENT name, so all it
              receives is `detected: none` — exactly as before. Retiring the sentence here would ship
              a claim the mechanism does not support. The page that CAN say which cause now does;
              see `WhatQuinceSaw` in OnboardingHTTPSPage. */}
          <p className="mt-2 text-xs">
            If you have set <code className="font-mono">QUINCE_TRUSTED_PROXIES</code>, quince may be
            ignoring the header instead — from here it cannot tell the two apart, so check that your
            proxy's address is in that list too. Opening that address directly says which.
          </p>
        </Box>
      );

    case "quince-tls":
      return (
        <Box tone="ok">
          <p>
            <strong>That address reaches quince's own certificate</strong>, not a proxy. Nothing is
            wrong — it is the next tier down rather than this one, and it already works.
          </p>
          <p className="mt-2">
            <a className="underline" href={`${outcome.url}/onboarding/https`}>
              Continue at {outcome.url}
            </a>
          </p>
        </Box>
      );

    case "other-quince":
      return (
        <Box tone="danger">
          <p>
            <strong>Something answered, and it was not this quince.</strong> A different quince is
            reachable at <Addr url={outcome.url} />, so following it would take you to another
            installation — not to this one.
          </p>
          <p className="mt-2">Point that name at this quince, or use a different name.</p>
        </Box>
      );

    case "unreachable":
      return (
        <Box tone="warn">
          {/* THE WORDING RULE IS quince#908 §6's: say what THIS browser could not do, never "DNS is
              wrong". A browser cannot distinguish a name that does not resolve, a refused
              connection, an untrusted certificate and a CORS refusal — `fetch` rejects the same way
              for all four — so naming one would be inventing a cause. */}
          <p>
            <strong>This browser could not reach quince at <Addr url={outcome.url} />.</strong> That
            covers several things at once: the name may not resolve here, nothing may be listening,
            or the certificate may not be trusted by this browser yet.
          </p>
          <p className="mt-2">
            Open <AddrLink url={outcome.url} /> in a new tab — whatever your browser says there is
            the specific answer this check cannot see.
          </p>
        </Box>
      );
  }
}

function Addr({ url }: { url: string }) {
  return <code className="font-mono">{url}</code>;
}

// THE ONE THE COPY TELLS YOU TO OPEN IS A LINK (quince#1066). *"Open https://… in a new tab"* above
// a piece of unclickable monospace asks somebody on a phone to retype an address they cannot select
// — and the whole point of that sentence is that the browser's own error is the answer this check
// cannot see, so getting there has to be easy.
//
// ONLY THAT ONE, deliberately. The other `Addr` on this card is the subject of a sentence about what
// FAILED, not an instruction; linking every address on the page would make the actionable one stop
// standing out.
//
// `target="_blank"` WITH `rel="noreferrer"`: a new tab is what the sentence promises, and this page
// is where somebody is mid-decision — taking the tab away from them would lose it. `noreferrer`
// keeps this origin out of the request, which matters more than usual here, since it is a plain-http
// address that would otherwise travel to a name the user does not yet control.
function AddrLink({ url }: { url: string }) {
  return (
    <a className="font-mono underline" href={url} target="_blank" rel="noreferrer">
      {url}
    </a>
  );
}

function Box({ tone, children }: { tone: "ok" | "warn" | "danger"; children: React.ReactNode }) {
  // House tokens, not shadcn defaults — `ReconcilingNotice` carries what happens otherwise: a name
  // that exists and means something else (`bg-muted` is a FOREGROUND colour) fails quietly.
  const border = tone === "ok" ? "border-ok" : tone === "danger" ? "border-danger" : "border-warn";
  return (
    <div role="status" className={`mt-3 rounded-card border ${border} bg-card px-3 py-2 text-sm text-fg`}>
      {children}
    </div>
  );
}
