import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import type { CertificateApplied } from "@/lib/types";

// THE APPLY HALF OF THE CERTIFICATE STEP — quince#908 §5, slice 5.
//
// It hands the pair to the running daemon, which starts serving TLS immediately, and then asks the
// user for the one thing no server can do for itself: reach quince over https and say so.
//
// NOTHING IS SAVED BY PRESSING THIS, and the copy says so twice because it is the whole design
// (Operator, 2026-08-14). `config.yml` is not written until a request arrives over quince's own https
// half carrying this trial's token — so a certificate the user tries and abandons leaves no trace in
// a file they hand-edit, and a restart before they confirm comes back on what worked before.
//
// WHY THE USER NAVIGATES INSTEAD OF BEING REDIRECTED. §5's original mechanism was *"the plain half
// starts redirecting, so turning it on IS the test"*. That redirect fires only when
// `keeper.HasCertificate() && !allowInsecure()` — so for a user who took the plain-http escape
// hatch, which is precisely the user most likely to be configuring a certificate, it never fires.
// The ruling chose a deliberate navigation for everybody rather than a mechanism that works for
// some. THE FACT PROVEN IS THE SAME: a request arrived on quince's own TLS half with this token.
export function CertificateApply({
  certFile,
  keyFile,
  hostname,
  currentHost,
  currentHostCovered,
}: {
  certFile: string;
  keyFile: string;
  hostname: string;
  // WHERE THE CONFIRM LINK WOULD POINT IF THE NAME IS LEFT EMPTY, and whether this pair covers it.
  // The apply answers the same question in `confirm_host_covered` — one press too late to stop
  // anybody. This is the CHECK's answer, already on screen before the button is reachable.
  currentHost: string;
  currentHostCovered: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [applied, setApplied] = useState<CertificateApplied | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function apply() {
    setBusy(true);
    setError(null);
    try {
      setApplied(
        await api.post<CertificateApplied>("/api/onboarding/certificate/apply", {
          cert_file: certFile,
          key_file: keyFile,
          hostname,
        }),
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  // THE TRIAL IS NOT OFFERED WHEN THE ADDRESS IT WOULD USE CANNOT MATCH — Operator direction,
  // 2026-08-18: *"there should be no way to tap Try it now — this is guaranteed dead end."*
  //
  // WITH THE NAME LEFT EMPTY the confirm link is built from the address this request arrived on. A
  // pair that does not cover that address produces a name mismatch there — not once, but on every
  // visit for as long as the install stands, because confirming writes exactly this pair for exactly
  // this address. A user CAN click through it; what they get is an install that warns every time.
  //
  // IT DOES NOT TOUCH THE JOURNEY THE OPERATOR RULED VALID — *"this is also a valid test case"* —
  // because that one is a certificate that DOES cover where you are: self-signed for this host, or
  // an internal CA. There `currentHostCovered` is true, the button stays live, the browser warns
  // about the ISSUER rather than the name, and trusting it once ends the warnings.
  //
  // A DISABLED BUTTON WITH A REASON, not a hidden one: the remedy is one field away, and a control
  // that vanishes teaches nothing.
  const deadEnd = currentHost !== "" && hostname === "" && !currentHostCovered;

  if (applied) return <ConfirmInstructions applied={applied} onRestart={() => setApplied(null)} />;

  return (
    <div className="mt-6 rounded-card border border-line bg-card px-3 py-3 text-sm">
      <p>
        <strong>Try this certificate.</strong> quince starts serving TLS with it immediately — no
        restart — and <strong>writes nothing to configuration yet</strong>.
      </p>
      {/* SAID BEFORE THE BUTTON, NOT AFTER. This is the reassurance that makes it safe to press, and
          a user who only learns it once the page has changed has already taken the risk they were
          being reassured about. */}
      <p className="mt-2 text-muted">
        You then have ten minutes to confirm it over https. If you do not — because it turns out to
        be a certificate your browser will not accept — quince goes back to what it was serving, and{" "}
        <code className="font-mono">config.yml</code> was never touched.
      </p>
      {/* THE COST TO **THIS** PAGE, WHICH NOTHING SAID. The moment a certificate is serving,
          `plainHalf` starts redirecting http to https, so every request from this page is sent to an
          origin the browser may refuse — the page goes dark along with everything else. The old copy
          covered it obliquely ("if the page will not load at all, do nothing"), which reads as being
          about the new tab.

          AND THE FAST EXIT IS NAMED. `certTrial` calls a mid-window restart fail-safe by design: the
          trial evaporates and the daemon comes back on what `config.yml` names. Offering only "wait
          ten minutes" under-sells a guarantee the daemon already makes. */}
      <p className="mt-2 text-muted">
        This page stops working while the trial runs — quince starts sending http to https at once,
        so use the link it gives you. If you want out sooner than ten minutes, restart quince: that
        cancels the trial immediately and nothing was written.
      </p>
      {deadEnd ? (
        <p role="status" className="mt-3 rounded-card border border-warn bg-bg px-3 py-2">
          <strong>Not from this address.</strong> The link would point at{" "}
          <code className="font-mono">{currentHost}</code>, which this certificate does not cover, so
          your browser would warn you there every time. Put a name it covers in the field above and
          check again.
        </p>
      ) : null}
      <div className="mt-3">
        <Button onClick={() => void apply()} disabled={busy || deadEnd}>
          {busy ? "Starting…" : "Try it now"}
        </Button>
      </div>
      {error ? (
        <div role="alert" className="mt-3 rounded-card border border-danger bg-bg px-3 py-2">
          {error}
        </div>
      ) : null}
    </div>
  );
}

// WHAT THE USER SEES WHILE THE WINDOW IS OPEN. This page cannot confirm on their behalf: it is on
// http, and the proof must arrive on the TLS half.
function ConfirmInstructions({
  applied,
  onRestart,
}: {
  applied: CertificateApplied;
  onRestart: () => void;
}) {
  const remaining = useCountdown(applied.expires_at);
  // THE TOKEN GOES IN THE FRAGMENT, NOT THE QUERY STRING (quince#979 review). A fragment is never
  // sent to the server, so it stays out of access logs and out of any `Referer` a later navigation
  // emits; a query parameter lands in all of them. The stakes are low — the token only confirms a
  // pair this user chose, on an install nobody has claimed — but the fragment costs nothing and the
  // choice is far easier to make now than to change later.
  const confirmURL = `${applied.confirm_origin}/onboarding/https/certificate/confirm#t=${encodeURIComponent(
    applied.confirm_token,
  )}`;

  // THE WINDOW CLOSES WHILE THIS PAGE IS OPEN, AND UNTIL NOW NOTHING SAID SO. The countdown reached
  // zero, `formatRemaining` returned its defensive string, and it landed in a sentence built to
  // assume time remained: *"quince is serving it now. Confirm within no time left to keep it."* Three
  // false claims at once — the pair is no longer served, there is nothing to confirm, and the advice
  // to wait describes something that has already happened.
  //
  // THE DEADLINE IS THE SERVER'S, SO THIS IS A CLAIM ABOUT TIME RATHER THAN ABOUT THE DAEMON. A
  // client whose clock runs slow could still be counting while the trial is gone; that user presses
  // the link and meets the 409, whose copy already names both causes. What this branch must not do is
  // keep asserting a live trial once the moment it was promised for has passed.
  if (remaining <= 0) {
    return (
      <div role="status" className="mt-6 rounded-card border border-line bg-card px-3 py-3 text-sm">
        <p>
          <strong>The ten minutes are up, so quince has gone back to what it was serving.</strong>
        </p>
        <p className="mt-2 text-muted">
          Nothing was written — <code className="font-mono">config.yml</code> is exactly as it was.
          The link from this trial no longer works; start another when you are ready, with the same
          files or different ones.
        </p>
        <div className="mt-3">
          <Button onClick={onRestart}>Try again</Button>
        </div>
      </div>
    );
  }

  return (
    <div role="status" className="mt-6 rounded-card border border-warn bg-card px-3 py-3 text-sm">
      <p>
        <strong>quince is serving it now. Confirm within {formatRemaining(remaining)} to keep it.</strong>
      </p>
      {/* THE COVERAGE CLAIM IS CONDITIONAL NOW, BECAUSE IT WAS AN ASSERTION QUINCE NEVER CHECKED.
          This read *"at the name the certificate covers"* unconditionally, and on an apply with the
          name left empty it pointed at whatever address the user was on — an IP, in the walk that
          found it — where the sentence was simply false and the next thing they met was
          "may be impersonating". The daemon now answers the question it was already composing the
          URL from. */}
      <p className="mt-2">
        {applied.confirm_host_covered
          ? "Open this link. It is the same quince, at a name this certificate covers, over https:"
          : "Open this link. It is the same quince over https — but at an address this certificate does not cover, so your browser will warn you first:"}
      </p>
      {/* A NEW TAB, DELIBERATELY. This page is the instructions; a user whose https link fails needs
          to still be looking at them, and on a name that does not resolve a same-tab navigation
          leaves them on a browser error with nothing to go back to but a broken address. */}
      <p className="mt-2">
        <a className="break-all font-mono underline" href={confirmURL} target="_blank" rel="noreferrer">
          {confirmURL}
        </a>
      </p>
      {/* THE TWO WAYS OUT, AND THE FAST ONE IS NAMED. Waiting works and is what the design leans on;
          a restart does the same thing in seconds, because a trial is held in memory and evaporates
          with the process — fail-safe by construction rather than by a handler running. */}
      <p className="mt-3 text-muted">
        If the page will not load at all, do nothing: quince goes back by itself in a few minutes,
        nothing was saved, and you can try a different name or a different file. Restarting quince
        does the same thing straight away.
      </p>
    </div>
  );
}

// useCountdown returns whole seconds left until an RFC3339 instant, never negative.
//
// FROM THE ABSOLUTE DEADLINE, NOT BY DECREMENTING — the same reasoning the server uses for the same
// window. A backgrounded tab gets its timers throttled, so a counter that subtracts one per tick
// drifts slow, and drifting slow here would tell somebody they had four minutes left after the
// window had already closed.
function useCountdown(deadline: string): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  const end = Date.parse(deadline);
  if (Number.isNaN(end)) return 0;
  return Math.max(0, Math.round((end - now) / 1000));
}

// ONLY EVER CALLED WITH TIME ON THE CLOCK. Zero is a different screen — see the expired branch in
// `ConfirmInstructions` — so this no longer carries a phrase for it. The one it had, "no time left",
// is what made the expired card read *"Confirm within no time left to keep it."*
function formatRemaining(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  if (m === 0) return `${s}s`;
  return `${m}m ${String(s).padStart(2, "0")}s`;
}
