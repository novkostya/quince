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
// THE SHIPPED WINDOW, which the offer card has to state BEFORE an apply has returned one. Every
// other mention reads `expires_seconds` off the response — this is the only place a client has to
// know it in advance, and `trial-window.test.ts` reads `certTrialWindow` out of the Go source and
// fails if the two drift apart.
export const TRIAL_WINDOW_SECONDS = 180;

export function CertificateApply({
  certFile,
  keyFile,
  hostname,
  blocked,
}: {
  certFile: string;
  keyFile: string;
  hostname: string;
  // WHETHER A TRIAL WOULD BE POINTLESS, DECIDED BY THE PAGE.
  //
  // A BOOLEAN, BECAUSE THE REASON IS ALREADY ON SCREEN. Both causes — the certificate cannot cover
  // the address the link would use, or this browser cannot reach that address at all — are reported
  // by the checks above this card, so carrying the sentence down here as well would ship a string
  // nothing renders.
  //
  // THE PAGE OWNS THE DECISION BECAUSE IT HOLDS BOTH FACTS, which arrive from two different checks.
  // Deriving one of them here is what let the other ship unguarded: a name the certificate covered,
  // unreachable from this browser, with the button live under a red box saying so.
  blocked: boolean;
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

  // THE TRIAL IS NOT OFFERED WHEN IT CANNOT SUCCEED — Operator direction, 2026-08-18: *"there should
  // be no way to tap Try it now — this is guaranteed dead end."* The reason arrives already worded,
  // because the page is where both halves of "pointless" are known.
  //
  // IT DOES NOT TOUCH THE JOURNEY THE OPERATOR RULED VALID — *"this is also a valid test case"* — a
  // certificate that DOES cover a reachable address, self-signed or from an internal CA. There the
  // browser warns about the ISSUER, trusting it once ends the warnings, and quince must not stand in
  // the way. Nothing here blocks a warning; it blocks an address that cannot work.
  //
  // A DISABLED BUTTON WITH A REASON, not a hidden one: the remedy is one field away, and a control
  // that vanishes teaches nothing.
  if (applied) return <ConfirmInstructions applied={applied} onRestart={() => setApplied(null)} />;

  return (
    <div className="mt-6 rounded-card border border-line bg-card px-3 py-3 text-sm">
      <p>
        <strong>Try this certificate.</strong> quince starts using it now and{" "}
        <strong>saves nothing yet</strong>.
      </p>
      {/* SAID BEFORE THE BUTTON, NOT AFTER. This is the reassurance that makes it safe to press, and
          a user who only learns it once the page has changed has already taken the risk they were
          being reassured about. */}
      {/* THE LENGTH COMES FROM THE APPLY, NOT FROM A SENTENCE. `expires_seconds` is the same window
          the daemon armed, so the copy cannot drift from `certTrialWindow` the way a written-out
          a written-out length did when the ruling changed. */}
      <p className="mt-2 text-muted">
        You have {minutes(TRIAL_WINDOW_SECONDS)} to open it over https and confirm. If you do not,
        quince goes back to what it had and nothing is saved.
      </p>
      {/* THE COST TO **THIS** PAGE, WHICH NOTHING SAID. The moment a certificate is serving,
          `plainHalf` starts redirecting http to https, so every request from this page is sent to an
          origin the browser may refuse — the page goes dark along with everything else. The old copy
          covered it obliquely ("if the page will not load at all, do nothing"), which reads as being
          about the new tab.

          AND THE FAST EXIT IS NAMED. `certTrial` calls a mid-window restart fail-safe by design: the
          trial evaporates and the daemon comes back on what `config.yml` names. Offering only "wait
          the window out" under-sells a guarantee the daemon already makes. */}
      <p className="mt-2 text-muted">
        This page will stop working while you do — use the link quince gives you. Restarting quince
        cancels the whole thing at once.
      </p>
      {/* A POINTER, NOT THE REASON AGAIN. Every reason this button is dead has already been stated
          above, in the check results — printing it a second time inside the card put the same
          sentence on screen twice, two boxes apart, which reads as a rendering fault rather than as
          emphasis. This says only why the button will not respond. */}
      {blocked ? (
        <p className="mt-3 text-muted">Fix the problem above to try this certificate.</p>
      ) : null}
      <div className="mt-3">
        <Button onClick={() => void apply()} disabled={busy || blocked}>
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
          <strong>Time is up, so quince has gone back to the certificate it had.</strong>
        </p>
        <p className="mt-2 text-muted">
          Nothing was saved. Start again whenever you are ready.
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
          ? "Open this link to confirm. It is this quince, over https:"
          : "Open this link to confirm. Your browser will warn you first — this certificate does not cover that address:"}
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
        If it will not load, do nothing — quince goes back on its own in {minutes(applied.expires_seconds)}{" "}
        and nothing is saved. Restarting quince does it at once.
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
// minutes renders a window length for prose.
function minutes(seconds: number): string {
  const m = Math.max(1, Math.round(seconds / 60));
  return m === 1 ? "a minute" : `${m} minutes`;
}

function formatRemaining(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  if (m === 0) return `${s}s`;
  return `${m}m ${String(s).padStart(2, "0")}s`;
}
