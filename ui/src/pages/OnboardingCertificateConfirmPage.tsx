import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";

// THE CONFIRMATION PAGE — quince#908 §5, slice 5. It exists to be opened at the https origin the
// apply named, and its whole job is to let the daemon observe a request arriving on its own TLS half.
// That request is what writes `config.yml`; nothing before it does.
//
// IT DOES NOT CONFIRM ON LOAD, and that is the decision worth defending. A page that posted
// automatically would confirm a certificate the moment it rendered — including in a preloading tab,
// or after somebody clicked through a browser warning they did not read. The button is what makes
// this a statement by a person: *I can see quince at this address.*
//
// THE TOKEN ARRIVES IN THE FRAGMENT, and it is read here rather than from a query parameter
// (quince#979 review). A fragment never reaches a server, so it stays out of access logs and out of
// any `Referer` a later navigation sends; a query parameter lands in both. It has to travel in the
// URL at all because this page is on a DIFFERENT ORIGIN from the one that applied — different
// scheme, usually a different host — so nothing in `sessionStorage` reaches it.
//
// IT IS NOT A CREDENTIAL either way: it cancels a trial on an install nobody has claimed, in the
// window where anyone reaching the port could claim the whole thing outright (contracts §1).
export function OnboardingCertificateConfirmPage() {
  const [token, setToken] = useState<string | null>(null);
  const [state, setState] = useState<"idle" | "busy" | "done" | "declined">("idle");
  const [error, setError] = useState<string | null>(null);
  const [secure, setSecure] = useState<boolean | null>(null);

  // BOTH READS HAPPEN IN AN EFFECT because both touch `window` — `location.hash` is not available
  // during a server render and `react-router` does not parse the fragment for us.
  useEffect(() => {
    setSecure(window.location.protocol === "https:");
    setToken(new URLSearchParams(window.location.hash.replace(/^#/, "")).get("t") ?? "");
  }, []);

  async function confirm() {
    setState("busy");
    setError(null);
    try {
      await api.post("/api/onboarding/certificate/confirm", { token });
      setState("done");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setState("idle");
    }
  }

  // DECLINING IS THE OTHER ANSWER, and it ends the trial rather than navigating away from it. The
  // link that used to sit at the bottom of this page merely went back to the certificate step — on
  // THIS origin, which the trial certificate is what makes reachable — so the user would have been
  // left reading a form served by the pair they had just refused, on an address that stops existing
  // when the window closes.
  async function decline() {
    setState("busy");
    setError(null);
    try {
      await api.post("/api/onboarding/certificate/cancel", { token });
      setState("declined");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setState("idle");
    }
  }

  // WHERE "NO" LEADS: the http origin, on this host and port, which is where the user came from and
  // where quince answers once the trial is dropped. Composed rather than remembered — this page is
  // on a different origin from the one that applied, so nothing it could have stored reaches here.
  function plainOrigin(): string {
    return `http://${window.location.host}/onboarding/https/certificate`;
  }

  return (
    <div className="min-h-dvh bg-bg pb-16 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(2.5rem,env(safe-area-inset-top))] text-fg">
      <div className="mx-auto w-full max-w-2xl">
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <h1 className="mt-4 text-xl font-semibold tracking-tight">Keep this certificate?</h1>

        {token === "" ? (
          <p className="mt-4 text-sm" role="alert">
            This link has no confirmation token, so there is nothing for it to confirm. Go back to the
            certificate step and try again — the link it gives you carries one.
          </p>
        ) : state === "declined" ? (
          <div role="status" className="mt-4 rounded-card border border-line bg-card px-3 py-3 text-sm">
            <p>
              <strong>Dropped.</strong> quince has gone back to the certificate it had, and nothing
              was saved.
            </p>
            {/* THE WAY BACK IS AN ORDINARY LINK ON THE http ORIGIN, not a router navigation: this
                page is on the https origin the trial created, and that origin has just stopped
                answering. A client-side route would leave the user on a dead address. */}
            <p className="mt-3">
              <a className="underline" href={plainOrigin()}>
                Back to the certificate step
              </a>
            </p>
          </div>
        ) : state === "done" ? (
          <div role="status" className="mt-4 rounded-card border border-ok bg-card px-3 py-3 text-sm">
            <p>
              <strong>Kept.</strong> quince has written this pair into{" "}
              <code className="font-mono">config.yml</code> and will go on serving it, including after
              a restart.
            </p>
            <p className="mt-2 text-muted">
              You are reading this over https at this address, which is the proof it needed — and the
              first thing in this whole step that changed a file.
            </p>
            {/* THE WAY ON. A terminal state needs a forward link or it is a dead end, and this one is
                the end of the LAST optional step — the user still has an admin password to set and a
                storage to declare. Relative, so it stays on this origin: the certificate that was
                just kept is what makes this address the right one to carry on at. */}
            <p className="mt-3">
              <Link className="underline" to="/">
                Continue setting up quince
              </Link>
            </p>
          </div>
        ) : token === null ? null : (
          <>
            <p className="mt-2 text-sm text-muted">
              You are seeing this page, so this browser reached quince over https at this address.
              That is the whole test. Nothing has been saved yet.
            </p>
            {secure === false ? (
              // THE HONEST REFUSAL BEFORE THE ROUND TRIP. The server answers 426 here regardless —
              // `X-Forwarded-Proto` is not evidence for this one question — but a user who followed
              // the wrong link deserves the reason rather than a status code.
              <div role="alert" className="mt-4 rounded-card border border-danger bg-card px-3 py-2 text-sm">
                <strong>This page is not on https.</strong> Confirming from here would prove nothing
                about the certificate, and quince refuses it. Open the exact link the previous step
                gave you — it begins <code className="font-mono">https://</code>.
              </div>
            ) : null}
            {/* THE PROJECT'S BUTTON ROW: `flex flex-wrap items-center gap-2`, which is what
                `BackupControls`, `PasswordControls`, `PlainHTTPSetting` and eight others use. A bare
                block put the two answers flush against each other. `flex-wrap` is not decoration —
                these labels are long enough to need a second line on a phone. */}
            <div className="mt-4 flex flex-wrap items-center gap-2">
              <Button onClick={() => void confirm()} disabled={state === "busy" || secure === false}>
                {state === "busy" ? "Working…" : "Yes, keep it"}
              </Button>
              {/* THE OTHER ANSWER, AND IT ENDS THE TRIAL. Declining used to mean waiting the window out
                  or navigating away and leaving it running. It is offered only here because only
                  here is it safe: reaching this page proves the trial certificate works, so the
                  request has a channel to travel over — which the apply page does not. */}
              <Button
                variant="outline"
                onClick={() => void decline()}
                disabled={state === "busy" || secure === false}
              >
                No, drop it
              </Button>
            </div>
            {error ? (
              <div role="alert" className="mt-4 rounded-card border border-danger bg-card px-3 py-2 text-sm">
                {error}
              </div>
            ) : null}
            <p className="mt-4 text-sm text-muted">
              Doing nothing is also an answer: quince goes back to what it was serving when the window
              closes, and since nothing was written, nothing is lost.
            </p>
          </>
        )}

        {/* NO STRAY LINK BACK. It pointed at the certificate step on THIS origin — the one the trial
            certificate makes reachable and the window closing takes away — and it left the trial
            running, which is what made it worse than nothing. Going back is now an ANSWER: "No, drop
            it" ends the trial and then offers the http address, where quince will still be. */}
      </div>
    </div>
  );
}
